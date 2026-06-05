package system

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Result holds the output of a command execution.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Run executes a command and captures its output.
func Run(name string, args ...string) (*Result, error) {
	return RunWithContext(context.Background(), name, args...)
}

// RunWithContext executes a command with context cancellation support.
func RunWithContext(ctx context.Context, name string, args ...string) (*Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := &Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return result, fmt.Errorf("failed to run %s: %w", name, err)
		}
	}

	return result, nil
}

// RunWithInput executes a command with stdin input.
func RunWithInput(input string, name string, args ...string) (*Result, error) {
	return RunWithInputContext(context.Background(), input, name, args...)
}

// RunWithInputContext executes a command with stdin input and context cancellation.
func RunWithInputContext(ctx context.Context, input string, name string, args ...string) (*Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := &Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return result, fmt.Errorf("failed to run %s: %w", name, err)
		}
	}

	return result, nil
}

// RunQuiet executes a command discarding output, returning only error status.
func RunQuiet(name string, args ...string) bool {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// RunQuietOutput executes a command and returns trimmed stdout.
func RunQuietOutput(name string, args ...string) string {
	res, err := Run(name, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// DpkgInstalled checks if a Debian package is installed.
func DpkgInstalled(pkg string) bool {
	return RunQuiet("dpkg", "-s", pkg)
}

// CommandExists checks if a command is available in PATH.
func CommandExists(name string) bool {
	return commandExists(name)
}

// NvmDir returns the NVM directory, defaulting to ~/.nvm.
func NvmDir() string {
	if d := os.Getenv("NVM_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".nvm")
}

// NvmShellScript wraps a script body with nvm.sh sourcing so that
// node/pnpm/bun installed via nvm are available on PATH.
func NvmShellScript(script string) string {
	return fmt.Sprintf(`export NVM_DIR="%s"
export PNPM_HOME="${PNPM_HOME:-$HOME/.local/share/pnpm}"
export BUN_INSTALL="${BUN_INSTALL:-$HOME/.bun}"
export PATH="$PNPM_HOME:$PNPM_HOME/bin:$BUN_INSTALL/bin:$PATH"
[ -s "$NVM_DIR/nvm.sh" ] && source "$NVM_DIR/nvm.sh"
%s`, NvmDir(), script)
}

// RunInNvmShell executes a script in a bash shell with nvm sourced.
func RunInNvmShell(script string) (*Result, error) {
	return RunWithInput("", "bash", "-c", NvmShellScript(script))
}

// NvmCommandExists checks if a binary is available inside an nvm-aware shell.
func NvmCommandExists(name string) bool {
	res, err := RunInNvmShell(fmt.Sprintf("command -v %s", name))
	return err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) != ""
}
