package settings

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestParseLine(t *testing.T) {
	tests := []struct {
		line  string
		key   string
		val   string
		valid bool
	}{
		{"lang=zh-CN", "lang", "zh-CN", true},
		{"apt_mirror=cernet", "apt_mirror", "cernet", true},
		{"key = value ", "key", "value", true},
		{"# comment", "", "", false},
		{"noequals", "", "", false},
		{"=", "", "", true},
	}
	for _, tt := range tests {
		k, v, ok := parseLine(tt.line)
		if ok != tt.valid {
			t.Errorf("parseLine(%q): ok=%v, want %v", tt.line, ok, tt.valid)
		}
		if ok && (k != tt.key || v != tt.val) {
			t.Errorf("parseLine(%q) = (%q, %q), want (%q, %q)", tt.line, k, v, tt.key, tt.val)
		}
	}
}

func TestMergeFile_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.env")
	os.WriteFile(path, []byte("lang=zh-CN\napt_mirror=cernet\n"), 0o644)

	s := Settings{}
	if err := mergeFile(&s, path); err != nil {
		t.Fatalf("mergeFile failed: %v", err)
	}
	if s.Lang != "zh-CN" {
		t.Errorf("Lang = %q, want zh-CN", s.Lang)
	}
	if s.AptMirror != "cernet" {
		t.Errorf("AptMirror = %q, want cernet", s.AptMirror)
	}
}

func TestMergeFile_IgnoresCommentsAndBlanks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.env")
	os.WriteFile(path, []byte("# comment\n\nlang=en\n\n"), 0o644)

	s := Settings{Lang: "zh-CN"}
	if err := mergeFile(&s, path); err != nil {
		t.Fatalf("mergeFile failed: %v", err)
	}
	if s.Lang != "en" {
		t.Errorf("Lang = %q, want en (should override)", s.Lang)
	}
}

func TestMergeFile_IgnoresInvalidValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.env")
	os.WriteFile(path, []byte("lang=invalid\napt_mirror=bad\n"), 0o644)

	s := Settings{Lang: "zh-CN", AptMirror: "cernet"}
	if err := mergeFile(&s, path); err != nil {
		t.Fatalf("mergeFile failed: %v", err)
	}
	if s.Lang != "zh-CN" {
		t.Errorf("Lang = %q, want zh-CN (invalid should be ignored)", s.Lang)
	}
	if s.AptMirror != "cernet" {
		t.Errorf("AptMirror = %q, want cernet (invalid should be ignored)", s.AptMirror)
	}
}

func TestMergeFile_MissingFile(t *testing.T) {
	s := Settings{Lang: "zh-CN"}
	if err := mergeFile(&s, "/nonexistent/path/config.env"); err != nil {
		t.Fatalf("missing config should be ignored: %v", err)
	}
	if s.Lang != "zh-CN" {
		t.Errorf("Lang = %q, want zh-CN (missing file should be no-op)", s.Lang)
	}
}

func TestMergeFile_UserOverridesSystem(t *testing.T) {
	dir := t.TempDir()
	sysPath := filepath.Join(dir, "system.env")
	usrPath := filepath.Join(dir, "user.env")
	os.WriteFile(sysPath, []byte("lang=zh-CN\napt_mirror=cernet\n"), 0o644)
	os.WriteFile(usrPath, []byte("lang=en\n"), 0o644)

	s := Settings{}
	if err := mergeFile(&s, sysPath); err != nil {
		t.Fatalf("merge system file: %v", err)
	}
	if err := mergeFile(&s, usrPath); err != nil {
		t.Fatalf("merge user file: %v", err)
	}
	if s.Lang != "en" {
		t.Errorf("Lang = %q, want en (user should override system)", s.Lang)
	}
	if s.AptMirror != "cernet" {
		t.Errorf("AptMirror = %q, want cernet (system should persist)", s.AptMirror)
	}
}

func TestSaveUser_CreatesFileAndDirs(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config", "sys-bootstrap", "config.env")

	orig := UserConfigPath
	UserConfigPath = func() string { return configPath }
	defer func() { UserConfigPath = orig }()

	err := SaveUser(Settings{Lang: "zh-CN", AptMirror: "cernet"})
	if err != nil {
		t.Fatalf("SaveUser failed: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("cannot read config: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "lang=zh-CN") {
		t.Errorf("config missing lang=zh-CN: %s", content)
	}
	if !strings.Contains(content, "apt_mirror=cernet") {
		t.Errorf("config missing apt_mirror=cernet: %s", content)
	}
}

func TestSaveUser_EmptyFieldsOmitted(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.env")

	orig := UserConfigPath
	UserConfigPath = func() string { return configPath }
	defer func() { UserConfigPath = orig }()

	err := SaveUser(Settings{Lang: "en"})
	if err != nil {
		t.Fatalf("SaveUser failed: %v", err)
	}

	data, _ := os.ReadFile(configPath)
	content := string(data)
	if strings.Contains(content, "apt_mirror") {
		t.Errorf("config should not contain apt_mirror when empty: %s", content)
	}
}

func TestSaveUserPreservesExistingExtendedAttributes(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.env")
	if err := os.WriteFile(configPath, []byte("lang=en\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	xattrName := "user.sys-bootstrap-settings-test"
	if runtime.GOOS == "darwin" {
		xattrName = "com.sys-bootstrap.settings-test"
	}
	xattrValue := []byte("preserve-me")
	f, err := os.OpenFile(configPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	err = unix.Fsetxattr(int(f.Fd()), xattrName, xattrValue, 0)
	_ = f.Close()
	if err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EPERM) {
			t.Skipf("extended attributes unavailable: %v", err)
		}
		t.Fatal(err)
	}

	originalPath := UserConfigPath
	UserConfigPath = func() string { return configPath }
	t.Cleanup(func() { UserConfigPath = originalPath })
	if err := SaveUser(Settings{Lang: "zh-CN"}); err != nil {
		t.Fatal(err)
	}

	f, err = os.Open(configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(xattrValue))
	n, err := unix.Fgetxattr(int(f.Fd()), xattrName, got)
	_ = f.Close()
	if err != nil {
		t.Fatalf("read preserved xattr: %v", err)
	}
	if string(got[:n]) != string(xattrValue) {
		t.Fatalf("xattr = %q, want %q", got[:n], xattrValue)
	}
}

func TestSaveUserDelegatesSudoWriteBeforeTouchingPath(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "must-not-be-created", "config.env")
	originalPath := UserConfigPath
	UserConfigPath = func() string { return configPath }
	t.Cleanup(func() { UserConfigPath = originalPath })

	originalEUID := settingsEffectiveUID
	settingsEffectiveUID = func() int { return 0 }
	t.Cleanup(func() { settingsEffectiveUID = originalEUID })
	t.Setenv("SUDO_USER", "alice")

	originalDelegate := saveUserAsInvokingUser
	delegated := false
	saveUserAsInvokingUser = func(s Settings) error {
		delegated = true
		if s.Lang != "en" || s.AptMirror != "cernet" {
			t.Fatalf("delegated settings = %#v", s)
		}
		return nil
	}
	t.Cleanup(func() { saveUserAsInvokingUser = originalDelegate })

	if err := SaveUser(Settings{Lang: "en", AptMirror: "cernet"}); err != nil {
		t.Fatalf("SaveUser failed: %v", err)
	}
	if !delegated {
		t.Fatal("sudo user write was not delegated")
	}
	if _, err := os.Lstat(filepath.Dir(configPath)); !os.IsNotExist(err) {
		t.Fatalf("privileged parent touched user path before delegation: %v", err)
	}
}

func TestUserHomeDirUsesSudoUser(t *testing.T) {
	current, err := user.Current()
	if err != nil {
		t.Skipf("cannot determine current user: %v", err)
	}
	if current.Username == "" || current.HomeDir == "" || current.Username == "root" {
		t.Skip("test requires a non-root current user")
	}

	t.Setenv("SUDO_USER", current.Username)
	if got := userHomeDir(); got != current.HomeDir {
		t.Errorf("userHomeDir() = %q, want %q", got, current.HomeDir)
	}
}

func TestIsValidLang(t *testing.T) {
	valid := []string{"en", "zh-CN", "zh-cn", "zh_cn", "zh", "chinese", "EN", "ZH-CN"}
	invalid := []string{"fr", "de", "jp", ""}
	for _, v := range valid {
		if !isValidLang(v) {
			t.Errorf("isValidLang(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if isValidLang(v) {
			t.Errorf("isValidLang(%q) = true, want false", v)
		}
	}
}

func TestIsValidAptMirror(t *testing.T) {
	valid := []string{"cernet", "default"}
	invalid := []string{"", "mirrors.tuna.tsinghua", "CERNET"}
	for _, v := range valid {
		if !isValidAptMirror(v) {
			t.Errorf("isValidAptMirror(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if isValidAptMirror(v) {
			t.Errorf("isValidAptMirror(%q) = true, want false", v)
		}
	}
}

func TestNormalizeLang(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"en", "en"},
		{"zh-CN", "zh-CN"},
		{"zh-cn", "zh-CN"},
		{"zh_cn", "zh-CN"},
		{"zh", "zh-CN"},
		{"chinese", "zh-CN"},
		{"CHINESE", "zh-CN"},
		{"fr", "en"},
		{"", "en"},
	}
	for _, tt := range tests {
		got := NormalizeLang(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeLang(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLoad_MergesSystemAndUser(t *testing.T) {
	dir := t.TempDir()
	sysPath := filepath.Join(dir, "system.env")
	usrPath := filepath.Join(dir, "user.env")
	os.WriteFile(sysPath, []byte("lang=zh-CN\napt_mirror=cernet\n"), 0o644)
	os.WriteFile(usrPath, []byte("lang=en\n"), 0o644)

	origSys := SystemConfigPath
	origUser := UserConfigPath
	SystemConfigPath = sysPath
	UserConfigPath = func() string { return usrPath }
	defer func() {
		SystemConfigPath = origSys
		UserConfigPath = origUser
	}()

	s, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if s.Lang != "en" {
		t.Errorf("Lang = %q, want en (user overrides system)", s.Lang)
	}
	if s.AptMirror != "cernet" {
		t.Errorf("AptMirror = %q, want cernet (from system)", s.AptMirror)
	}
}

func TestLoad_OnlySystem(t *testing.T) {
	dir := t.TempDir()
	sysPath := filepath.Join(dir, "system.env")
	os.WriteFile(sysPath, []byte("lang=zh-CN\n"), 0o644)

	origSys := SystemConfigPath
	origUser := UserConfigPath
	SystemConfigPath = sysPath
	UserConfigPath = func() string { return filepath.Join(dir, "nonexistent.env") }
	defer func() {
		SystemConfigPath = origSys
		UserConfigPath = origUser
	}()

	s, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if s.Lang != "zh-CN" {
		t.Errorf("Lang = %q, want zh-CN", s.Lang)
	}
	if s.AptMirror != "" {
		t.Errorf("AptMirror = %q, want empty", s.AptMirror)
	}
}

func TestLoad_EmptyWhenNoFiles(t *testing.T) {
	dir := t.TempDir()

	origSys := SystemConfigPath
	origUser := UserConfigPath
	SystemConfigPath = filepath.Join(dir, "no-sys.env")
	UserConfigPath = func() string { return filepath.Join(dir, "no-user.env") }
	defer func() {
		SystemConfigPath = origSys
		UserConfigPath = origUser
	}()

	s, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if s.Lang != "" {
		t.Errorf("Lang = %q, want empty", s.Lang)
	}
	if s.AptMirror != "" {
		t.Errorf("AptMirror = %q, want empty", s.AptMirror)
	}
}

func TestLoad_ReportsScannerErrors(t *testing.T) {
	dir := t.TempDir()
	sysPath := filepath.Join(dir, "system.env")
	if err := os.WriteFile(sysPath, []byte("lang="+strings.Repeat("x", 70*1024)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	origSys := SystemConfigPath
	origUser := UserConfigPath
	SystemConfigPath = sysPath
	UserConfigPath = func() string { return filepath.Join(dir, "missing-user.env") }
	t.Cleanup(func() {
		SystemConfigPath = origSys
		UserConfigPath = origUser
	})

	if _, err := Load(); err == nil {
		t.Fatal("expected oversized config line to produce a load error")
	}
}
