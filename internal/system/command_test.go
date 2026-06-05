package system

import (
	"strings"
	"testing"
)

func TestNvmShellScript(t *testing.T) {
	script := NvmShellScript("echo hello")

	if !strings.Contains(script, `export NVM_DIR="`) {
		t.Error("expected NVM_DIR export")
	}
	if !strings.Contains(script, `source "$NVM_DIR/nvm.sh"`) {
		t.Error("expected nvm.sh source")
	}
	if !strings.Contains(script, `export PNPM_HOME="${PNPM_HOME:-$HOME/.local/share/pnpm}"`) {
		t.Error("expected PNPM_HOME export")
	}
	if !strings.Contains(script, `export BUN_INSTALL="${BUN_INSTALL:-$HOME/.bun}"`) {
		t.Error("expected BUN_INSTALL export")
	}
	if !strings.Contains(script, `export PATH="$PNPM_HOME:$PNPM_HOME/bin:$BUN_INSTALL/bin:$PATH"`) {
		t.Error("expected PATH export for pnpm and bun")
	}
	if !strings.Contains(script, "echo hello") {
		t.Error("expected original script body")
	}
	// Verify ordering: NVM_DIR export before source, source before script body
	idxExport := strings.Index(script, `export NVM_DIR=`)
	idxSource := strings.Index(script, `source "$NVM_DIR/nvm.sh"`)
	idxBody := strings.Index(script, "echo hello")
	if idxExport >= idxSource || idxSource >= idxBody {
		t.Error("expected export → source → body ordering")
	}
}

func TestNvmShellScriptCustomDir(t *testing.T) {
	t.Setenv("NVM_DIR", "/custom/nvm")
	script := NvmShellScript("node --version")

	if !strings.Contains(script, `/custom/nvm`) {
		t.Errorf("expected custom NVM_DIR, got: %s", script)
	}
}

func TestNvmDirDefault(t *testing.T) {
	t.Setenv("NVM_DIR", "")
	dir := NvmDir()
	if !strings.HasSuffix(dir, ".nvm") {
		t.Errorf("expected .nvm suffix, got: %s", dir)
	}
}

func TestNvmDirCustom(t *testing.T) {
	t.Setenv("NVM_DIR", "/opt/nvm")
	dir := NvmDir()
	if dir != "/opt/nvm" {
		t.Errorf("expected /opt/nvm, got: %s", dir)
	}
}
