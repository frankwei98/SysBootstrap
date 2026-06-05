package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/frankwei98/sys-bootstrap/internal/app"
	"github.com/frankwei98/sys-bootstrap/internal/i18n"
	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/modules"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
	"github.com/frankwei98/sys-bootstrap/internal/ui"
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

	// Root user install protection: check before collecting config
	if err := app.CheckRootUserInstall(sys, ordered, isInteractiveTerminal()); err != nil {
		return err
	}

	// Collect config from forms
	cfg := &types.Config{SSHPort: 22122}

	// APT mirror selection: env var override or interactive form
	if !applyAptMirrorEnv(cfg) {
		if err := aptMirrorForm(cfg); err != nil {
			return err
		}
	}

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
		log.Warn(i18n.T("runner_cancelled"))
		return nil
	}

	// Execute
	runner := app.NewRunner(registry, sys, log)
	if err := runner.Run(ctx, cfg, ordered); err != nil {
		return err
	}

	log.Success(i18n.T("runner_all_done"))
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
	applyAptMirrorEnv(cfg)
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
		fmt.Printf("✗ %v\n", err)
		return &DoctorResult{HasFatal: true}, err
	}

	// Determine if this is a primary-supported distro or an apt-compatible one
	osDetail := fmt.Sprintf("%s %s", sys.OSID, sys.OSVersion)
	primarySupported := (sys.OSID == "debian" && sys.OSVersionMajor >= 11) ||
		(sys.OSID == "ubuntu" && sys.OSVersionMajor >= 22)
	if !primarySupported && sys.HasApt {
		osDetail += i18n.T("doctor_os_detail_apt_compat")
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
		{"Root", sys.IsRoot, boolStr(sys.IsRoot, i18n.T("doctor_root_yes"), i18n.T("doctor_root_no")), false},
		{"systemd", sys.HasSystemd, boolStr(sys.HasSystemd, i18n.T("doctor_systemd_yes"), i18n.T("doctor_systemd_no")), false},
		{"apt-get", sys.HasApt, boolStr(sys.HasApt, i18n.T("doctor_apt_yes"), i18n.T("doctor_apt_no")), true},
		{"bash", sys.HasBash, boolStr(sys.HasBash, i18n.T("doctor_bash_yes"), i18n.T("doctor_bash_no")), false},
		{"curl", sys.HasCurl, boolStr(sys.HasCurl, i18n.T("doctor_curl_yes"), i18n.T("doctor_curl_no")), false},
		{"network", sys.HasNetwork, boolStr(sys.HasNetwork, i18n.T("doctor_network_ok"), i18n.T("doctor_network_fail")), true},
		{"sshd", sys.HasSSHD, boolStr(sys.HasSSHD, i18n.T("doctor_sshd_yes"), i18n.T("doctor_sshd_no")), false},
		{"sshd service", sys.HasSSHDService, boolStr(sys.HasSSHDService, i18n.T("doctor_sshd_svc_yes"), i18n.T("doctor_sshd_svc_no")), false},
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
		return fmt.Errorf(i18n.T("module_requires_root"), m.Name())
	}

	// Check dependencies first so root user install covers both the target
	// module and any dependencies that will be auto-run.
	deps := m.Dependencies()
	var missing []string
	if len(deps) > 0 {
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
	}

	// Root user install protection: covers target module + missing deps
	modsToCheck := append([]string{moduleID}, missing...)
	if err := app.CheckRootUserInstall(sys, modsToCheck, isInteractiveTerminal()); err != nil {
		return err
	}

	if len(missing) > 0 {
		log.Warnf(i18n.T("module_needs_deps"), m.Name(), strings.Join(missing, ", "))
		if !isInteractiveTerminal() {
			return fmt.Errorf(i18n.T("module_needs_deps_tty"), m.Name(), strings.Join(missing, ", "))
		}
		var confirm bool
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf(i18n.T("module_run_deps_title"), strings.Join(missing, ", "))).
					Description(i18n.T("module_run_deps_desc")).
					Value(&confirm),
			),
		).Run(); err != nil {
			return err
		}
		if !confirm {
			return fmt.Errorf(i18n.T("module_cannot_run_without"), m.Name(), strings.Join(missing, ", "))
		}
		runner := app.NewRunner(registry, sys, log)
		if err := runner.Run(ctx, &types.Config{SSHPort: 22122}, missing); err != nil {
			return fmt.Errorf("dependency %s failed: %w", strings.Join(missing, ", "), err)
		}
	}

	// Collect config
	cfg := &types.Config{SSHPort: 22122}
	applyAptMirrorEnv(cfg)
	switch moduleID {
	case "ssh":
		if !isInteractiveTerminal() {
			return fmt.Errorf(i18n.T("module_needs_tty"), m.Name())
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
			return fmt.Errorf(i18n.T("module_needs_tty"), m.Name())
		}
		if err := ui.UserConfigForm(cfg); err != nil {
			return err
		}
	case "ssh_keygen":
		if !isInteractiveTerminal() {
			return fmt.Errorf(i18n.T("module_needs_tty"), m.Name())
		}
		if err := ui.SSHKeygenForm(cfg); err != nil {
			return err
		}
	}

	log.SetModule(m.Name())
	log.Infof(i18n.T("runner_starting"), m.Name())

	check := m.Check(ctx, sys)
	if app.ShouldSkipSatisfiedForModule(moduleID, cfg, check) {
		log.Successf(i18n.T("runner_skipping"), m.Name())
		return nil
	}

	if err := m.Run(ctx, sys, cfg, log); err != nil {
		return fmt.Errorf(i18n.T("runner_failed"), m.Name(), err)
	}

	log.Successf(i18n.T("runner_completed"), m.Name())
	return nil
}

func isInteractiveTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && (info.Mode()&os.ModeCharDevice) != 0
}

// VersionCmd handles the `version` command.
func VersionCmd() {
	v := app.GetVersion()
	fmt.Printf(i18n.T("version_label"), v.Version)
	fmt.Printf(i18n.T("version_commit"), v.Commit)
	fmt.Printf(i18n.T("version_built"), v.BuildDate)
	fmt.Printf(i18n.T("version_go"), v.GoVersion)
	fmt.Printf(i18n.T("version_platform"), v.OS, v.Arch)
}

func boolStr(b bool, trueStr, falseStr string) string {
	if b {
		return trueStr
	}
	return falseStr
}

// applyAptMirrorEnv reads SYS_BOOTSTRAP_APT_MIRROR and sets cfg.AptMirror.
// Returns true if the env var was set to a recognized value.
// Unknown values are logged to stderr and ignored.
func applyAptMirrorEnv(cfg *types.Config) bool {
	v := os.Getenv("SYS_BOOTSTRAP_APT_MIRROR")
	if v == "" {
		return false
	}
	switch v {
	case "cernet":
		cfg.AptMirror = v
		return true
	default:
		fmt.Fprintf(os.Stderr, "Warning: ignoring unknown SYS_BOOTSTRAP_APT_MIRROR=%q (valid: cernet)\n", v)
		return false
	}
}

// aptMirrorForm shows an interactive form to ask about CERNET APT mirror.
func aptMirrorForm(cfg *types.Config) error {
	useCernet := false
	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(i18n.T("apt_mirror_form_title")).
				Description(i18n.T("apt_mirror_form_desc")).
				Value(&useCernet),
		),
	).Run(); err != nil {
		return err
	}
	if useCernet {
		cfg.AptMirror = "cernet"
	}
	return nil
}
