package modules

import (
	"context"
	"fmt"

	"github.com/FrankWiZe/sys-bootstrap/internal/logging"
	"github.com/FrankWiZe/sys-bootstrap/internal/system"
	"github.com/FrankWiZe/sys-bootstrap/internal/types"
)

var basePackages = []string{
	"sudo", "zsh", "gnupg", "apt-transport-https",
	"git", "curl", "wget", "unzip", "tree", "neovim",
}

type BaseModule struct{}

func NewBaseModule() *BaseModule { return &BaseModule{} }

func (m *BaseModule) ID() string            { return "base" }
func (m *BaseModule) Name() string          { return "Base Environment" }
func (m *BaseModule) Description() string   { return "System update and essential packages" }
func (m *BaseModule) DefaultEnabled() bool  { return true }
func (m *BaseModule) RequiresRoot() bool    { return true }
func (m *BaseModule) Dependencies() []string { return nil }

func (m *BaseModule) Check(ctx context.Context, sys *system.Context) CheckResult {
	allInstalled := true
	for _, pkg := range basePackages {
		if !system.DpkgInstalled(pkg) {
			allInstalled = false
			break
		}
	}
	if allInstalled && system.CommandExists("zellij") {
		return CheckResult{Satisfied: true, Message: "All base packages and zellij installed"}
	}
	return CheckResult{Satisfied: false, Message: "Base packages need installation"}
}

func (m *BaseModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	steps := []types.Step{
		{Module: "base", Title: "Run apt update & upgrade", Detail: "Update package lists and upgrade installed packages"},
		{Module: "base", Title: "Install base packages", Detail: fmt.Sprintf("%v", basePackages)},
	}
	if !system.CommandExists("zellij") {
		steps = append(steps, types.Step{
			Module: "base",
			Title:  "Install zellij",
			Detail: "Terminal multiplexer via official installer",
		})
	}
	return steps, nil
}

func (m *BaseModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	log.Info("Running apt update & upgrade...")
	if res, err := system.RunWithContext(ctx, "apt-get", "update", "-y"); err != nil {
		return fmt.Errorf("apt-get update failed: %w", err)
	} else if res.ExitCode != 0 {
		return fmt.Errorf("apt-get update failed (exit %d): %s", res.ExitCode, res.Stderr)
	}

	if res, err := system.RunWithContext(ctx, "apt-get", "upgrade", "-y"); err != nil {
		return fmt.Errorf("apt-get upgrade failed: %w", err)
	} else if res.ExitCode != 0 {
		return fmt.Errorf("apt-get upgrade failed (exit %d): %s", res.ExitCode, res.Stderr)
	}
	log.Success("System update complete")

	log.Info("Installing base packages...")
	args := make([]string, 0, len(basePackages)+2)
	args = append(args, "install", "-y")
	args = append(args, basePackages...)
	if res, err := system.RunWithContext(ctx, "apt-get", args...); err != nil {
		return fmt.Errorf("package installation failed: %w", err)
	} else if res.ExitCode != 0 {
		return fmt.Errorf("package installation failed (exit %d): %s", res.ExitCode, res.Stderr)
	}
	log.Success("Base packages installed")

	if system.CommandExists("zellij") {
		log.Info("zellij already installed, skipping")
	} else {
		log.Info("Installing zellij...")
		if res, err := system.RunWithInput("", "bash", "-c", "curl -fsSL https://zellij.dev/launch | bash"); err != nil || res.ExitCode != 0 {
			return fmt.Errorf("zellij installation failed")
		}
		log.Success("zellij installed")
	}

	return nil
}
