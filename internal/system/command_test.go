package system

import (
	"errors"
	"os/user"
	"path/filepath"
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

func TestTargetHomeDirUsesInvokingUser(t *testing.T) {
	sys := &Context{
		InvokingUser: &user.User{
			Username: "frank",
			HomeDir:  "/home/frank",
		},
	}

	if got := TargetHomeDir(sys); got != "/home/frank" {
		t.Errorf("TargetHomeDir() = %q, want /home/frank", got)
	}
	if got := TargetUsername(sys); got != "frank" {
		t.Errorf("TargetUsername() = %q, want frank", got)
	}
}

func TestNvmDirForContextIgnoresRootNvmDirForInvokingUser(t *testing.T) {
	t.Setenv("NVM_DIR", "/root/.nvm")
	sys := &Context{
		InvokingUser: &user.User{
			Username: "frank",
			HomeDir:  "/home/frank",
		},
	}

	want := filepath.Join("/home/frank", ".nvm")
	if got := NvmDirForContext(sys); got != want {
		t.Errorf("NvmDirForContext() = %q, want %q", got, want)
	}
}

func TestNvmShellScriptForContextInHomeChangesDirectory(t *testing.T) {
	sys := &Context{
		CurrentUser: &user.User{
			Username: "frank",
			HomeDir:  "/home/frank",
		},
	}

	script := NvmShellScriptForContextInHome(sys, "pnpm install -g @openai/codex")
	if !strings.Contains(script, "cd '/home/frank'") {
		t.Fatalf("expected script to change into target home, got:\n%s", script)
	}
	if !strings.Contains(script, "pnpm install -g @openai/codex") {
		t.Fatalf("expected original command to remain in script, got:\n%s", script)
	}
}

func TestFormatCommandError(t *testing.T) {
	err := FormatCommandError("download failed", &Result{
		Stderr:   "curl: failed to connect",
		ExitCode: 7,
	}, nil)

	got := err.Error()
	if !strings.Contains(got, "download failed") {
		t.Fatalf("missing action in error: %s", got)
	}
	if !strings.Contains(got, "exit 7") {
		t.Fatalf("missing exit code in error: %s", got)
	}
	if !strings.Contains(got, "curl: failed to connect") {
		t.Fatalf("missing stderr summary in error: %s", got)
	}
}

func TestFormatCommandErrorFallsBackToWrappedError(t *testing.T) {
	err := FormatCommandError("command failed", nil, errors.New("exec: not found"))
	if !strings.Contains(err.Error(), "exec: not found") {
		t.Fatalf("expected wrapped error, got: %v", err)
	}
}

func TestResultFromErrorExitCode(t *testing.T) {
	res, err := Run("bash", "-lc", "exit 23")
	if err != nil {
		t.Fatalf("Run returned unexpected transport error: %v", err)
	}
	derived := ResultFromError(errors.New("not an exit error"))
	if derived != nil {
		t.Fatal("non-exit errors should not derive a result")
	}
	if res.ExitCode != 23 {
		t.Fatalf("run result exit code = %d, want 23", res.ExitCode)
	}
}
