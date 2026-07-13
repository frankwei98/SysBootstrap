package system

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// Result holds the output of a command execution.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// FormatCommandError builds a compact, user-facing command failure summary.
// It prefers stderr, then stdout, and always includes the exit code when known.
func FormatCommandError(action string, res *Result, err error) error {
	if err != nil && res == nil {
		if derived := ResultFromError(err); derived != nil {
			res = derived
		}
	}
	if err != nil && res == nil {
		return fmt.Errorf("%s: %w", action, err)
	}

	var parts []string
	if res != nil && res.ExitCode != 0 {
		parts = append(parts, fmt.Sprintf("exit %d", res.ExitCode))
	}
	if summary := summarizeCommandOutput(res); summary != "" {
		parts = append(parts, summary)
	}
	if err != nil {
		parts = append(parts, err.Error())
	}

	if len(parts) == 0 {
		return fmt.Errorf("%s failed", action)
	}
	return fmt.Errorf("%s: %s", action, strings.Join(parts, " — "))
}

// ResultFromError extracts a minimal command Result from an execution error.
// It is mainly useful for interactive commands where stdout/stderr are already
// attached to the terminal and only the exit code can still be surfaced.
func ResultFromError(err error) *Result {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return &Result{ExitCode: exitErr.ExitCode()}
	}
	return nil
}

func summarizeCommandOutput(res *Result) string {
	if res == nil {
		return ""
	}
	summary := strings.TrimSpace(res.Stderr)
	if summary == "" {
		summary = strings.TrimSpace(res.Stdout)
	}
	if summary == "" {
		return ""
	}
	return truncateForError(summary, 300)
}

func truncateForError(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return strings.TrimSpace(s[:maxLen]) + "...(truncated)"
}

// Run executes a command and captures its output.
func Run(name string, args ...string) (*Result, error) {
	return RunWithContext(context.Background(), name, args...)
}

// RunWithContext executes a command with context cancellation support.
// The entire process group is killed on cancellation, and the context error
// is preserved for the caller to distinguish cancellation from normal failure.
func RunWithContext(ctx context.Context, name string, args ...string) (*Result, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start %s: %w", name, err)
	}

	// Cancellation handler: kill the entire process group
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if cmd.Process != nil {
				// Negative PID kills the process group
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-done:
		}
	}()

	err := cmd.Wait()
	close(done)

	result := &Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		// Prefer context error for cancellation; original error is wrapped
		if ctx.Err() != nil {
			return result, fmt.Errorf("%s cancelled: %w", name, ctx.Err())
		}
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
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start %s: %w", name, err)
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if cmd.Process != nil {
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-done:
		}
	}()

	err := cmd.Wait()
	close(done)

	result := &Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		if ctx.Err() != nil {
			return result, fmt.Errorf("%s cancelled: %w", name, ctx.Err())
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return result, fmt.Errorf("failed to run %s: %w", name, err)
		}
	}

	return result, nil
}

// RunAsUserWithInputContext executes a command as the invoking non-root user
// when the process is running as root via sudo. Otherwise it runs directly.
func RunAsUserWithInputContext(ctx context.Context, sys *Context, input string, name string, args ...string) (*Result, error) {
	if sys == nil || sys.InvokingUser == nil {
		return RunWithInputContext(ctx, input, name, args...)
	}

	sudoArgs := []string{"-H", "-u", sys.InvokingUser.Username, "env", "HOME=" + sys.InvokingUser.HomeDir, name}
	sudoArgs = append(sudoArgs, args...)
	return RunWithInputContext(ctx, input, "sudo", sudoArgs...)
}

// RunAsUserWithInput executes a command as the invoking non-root user when needed.
func RunAsUserWithInput(sys *Context, input string, name string, args ...string) (*Result, error) {
	return RunAsUserWithInputContext(context.Background(), sys, input, name, args...)
}

// RunQuiet executes a command discarding output, returning only error status.
func RunQuiet(name string, args ...string) bool {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// RunInteractiveContext executes a command attached to the current terminal.
func RunInteractiveContext(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// RunInteractive executes a command attached to the current terminal.
func RunInteractive(name string, args ...string) error {
	return RunInteractiveContext(context.Background(), name, args...)
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

// TargetUser returns the user that should receive user-level installs.
func TargetUser(sys *Context) *user.User {
	if sys != nil && sys.InvokingUser != nil {
		return sys.InvokingUser
	}
	if sys != nil && sys.CurrentUser != nil {
		return sys.CurrentUser
	}
	u, _ := user.Current()
	return u
}

// TargetHomeDir returns the home directory that should receive user-level installs.
func TargetHomeDir(sys *Context) string {
	if u := TargetUser(sys); u != nil && u.HomeDir != "" {
		return u.HomeDir
	}
	home, _ := os.UserHomeDir()
	return home
}

// TargetUsername returns the username that should receive user-level installs.
func TargetUsername(sys *Context) string {
	if u := TargetUser(sys); u != nil && u.Username != "" {
		return u.Username
	}
	return ""
}

// NvmDirForContext returns the NVM directory for the user-level install target.
func NvmDirForContext(sys *Context) string {
	if d := os.Getenv("NVM_DIR"); d != "" && (sys == nil || sys.InvokingUser == nil) {
		return d
	}
	return filepath.Join(TargetHomeDir(sys), ".nvm")
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

// NvmShellScriptForContext wraps a script with nvm paths for the user-level target.
func NvmShellScriptForContext(sys *Context, script string) string {
	return fmt.Sprintf(`export NVM_DIR="%s"
export PNPM_HOME="${PNPM_HOME:-$HOME/.local/share/pnpm}"
export BUN_INSTALL="${BUN_INSTALL:-$HOME/.bun}"
export PATH="$PNPM_HOME:$PNPM_HOME/bin:$BUN_INSTALL/bin:$PATH"
[ -s "$NVM_DIR/nvm.sh" ] && source "$NVM_DIR/nvm.sh"
%s`, NvmDirForContext(sys), script)
}

// NvmShellScriptForContextInHome wraps a script with nvm paths and executes it
// from the target user's home directory to avoid project-directory interference
// with global package manager commands.
func NvmShellScriptForContextInHome(sys *Context, script string) string {
	home := TargetHomeDir(sys)
	if home == "" {
		return NvmShellScriptForContext(sys, script)
	}
	return NvmShellScriptForContext(sys, fmt.Sprintf("cd %s\n%s", shellQuote(home), script))
}

// RunInNvmShell executes a script in a bash shell with nvm sourced.
func RunInNvmShell(script string) (*Result, error) {
	return RunWithInput("", "bash", "-c", NvmShellScript(script))
}

// RunInNvmShellForContext executes an nvm-aware shell as the user-level target.
func RunInNvmShellForContext(sys *Context, script string) (*Result, error) {
	return RunAsUserWithInput(sys, "", "bash", "-c", NvmShellScriptForContext(sys, script))
}

// RunInNvmShellForContextInHome executes an nvm-aware shell as the user-level
// target after first changing into that user's home directory.
func RunInNvmShellForContextInHome(sys *Context, script string) (*Result, error) {
	return RunAsUserWithInput(sys, "", "bash", "-c", NvmShellScriptForContextInHome(sys, script))
}

// NvmCommandExists checks if a binary is available inside an nvm-aware shell.
func NvmCommandExists(name string) bool {
	res, err := RunInNvmShell(fmt.Sprintf("command -v %s", name))
	return err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) != ""
}

// NvmCommandExistsForContext checks command availability for the user-level target.
func NvmCommandExistsForContext(sys *Context, name string) bool {
	res, err := RunInNvmShellForContext(sys, fmt.Sprintf("command -v %s", name))
	return err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) != ""
}

// RetryConfig defines bounded retry behavior for idempotent operations.
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	Timeout     time.Duration
}

// RunApt executes apt-get with one bounded, noninteractive policy. Acquire
// retries are handled by apt itself; locally modified conffiles are preserved.
func RunApt(ctx context.Context, args ...string) (*Result, error) {
	opCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	aptArgs := []string{
		"DEBIAN_FRONTEND=noninteractive",
		"APT_LISTCHANGES_FRONTEND=none",
		"apt-get",
		"-o", "DPkg::Lock::Timeout=120",
		"-o", "Acquire::Retries=3",
		"-o", "Dpkg::Options::=--force-confold",
	}
	aptArgs = append(aptArgs, args...)
	return RunWithContext(opCtx, "env", aptArgs...)
}

// RunWithRetry runs a command with bounded retries. Retries only for failures
// that appear idempotent (network, transient). Cancellation and context
// deadline errors are not retried.
func RunWithRetry(ctx context.Context, cfg RetryConfig, name string, args ...string) (*Result, error) {
	var lastErr error
	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		if attempt > 0 {
			delay := time.NewTimer(cfg.BaseDelay * time.Duration(1<<uint(attempt-1)))
			select {
			case <-ctx.Done():
				delay.Stop()
				return nil, ctx.Err()
			case <-delay.C:
			}
		}
		opCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
		res, err := RunWithContext(opCtx, name, args...)
		cancel()
		if err == nil && res.ExitCode == 0 {
			return res, nil
		}
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return res, err
			}
			lastErr = err
		} else {
			lastErr = fmt.Errorf("%s exited with code %d", name, res.ExitCode)
		}
	}
	return nil, fmt.Errorf("%s: all %d attempts failed: %w", name, cfg.MaxAttempts, lastErr)
}
