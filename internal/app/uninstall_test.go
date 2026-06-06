package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/i18n"
	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
)

func init() {
	i18n.SetLang(i18n.LangEN)
}

// --- ValidatePathSafety tests ---

func TestValidatePathSafety_RejectsEmptyPath(t *testing.T) {
	err := ValidatePathSafety("", "/home/user")
	if err == nil {
		t.Error("expected error for empty path")
	}
}

func TestValidatePathSafety_RejectsRoot(t *testing.T) {
	err := ValidatePathSafety("/", "/home/user")
	if err == nil {
		t.Error("expected error for / path")
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("error should mention root, got: %s", err.Error())
	}
}

func TestValidatePathSafety_RejectsHomeItself(t *testing.T) {
	home := "/home/user"
	err := ValidatePathSafety(home, home)
	if err == nil {
		t.Error("expected error when target equals home")
	}
	if !strings.Contains(err.Error(), "home directory itself") {
		t.Errorf("error should mention home directory, got: %s", err.Error())
	}
}

func TestValidatePathSafety_RejectsHomeTrailingSlash(t *testing.T) {
	home := "/home/user"
	err := ValidatePathSafety(home+"/", home)
	if err == nil {
		t.Error("expected error when target is home with trailing slash")
	}
}

func TestValidatePathSafety_RejectsOutsideHome(t *testing.T) {
	tests := []struct {
		name   string
		target string
		home   string
	}{
		{"sibling", "/home/other", "/home/user"},
		{"parent", "/home", "/home/user"},
		{"absolute other", "/etc/passwd", "/home/user"},
		{"tmp", "/tmp/foo", "/home/user"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePathSafety(tt.target, tt.home)
			if err == nil {
				t.Errorf("expected error for target=%s home=%s", tt.target, tt.home)
			}
		})
	}
}

func TestValidatePathSafety_AllowsInsideHome(t *testing.T) {
	tests := []struct {
		name   string
		target string
		home   string
	}{
		{"nvm", "/home/user/.nvm", "/home/user"},
		{"bun", "/home/user/.bun", "/home/user"},
		{"pnpm share", "/home/user/.local/share/pnpm", "/home/user"},
		{"pnpm config", "/home/user/.config/pnpm", "/home/user"},
		{"nested", "/home/user/.nvm/versions/node/v20", "/home/user"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePathSafety(tt.target, tt.home)
			if err != nil {
				t.Errorf("expected no error for target=%s home=%s, got: %v", tt.target, tt.home, err)
			}
		})
	}
}

func TestValidatePathSafety_RejectsRootHome(t *testing.T) {
	err := ValidatePathSafety("/root", "/root")
	if err == nil {
		t.Error("expected error when target is /root itself")
	}
}

func TestValidatePathSafety_AllowsInsideRootHome(t *testing.T) {
	err := ValidatePathSafety("/root/.nvm", "/root")
	if err != nil {
		t.Errorf("expected no error for /root/.nvm inside /root, got: %v", err)
	}
}

// --- ResolveUserInfo tests ---

func TestResolveUserInfo_ReturnsValidInfo(t *testing.T) {
	info, err := ResolveUserInfo()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Username == "" {
		t.Error("username should not be empty")
	}
	if info.UID == "" {
		t.Error("UID should not be empty")
	}
	if info.HomeDir == "" {
		t.Error("home directory should not be empty")
	}
	if !filepath.IsAbs(info.HomeDir) {
		t.Errorf("home directory should be absolute, got: %s", info.HomeDir)
	}
}

func TestResolveUserInfo_DetectsRoot(t *testing.T) {
	info, err := ResolveUserInfo()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// We might be running as root in CI or as a regular user locally
	if os.Getuid() == 0 {
		if !info.IsRoot {
			t.Error("expected IsRoot=true when uid=0")
		}
	} else {
		if info.IsRoot {
			t.Error("expected IsRoot=false when uid!=0")
		}
	}
}

// --- ScanUninstallItems tests ---

func TestScanUninstallItems_DetectsNVM(t *testing.T) {
	home := t.TempDir()
	nvmDir := filepath.Join(home, ".nvm")
	os.MkdirAll(nvmDir, 0o755)

	t.Setenv("NVM_DIR", nvmDir)

	items := ScanUninstallItems(home)
	found := false
	for _, item := range items {
		if item.ID == "nvm" {
			found = true
			if len(item.Dirs) == 0 {
				t.Error("nvm item should have dirs")
			}
			if item.Dirs[0] != nvmDir {
				t.Errorf("nvm dir = %s, want %s", item.Dirs[0], nvmDir)
			}
		}
	}
	if !found {
		t.Error("expected nvm item in scan results")
	}
}

func TestScanUninstallItems_DetectsBun(t *testing.T) {
	home := t.TempDir()
	bunDir := filepath.Join(home, ".bun")
	os.MkdirAll(bunDir, 0o755)

	t.Setenv("BUN_INSTALL", bunDir)

	items := ScanUninstallItems(home)
	found := false
	for _, item := range items {
		if item.ID == "bun" {
			found = true
			if len(item.Dirs) == 0 {
				t.Error("bun item should have dirs")
			}
		}
	}
	if !found {
		t.Error("expected bun item in scan results")
	}
}

func TestScanUninstallItems_DetectsPnpm(t *testing.T) {
	home := t.TempDir()
	pnpmDir := filepath.Join(home, ".local", "share", "pnpm")
	os.MkdirAll(pnpmDir, 0o755)

	items := ScanUninstallItems(home)
	found := false
	for _, item := range items {
		if item.ID == "pnpm" {
			found = true
			if len(item.Dirs) == 0 {
				t.Error("pnpm item should have dirs")
			}
		}
	}
	if !found {
		t.Error("expected pnpm item in scan results")
	}
}

func TestScanUninstallItems_DetectsPnpmConfig(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "pnpm")
	os.MkdirAll(configDir, 0o755)

	items := ScanUninstallItems(home)
	found := false
	for _, item := range items {
		if item.ID == "pnpm" {
			found = true
			hasConfig := false
			for _, d := range item.Dirs {
				if d == configDir {
					hasConfig = true
				}
			}
			if !hasConfig {
				t.Error("pnpm should include .config/pnpm dir")
			}
		}
	}
	if !found {
		t.Error("expected pnpm item in scan results")
	}
}

func TestScanUninstallItems_DetectsPnpmBothDirs(t *testing.T) {
	home := t.TempDir()
	pnpmDir := filepath.Join(home, ".local", "share", "pnpm")
	configDir := filepath.Join(home, ".config", "pnpm")
	os.MkdirAll(pnpmDir, 0o755)
	os.MkdirAll(configDir, 0o755)

	items := ScanUninstallItems(home)
	for _, item := range items {
		if item.ID == "pnpm" {
			if len(item.Dirs) != 2 {
				t.Errorf("pnpm should have 2 dirs, got %d", len(item.Dirs))
			}
		}
	}
}

func TestScanUninstallItems_AIOnlyWhenInstalled(t *testing.T) {
	home := t.TempDir()
	items := ScanUninstallItems(home)

	// AI items should only appear if the commands actually exist in the nvm shell.
	// In CI/test environments without nvm, these commands won't exist,
	// so we just verify the scan doesn't unconditionally include them.
	hasClaude := false
	hasCodex := false
	for _, item := range items {
		if item.ID == "ai-claude" {
			hasClaude = true
			if item.PkgName != "@anthropic-ai/claude-code" {
				t.Errorf("claude pkg = %q, want @anthropic-ai/claude-code", item.PkgName)
			}
		}
		if item.ID == "ai-codex" {
			hasCodex = true
			if item.PkgName != "@openai/codex" {
				t.Errorf("codex pkg = %q, want @openai/codex", item.PkgName)
			}
		}
	}

	// Verify detection logic: if command exists, item must be present; if not, must be absent.
	// We can't assert presence/absence absolutely since it depends on the test environment.
	if hasClaude && !system.NvmCommandExists("claude") {
		t.Error("ai-claude included but 'claude' command not found in nvm shell")
	}
	if hasCodex && !system.NvmCommandExists("codex") {
		t.Error("ai-codex included but 'codex' command not found in nvm shell")
	}
	if !hasClaude && system.NvmCommandExists("claude") {
		t.Error("ai-claude not included but 'claude' command exists in nvm shell")
	}
	if !hasCodex && system.NvmCommandExists("codex") {
		t.Error("ai-codex not included but 'codex' command exists in nvm shell")
	}
}

// --- FilterInstalledItems tests ---

func TestFilterInstalledItems_WithDirs(t *testing.T) {
	items := []UninstallItem{
		{ID: "nvm", Dirs: []string{"/home/user/.nvm"}},
		{ID: "bun", Dirs: nil},
	}
	filtered := FilterInstalledItems(items)
	if len(filtered) != 1 {
		t.Errorf("expected 1 item (nvm has dirs), got %d", len(filtered))
	}
	if filtered[0].ID != "nvm" {
		t.Errorf("expected nvm, got %s", filtered[0].ID)
	}
}

func TestFilterInstalledItems_WithPkgManager(t *testing.T) {
	items := []UninstallItem{
		{ID: "ai-claude", PkgName: "@anthropic-ai/claude-code", PkgManager: "pnpm"},
	}
	filtered := FilterInstalledItems(items)
	if len(filtered) != 1 {
		t.Errorf("expected 1 item, got %d", len(filtered))
	}
}

func TestFilterInstalledItems_EmptyDirsNoPkg(t *testing.T) {
	items := []UninstallItem{
		{ID: "ai-codex", PkgName: "@openai/codex", PkgManager: ""},
		{ID: "empty", Dirs: nil, PkgName: "", PkgManager: ""},
	}
	filtered := FilterInstalledItems(items)
	if len(filtered) != 1 {
		t.Errorf("expected 1 item (known package without pkg manager should remain), got %d", len(filtered))
	}
	if filtered[0].ID != "ai-codex" {
		t.Errorf("expected ai-codex, got %s", filtered[0].ID)
	}
}

// --- BuildUninstallPlan tests ---

func TestBuildUninstallPlan_CollectsDirs(t *testing.T) {
	home := t.TempDir()
	nvmDir := filepath.Join(home, ".nvm")
	os.MkdirAll(nvmDir, 0o755)

	items := []UninstallItem{
		{ID: "nvm", Dirs: []string{nvmDir}},
	}
	plan := BuildUninstallPlan(items, home)
	if len(plan.DirsToDelete) != 1 {
		t.Errorf("expected 1 dir, got %d", len(plan.DirsToDelete))
	}
	if plan.DirsToDelete[0] != nvmDir {
		t.Errorf("dir = %s, want %s", plan.DirsToDelete[0], nvmDir)
	}
}

func TestBuildUninstallPlan_SkipsUnsafePaths(t *testing.T) {
	home := t.TempDir()
	items := []UninstallItem{
		{ID: "bad", Dirs: []string{"/etc"}},
	}
	plan := BuildUninstallPlan(items, home)
	if len(plan.DirsToDelete) != 0 {
		t.Errorf("expected 0 dirs (unsafe path), got %d", len(plan.DirsToDelete))
	}
}

func TestBuildUninstallPlan_CollectsCommands(t *testing.T) {
	home := t.TempDir()
	items := []UninstallItem{
		{ID: "ai-claude", PkgName: "@anthropic-ai/claude-code", PkgManager: "pnpm"},
		{ID: "ai-codex", PkgName: "@openai/codex", PkgManager: "npm"},
	}
	plan := BuildUninstallPlan(items, home)
	if len(plan.Commands) != 2 {
		t.Errorf("expected 2 commands, got %d", len(plan.Commands))
	}
	if plan.Commands[0] != "pnpm remove -g @anthropic-ai/claude-code" {
		t.Errorf("cmd[0] = %q", plan.Commands[0])
	}
	if plan.Commands[1] != "npm uninstall -g @openai/codex" {
		t.Errorf("cmd[1] = %q", plan.Commands[1])
	}
}

func TestBuildUninstallPlan_DetectsRCFiles(t *testing.T) {
	home := t.TempDir()
	// Create .bashrc and .zshrc
	os.WriteFile(filepath.Join(home, ".bashrc"), []byte("# bashrc\n"), 0o644)
	os.WriteFile(filepath.Join(home, ".zshrc"), []byte("# zshrc\n"), 0o644)

	plan := BuildUninstallPlan(nil, home)
	if len(plan.RCFiles) != 2 {
		t.Errorf("expected 2 rc files, got %d", len(plan.RCFiles))
	}
}

// --- CleanShellRC tests ---

func TestCleanShellRC_RemovesNVMLines(t *testing.T) {
	home := t.TempDir()
	rcFile := filepath.Join(home, ".bashrc")
	content := `# .bashrc
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && source "$NVM_DIR/nvm.sh"
# other stuff
export PATH="$PATH:/usr/local/bin"
`
	os.WriteFile(rcFile, []byte(content), 0o644)

	// Create backup source
	backupFile := filepath.Join(home, ".bashrc.bak.sys-bootstrap")

	log := createTestLogger(t)
	removed, err := CleanShellRC([]string{rcFile}, []string{"nvm"}, false, log)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 2 {
		t.Errorf("expected 2 lines removed, got %d", removed)
	}

	// Verify backup was created
	if _, err := os.Stat(backupFile); os.IsNotExist(err) {
		t.Error("expected backup file to be created")
	}

	// Verify content was cleaned
	result, _ := os.ReadFile(rcFile)
	resultStr := string(result)
	if strings.Contains(resultStr, "NVM_DIR") {
		t.Error("NVM_DIR line should have been removed")
	}
	if strings.Contains(resultStr, "nvm.sh") {
		t.Error("nvm.sh line should have been removed")
	}
	if !strings.Contains(resultStr, "PATH") {
		t.Error("PATH line should be preserved")
	}
	if !strings.Contains(resultStr, "# other stuff") {
		t.Error("other comments should be preserved")
	}
}

func TestCleanShellRC_RemovesBunLines(t *testing.T) {
	home := t.TempDir()
	rcFile := filepath.Join(home, ".zshrc")
	content := `# .zshrc
export BUN_INSTALL="$HOME/.bun"
export PATH="$BUN_INSTALL/bin:$PATH"
# keep this
`
	os.WriteFile(rcFile, []byte(content), 0o644)

	log := createTestLogger(t)
	removed, err := CleanShellRC([]string{rcFile}, []string{"bun"}, false, log)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 2 {
		t.Errorf("expected 2 lines removed, got %d", removed)
	}

	result, _ := os.ReadFile(rcFile)
	resultStr := string(result)
	if strings.Contains(resultStr, "BUN_INSTALL") {
		t.Error("BUN_INSTALL line should have been removed")
	}
	if strings.Contains(resultStr, ".bun/bin") {
		t.Error(".bun/bin PATH line should have been removed")
	}
	if !strings.Contains(resultStr, "# keep this") {
		t.Error("other comments should be preserved")
	}
}

func TestCleanShellRC_RemovesPnpmLines(t *testing.T) {
	home := t.TempDir()
	rcFile := filepath.Join(home, ".bashrc")
	content := `export PNPM_HOME="$HOME/.local/share/pnpm"
export PATH="$PNPM_HOME:$PATH"
`
	os.WriteFile(rcFile, []byte(content), 0o644)

	log := createTestLogger(t)
	removed, err := CleanShellRC([]string{rcFile}, []string{"pnpm"}, false, log)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 2 {
		t.Errorf("expected 2 lines removed, got %d", removed)
	}

	result, _ := os.ReadFile(rcFile)
	resultStr := string(result)
	if strings.Contains(resultStr, "PNPM_HOME") {
		t.Error("PNPM_HOME line should have been removed")
	}
}

func TestCleanShellRC_DryRunDoesNotModify(t *testing.T) {
	home := t.TempDir()
	rcFile := filepath.Join(home, ".bashrc")
	content := `export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && source "$NVM_DIR/nvm.sh"
`
	os.WriteFile(rcFile, []byte(content), 0o644)

	log := createTestLogger(t)
	removed, err := CleanShellRC([]string{rcFile}, []string{"nvm"}, true, log)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 2 {
		t.Errorf("expected 2 lines counted, got %d", removed)
	}

	// File should be unchanged
	result, _ := os.ReadFile(rcFile)
	if string(result) != content {
		t.Error("dry run should not modify the file")
	}

	// No backup should exist
	backupFile := rcFile + ".bak.sys-bootstrap"
	if _, err := os.Stat(backupFile); !os.IsNotExist(err) {
		t.Error("dry run should not create backup")
	}
}

func TestCleanShellRC_NoMatchNoChange(t *testing.T) {
	home := t.TempDir()
	rcFile := filepath.Join(home, ".bashrc")
	content := `# normal bashrc
export PATH="$PATH:/usr/local/bin"
alias ll='ls -la'
`
	os.WriteFile(rcFile, []byte(content), 0o644)

	log := createTestLogger(t)
	removed, err := CleanShellRC([]string{rcFile}, []string{"nvm", "bun", "pnpm"}, false, log)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 0 {
		t.Errorf("expected 0 lines removed, got %d", removed)
	}

	result, _ := os.ReadFile(rcFile)
	if string(result) != content {
		t.Error("file should be unchanged when no patterns match")
	}
}

func TestCleanShellRC_PreservesNonTargetPatterns(t *testing.T) {
	home := t.TempDir()
	rcFile := filepath.Join(home, ".bashrc")
	content := `export NVM_DIR="$HOME/.nvm"
export BUN_INSTALL="$HOME/.bun"
export PATH="$PATH:/usr/local/bin"
`
	os.WriteFile(rcFile, []byte(content), 0o644)

	// Only clean nvm, not bun
	log := createTestLogger(t)
	removed, err := CleanShellRC([]string{rcFile}, []string{"nvm"}, false, log)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 1 {
		t.Errorf("expected 1 line removed (only nvm), got %d", removed)
	}

	result, _ := os.ReadFile(rcFile)
	resultStr := string(result)
	if strings.Contains(resultStr, "NVM_DIR") {
		t.Error("NVM_DIR should have been removed")
	}
	if !strings.Contains(resultStr, "BUN_INSTALL") {
		t.Error("BUN_INSTALL should be preserved (not in target IDs)")
	}
}

// --- buildPkgRemoveCmd tests ---

func TestBuildPkgRemoveCmd_Pnpm(t *testing.T) {
	cmd := buildPkgRemoveCmd("pnpm", "@anthropic-ai/claude-code")
	if cmd != "pnpm remove -g @anthropic-ai/claude-code" {
		t.Errorf("cmd = %q", cmd)
	}
}

func TestBuildPkgRemoveCmd_Npm(t *testing.T) {
	cmd := buildPkgRemoveCmd("npm", "@openai/codex")
	if cmd != "npm uninstall -g @openai/codex" {
		t.Errorf("cmd = %q", cmd)
	}
}

func TestBuildPkgRemoveCmd_Unknown(t *testing.T) {
	cmd := buildPkgRemoveCmd("yarn", "foo")
	if cmd != "" {
		t.Errorf("expected empty for unknown manager, got %q", cmd)
	}
}

// --- detectRCFiles tests ---

func TestDetectRCFiles_ReturnsExistingFiles(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".bashrc"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(home, ".zshrc"), []byte(""), 0o644)

	files := detectRCFiles(home)
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}
}

func TestDetectRCFiles_SkipsMissingFiles(t *testing.T) {
	home := t.TempDir()
	// Don't create any files
	files := detectRCFiles(home)
	if len(files) != 0 {
		t.Errorf("expected 0 files, got %d", len(files))
	}
}

func TestDetectRCFiles_IncludesProfile(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".profile"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(home, ".bash_profile"), []byte(""), 0o644)

	files := detectRCFiles(home)
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d", len(files))
	}
}

// --- pnpmUserDirs tests ---

func TestPnpmUserDirs_PrimaryOnly(t *testing.T) {
	home := t.TempDir()
	pnpmDir := filepath.Join(home, ".local", "share", "pnpm")
	os.MkdirAll(pnpmDir, 0o755)

	// Clear PNPM_HOME to test default behavior
	t.Setenv("PNPM_HOME", "")

	dirs := pnpmUserDirs(home)
	if len(dirs) != 1 {
		t.Errorf("expected 1 dir, got %d", len(dirs))
	}
	if dirs[0] != pnpmDir {
		t.Errorf("dir = %s, want %s", dirs[0], pnpmDir)
	}
}

func TestPnpmUserDirs_ConfigOnly(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, ".config", "pnpm")
	os.MkdirAll(configDir, 0o755)

	// Clear PNPM_HOME to test default behavior
	t.Setenv("PNPM_HOME", "")

	dirs := pnpmUserDirs(home)
	if len(dirs) != 1 {
		t.Errorf("expected 1 dir, got %d", len(dirs))
	}
	if dirs[0] != configDir {
		t.Errorf("dir = %s, want %s", dirs[0], configDir)
	}
}

func TestPnpmUserDirs_Both(t *testing.T) {
	home := t.TempDir()
	pnpmDir := filepath.Join(home, ".local", "share", "pnpm")
	configDir := filepath.Join(home, ".config", "pnpm")
	os.MkdirAll(pnpmDir, 0o755)
	os.MkdirAll(configDir, 0o755)

	// Clear PNPM_HOME to test default behavior
	t.Setenv("PNPM_HOME", "")

	dirs := pnpmUserDirs(home)
	if len(dirs) != 2 {
		t.Errorf("expected 2 dirs, got %d", len(dirs))
	}
}

func TestPnpmUserDirs_Neither(t *testing.T) {
	home := t.TempDir()

	// Clear PNPM_HOME to test default behavior
	t.Setenv("PNPM_HOME", "")

	dirs := pnpmUserDirs(home)
	if len(dirs) != 0 {
		t.Errorf("expected 0 dirs, got %d", len(dirs))
	}
}

func TestPnpmUserDirs_PnpmHomeEnv(t *testing.T) {
	home := t.TempDir()
	customDir := filepath.Join(home, "custom-pnpm")
	os.MkdirAll(customDir, 0o755)

	t.Setenv("PNPM_HOME", customDir)

	dirs := pnpmUserDirs(home)
	if len(dirs) != 1 {
		t.Errorf("expected 1 dir, got %d", len(dirs))
	}
	if dirs[0] != customDir {
		t.Errorf("dir = %s, want %s", dirs[0], customDir)
	}
}

// --- copyFile tests ---

func TestCopyFile_CreatesBackup(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "original")
	dst := filepath.Join(dir, "backup")
	content := "hello world\n"

	os.WriteFile(src, []byte(content), 0o644)
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("cannot read backup: %v", err)
	}
	if string(result) != content {
		t.Errorf("backup content = %q, want %q", string(result), content)
	}
}

// --- ScanUninstallItems empty environment ---

func TestScanUninstallItems_NothingInstalled(t *testing.T) {
	home := t.TempDir()
	// Don't create any dirs, and point env vars to non-existent paths
	t.Setenv("NVM_DIR", filepath.Join(home, "nonexistent-nvm"))
	t.Setenv("BUN_INSTALL", filepath.Join(home, "nonexistent-bun"))
	t.Setenv("PNPM_HOME", filepath.Join(home, "nonexistent-pnpm"))

	items := ScanUninstallItems(home)
	// With no nvm/bun/pnpm dirs and no claude/codex commands, only AI items
	// might appear if the commands happen to exist in the test environment.
	// Directory-based items must be absent.
	for _, item := range items {
		if item.ID == "nvm" || item.ID == "bun" || item.ID == "pnpm" {
			t.Errorf("unexpected directory-based item %q when nothing is installed", item.ID)
		}
	}
}

// --- ExecuteUninstall tests ---

func TestExecuteUninstall_RemovesDirs(t *testing.T) {
	home := t.TempDir()
	targetDir := filepath.Join(home, ".test-remove")
	os.MkdirAll(targetDir, 0o755)
	os.WriteFile(filepath.Join(targetDir, "file.txt"), []byte("data"), 0o644)

	plan := UninstallPlan{
		Items:        []UninstallItem{{ID: "test", Dirs: []string{targetDir}}},
		DirsToDelete: []string{targetDir},
	}

	log := createTestLogger(t)
	err := ExecuteUninstall(plan, home, false, log)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(targetDir); !os.IsNotExist(err) {
		t.Error("expected directory to be removed")
	}
}

func TestExecuteUninstall_DryRunDoesNotRemove(t *testing.T) {
	home := t.TempDir()
	targetDir := filepath.Join(home, ".test-dryrun")
	os.MkdirAll(targetDir, 0o755)

	plan := UninstallPlan{
		Items:        []UninstallItem{{ID: "test", Dirs: []string{targetDir}}},
		DirsToDelete: []string{targetDir},
	}

	log := createTestLogger(t)
	err := ExecuteUninstall(plan, home, true, log)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(targetDir); os.IsNotExist(err) {
		t.Error("dry run should not remove directory")
	}
}

func TestExecuteUninstall_SkipsUnsafePaths(t *testing.T) {
	home := t.TempDir()
	// Try to remove a path outside home — should be skipped
	plan := UninstallPlan{
		Items:        []UninstallItem{{ID: "bad", Dirs: []string{"/tmp/outside-home"}}},
		DirsToDelete: []string{"/tmp/outside-home"},
	}

	log := createTestLogger(t)
	err := ExecuteUninstall(plan, home, false, log)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should log a warning but not error out
}

func TestExecuteUninstall_CleansRC(t *testing.T) {
	home := t.TempDir()
	rcFile := filepath.Join(home, ".bashrc")
	os.WriteFile(rcFile, []byte("export NVM_DIR=\"$HOME/.nvm\"\nkeep this\n"), 0o644)

	plan := UninstallPlan{
		Items:   []UninstallItem{{ID: "nvm"}},
		RCFiles: []string{rcFile},
	}

	log := createTestLogger(t)
	err := ExecuteUninstall(plan, home, false, log)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	result, _ := os.ReadFile(rcFile)
	if strings.Contains(string(result), "NVM_DIR") {
		t.Error("NVM_DIR should have been cleaned from RC")
	}
	if !strings.Contains(string(result), "keep this") {
		t.Error("unrelated lines should be preserved")
	}
}

func TestExecuteUninstall_SkipsPkgRemovalWhenPkgManagerUnavailable(t *testing.T) {
	home := t.TempDir()
	plan := UninstallPlan{
		Items: []UninstallItem{
			{
				ID:         "ai-claude",
				Name:       "Claude Code",
				PkgName:    "@anthropic-ai/claude-code",
				PkgManager: "",
			},
		},
	}

	log := createTestLogger(t)
	err := ExecuteUninstall(plan, home, false, log)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- FormatUninstallPlan tests ---

func TestFormatUninstallPlan_WithDirsAndCommands(t *testing.T) {
	plan := UninstallPlan{
		DirsToDelete: []string{"/home/user/.nvm", "/home/user/.bun"},
		Commands:     []string{"pnpm remove -g @anthropic-ai/claude-code"},
		RCFiles:      []string{"/home/user/.zshrc"},
	}

	result := FormatUninstallPlan(plan)
	if !strings.Contains(result, "/home/user/.nvm") {
		t.Error("should contain nvm dir")
	}
	if !strings.Contains(result, "/home/user/.bun") {
		t.Error("should contain bun dir")
	}
	if !strings.Contains(result, "pnpm remove -g") {
		t.Error("should contain pnpm command")
	}
	if !strings.Contains(result, "/home/user/.zshrc") {
		t.Error("should contain rc file")
	}
}

func TestFormatUninstallPlan_EmptyPlan(t *testing.T) {
	plan := UninstallPlan{}
	result := FormatUninstallPlan(plan)
	if result != "" {
		t.Errorf("expected empty string for empty plan, got: %q", result)
	}
}

func TestFormatUninstallPlan_DirsOnly(t *testing.T) {
	plan := UninstallPlan{
		DirsToDelete: []string{"/home/user/.nvm"},
	}
	result := FormatUninstallPlan(plan)
	if !strings.Contains(result, "/home/user/.nvm") {
		t.Error("should contain dir")
	}
	if strings.Contains(result, "Commands") {
		t.Error("should not contain commands section when empty")
	}
	if strings.Contains(result, "RC") {
		t.Error("should not contain RC section when empty")
	}
}

// --- Helper to create a test logger ---

func createTestLogger(t *testing.T) *logging.Logger {
	t.Helper()
	log, err := logging.New(true) // quiet mode for tests
	if err != nil {
		t.Fatalf("cannot create logger: %v", err)
	}
	t.Cleanup(func() { log.Close() })
	return log
}
