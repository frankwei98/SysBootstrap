package modules

import (
	"archive/zip"
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

func TestExtractBunFromZipRejectsSymlinkDestinationWithoutModifyingTarget(t *testing.T) {
	replacement := []byte("replacement bun binary")
	zipPath := writeTestBunZip(t, replacement)

	externalPath := filepath.Join(t.TempDir(), "must-not-change")
	original := []byte("external file contents")
	if err := os.WriteFile(externalPath, original, 0o600); err != nil {
		t.Fatalf("write external target: %v", err)
	}

	destPath := filepath.Join(t.TempDir(), "bun")
	if err := os.Symlink(externalPath, destPath); err != nil {
		t.Skipf("cannot create destination symlink: %v", err)
	}

	err := extractBunFromZip(zipPath, destPath)
	if err == nil {
		t.Error("extractBunFromZip accepted a symlink destination")
	}
	got, readErr := os.ReadFile(externalPath)
	if readErr != nil {
		t.Fatalf("read external target after extraction: %v", readErr)
	}
	if string(got) != string(original) {
		t.Fatalf("external target was modified through destination symlink: got %q, want %q", got, original)
	}
	info, statErr := os.Lstat(destPath)
	if statErr != nil {
		t.Fatalf("lstat destination symlink after extraction: %v", statErr)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("destination symlink was replaced; mode = %v", info.Mode())
	}
}

func TestExtractBunFromZipAtomicallyReplacesExistingFileWithoutModifyingHardlinkTarget(t *testing.T) {
	replacement := []byte("replacement bun binary")
	zipPath := writeTestBunZip(t, replacement)

	dir := t.TempDir()
	externalPath := filepath.Join(dir, "external")
	original := []byte("external file contents")
	if err := os.WriteFile(externalPath, original, 0o600); err != nil {
		t.Fatalf("write external target: %v", err)
	}
	destPath := filepath.Join(dir, "bun")
	if err := os.Link(externalPath, destPath); err != nil {
		t.Skipf("cannot create destination hardlink: %v", err)
	}

	if err := extractBunFromZip(zipPath, destPath); err != nil {
		t.Fatalf("extractBunFromZip failed: %v", err)
	}
	external, err := os.ReadFile(externalPath)
	if err != nil {
		t.Fatalf("read hardlink target after extraction: %v", err)
	}
	if string(external) != string(original) {
		t.Fatalf("hardlink target was modified in place: got %q, want %q", external, original)
	}
	dest, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read extracted bun destination: %v", err)
	}
	if string(dest) != string(replacement) {
		t.Fatalf("bun destination = %q, want %q", dest, replacement)
	}
}

func writeTestBunZip(t *testing.T, content []byte) string {
	t.Helper()

	zipPath := filepath.Join(t.TempDir(), "bun.zip")
	zipFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create bun zip: %v", err)
	}
	zipWriter := zip.NewWriter(zipFile)
	entry, err := zipWriter.Create("bun-linux-x64/bun")
	if err != nil {
		t.Fatalf("create bun zip entry: %v", err)
	}
	if _, err := entry.Write(content); err != nil {
		t.Fatalf("write bun zip entry: %v", err)
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("close bun zip writer: %v", err)
	}
	if err := zipFile.Close(); err != nil {
		t.Fatalf("close bun zip: %v", err)
	}
	return zipPath
}

func TestNodeModuleCheckNoNvm(t *testing.T) {
	t.Setenv("NVM_DIR", filepath.Join(t.TempDir(), ".nvm"))

	// In CI/test environment, nvm is not installed.
	// Check should return Satisfied=false with descriptive message.
	m := NewNodeModule()

	if m.ID() != "node" {
		t.Errorf("ID() = %q, want node", m.ID())
	}
	if m.RequiresRoot() {
		t.Error("node module should not require root")
	}
	if m.Dependencies() != nil {
		t.Errorf("Dependencies() = %v, want nil", m.Dependencies())
	}

	result := m.Check(context.Background(), &system.Context{}, nil)
	// In test env without nvm, should not be satisfied
	if result.Satisfied {
		t.Log("nvm is installed in test environment — skipping unsatisfied check")
	} else {
		if result.Message == "" {
			t.Error("expected non-empty check message")
		}
	}
}

func TestNodeRunRejectsMissingCurlBeforeMutatingHome(t *testing.T) {
	originalCommandExists := nodeCommandExistsFn
	nodeCommandExistsFn = func(name string) bool {
		return name == "bash"
	}
	t.Cleanup(func() { nodeCommandExistsFn = originalCommandExists })

	home := t.TempDir()
	t.Setenv("NVM_DIR", filepath.Join(home, ".nvm"))
	zshrc := filepath.Join(home, ".zshrc")
	wantZshrc := []byte("# existing user configuration\n")
	if err := os.WriteFile(zshrc, wantZshrc, 0o644); err != nil {
		t.Fatalf("write existing .zshrc: %v", err)
	}

	tempBin := t.TempDir()
	bashMarker := filepath.Join(t.TempDir(), "bash-called")
	fakeBash := "#!/bin/sh\n: > \"" + bashMarker + "\"\nexit 99\n"
	if err := os.WriteFile(filepath.Join(tempBin, "bash"), []byte(fakeBash), 0o755); err != nil {
		t.Fatalf("write fake bash: %v", err)
	}
	t.Setenv("PATH", tempBin)

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New failed: %v", err)
	}
	t.Cleanup(log.Close)

	err = NewNodeModule().Run(context.Background(), &system.Context{
		CurrentUser: &user.User{Username: "testuser", HomeDir: home},
	}, &types.Config{}, log)
	if err == nil {
		t.Fatal("expected missing curl to be rejected")
	}
	if !strings.Contains(err.Error(), "install curl") {
		t.Fatalf("error = %q, want curl installation guidance", err)
	}

	gotZshrc, readErr := os.ReadFile(zshrc)
	if readErr != nil {
		t.Fatalf("read existing .zshrc after failure: %v", readErr)
	}
	if string(gotZshrc) != string(wantZshrc) {
		t.Fatalf(".zshrc changed before dependency validation: got %q, want %q", gotZshrc, wantZshrc)
	}
	for _, path := range []string{
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".profile"),
		filepath.Join(home, ".nvm"),
		bashMarker,
	} {
		if _, statErr := os.Lstat(path); !os.IsNotExist(statErr) {
			t.Fatalf("%s was created before dependency validation; stat error = %v", path, statErr)
		}
	}
}

func TestAIModuleInterface(t *testing.T) {
	m := NewAIModule()

	if m.ID() != "ai" {
		t.Errorf("ID() = %q, want ai", m.ID())
	}
	if m.RequiresRoot() {
		t.Error("ai module should not require root")
	}
	deps := m.Dependencies()
	if len(deps) != 1 || deps[0] != "node" {
		t.Errorf("Dependencies() = %v, want [node]", deps)
	}
}

func TestRequestedAIToolsHonorsNilAndExplicitEmptySelection(t *testing.T) {
	claude, codex := requestedAITools(nil)
	if claude || codex {
		t.Fatalf("nil selection = (%v, %v), want neither tool", claude, codex)
	}

	claude, codex = requestedAITools(&types.Config{AISelectionSet: true})
	if claude || codex {
		t.Fatalf("explicit empty selection = (%v, %v), want neither tool", claude, codex)
	}
	claude, codex = requestedAITools(&types.Config{})
	if !claude || !codex {
		t.Fatalf("legacy/unset selection = (%v, %v), want both default tools", claude, codex)
	}
}

func TestAIModuleCheckRequiresNode(t *testing.T) {
	t.Setenv("NVM_DIR", filepath.Join(t.TempDir(), ".nvm"))

	m := NewAIModule()
	result := m.Check(context.Background(), &system.Context{}, &types.Config{})
	if result.Satisfied {
		t.Error("ai module should not be satisfied without node")
	}
}

func TestAIModuleNilConfigIsNoOp(t *testing.T) {
	m := NewAIModule()
	sys := &system.Context{}
	ctx := context.Background()

	check := m.Check(ctx, sys, nil)
	if !check.Satisfied {
		t.Fatalf("nil config Check = %+v, want satisfied no-op", check)
	}

	steps, err := m.Plan(ctx, sys, nil)
	if err != nil {
		t.Fatalf("nil config Plan failed: %v", err)
	}
	if len(steps) != 0 {
		t.Fatalf("nil config Plan = %v, want no steps", steps)
	}

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New() failed: %v", err)
	}
	t.Cleanup(log.Close)
	if err := m.Run(ctx, sys, nil, log); err != nil {
		t.Fatalf("nil config Run failed: %v", err)
	}
}

func TestEnsurePnpmShellPathWritesStartupFiles(t *testing.T) {
	home := t.TempDir()
	sys := &system.Context{
		CurrentUser: &user.User{
			Username: "testuser",
			HomeDir:  home,
		},
	}

	if pnpmShellPathConfigured(sys) {
		t.Fatal("pnpm shell path should not be configured before writing rc files")
	}
	if err := ensurePnpmShellPath(sys); err != nil {
		t.Fatalf("ensurePnpmShellPath failed: %v", err)
	}
	if !pnpmShellPathConfigured(sys) {
		t.Fatal("pnpm shell path should be configured after writing rc files")
	}

	zshrc := filepath.Join(home, ".zshrc")
	content, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatalf("reading .zshrc failed: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, `export PNPM_HOME="${PNPM_HOME:-$HOME/.local/share/pnpm}"`) {
		t.Errorf(".zshrc missing PNPM_HOME export: %s", text)
	}
	if !strings.Contains(text, `export PATH="$PNPM_HOME/bin:$PNPM_HOME:$PATH"`) {
		t.Errorf(".zshrc missing PNPM_HOME PATH export: %s", text)
	}

	if err := ensurePnpmShellPath(sys); err != nil {
		t.Fatalf("second ensurePnpmShellPath failed: %v", err)
	}
	content, err = os.ReadFile(zshrc)
	if err != nil {
		t.Fatalf("reading .zshrc after second run failed: %v", err)
	}
	if count := strings.Count(string(content), "SYS_BOOTSTRAP_PNPM_HOME"); count != 1 {
		t.Errorf("expected one pnpm marker after second run, got %d", count)
	}
}

func TestEnsurePnpmShellPathRequiresEveryStartupFile(t *testing.T) {
	home := t.TempDir()
	sys := &system.Context{
		CurrentUser: &user.User{
			Username: "testuser",
			HomeDir:  home,
		},
	}

	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte(`# SYS_BOOTSTRAP_PNPM_HOME
export PNPM_HOME="${PNPM_HOME:-$HOME/.local/share/pnpm}"
export PATH="$PNPM_HOME/bin:$PNPM_HOME:$PATH"
`), 0o644); err != nil {
		t.Fatalf("writing .bashrc failed: %v", err)
	}
	if pnpmShellPathConfigured(sys) {
		t.Fatal("pnpm shell path should require zsh, bash, and profile startup files")
	}
}

func TestPnpmShellFileConfiguredRejectsOrphanedMarker(t *testing.T) {
	if pnpmShellFileConfigured("# SYS_BOOTSTRAP_PNPM_HOME\n") {
		t.Fatal("orphaned marker must not be treated as a configured pnpm environment")
	}
}

func TestEnsurePnpmShellPathRepairsOrphanedMarker(t *testing.T) {
	home := t.TempDir()
	sys := &system.Context{CurrentUser: &user.User{Username: "testuser", HomeDir: home}}
	for _, name := range []string{".zshrc", ".bashrc", ".profile"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("# SYS_BOOTSTRAP_PNPM_HOME\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	if err := ensurePnpmShellPath(sys); err != nil {
		t.Fatalf("ensurePnpmShellPath failed: %v", err)
	}
	if !pnpmShellPathConfigured(sys) {
		t.Fatal("pnpm shell path should be configured after repairing orphaned markers")
	}
}

func TestEnsureNodeShellPathWritesStartupFiles(t *testing.T) {
	home := t.TempDir()
	sys := &system.Context{
		CurrentUser: &user.User{
			Username: "testuser",
			HomeDir:  home,
		},
	}

	if nodeShellPathConfigured(sys) {
		t.Fatal("node shell path should not be configured before writing rc files")
	}
	if err := ensureNodeShellPath(sys); err != nil {
		t.Fatalf("ensureNodeShellPath failed: %v", err)
	}
	if !nodeShellPathConfigured(sys) {
		t.Fatal("node shell path should be configured after writing rc files")
	}

	zshrc := filepath.Join(home, ".zshrc")
	content, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatalf("reading .zshrc failed: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, `export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"`) {
		t.Errorf(".zshrc missing NVM_DIR export: %s", text)
	}
	if !strings.Contains(text, `[ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"`) {
		t.Errorf(".zshrc missing nvm loader: %s", text)
	}
	if !strings.Contains(text, `export BUN_INSTALL="${BUN_INSTALL:-$HOME/.bun}"`) {
		t.Errorf(".zshrc missing BUN_INSTALL export: %s", text)
	}
	if !strings.Contains(text, `export PATH="$BUN_INSTALL/bin:$PATH"`) {
		t.Errorf(".zshrc missing bun PATH export: %s", text)
	}

	if err := ensureNodeShellPath(sys); err != nil {
		t.Fatalf("second ensureNodeShellPath failed: %v", err)
	}
	content, err = os.ReadFile(zshrc)
	if err != nil {
		t.Fatalf("reading .zshrc after second run failed: %v", err)
	}
	if count := strings.Count(string(content), "SYS_BOOTSTRAP_NODE_ENV"); count != 1 {
		t.Errorf("expected one node marker after second run, got %d", count)
	}
}

func TestEnsureNodeShellPathRestoresBunWithoutDuplicatingNodeEnvironment(t *testing.T) {
	home := t.TempDir()
	sys := &system.Context{
		CurrentUser: &user.User{
			Username: "testuser",
			HomeDir:  home,
		},
	}
	remainingNodeSetup := `# SYS_BOOTSTRAP_NODE_ENV
export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"
[ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"
`
	for _, name := range []string{".zshrc", ".bashrc", ".profile"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte(remainingNodeSetup), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	if err := ensureNodeShellPath(sys); err != nil {
		t.Fatalf("ensureNodeShellPath failed: %v", err)
	}
	for _, rcFile := range nodeShellRCFiles(sys) {
		content, err := os.ReadFile(rcFile)
		if err != nil {
			t.Fatalf("read %s: %v", rcFile, err)
		}
		text := string(content)
		for _, line := range []string{
			`export BUN_INSTALL="${BUN_INSTALL:-$HOME/.bun}"`,
			`export PATH="$BUN_INSTALL/bin:$PATH"`,
		} {
			if !strings.Contains(text, line) {
				t.Errorf("%s missing restored line %q:\n%s", rcFile, line, text)
			}
		}
		for _, line := range []string{
			"SYS_BOOTSTRAP_NODE_ENV",
			`export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"`,
			`[ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"`,
		} {
			if count := strings.Count(text, line); count != 1 {
				t.Errorf("%s contains %d copies of %q, want 1:\n%s", rcFile, count, line, text)
			}
		}
	}
}

func TestNodeShellFileConfiguredRejectsOrphanedMarker(t *testing.T) {
	if nodeShellFileConfigured("# SYS_BOOTSTRAP_NODE_ENV\n") {
		t.Fatal("orphaned marker must not be treated as a configured Node environment")
	}
}

func TestAIModuleDependencyResolution(t *testing.T) {
	r := NewRegistry()
	r.Register(NewBaseModule())
	r.Register(NewSSHModule())
	r.Register(NewNodeModule())
	r.Register(NewAIModule())
	r.Register(NewUserModule())
	r.Register(NewSSHKeygenModule())

	// Resolving just "ai" should pull in "node" (but not "base", since node doesn't depend on base)
	ordered, err := r.ResolveOrder([]string{"ai"})
	if err != nil {
		t.Fatalf("ResolveOrder failed: %v", err)
	}

	// node must appear before ai
	nodeIdx, aiIdx := -1, -1
	for i, id := range ordered {
		if id == "node" {
			nodeIdx = i
		}
		if id == "ai" {
			aiIdx = i
		}
	}
	if nodeIdx == -1 {
		t.Fatal("node not found in resolved order")
	}
	if aiIdx == -1 {
		t.Fatal("ai not found in resolved order")
	}
	if nodeIdx >= aiIdx {
		t.Errorf("node (idx %d) must come before ai (idx %d)", nodeIdx, aiIdx)
	}
}

func TestAIModuleDependencyResolutionWithBase(t *testing.T) {
	r := NewRegistry()
	r.Register(NewBaseModule())
	r.Register(NewSSHModule())
	r.Register(NewNodeModule())
	r.Register(NewAIModule())
	r.Register(NewUserModule())
	r.Register(NewSSHKeygenModule())

	// Resolving "base" + "ai" should produce base → node → ai
	ordered, err := r.ResolveOrder([]string{"base", "ai"})
	if err != nil {
		t.Fatalf("ResolveOrder failed: %v", err)
	}

	expectedOrder := map[string]int{"base": 0, "node": 1, "ai": 2}
	for id, wantIdx := range expectedOrder {
		found := false
		for i, got := range ordered {
			if got == id {
				if i != wantIdx {
					t.Errorf("%s at idx %d, want %d (order: %v)", id, i, wantIdx, ordered)
				}
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s not found in resolved order: %v", id, ordered)
		}
	}
}

func TestAIModuleCheckSatisfiedWithoutPnpm(t *testing.T) {
	home := t.TempDir()
	setupAICheckStubs(t, map[string]bool{"node": true}, map[string]bool{"claude": true, "codex": true})
	writeFakeNvmScript(t, home)

	m := NewAIModule()
	result := m.Check(context.Background(), &system.Context{
		CurrentUser: &user.User{Username: "testuser", HomeDir: home},
	}, &types.Config{})
	if !result.Satisfied {
		t.Fatalf("AI tools installed without pnpm should be satisfied, got: %+v", result)
	}
}

func TestAIModuleCheckRequiresPnpmShellPathWhenPnpmExists(t *testing.T) {
	home := t.TempDir()
	setupAICheckStubs(t, map[string]bool{"node": true, "pnpm": true}, map[string]bool{"claude": true, "codex": true})
	writeFakeNvmScript(t, home)

	m := NewAIModule()
	result := m.Check(context.Background(), &system.Context{
		CurrentUser: &user.User{Username: "testuser", HomeDir: home},
	}, &types.Config{})
	if result.Satisfied {
		t.Fatalf("AI tools with pnpm but missing startup files should be unsatisfied")
	}
	if !strings.Contains(result.Message, "pnpm global bin is missing") {
		t.Fatalf("unexpected message: %q", result.Message)
	}
}

func TestAIModuleRunUsesHomeSafeShell(t *testing.T) {
	home := t.TempDir()
	writeFakeNvmScript(t, home)

	oldRun := runAIShellForContext
	oldCmdExists := nvmCommandExistsForAI
	oldToolWorks := aiToolWorksForCheck
	runAIShellForContext = func(_ *system.Context, script string) (*system.Result, error) {
		if strings.Contains(script, "install -g @openai/codex") || strings.Contains(script, "codex --version") {
			return &system.Result{ExitCode: 0}, nil
		}
		return &system.Result{ExitCode: 0}, nil
	}
	nvmCommandExistsForAI = func(_ *system.Context, name string) bool {
		return name == "node"
	}
	aiToolWorksForCheck = func(_ *system.Context, _ string) bool {
		return false
	}
	t.Cleanup(func() {
		runAIShellForContext = oldRun
		nvmCommandExistsForAI = oldCmdExists
		aiToolWorksForCheck = oldToolWorks
	})

	m := NewAIModule()
	sys := &system.Context{
		CurrentUser: &user.User{Username: "testuser", HomeDir: home},
	}
	cfg := &types.Config{InstallCodex: true}
	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New() failed: %v", err)
	}
	defer log.Close()

	if err := m.Run(context.Background(), sys, cfg, log); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
}

func setupAICheckStubs(t *testing.T, commands map[string]bool, tools map[string]bool) {
	t.Helper()

	oldCommandExists := nvmCommandExistsForAI
	oldToolWorks := aiToolWorksForCheck
	nvmCommandExistsForAI = func(_ *system.Context, name string) bool {
		return commands[name]
	}
	aiToolWorksForCheck = func(_ *system.Context, name string) bool {
		return tools[name]
	}
	t.Cleanup(func() {
		nvmCommandExistsForAI = oldCommandExists
		aiToolWorksForCheck = oldToolWorks
	})
}

func writeFakeNvmScript(t *testing.T, home string) {
	t.Helper()

	nvmDir := filepath.Join(home, ".nvm")
	if err := os.MkdirAll(nvmDir, 0o755); err != nil {
		t.Fatalf("mkdir nvm dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nvmDir, "nvm.sh"), []byte("# fake nvm\n"), 0o644); err != nil {
		t.Fatalf("write nvm.sh: %v", err)
	}
	t.Setenv("NVM_DIR", nvmDir)
}

func TestShellQuoteSpecialChars(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", "/home/user", "'/home/user'"},
		{"with space", "/home/my user", "'/home/my user'"},
		{"with single quote", "/home/user's dir", "'/home/user'\\''s dir'"},
		{"with dollar", "/home/$USER", "'/home/$USER'"},
		{"with backtick", "/home/`whoami`", "'/home/`whoami`'"},
		{"with parens", "/home/$(id)", "'/home/$(id)'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellQuote(tt.input)
			if got != tt.want {
				t.Errorf("shellQuote(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestEnsurePnpmUserDirsRejectsSymlink(t *testing.T) {
	home := t.TempDir()

	// Create a symlink at ~/.local pointing somewhere else
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(home, ".local")); err != nil {
		t.Fatalf("creating symlink: %v", err)
	}

	sys := &system.Context{
		CurrentUser: &user.User{
			Username: "testuser",
			HomeDir:  home,
		},
	}

	err := ensurePnpmUserDirs(sys)
	if err == nil {
		t.Fatal("expected error when ~/.local is a symlink, got nil")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error should mention symlink, got: %v", err)
	}
}
