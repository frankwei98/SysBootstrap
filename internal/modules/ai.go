package modules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FrankWiZe/sys-bootstrap/internal/logging"
	"github.com/FrankWiZe/sys-bootstrap/internal/system"
	"github.com/FrankWiZe/sys-bootstrap/internal/types"
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
	if _, err := os.Stat(filepath.Join(system.NvmDir(), "nvm.sh")); err != nil {
		return CheckResult{Satisfied: false, Message: "Node.js not installed (run node module first)"}
	}
	if !system.NvmCommandExists("node") {
		return CheckResult{Satisfied: false, Message: "Node.js not installed (run node module first)"}
	}

	hasClaude := system.NvmCommandExists("claude")
	hasCodex := system.NvmCommandExists("codex")
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
	if _, err := os.Stat(filepath.Join(system.NvmDir(), "nvm.sh")); err != nil {
		return fmt.Errorf("Node.js is not installed — please run the node module first")
	}
	if !system.NvmCommandExists("node") {
		return fmt.Errorf("Node.js is not installed — please run the node module first")
	}

	// Detect package manager inside nvm-aware shell
	pm := "npm"
	if system.NvmCommandExists("pnpm") {
		pm = "pnpm"
	} else {
		log.Warn("pnpm not found in nvm environment, falling back to npm")
	}
	log.Infof("Using %s for installation", pm)

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
		if res, err := system.RunInNvmShell(script); err != nil || res.ExitCode != 0 {
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
		if res, err := system.RunInNvmShell(script); err != nil || res.ExitCode != 0 {
			return fmt.Errorf("Codex installation failed: %s", res.Stderr)
		}
		log.Success("Codex installed")
	}

	return nil
}
