package modules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

type AIModule struct{}

func NewAIModule() *AIModule { return &AIModule{} }

var (
	aiToolWorksForCheck        = aiToolWorks
	nvmCommandExistsForAICheck = system.NvmCommandExistsForContext
)

func (m *AIModule) ID() string             { return "ai" }
func (m *AIModule) Name() string           { return "AI CLI Tools" }
func (m *AIModule) Description() string    { return "Claude Code and Codex" }
func (m *AIModule) DefaultEnabled() bool   { return false }
func (m *AIModule) RequiresRoot() bool     { return false }
func (m *AIModule) Dependencies() []string { return []string{"node"} }

func (m *AIModule) Check(ctx context.Context, sys *system.Context) CheckResult {
	if _, err := os.Stat(filepath.Join(system.NvmDirForContext(sys), "nvm.sh")); err != nil {
		return CheckResult{Satisfied: false, Message: "Node.js not installed (run node module first)"}
	}
	if !nvmCommandExistsForAICheck(sys, "node") {
		return CheckResult{Satisfied: false, Message: "Node.js not installed (run node module first)"}
	}

	hasClaude := aiToolWorksForCheck(sys, "claude")
	hasCodex := aiToolWorksForCheck(sys, "codex")
	if hasClaude && hasCodex {
		// Only require pnpm shell path if pnpm is actually installed
		if nvmCommandExistsForAICheck(sys, "pnpm") && !pnpmShellPathConfigured(sys) {
			return CheckResult{Satisfied: false, Message: "AI tools installed, but pnpm global bin is missing from shell startup files"}
		}
		return CheckResult{Satisfied: true, Message: "Claude Code and Codex installed"}
	}
	if hasClaude || hasCodex {
		return CheckResult{Satisfied: false, Message: "Only part of the AI toolchain is installed"}
	}

	return CheckResult{Satisfied: false, Message: "AI tools not yet installed"}
}

func (m *AIModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	var steps []types.Step
	installClaude := cfg.InstallClaudeCode || (!cfg.InstallClaudeCode && !cfg.InstallCodex)
	installCodex := cfg.InstallCodex || (!cfg.InstallClaudeCode && !cfg.InstallCodex)
	pmDetail := "pnpm when available, otherwise npm"

	if installClaude && !aiToolWorksForCheck(sys, "claude") {
		steps = append(steps, types.Step{Module: "ai", Title: "Install Claude Code", Detail: "@anthropic-ai/claude-code — " + pmDetail})
	}
	if installCodex && !aiToolWorksForCheck(sys, "codex") {
		steps = append(steps, types.Step{Module: "ai", Title: "Install Codex", Detail: "@openai/codex — " + pmDetail})
	}
	if nvmCommandExistsForAICheck(sys, "pnpm") && !pnpmShellPathConfigured(sys) {
		steps = append(steps, types.Step{Module: "ai", Title: "Update shell startup", Detail: "Add PNPM_HOME to shell rc files"})
	}
	return steps, nil
}

func (m *AIModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	if _, err := os.Stat(filepath.Join(system.NvmDirForContext(sys), "nvm.sh")); err != nil {
		return fmt.Errorf("Node.js is not installed — please run the node module first")
	}
	if !system.NvmCommandExistsForContext(sys, "node") {
		return fmt.Errorf("Node.js is not installed — please run the node module first")
	}

	// Detect package manager inside nvm-aware shell
	pm := "npm"
	if system.NvmCommandExistsForContext(sys, "pnpm") {
		pm = "pnpm"
	} else {
		log.Warn("pnpm not found in nvm environment, falling back to npm")
	}
	log.Infof("Using %s for installation", pm)
	if pm == "pnpm" {
		if err := ensurePnpmUserDirs(sys); err != nil {
			return err
		}
		if err := ensurePnpmShellPath(sys); err != nil {
			return err
		}
	}

	installClaude := cfg.InstallClaudeCode || (!cfg.InstallClaudeCode && !cfg.InstallCodex)
	installCodex := cfg.InstallCodex || (!cfg.InstallClaudeCode && !cfg.InstallCodex)

	if installClaude {
		log.Info("Installing Claude Code...")
		script := fmt.Sprintf("%s install -g @anthropic-ai/claude-code", pm)
		if pm == "pnpm" {
			script = `mkdir -p "$PNPM_HOME/bin"
pnpm config set global-bin-dir "$PNPM_HOME/bin"
` + script
		}
		if res, err := system.RunInNvmShellForContext(sys, script); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("Claude Code installation failed", res, err)
		}
		if err := verifyClaudeCode(sys, pm, log); err != nil {
			return err
		}
		log.Success("Claude Code installed")
	}

	if installCodex {
		log.Info("Installing Codex...")
		script := fmt.Sprintf("%s install -g @openai/codex", pm)
		if pm == "pnpm" {
			script = `mkdir -p "$PNPM_HOME/bin"
pnpm config set global-bin-dir "$PNPM_HOME/bin"
` + script
		}
		if res, err := system.RunInNvmShellForContext(sys, script); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("Codex installation failed", res, err)
		}
		if err := verifyAITool(sys, "codex"); err != nil {
			return fmt.Errorf("Codex installation verification failed: %w", err)
		}
		log.Success("Codex installed")
	}

	return nil
}

func aiToolWorks(sys *system.Context, name string) bool {
	return verifyAITool(sys, name) == nil
}

func verifyAITool(sys *system.Context, name string) error {
	res, err := system.RunInNvmShellForContext(sys, fmt.Sprintf("%s --version", name))
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		output := strings.TrimSpace(res.Stderr)
		if output == "" {
			output = strings.TrimSpace(res.Stdout)
		}
		if output == "" {
			output = fmt.Sprintf("%s --version exited with code %d", name, res.ExitCode)
		}
		return fmt.Errorf("%s", output)
	}
	return nil
}

func verifyClaudeCode(sys *system.Context, pm string, log *logging.Logger) error {
	if err := verifyAITool(sys, "claude"); err == nil {
		return nil
	} else if pm != "pnpm" {
		return fmt.Errorf("Claude Code installation verification failed: %w", err)
	} else {
		log.Warnf("Claude Code verification failed, running postinstall repair: %v", err)
	}

	if res, err := system.RunInNvmShellForContext(sys, claudeCodePostinstallScript()); err != nil || res.ExitCode != 0 {
		return system.FormatCommandError("Claude Code postinstall repair failed", res, err)
	}
	if err := verifyAITool(sys, "claude"); err != nil {
		return fmt.Errorf("Claude Code installation verification failed after postinstall repair: %w", err)
	}
	return nil
}

func claudeCodePostinstallScript() string {
	return `postinstall="$(find "$PNPM_HOME/store" -path "*/@anthropic-ai/claude-code/install.cjs" -type f | head -n 1)"
if [ -z "$postinstall" ]; then
  postinstall="$(find "$PNPM_HOME/global" -path "*/@anthropic-ai/claude-code/install.cjs" -type f | head -n 1)"
fi
if [ -z "$postinstall" ]; then
  echo "Claude Code install.cjs not found under $PNPM_HOME" >&2
  exit 1
fi
node "$postinstall"`
}

func pnpmShellPathConfigured(sys *system.Context) bool {
	rcFiles := pnpmShellRCFiles(sys)
	if len(rcFiles) == 0 {
		return false
	}
	for _, rcFile := range rcFiles {
		content, err := os.ReadFile(rcFile)
		if err != nil {
			return false
		}
		if !pnpmShellFileConfigured(string(content)) {
			return false
		}
	}
	return true
}

func pnpmShellFileConfigured(content string) bool {
	return strings.Contains(content, "SYS_BOOTSTRAP_PNPM_HOME") || strings.Contains(content, "$PNPM_HOME/bin")
}

func ensurePnpmShellPath(sys *system.Context) error {
	home := system.TargetHomeDir(sys)
	if home == "" {
		return fmt.Errorf("cannot determine target home directory for pnpm shell setup")
	}

	script := fmt.Sprintf(`set -e
export HOME=%s
marker="# SYS_BOOTSTRAP_PNPM_HOME"
for rc in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.profile"; do
  touch "$rc"
  if ! grep -qF "$marker" "$rc"; then
    cat >> "$rc" <<'EOF'

# SYS_BOOTSTRAP_PNPM_HOME
export PNPM_HOME="${PNPM_HOME:-$HOME/.local/share/pnpm}"
export PATH="$PNPM_HOME/bin:$PNPM_HOME:$PATH"
EOF
  fi
done`, shellQuote(home))
	res, err := system.RunAsUserWithInput(sys, "", "bash", "-c", script)
	if err != nil {
		return fmt.Errorf("failed to update shell startup files for pnpm: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("failed to update shell startup files for pnpm: %s", res.Stderr)
	}
	return nil
}

func pnpmShellRCFiles(sys *system.Context) []string {
	home := system.TargetHomeDir(sys)
	if home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".profile"),
	}
}

func ensurePnpmUserDirs(sys *system.Context) error {
	home := system.TargetHomeDir(sys)
	if home == "" {
		return fmt.Errorf("cannot determine target home directory for pnpm")
	}

	// Reject symlinks in parent directories to prevent path traversal
	for _, dir := range []string{
		filepath.Join(home, ".local"),
		filepath.Join(home, ".local", "share"),
		filepath.Join(home, ".config"),
	} {
		if info, err := os.Lstat(dir); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing to modify %s: it is a symlink (potential path traversal)", dir)
			}
		}
	}

	dirs := []string{
		filepath.Join(home, ".local", "share", "pnpm", "bin"),
		filepath.Join(home, ".config", "pnpm"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create pnpm directory %s: %w", dir, err)
		}
	}

	if os.Geteuid() != 0 {
		return nil
	}

	username := system.TargetUsername(sys)
	if username == "" || username == "root" {
		return nil
	}

	targetUser := system.TargetUser(sys)
	owner := fmt.Sprintf("%s:%s", username, username)
	if targetUser != nil && targetUser.Uid != "" && targetUser.Gid != "" {
		owner = fmt.Sprintf("%s:%s", targetUser.Uid, targetUser.Gid)
	}
	parentDirs := []string{
		filepath.Join(home, ".local"),
		filepath.Join(home, ".local", "share"),
		filepath.Join(home, ".config"),
	}
	for _, path := range parentDirs {
		if res, err := system.Run("chown", owner, path); err != nil || res.ExitCode != 0 {
			return fmt.Errorf("failed to chown pnpm directory %s: %s", path, res.Stderr)
		}
	}

	pnpmDirs := []string{
		filepath.Join(home, ".local", "share", "pnpm"),
		filepath.Join(home, ".config", "pnpm"),
	}
	for _, path := range pnpmDirs {
		if res, err := system.Run("chown", "-R", owner, path); err != nil || res.ExitCode != 0 {
			return fmt.Errorf("failed to chown pnpm directory %s: %s", path, res.Stderr)
		}
	}
	return nil
}
