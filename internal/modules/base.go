package modules

import (
	"context"
	"fmt"
	"strings"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

var basePackages = []string{
	"sudo", "zsh", "gnupg", "apt-transport-https",
	"git", "curl", "wget", "unzip", "tree", "neovim",
}

type BaseModule struct{}

func NewBaseModule() *BaseModule { return &BaseModule{} }

func (m *BaseModule) ID() string             { return "base" }
func (m *BaseModule) Name() string           { return "Base Environment" }
func (m *BaseModule) Description() string    { return "System update and essential packages" }
func (m *BaseModule) DefaultEnabled() bool   { return true }
func (m *BaseModule) RequiresRoot() bool     { return true }
func (m *BaseModule) Dependencies() []string { return nil }

func (m *BaseModule) Check(ctx context.Context, sys *system.Context) CheckResult {
	installedMap := detectBasePackages()
	installed, missing := summarizeBasePackages(installedMap)
	hasZellij := system.CommandExists("zellij")

	if len(missing) == 0 && hasZellij {
		return CheckResult{Satisfied: true, Message: "All base packages and zellij installed"}
	}
	return CheckResult{
		Satisfied: false,
		Message:   buildBaseCheckMessage(installed, missing, hasZellij),
	}
}

func (m *BaseModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	var steps []types.Step
	installedMap := detectBasePackages()
	_, missing := summarizeBasePackages(installedMap)
	hasZellij := system.CommandExists("zellij")

	if cfg.AptMirror == "cernet" {
		steps = append(steps, types.Step{
			Module: "base",
			Title:  "Switch APT mirror to CERNET",
			Detail: "Rewrite Debian/Ubuntu official sources to mirrors.cernet.edu.cn (security sources unchanged)",
		})
	}
	steps = append(steps,
		types.Step{Module: "base", Title: "Run apt update & upgrade", Detail: "Update package lists and upgrade installed packages"},
	)
	steps = append(steps, buildBasePackageSteps(missing)...)
	if !hasZellij {
		steps = append(steps, types.Step{
			Module: "base",
			Title:  "Install zellij",
			Detail: "Terminal multiplexer via GitHub release binary",
		})
	}
	return steps, nil
}

func (m *BaseModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	// Switch APT mirror to CERNET if requested (before apt-get update)
	aptUpdateDone := false
	if cfg.AptMirror == "cernet" {
		log.Info("Switching APT mirror to CERNET...")
		result, restoreFunc, err := system.SwitchAPTMirrorToCernet()
		if err != nil {
			return fmt.Errorf("APT mirror switch failed: %w", err)
		}
		if len(result.ChangedFiles) > 0 {
			log.Successf("APT mirror switched to CERNET (%d file(s) modified)", len(result.ChangedFiles))
			for _, f := range result.ChangedFiles {
				log.Infof("  Modified: %s", f)
			}

			// Verify with apt-get update
			log.Info("Verifying APT sources with apt-get update...")
			res, updateErr := system.RunWithContext(ctx, "apt-get", "update", "-y")
			if updateErr != nil || res.ExitCode != 0 {
				log.Warn("apt-get update failed after mirror switch, restoring backup...")
				if restoreErr := restoreFunc(); restoreErr != nil {
					return fmt.Errorf("APT mirror switch failed and restore also failed: update error: %v (restore error: %v)", updateErr, restoreErr)
				}
				log.Info("APT sources restored from backup")
				// Re-run apt-get update with original sources
				if res, err := system.RunWithContext(ctx, "apt-get", "update", "-y"); err != nil || res.ExitCode != 0 {
					return system.FormatCommandError("apt-get update failed after restore", res, err)
				}
			}
			aptUpdateDone = true
		} else {
			log.Info("No APT sources matched for CERNET mirror switch")
		}
	}

	if !aptUpdateDone {
		log.Info("Running apt update & upgrade...")
		if res, err := system.RunWithContext(ctx, "apt-get", "update", "-y"); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("apt-get update failed", res, err)
		}
	} else {
		log.Info("Running apt upgrade...")
	}

	if res, err := system.RunWithContext(ctx, "apt-get", "upgrade", "-y"); err != nil || res.ExitCode != 0 {
		return system.FormatCommandError("apt-get upgrade failed", res, err)
	}
	log.Success("System update complete")

	installedMap := detectBasePackages()
	installed, missing := summarizeBasePackages(installedMap)
	if len(installed) > 0 {
		log.Infof("Already installed base packages: %s", strings.Join(installed, ", "))
	}
	if len(missing) == 0 {
		log.Info("All base packages already installed, skipping package installation")
	} else {
		log.Infof("Installing missing base packages: %s", strings.Join(missing, ", "))
		args := make([]string, 0, len(missing)+2)
		args = append(args, "install", "-y")
		args = append(args, missing...)
		if res, err := system.RunWithContext(ctx, "apt-get", args...); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("package installation failed", res, err)
		}
		log.Success("Missing base packages installed")
	}

	if system.CommandExists("zellij") {
		log.Info("zellij already installed, skipping")
	} else {
		log.Info("Installing zellij...")
		if err := installZellij(ctx); err != nil {
			return err
		}
		log.Success("zellij installed")
	}

	return nil
}

func detectBasePackages() map[string]bool {
	status := make(map[string]bool, len(basePackages))
	for _, pkg := range basePackages {
		status[pkg] = system.DpkgInstalled(pkg)
	}
	return status
}

func summarizeBasePackages(status map[string]bool) (installed []string, missing []string) {
	for _, pkg := range basePackages {
		if status[pkg] {
			installed = append(installed, pkg)
		} else {
			missing = append(missing, pkg)
		}
	}
	return installed, missing
}

func buildBaseCheckMessage(installed []string, missing []string, hasZellij bool) string {
	var parts []string
	if len(installed) > 0 {
		parts = append(parts, "Installed packages: "+strings.Join(installed, ", "))
	}
	if len(missing) > 0 {
		parts = append(parts, "Missing packages: "+strings.Join(missing, ", "))
	}
	if hasZellij {
		parts = append(parts, "zellij installed")
	} else {
		parts = append(parts, "zellij missing")
	}
	return strings.Join(parts, ". ")
}

func buildBasePackageSteps(missing []string) []types.Step {
	if len(missing) == 0 {
		return nil
	}

	var steps []types.Step
	for _, pkg := range missing {
		steps = append(steps, types.Step{
			Module: "base",
			Title:  "Install base packages",
			Detail: pkg,
			Status: "pending",
		})
	}
	return steps
}

func installZellij(ctx context.Context) error {
	arch := "x86_64"
	if strings.Contains(system.RunQuietOutput("uname", "-m"), "aarch64") || strings.Contains(system.RunQuietOutput("uname", "-m"), "arm64") {
		arch = "aarch64"
	}

	url := fmt.Sprintf("https://github.com/zellij-org/zellij/releases/latest/download/zellij-%s-unknown-linux-musl.tar.gz", arch)
	script := fmt.Sprintf(`
set -euo pipefail
tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT
curl -fsSL %q -o "$tmpdir/zellij.tar.gz"
tar -xzf "$tmpdir/zellij.tar.gz" -C "$tmpdir"
install -m 0755 "$tmpdir/zellij" /usr/local/bin/zellij
`, url)

	if res, err := system.RunWithContext(ctx, "bash", "-lc", script); err != nil || res.ExitCode != 0 {
		return system.FormatCommandError("zellij installation failed", res, err)
	}

	if !system.CommandExists("zellij") {
		return fmt.Errorf("zellij installation completed but zellij is still not available on PATH")
	}

	return nil
}
