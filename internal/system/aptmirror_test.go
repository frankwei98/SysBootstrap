package system

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPTMirrorNeedsSwitchInFiles(t *testing.T) {
	official := filepath.Join(t.TempDir(), "debian.list")
	if err := os.WriteFile(official, []byte("deb https://deb.debian.org/debian bookworm main\n"), 0o644); err != nil {
		t.Fatalf("write official source: %v", err)
	}
	needsSwitch, err := aptMirrorNeedsSwitchInFiles([]string{official})
	if err != nil {
		t.Fatalf("aptMirrorNeedsSwitchInFiles() failed: %v", err)
	}
	if !needsSwitch {
		t.Fatal("official Debian source should require a CERNET switch")
	}

	configured := filepath.Join(t.TempDir(), "ubuntu.sources")
	content := "Types: deb\nURIs: https://mirrors.cernet.edu.cn/ubuntu\nSuites: jammy\nComponents: main\n"
	if err := os.WriteFile(configured, []byte(content), 0o644); err != nil {
		t.Fatalf("write configured source: %v", err)
	}
	needsSwitch, err = aptMirrorNeedsSwitchInFiles([]string{configured})
	if err != nil {
		t.Fatalf("aptMirrorNeedsSwitchInFiles() failed: %v", err)
	}
	if needsSwitch {
		t.Fatal("configured CERNET source should already satisfy the target")
	}
}

func TestRewriteListLine_Debian(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			"debian http",
			"deb http://deb.debian.org/debian bookworm main",
			"deb https://mirrors.cernet.edu.cn/debian bookworm main",
		},
		{
			"debian https",
			"deb https://deb.debian.org/debian bookworm main",
			"deb https://mirrors.cernet.edu.cn/debian bookworm main",
		},
		{
			"debian-src",
			"deb-src http://deb.debian.org/debian bookworm main",
			"deb-src https://mirrors.cernet.edu.cn/debian bookworm main",
		},
		{
			"debian security unchanged",
			"deb http://security.debian.org/debian-security bookworm-security main",
			"deb http://security.debian.org/debian-security bookworm-security main",
		},
		{
			"comment unchanged",
			"# deb http://deb.debian.org/debian bookworm main",
			"# deb http://deb.debian.org/debian bookworm main",
		},
		{
			"empty unchanged",
			"",
			"",
		},
		{
			"third-party unchanged",
			"deb https://packages.sury.org/php/ bookworm main",
			"deb https://packages.sury.org/php/ bookworm main",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewriteListLine(tt.input)
			if got != tt.want {
				t.Errorf("rewriteListLine(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRewriteListLine_WithOptions(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			"debian with signed-by option",
			"deb [signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] http://deb.debian.org/debian bookworm main",
			"deb [signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] https://mirrors.cernet.edu.cn/debian bookworm main",
		},
		{
			"debian with arch option",
			"deb [arch=amd64] https://deb.debian.org/debian bookworm main",
			"deb [arch=amd64] https://mirrors.cernet.edu.cn/debian bookworm main",
		},
		{
			"debian-src with options",
			"deb-src [signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] http://deb.debian.org/debian bookworm main",
			"deb-src [signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] https://mirrors.cernet.edu.cn/debian bookworm main",
		},
		{
			"ubuntu with signed-by option",
			"deb [signed-by=/usr/share/keyrings/ubuntu-archive-keyring.gpg] http://archive.ubuntu.com/ubuntu jammy main",
			"deb [signed-by=/usr/share/keyrings/ubuntu-archive-keyring.gpg] https://mirrors.cernet.edu.cn/ubuntu jammy main",
		},
		{
			"ubuntu ports with options",
			"deb [arch=arm64] http://ports.ubuntu.com/ubuntu-ports jammy main",
			"deb [arch=arm64] https://mirrors.cernet.edu.cn/ubuntu-ports jammy main",
		},
		{
			"security with options unchanged",
			"deb [signed-by=/usr/share/keyrings/ubuntu-archive-keyring.gpg] http://security.ubuntu.com/ubuntu jammy-security main",
			"deb [signed-by=/usr/share/keyrings/ubuntu-archive-keyring.gpg] http://security.ubuntu.com/ubuntu jammy-security main",
		},
		{
			"third-party with options unchanged",
			"deb [signed-by=/usr/share/keyrings/example.gpg] https://packages.example.com/repo stable main",
			"deb [signed-by=/usr/share/keyrings/example.gpg] https://packages.example.com/repo stable main",
		},
		{
			"debian security path with options unchanged",
			"deb [signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] http://deb.debian.org/debian-security bookworm-security main",
			"deb [signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] http://deb.debian.org/debian-security bookworm-security main",
		},
		{
			"debian with multiple options",
			"deb [arch=amd64 signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] http://deb.debian.org/debian bookworm main",
			"deb [arch=amd64 signed-by=/usr/share/keyrings/debian-archive-keyring.gpg] https://mirrors.cernet.edu.cn/debian bookworm main",
		},
		{
			"malformed bracket no closing",
			"deb [broken http://deb.debian.org/debian bookworm main",
			"deb [broken http://deb.debian.org/debian bookworm main",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewriteListLine(tt.input)
			if got != tt.want {
				t.Errorf("rewriteListLine(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRewriteListLine_Ubuntu(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			"ubuntu http",
			"deb http://archive.ubuntu.com/ubuntu jammy main",
			"deb https://mirrors.cernet.edu.cn/ubuntu jammy main",
		},
		{
			"ubuntu https",
			"deb https://archive.ubuntu.com/ubuntu jammy main",
			"deb https://mirrors.cernet.edu.cn/ubuntu jammy main",
		},
		{
			"ubuntu-src",
			"deb-src http://archive.ubuntu.com/ubuntu jammy main",
			"deb-src https://mirrors.cernet.edu.cn/ubuntu jammy main",
		},
		{
			"ubuntu ports",
			"deb http://ports.ubuntu.com/ubuntu-ports jammy main",
			"deb https://mirrors.cernet.edu.cn/ubuntu-ports jammy main",
		},
		{
			"ubuntu security unchanged",
			"deb http://security.ubuntu.com/ubuntu jammy-security main",
			"deb http://security.ubuntu.com/ubuntu jammy-security main",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewriteListLine(tt.input)
			if got != tt.want {
				t.Errorf("rewriteListLine(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRewriteSourcesContent(t *testing.T) {
	input := `Types: deb
URIs: http://deb.debian.org/debian
Suites: bookworm
Components: main

Types: deb
URIs: http://security.debian.org/debian-security
Suites: bookworm-security
Components: main

Types: deb
URIs: http://archive.ubuntu.com/ubuntu
Suites: jammy
Components: main universe
`
	want := `Types: deb
URIs: https://mirrors.cernet.edu.cn/debian
Suites: bookworm
Components: main

Types: deb
URIs: http://security.debian.org/debian-security
Suites: bookworm-security
Components: main

Types: deb
URIs: https://mirrors.cernet.edu.cn/ubuntu
Suites: jammy
Components: main universe
`
	got := rewriteSourcesContent(input)
	if got != want {
		t.Errorf("rewriteSourcesContent:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRewriteSourcesContent_NoChange(t *testing.T) {
	input := `Types: deb
URIs: http://security.debian.org/debian-security
Suites: bookworm-security
Components: main
`
	got := rewriteSourcesContent(input)
	if got != input {
		t.Errorf("rewriteSourcesContent should not change security sources")
	}
}

func TestSwitchListFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.list")

	content := "deb http://deb.debian.org/debian bookworm main\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, bk, err := switchListFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("expected changed=true")
	}
	if bk.path != path {
		t.Errorf("backup path = %q, want %q", bk.path, path)
	}

	result, _ := os.ReadFile(path)
	if !strings.Contains(string(result), "mirrors.cernet.edu.cn") {
		t.Errorf("result should contain CERNET mirror, got: %s", result)
	}
}

func TestSwitchListFile_PreservesLinesLargerThanScannerLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sources.list")
	longComment := "# " + strings.Repeat("x", 70*1024)
	tail := "deb https://packages.example.com stable main"
	content := "deb http://deb.debian.org/debian bookworm main\n" + longComment + "\n" + tail + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, _, err := switchListFile(path)
	if err != nil {
		t.Fatalf("switchListFile() failed: %v", err)
	}
	if !changed {
		t.Fatal("expected official source to be rewritten")
	}
	result, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), longComment) || !strings.Contains(string(result), tail) {
		t.Fatal("source rewrite truncated the long line or following content")
	}
}

func TestSwitchListFile_RejectsOversizedLineWithoutChangingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sources.list")
	content := "deb http://deb.debian.org/debian bookworm main\n# " + strings.Repeat("x", 5*1024*1024) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, _, err := switchListFile(path)
	if err == nil {
		t.Fatal("expected an oversized source line to be rejected")
	}
	if changed {
		t.Fatal("oversized source file must not be reported as changed")
	}
	result, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(result) != content {
		t.Fatal("oversized source file changed after scan failure")
	}
}

func TestRestoreAll_ContinuesAfterOneRestoreFails(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	unsafePath := filepath.Join(dir, "unsafe")
	if err := os.Symlink(victim, unsafePath); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	restorablePath := filepath.Join(dir, "restorable")
	if err := os.WriteFile(restorablePath, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := restoreAll([]backupEntry{
		{path: unsafePath, content: []byte("must not write"), mode: 0o600},
		{path: restorablePath, content: []byte("original"), mode: 0o600},
	})
	if err == nil {
		t.Fatal("expected unsafe restore target to fail")
	}
	got, readErr := os.ReadFile(restorablePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "original" {
		t.Fatalf("later backup was not restored after an earlier failure: %q", got)
	}
}

func TestRestoreAfterAPTSwitchFailure_ReportsProcessingAndRollbackErrors(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	unsafePath := filepath.Join(dir, "unsafe")
	if err := os.Symlink(victim, unsafePath); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	err := restoreAfterAPTSwitchFailure(
		fmt.Errorf("processing later.sources: parse failed"),
		[]backupEntry{{path: unsafePath, content: []byte("no"), mode: 0o600}},
	)
	if err == nil {
		t.Fatal("expected combined processing and rollback error")
	}
	for _, want := range []string{"processing later.sources", "rolling back APT source changes", "restoring"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("combined error %q does not contain %q", err, want)
		}
	}
}

func TestSwitchListFile_NoChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.list")

	content := "deb http://security.debian.org/debian-security bookworm-security main\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, _, err := switchListFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("security source should not be changed")
	}
}

func TestSwitchListFile_ThirdParty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sources.list")

	content := "deb https://packages.sury.org/php/ bookworm main\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, _, err := switchListFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("third-party source should not be changed")
	}
}

func TestSwitchSourcesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sources")

	content := "Types: deb\nURIs: http://deb.debian.org/debian\nSuites: bookworm\nComponents: main\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, _, err := switchSourcesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("expected changed=true")
	}

	result, _ := os.ReadFile(path)
	if !strings.Contains(string(result), "mirrors.cernet.edu.cn") {
		t.Errorf("result should contain CERNET mirror, got: %s", result)
	}
}

func TestSwitchSourcesFile_SecurityUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sources")

	content := "Types: deb\nURIs: http://security.debian.org/debian-security\nSuites: bookworm-security\nComponents: main\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, _, err := switchSourcesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("security source should not be changed")
	}
}

func TestSwitchAPTMirrorToCernet_Integration(t *testing.T) {
	// Create a temp apt directory structure
	dir := t.TempDir()
	sourcesList := filepath.Join(dir, "sources.list")
	listD := filepath.Join(dir, "sources.list.d")
	os.MkdirAll(listD, 0o755)

	extraList := filepath.Join(listD, "extra.list")
	extraSources := filepath.Join(listD, "extra.sources")

	// Write test files
	os.WriteFile(sourcesList, []byte(
		"deb http://deb.debian.org/debian bookworm main\n"+
			"deb http://security.debian.org/debian-security bookworm-security main\n",
	), 0o644)

	os.WriteFile(extraList, []byte(
		"deb http://archive.ubuntu.com/ubuntu jammy main\n"+
			"deb http://packages.example.com/repo stable main\n",
	), 0o644)

	os.WriteFile(extraSources, []byte(
		"Types: deb\nURIs: http://ports.ubuntu.com/ubuntu-ports\nSuites: jammy\nComponents: main\n",
	), 0o644)

	// We can't call SwitchAPTMirrorToCernet directly because it uses
	// hardcoded /etc/apt paths. Instead test the individual functions.
	changed1, _, _ := switchListFile(sourcesList)
	changed2, _, _ := switchListFile(extraList)
	changed3, _, _ := switchSourcesFile(extraSources)

	if !changed1 {
		t.Error("sources.list should be changed")
	}
	if !changed2 {
		t.Error("extra.list should be changed")
	}
	if !changed3 {
		t.Error("extra.sources should be changed")
	}

	// Verify sources.list: debian changed, security unchanged
	content1, _ := os.ReadFile(sourcesList)
	s1 := string(content1)
	if !strings.Contains(s1, "mirrors.cernet.edu.cn/debian") {
		t.Error("sources.list should contain CERNET mirror for debian")
	}
	if strings.Contains(s1, "mirrors.cernet.edu.cn/debian-security") {
		t.Error("security source should NOT be changed to CERNET")
	}
	if !strings.Contains(s1, "security.debian.org") {
		t.Error("security.debian.org should remain in sources.list")
	}

	// Verify extra.list: ubuntu changed, third-party unchanged
	content2, _ := os.ReadFile(extraList)
	s2 := string(content2)
	if !strings.Contains(s2, "mirrors.cernet.edu.cn/ubuntu") {
		t.Error("extra.list should contain CERNET mirror for ubuntu")
	}
	if strings.Contains(s2, "mirrors.cernet.edu.cn") && strings.Contains(s2, "packages.example.com") {
		// The third-party line should remain unchanged
	}
	if !strings.Contains(s2, "packages.example.com") {
		t.Error("third-party source should remain unchanged")
	}

	// Verify extra.sources: ubuntu-ports changed
	content3, _ := os.ReadFile(extraSources)
	s3 := string(content3)
	if !strings.Contains(s3, "mirrors.cernet.edu.cn/ubuntu-ports") {
		t.Error("extra.sources should contain CERNET mirror for ubuntu-ports")
	}
}

func TestRestoreAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.list")

	original := "deb http://deb.debian.org/debian bookworm main\n"
	os.WriteFile(path, []byte(original), 0o644)

	// Modify the file
	switchListFile(path)

	modified, _ := os.ReadFile(path)
	if string(modified) == original {
		t.Error("file should have been modified")
	}

	// Restore with mode preserved
	bk := backupEntry{path: path, content: []byte(original), mode: 0o644}
	restoreAll([]backupEntry{bk})

	restored, _ := os.ReadFile(path)
	if string(restored) != original {
		t.Errorf("file should be restored to original, got: %s", restored)
	}

	// Verify mode is preserved (not 0000)
	info, _ := os.Stat(path)
	if info.Mode().Perm() == 0 {
		t.Errorf("restored file mode should not be 0000, got: %v", info.Mode())
	}
}

func TestBackupFile_PreservesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.list")
	os.WriteFile(path, []byte("test"), 0o755)

	bk, err := backupFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bk.mode != 0o755 {
		t.Errorf("backupFile mode = %v, want 0o755", bk.mode)
	}
}

func TestIsSecuritySource(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"deb http://security.debian.org/debian-security bookworm-security main", true},
		{"deb http://security.ubuntu.com/ubuntu jammy-security main", true},
		{"deb http://deb.debian.org/debian-security bookworm-security main", true},
		{"deb http://deb.debian.org/debian bookworm main", false},
		{"deb http://archive.ubuntu.com/ubuntu jammy main", false},
	}
	for _, tt := range tests {
		if got := isSecuritySource(tt.line); got != tt.want {
			t.Errorf("isSecuritySource(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func TestRewriteListLine_DebianSecurityPath(t *testing.T) {
	// deb.debian.org/debian-security must NOT be rewritten
	input := "deb http://deb.debian.org/debian-security bookworm-security main"
	got := rewriteListLine(input)
	if got != input {
		t.Errorf("rewriteListLine(%q) = %q, should be unchanged", input, got)
	}
}

func TestRewriteListLine_DebianSecurityPath_Vs_MainRepo(t *testing.T) {
	// Main repo must still be rewritten
	input := "deb http://deb.debian.org/debian bookworm main"
	want := "deb https://mirrors.cernet.edu.cn/debian bookworm main"
	got := rewriteListLine(input)
	if got != want {
		t.Errorf("rewriteListLine(%q) = %q, want %q", input, got, want)
	}
}

func TestRewriteSourcesContent_DebianSecurityPath(t *testing.T) {
	input := "Types: deb\nURIs: http://deb.debian.org/debian-security\nSuites: bookworm-security\nComponents: main\n"
	got := rewriteSourcesContent(input)
	if got != input {
		t.Errorf("rewriteSourcesContent should not change deb.debian.org/debian-security, got:\n%s", got)
	}
}

func TestRewriteSourcesContent_MultipleStanzas(t *testing.T) {
	input := "Types: deb\nURIs: http://deb.debian.org/debian\nSuites: bookworm\nComponents: main\n\nTypes: deb\nURIs: http://security.debian.org/debian-security\nSuites: bookworm-security\nComponents: main\n"
	got := rewriteSourcesContent(input)
	// First stanza should be changed, second (security) should not
	if !strings.Contains(got, "mirrors.cernet.edu.cn/debian") {
		t.Error("first stanza should be rewritten to CERNET")
	}
	if strings.Contains(got, "mirrors.cernet.edu.cn/debian-security") {
		t.Error("security stanza should NOT be rewritten")
	}
	if !strings.Contains(got, "security.debian.org") {
		t.Error("security.debian.org should remain")
	}
}
