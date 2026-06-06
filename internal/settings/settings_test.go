package settings

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
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
	mergeFile(&s, path)
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
	mergeFile(&s, path)
	if s.Lang != "en" {
		t.Errorf("Lang = %q, want en (should override)", s.Lang)
	}
}

func TestMergeFile_IgnoresInvalidValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.env")
	os.WriteFile(path, []byte("lang=invalid\napt_mirror=bad\n"), 0o644)

	s := Settings{Lang: "zh-CN", AptMirror: "cernet"}
	mergeFile(&s, path)
	if s.Lang != "zh-CN" {
		t.Errorf("Lang = %q, want zh-CN (invalid should be ignored)", s.Lang)
	}
	if s.AptMirror != "cernet" {
		t.Errorf("AptMirror = %q, want cernet (invalid should be ignored)", s.AptMirror)
	}
}

func TestMergeFile_MissingFile(t *testing.T) {
	s := Settings{Lang: "zh-CN"}
	mergeFile(&s, "/nonexistent/path/config.env")
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
	mergeFile(&s, sysPath)
	mergeFile(&s, usrPath)
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

	s := Load()
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

	s := Load()
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

	s := Load()
	if s.Lang != "" {
		t.Errorf("Lang = %q, want empty", s.Lang)
	}
	if s.AptMirror != "" {
		t.Errorf("AptMirror = %q, want empty", s.AptMirror)
	}
}
