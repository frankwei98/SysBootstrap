package modules

import (
	"context"
	"fmt"

	"github.com/FrankWiZe/sys-bootstrap/internal/logging"
	"github.com/FrankWiZe/sys-bootstrap/internal/system"
	"github.com/FrankWiZe/sys-bootstrap/internal/types"
)

type AIModule struct{}

func NewAIModule() *AIModule { return &AIModule{} }

func (m *AIModule) ID() string            { return "ai" }
func (m *AIModule) Name() string          { return "AI CLI Tools" }
func (m *AIModule) Description() string   { return "Claude Code and Codex" }
func (m *AIModule) DefaultEnabled() bool  { return false }
func (m *AIModule) RequiresRoot() bool    { return false }
func (m *AIModule) Dependencies() []string { return []string{"node"} }

func (m *AIModule) Check(ctx context.Context, sys *system.Context) CheckResult {
	if !system.CommandExists("node") {
		return CheckResult{Satisfied: false, Message: "Node.js not installed (run node module first)"}
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
	if !system.CommandExists("node") {
		return fmt.Errorf("Node.js is not installed — please run the node module first")
	}

	pm := "npm"
	if system.CommandExists("pnpm") {
		pm = "pnpm"
	}
	log.Infof("Using %s for installation", pm)

	installClaude := cfg.InstallClaudeCode || (!cfg.InstallClaudeCode && !cfg.InstallCodex)
	installCodex := cfg.InstallCodex || (!cfg.InstallClaudeCode && !cfg.InstallCodex)

	if installClaude {
		log.Info("Installing Claude Code...")
		if res, err := system.Run(pm, "install", "-g", "@anthropic-ai/claude-code"); err != nil || res.ExitCode != 0 {
			return fmt.Errorf("Claude Code installation failed: %s", res.Stderr)
		}
		log.Success("Claude Code installed")
	}

	if installCodex {
		log.Info("Installing Codex...")
		if res, err := system.Run(pm, "install", "-g", "@openai/codex"); err != nil || res.ExitCode != 0 {
			return fmt.Errorf("Codex installation failed: %s", res.Stderr)
		}
		log.Success("Codex installed")
	}

	return nil
}
