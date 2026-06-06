package modules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

type AIModule struct{}

func NewAIModule() *AIModule { return &AIModule{} }

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
	if !system.NvmCommandExistsForContext(sys, "node") {
		return CheckResult{Satisfied: false, Message: "Node.js not installed (run node module first)"}
	}

	hasClaude := system.NvmCommandExistsForContext(sys, "claude")
	hasCodex := system.NvmCommandExistsForContext(sys, "codex")
	if hasClaude && hasCodex {
		return CheckResult{Satisfied: true, Message: "Claude Code and Codex installed"}
	}
	if hasClaude || hasCodex {
		return CheckResult{Satisfied: false, Message: "Only part of the AI toolchain is installed"}
	}

	return CheckResult{Satisfied: false, Message: "AI tools not yet installed"}
}

func (m *AIModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	var steps []types.Step
	if cfg.InstallClaudeCode {
		steps = append(steps, types.Step{Module: "ai", Title: "Install Claude Code", Detail: "@anthropic-ai/claude-code via pnpm"})
	}
	if cfg.InstallCodex {
		steps = append(steps, types.Step{Module: "ai", Title: "Install Codex", Detail: "@openai/codex via pnpm"})
	}
	if len(steps) == 0 {
		steps = append(steps, types.Step{Module: "ai", Title: "Install AI tools", Detail: "Claude Code and Codex (default)"})
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
			return fmt.Errorf("Claude Code installation failed: %s", res.Stderr)
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
			return fmt.Errorf("Codex installation failed: %s", res.Stderr)
		}
		log.Success("Codex installed")
	}

	return nil
}

func ensurePnpmUserDirs(sys *system.Context) error {
	home := system.TargetHomeDir(sys)
	if home == "" {
		return fmt.Errorf("cannot determine target home directory for pnpm")
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
