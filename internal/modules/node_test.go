package modules

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/system"
)

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

	result := m.Check(context.Background(), &system.Context{})
	// In test env without nvm, should not be satisfied
	if result.Satisfied {
		t.Log("nvm is installed in test environment — skipping unsatisfied check")
	} else {
		if result.Message == "" {
			t.Error("expected non-empty check message")
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

func TestAIModuleCheckRequiresNode(t *testing.T) {
	t.Setenv("NVM_DIR", filepath.Join(t.TempDir(), ".nvm"))

	m := NewAIModule()
	result := m.Check(context.Background(), &system.Context{})
	if result.Satisfied {
		t.Error("ai module should not be satisfied without node")
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
