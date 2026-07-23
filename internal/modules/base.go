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

	if len(missing) == 0 {
		return CheckResult{Satisfied: true, Message: "All base packages installed"}
	}
	return CheckResult{
		Satisfied: false,
		Message:   buildBaseCheckMessage(installed, missing),
	}
}

func (m *BaseModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	var steps []types.Step
	installedMap := detectBasePackages()
	_, missing := summarizeBasePackages(installedMap)

	if cfg.AptMirror == "cernet" {
		steps = append(steps, types.Step{
			Module: "base",
			Title:  "Switch APT mirror to CERNET",
			Detail: "Rewrite Debian/Ubuntu official sources to mirrors.cernet.edu.cn (security sources unchanged)",
		})
	}
	if len(missing) > 0 || cfg.AptMirror == "cernet" {
		steps = append(steps,
			types.Step{Module: "base", Title: "Run apt update & upgrade", Detail: "Update package lists and upgrade installed packages"},
		)
	}
	steps = append(steps, buildBasePackageSteps(missing)...)
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
			res, updateErr := system.RunApt(ctx, "update", "-y")
			if updateErr != nil || res == nil || res.ExitCode != 0 {
				log.Warn("apt-get update failed after mirror switch, restoring backup...")
				if restoreErr := restoreFunc(); restoreErr != nil {
					return fmt.Errorf("APT mirror switch failed and restore also failed: update error: %v (restore error: %v)", updateErr, restoreErr)
				}
				log.Info("APT sources restored from backup")
				// Re-run apt-get update with original sources.
				if res, err := system.RunApt(ctx, "update", "-y"); err != nil || res == nil || res.ExitCode != 0 {
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
		if res, err := system.RunApt(ctx, "update", "-y"); err != nil || res == nil || res.ExitCode != 0 {
			return system.FormatCommandError("apt-get update failed", res, err)
		}
	} else {
		log.Info("Running apt upgrade...")
	}

	if res, err := system.RunApt(ctx, "upgrade", "-y"); err != nil || res == nil || res.ExitCode != 0 {
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
		if res, err := system.RunApt(ctx, args...); err != nil || res == nil || res.ExitCode != 0 {
			return system.FormatCommandError("package installation failed", res, err)
		}
		log.Success("Missing base packages installed")
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

func buildBaseCheckMessage(installed []string, missing []string) string {
	var parts []string
	if len(installed) > 0 {
		parts = append(parts, "Installed packages: "+strings.Join(installed, ", "))
	}
	if len(missing) > 0 {
		parts = append(parts, "Missing packages: "+strings.Join(missing, ", "))
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
