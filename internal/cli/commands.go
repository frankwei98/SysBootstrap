package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/FrankWiZe/sys-bootstrap/internal/app"
	"github.com/FrankWiZe/sys-bootstrap/internal/logging"
	"github.com/FrankWiZe/sys-bootstrap/internal/modules"
	"github.com/FrankWiZe/sys-bootstrap/internal/system"
	"github.com/FrankWiZe/sys-bootstrap/internal/types"
	"github.com/FrankWiZe/sys-bootstrap/internal/ui"
	"github.com/charmbracelet/huh"
)

// DoctorResult holds the outcome of a doctor check.
type DoctorResult struct {
	HasFatal bool
}

// RunCmd handles the `run` command (interactive execution).
func RunCmd(registry *modules.Registry) error {
	ctx := context.Background()

	sys, err := system.NewContext()
	if err != nil {
		return fmt.Errorf("system detection failed: %w", err)
	}

	log, err := logging.New(false)
	if err != nil {
		return fmt.Errorf("logger init failed: %w", err)
	}
	defer log.Close()

	// Select modules
	selected, err := ui.ModuleSelect(registry)
	if err != nil {
		return err
	}

	// Check if ai is selected without node
	hasAI := false
	hasNode := false
	for _, id := range selected {
		if id == "ai" {
			hasAI = true
		}
		if id == "node" {
			hasNode = true
		}
	}
	if hasAI && !hasNode {
		log.Warn("AI module requires Node.js — adding node to selection")
		selected = append([]string{"node"}, selected...)
	}

	// Always include base (mandatory)
	selected = append([]string{"base"}, selected...)

	// Resolve order
	ordered, err := registry.ResolveOrder(selected)
	if err != nil {
		return err
	}

	// Collect config from forms
	cfg := &types.Config{SSHPort: 22122}

	for _, id := range ordered {
		switch id {
		case "ssh":
			if err := ui.SSHConfigForm(cfg, sys); err != nil {
				return err
			}
		case "ai":
			if err := ui.AIConfigForm(cfg); err != nil {
				return err
			}
		case "user":
			if err := ui.UserConfigForm(cfg); err != nil {
				return err
			}
		case "ssh_keygen":
			if err := ui.SSHKeygenForm(cfg); err != nil {
				return err
			}
		}
	}

	// Generate and show plan
	plan, err := app.GeneratePlan(ctx, sys, cfg, registry, ordered)
	if err != nil {
		return err
	}

	planText := app.FormatPlanText(plan)

	// Confirm execution
	confirmed, err := ui.ConfirmRun(planText)
	if err != nil {
		return err
	}
	if !confirmed {
		log.Warn("Execution cancelled")
		return nil
	}

	// Execute
	runner := app.NewRunner(registry, sys, log)
	if err := runner.Run(ctx, cfg, ordered); err != nil {
		return err
	}

	log.Success("All done!")
	return nil
}

// PlanCmd handles the `plan` command.
func PlanCmd(registry *modules.Registry, jsonOutput bool) error {
	ctx := context.Background()

	sys, err := system.NewContext()
	if err != nil {
		return fmt.Errorf("system detection failed: %w", err)
	}

	// Include all module IDs in registration order
	ids := registry.IDs()

	cfg := &types.Config{
		SSHPort:     22122,
		SSHAllowUFW: sys.HasUFW && sys.UFWActive, // recommended default when UFW is active
	}
	plan, err := app.GeneratePlan(ctx, sys, cfg, registry, ids)
	if err != nil {
		return err
	}

	if jsonOutput {
		json, err := app.FormatPlanJSON(plan)
		if err != nil {
			return err
		}
		fmt.Println(json)
	} else {
		fmt.Print(app.FormatPlanText(plan))
	}

	return nil
}

// DoctorCmd handles the `doctor` command.
// Returns DoctorResult so callers can distinguish fatal vs warning.
func DoctorCmd() (*DoctorResult, error) {
	sys, err := system.NewContext()
	if err != nil {
		fmt.Printf("✗ System detection failed: %v\n", err)
		return &DoctorResult{HasFatal: true}, err
	}

	// Determine if this is a primary-supported distro or an apt-compatible one
	osDetail := fmt.Sprintf("%s %s", sys.OSID, sys.OSVersion)
	primarySupported := (sys.OSID == "debian" && sys.OSVersionMajor >= 11) ||
		(sys.OSID == "ubuntu" && sys.OSVersionMajor >= 22)
	if !primarySupported && sys.HasApt {
		osDetail += " (apt-compatible, untested)"
	}

	checks := []struct {
		name   string
		ok     bool
		detail string
		fatal  bool
	}{
		{"OS ID", sys.OSID != "", sys.OSID, true},
		{"OS Version", sys.OSVersion != "", sys.OSVersion, true},
		{"Supported OS", sys.IsSupportedOS(), osDetail, false},
		{"Architecture", true, sys.Arch, false},
		{"Root", sys.IsRoot, boolStr(sys.IsRoot, "yes", "no (some modules need sudo)"), false},
		{"systemd", sys.HasSystemd, boolStr(sys.HasSystemd, "yes", "not found"), false},
		{"apt-get", sys.HasApt, boolStr(sys.HasApt, "yes", "not found"), true},
		{"bash", sys.HasBash, boolStr(sys.HasBash, "yes", "not found"), false},
		{"curl", sys.HasCurl, boolStr(sys.HasCurl, "yes", "not found"), false},
		{"network", sys.HasNetwork, boolStr(sys.HasNetwork, "DNS OK", "DNS resolution failed"), true},
		{"sshd", sys.HasSSHD, boolStr(sys.HasSSHD, "yes", "not found"), false},
		{"sshd service", sys.HasSSHDService, boolStr(sys.HasSSHDService, "yes", "systemd unit not found"), false},
	}

	result := &DoctorResult{}
	for _, c := range checks {
		icon := "✓"
		if !c.ok {
			if c.fatal {
				icon = "✗"
				result.HasFatal = true
			} else {
				icon = "⚠"
			}
		}
		fmt.Printf("  %s %-15s %s\n", icon, c.name, c.detail)
	}

	if result.HasFatal {
		return result, fmt.Errorf("critical checks failed")
	}
	return result, nil
}

// ModuleCmd handles `module <id>` command.
func ModuleCmd(registry *modules.Registry, moduleID string) error {
	ctx := context.Background()

	sys, err := system.NewContext()
	if err != nil {
		return fmt.Errorf("system detection failed: %w", err)
	}

	log, err := logging.New(false)
	if err != nil {
		return fmt.Errorf("logger init failed: %w", err)
	}
	defer log.Close()

	m, err := registry.Get(moduleID)
	if err != nil {
		return err
	}

	if m.RequiresRoot() && !sys.IsRoot {
		return fmt.Errorf("module %s requires root — please re-run with sudo", m.Name())
	}

	// Check dependencies
	deps := m.Dependencies()
	if len(deps) > 0 {
		var missing []string
		for _, dep := range deps {
			dm, err := registry.Get(dep)
			if err != nil {
				continue
			}
			check := dm.Check(ctx, sys)
			if !check.Satisfied {
				missing = append(missing, dep)
			}
		}
		if len(missing) > 0 {
			log.Warnf("Module %s has unsatisfied dependencies: %s", m.Name(), strings.Join(missing, ", "))
			if !isInteractiveTerminal() {
				return fmt.Errorf("module %s has unsatisfied dependencies (%s); run them first or use an interactive TTY", m.Name(), strings.Join(missing, ", "))
			}
			var confirm bool
			if err := huh.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title(fmt.Sprintf("Run missing dependencies (%s) first?", strings.Join(missing, ", "))).
						Description("Required modules will be executed before this one").
						Value(&confirm),
				),
			).Run(); err != nil {
				return err
			}
			if !confirm {
				return fmt.Errorf("cannot run %s without dependencies: %s", m.Name(), strings.Join(missing, ", "))
			}
			runner := app.NewRunner(registry, sys, log)
			if err := runner.Run(ctx, &types.Config{SSHPort: 22122}, missing); err != nil {
				return fmt.Errorf("dependency %s failed: %w", strings.Join(missing, ", "), err)
			}
		}
	}

	// Collect config
	cfg := &types.Config{SSHPort: 22122}
	switch moduleID {
	case "ssh":
		if !isInteractiveTerminal() {
			return fmt.Errorf("module %s requires an interactive TTY for configuration", m.Name())
		}
		if err := ui.SSHConfigForm(cfg, sys); err != nil {
			return err
		}
	case "ai":
		if isInteractiveTerminal() {
			if err := ui.AIConfigForm(cfg); err != nil {
				return err
			}
		} else {
			cfg.InstallClaudeCode = true
			cfg.InstallCodex = true
		}
	case "user":
		if !isInteractiveTerminal() {
			return fmt.Errorf("module %s requires an interactive TTY for configuration", m.Name())
		}
		if err := ui.UserConfigForm(cfg); err != nil {
			return err
		}
	case "ssh_keygen":
		if !isInteractiveTerminal() {
			return fmt.Errorf("module %s requires an interactive TTY for configuration", m.Name())
		}
		if err := ui.SSHKeygenForm(cfg); err != nil {
			return err
		}
	}

	log.SetModule(m.Name())
	log.Infof("Starting %s...", m.Name())

	check := m.Check(ctx, sys)
	if check.Satisfied {
		log.Successf("%s — already configured, skipping", m.Name())
		return nil
	}

	if err := m.Run(ctx, sys, cfg, log); err != nil {
		return fmt.Errorf("module %s failed: %w", m.Name(), err)
	}

	log.Successf("%s completed", m.Name())
	return nil
}

func isInteractiveTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

// VersionCmd handles the `version` command.
func VersionCmd() {
	v := app.GetVersion()
	fmt.Printf("sys-bootstrap %s\n", v.Version)
	fmt.Printf("  commit:     %s\n", v.Commit)
	fmt.Printf("  built:      %s\n", v.BuildDate)
	fmt.Printf("  go:         %s\n", v.GoVersion)
	fmt.Printf("  platform:   %s/%s\n", v.OS, v.Arch)
}

func boolStr(b bool, trueStr, falseStr string) string {
	if b {
		return trueStr
	}
	return falseStr
}
