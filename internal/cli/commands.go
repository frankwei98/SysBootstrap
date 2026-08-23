package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	term "github.com/charmbracelet/x/term"
	"github.com/frankwei98/sys-bootstrap/internal/app"
	"github.com/frankwei98/sys-bootstrap/internal/i18n"
	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/modules"
	"github.com/frankwei98/sys-bootstrap/internal/settings"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
	"github.com/frankwei98/sys-bootstrap/internal/ui"
)

// RunMode controls which modules are available and whether root is required.
type RunMode string

const (
	RunModeUser RunMode = "user"
	RunModeFull RunMode = "full"
)

// resolveRunMode determines the run mode from env var or interactive prompt.
// Priority: SYS_BOOTSTRAP_RUN_MODE env > interactive prompt > error.
func resolveRunMode(interactive bool) (RunMode, error) {
	if env := os.Getenv("SYS_BOOTSTRAP_RUN_MODE"); env != "" {
		switch env {
		case "user":
			return RunModeUser, nil
		case "full":
			return RunModeFull, nil
		default:
			return "", fmt.Errorf("invalid SYS_BOOTSTRAP_RUN_MODE=%q (valid: user, full)", env)
		}
	}

	if interactive {
		return runModeForm()
	}

	return "", fmt.Errorf("%s", i18n.T("run_noninteractive_no_mode"))
}

// runModeForm shows an interactive form to select the run mode.
func runModeForm() (RunMode, error) {
	mode := string(RunModeUser)
	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(i18n.T("form_run_mode")).
				Description(i18n.T("form_run_mode_desc")).
				Options(
					huh.NewOption(i18n.T("form_run_mode_user"), string(RunModeUser)),
					huh.NewOption(i18n.T("form_run_mode_full"), string(RunModeFull)),
				).
				Value(&mode),
		),
	).Run(); err != nil {
		return "", err
	}
	return RunMode(mode), nil
}

// DoctorResult holds the outcome of a doctor check.
type DoctorResult struct {
	HasFatal bool
}

// RunCmd handles the `run` command (interactive execution).
func RunCmd(ctx context.Context, registry *modules.Registry) error {
	if !isInteractiveTerminal() {
		return fmt.Errorf("%s", i18n.T("run_requires_tty"))
	}

	sys, err := system.NewContext()
	if err != nil {
		return fmt.Errorf("system detection failed: %w", err)
	}

	log, err := logging.New(false)
	if err != nil {
		return fmt.Errorf("logger init failed: %w", err)
	}
	defer log.Close()

	// Resolve run mode
	mode, err := resolveRunMode(isInteractiveTerminal())
	if err != nil {
		return err
	}

	var selected []string

	switch mode {
	case RunModeUser:
		log.Info(i18n.T("run_mode_user_header"))
		// User-level mode: only show user-level modules, no base injection
		selected, err = ui.ModuleSelectFiltered(registry, app.UserLevelModuleSet())
		if err != nil {
			return err
		}
	case RunModeFull:
		log.Info(i18n.T("run_mode_full_header"))
		// Full mode: show all optional modules
		selected, err = ui.ModuleSelect(registry)
		if err != nil {
			return err
		}
	}

	// Build final module list (ai→node injection, base injection, dependency order)
	ordered, err := buildModuleList(registry, mode, selected)
	if err != nil {
		return err
	}

	// In full mode, fail early if root-required modules are present without root.
	if err := checkRootRequirementsForMode(mode, registry, ordered, sys.IsRoot); err != nil {
		return err
	}

	// Root user install protection: check before collecting config
	if err := app.CheckRootUserInstall(sys, ordered, isInteractiveTerminal()); err != nil {
		return err
	}

	// Collect config from forms
	cfg := &types.Config{}

	// APT mirror: env > settings > interactive prompt (skip in user mode)
	if mode == RunModeFull {
		st := settings.Load()
		resolved, saveNeeded := resolveAptMirror(cfg, st, isInteractiveTerminal())
		if !resolved {
			if saveNeeded {
				mirrorChoice, err := aptMirrorForm(cfg)
				if err != nil {
					return err
				}
				st.AptMirror = mirrorChoice
				if err := settings.SaveUser(st); err != nil {
					log.Warnf(i18n.T("config_save_failed"), err)
				}
			}
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
			if err := ui.SSHKeygenForm(cfg, sys); err != nil {
				return err
			}
		case "docker":
			if err := ui.DockerConfigForm(cfg, sys); err != nil {
				return err
			}
		case "timezone":
			if err := ui.TimezoneConfigForm(cfg, sys); err != nil {
				return err
			}
		case "fail2ban":
			if err := ui.Fail2banConfigForm(cfg); err != nil {
				return err
			}
		}
	}

	if cfg.AISelectionSet && !cfg.InstallClaudeCode && !cfg.InstallCodex {
		// The AI selector is explicit: an empty choice means no AI work. Remove
		// its auto-injected Node dependency unless Node was selected on its own.
		ordered = removeModuleID(ordered, "ai")
		if !containsModuleID(selected, "node") {
			ordered = removeModuleID(ordered, "node")
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
	runner.SetSSHCheckpoint(ui.NewSSHCheckpointFunc())
	if err := runner.Run(ctx, cfg, ordered); err != nil {
		if errors.Is(err, types.ErrSSHPendingConfirmation) {
			return nil
		}
		return err
	}

	log.Success(i18n.T("runner_all_done"))
	if needsShellReloadHint(ordered) {
		log.Warnf(i18n.T("shell_reload_hint"), shellReloadCommand())
	}
	return nil
}

// PlanCmd handles the `plan` command.
func PlanCmd(ctx context.Context, registry *modules.Registry, jsonOutput bool) error {

	sys, err := system.NewContext()
	if err != nil {
		return fmt.Errorf("system detection failed: %w", err)
	}

	// Include all module IDs in registration order
	ids := registry.IDs()

	cfg := previewPlanConfig(sys)
	st := settings.Load()
	resolveAptMirror(cfg, st, false)
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
	if sys.SupportTier() == system.SupportTierAptCompatible {
		osDetail += i18n.T("doctor_os_detail_apt_compat")
	}
	fmt.Printf("%s %s: %s\n", i18n.T("doctor_support_tier"), i18n.T("doctor_support_tier_value"), sys.SupportTier())

	checks := []struct {
		name   string
		ok     bool
		detail string
		fatal  bool
	}{
		{"OS ID", sys.OSID != "", sys.OSID, true},
		{"OS Version", sys.OSVersion != "", sys.OSVersion, true},
		{"Supported OS", sys.IsSupportedOS(), osDetail, false},
		{"Architecture", system.IsSupportedArchitecture(sys.Arch), sys.Arch, true},
		{"Root", true, boolStr(sys.IsRoot, i18n.T("doctor_root_yes"), i18n.T("doctor_root_no")), false},
		{"systemd", sys.HasSystemd, boolStr(sys.HasSystemd, i18n.T("doctor_systemd_yes"), i18n.T("doctor_systemd_no")), false},
		{"apt-get", sys.HasApt, boolStr(sys.HasApt, i18n.T("doctor_apt_yes"), i18n.T("doctor_apt_no")), true},
		{"bash", sys.HasBash, boolStr(sys.HasBash, i18n.T("doctor_bash_yes"), i18n.T("doctor_bash_no")), false},
		{"curl", sys.HasCurl, boolStr(sys.HasCurl, i18n.T("doctor_curl_yes"), i18n.T("doctor_curl_no")), false},
		{"network", sys.HasNetwork, boolStr(sys.HasNetwork, i18n.T("doctor_network_ok"), i18n.T("doctor_network_fail")), false},
		{"sshd", sys.HasSSHD, boolStr(sys.HasSSHD, i18n.T("doctor_sshd_yes"), i18n.T("doctor_sshd_no")), false},
		{"sshd service", sys.HasSSHDService, boolStr(sys.HasSSHDService, i18n.T("doctor_sshd_svc_yes"), i18n.T("doctor_sshd_svc_no")), false},
	}

	result := &DoctorResult{}
	for _, c := range checks {
		icon := "✓"
		status := "ok"
		if !c.ok {
			if c.fatal {
				icon = "✗"
				status = "fatal"
				result.HasFatal = true
			} else {
				icon = "⚠"
				status = "warning"
			}
		}
		fmt.Printf("  %s %-15s %-7s %s\n", icon, c.name, status, c.detail)
	}
	if result.HasFatal {
		fmt.Println("Conclusion: fatal compatibility issues detected")
	} else {
		fmt.Println("Conclusion: environment looks compatible")
	}

	if result.HasFatal {
		return result, fmt.Errorf("critical checks failed")
	}
	return result, nil
}

// ModuleCmd handles `module <id>` command.
func ModuleCmd(ctx context.Context, registry *modules.Registry, moduleID string) error {

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

	// Resolve one execution config before dependency checks. The same config is
	// used for dependency inspection, dependency execution, target forms, and
	// final execution so requested settings cannot drift between phases.
	cfg := moduleDefaultConfig(moduleID, sys)
	st := settings.Load()
	resolveAptMirror(cfg, st, false)

	// Ask for the AI selection before resolving its Node dependency. An explicit
	// empty selection must be a no-op rather than installing Node unnecessarily.
	if moduleID == "ai" && isInteractiveTerminal() {
		if err := ui.AIConfigForm(cfg); err != nil {
			return err
		}
		if !cfg.InstallClaudeCode && !cfg.InstallCodex {
			log.Info("No AI CLI tools selected, nothing to do")
			return nil
		}
	}

	// Check dependencies first so root user install covers both the target
	// module and any dependencies that will be auto-run.
	missing, err := missingDependenciesForModule(ctx, registry, m, sys, cfg)
	if err != nil {
		return err
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
		result, err := runner.RunWithResult(ctx, cfg, missing)
		if err != nil {
			return fmt.Errorf("dependency %s failed: %w", strings.Join(missing, ", "), err)
		}
		var failedDeps []string
		for _, dep := range missing {
			if result.ModuleFailed(dep) {
				failedDeps = append(failedDeps, dep)
			}
		}
		if len(failedDeps) > 0 {
			log.SetModule(m.Name())
			log.Warnf(i18n.T("runner_skipping_failed_dependencies"), m.Name(), failedDeps)
			return fmt.Errorf("cannot run %s: %w", m.Name(), result.Err())
		}
	}

	// Collect module-specific config.
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
			if !cfg.AISelectionSet {
				if err := ui.AIConfigForm(cfg); err != nil {
					return err
				}
			}
		} else {
			cfg.InstallClaudeCode = true
			cfg.InstallCodex = true
			cfg.AISelectionSet = true
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
		if err := ui.SSHKeygenForm(cfg, sys); err != nil {
			return err
		}
	case "docker":
		if !isInteractiveTerminal() {
			break
		}
		if err := ui.DockerConfigForm(cfg, sys); err != nil {
			return err
		}
	case "timezone":
		if !isInteractiveTerminal() {
			break
		}
		if err := ui.TimezoneConfigForm(cfg, sys); err != nil {
			return err
		}
	case "fail2ban":
		if !isInteractiveTerminal() {
			break
		}
		if err := ui.Fail2banConfigForm(cfg); err != nil {
			return err
		}
	}

	runner := app.NewRunner(registry, sys, log)
	runner.SetSSHCheckpoint(ui.NewSSHCheckpointFunc())
	result, err := runner.RunWithResult(ctx, cfg, []string{moduleID})
	if err != nil {
		if errors.Is(err, types.ErrSSHPendingConfirmation) {
			return nil
		}
		return err
	}
	if result.ModuleFailed(moduleID) {
		return fmt.Errorf("module %s failed; see warning output above", m.Name())
	}

	if needsShellReloadHint(append(append([]string{}, missing...), moduleID)) {
		log.Warnf(i18n.T("shell_reload_hint"), shellReloadCommand())
	}
	return nil
}

func missingDependenciesForModule(
	ctx context.Context,
	registry *modules.Registry,
	target modules.Module,
	sys *system.Context,
	cfg *types.Config,
) ([]string, error) {
	ordered, err := registry.ResolveOrder([]string{target.ID()})
	if err != nil {
		return nil, fmt.Errorf("resolve dependencies for %s: %w", target.ID(), err)
	}

	var missing []string
	for _, dependencyID := range ordered {
		if dependencyID == target.ID() {
			continue
		}
		dependency, err := registry.Get(dependencyID)
		if err != nil {
			return nil, fmt.Errorf("load dependency %s for %s: %w", dependencyID, target.ID(), err)
		}
		if check := dependency.Check(ctx, sys, cfg); !check.Satisfied {
			missing = append(missing, dependencyID)
		}
	}
	return missing, nil
}

func needsShellReloadHint(moduleIDs []string) bool {
	for _, id := range moduleIDs {
		if id == "node" || id == "ai" {
			return true
		}
	}
	return false
}

func shellReloadCommand() string {
	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		shellPath = "/bin/bash"
	}
	return fmt.Sprintf("exec %s -l", shellPath)
}

func containsModuleID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func removeModuleID(ids []string, target string) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if id != target {
			result = append(result, id)
		}
	}
	return result
}

func moduleDefaultConfig(moduleID string, sys *system.Context) *types.Config {
	cfg := &types.Config{}
	switch moduleID {
	case "docker":
		cfg.DockerUser = system.TargetUsername(sys)
	case "fail2ban":
		cfg.Fail2banMaxRetry = 5
		cfg.Fail2banFindTime = "10m"
		cfg.Fail2banBanTime = "1h"
	}
	return cfg
}

func previewPlanConfig(sys *system.Context) *types.Config {
	return &types.Config{
		SSHAllowUFW: sys != nil && sys.HasUFW && sys.UFWActive,
		DockerUser:  system.TargetUsername(sys),
		Timezone:    "Etc/UTC",
	}
}

// uninstallFlags holds parsed uninstall command flags.
type uninstallFlags struct {
	DryRun bool
	All    bool
	Yes    bool
}

// parseUninstallFlags parses uninstall command args into flags.
func parseUninstallFlags(args []string) (uninstallFlags, error) {
	var f uninstallFlags
	for _, a := range args {
		switch a {
		case "--dry-run":
			f.DryRun = true
		case "--all":
			f.All = true
		case "--yes":
			f.Yes = true
		default:
			return uninstallFlags{}, fmt.Errorf("unknown uninstall option %q", a)
		}
	}
	return f, nil
}

// validateUninstallFlags checks flag combinations. interactive indicates whether
// stdin is a TTY; dryRun is exempt from the --all --yes requirement.
func validateUninstallFlags(f uninstallFlags, interactive bool) error {
	if !f.DryRun && !interactive && !(f.All && f.Yes) {
		return fmt.Errorf("%s", i18n.T("uninstall_all_no_yes"))
	}
	return nil
}

// UninstallCmd handles the `uninstall` command.
func UninstallCmd(ctx context.Context, args []string) error {
	flags, err := parseUninstallFlags(args)
	if err != nil {
		return err
	}

	if err := validateUninstallFlags(flags, isInteractiveTerminal()); err != nil {
		return err
	}

	// Resolve current user info
	info, err := app.ResolveUserInfo()
	if err != nil {
		return fmt.Errorf("cannot determine user: %w", err)
	}

	// Display user info
	app.PrintUserInfo(info)
	fmt.Println()

	// Scan for uninstallable items
	allItems := app.ScanUninstallItems(info.HomeDir)
	items := app.FilterInstalledItems(allItems)

	if len(items) == 0 {
		fmt.Println(i18n.T("uninstall_no_items"))
		return nil
	}

	// Build plan
	plan := app.BuildUninstallPlan(items, info.HomeDir)

	if flags.DryRun {
		if err := app.ValidateUninstallPlan(plan, info.HomeDir); err != nil {
			return err
		}
		fmt.Println(i18n.T("uninstall_dry_run_header"))
		fmt.Println()
		app.PrintUninstallPlan(plan)
		fmt.Println()
		// Execute the same read-only validation and preview path used by the
		// actual uninstaller so dry-run cannot promise an unsafe deletion.
		log, err := logging.New(false)
		if err != nil {
			return fmt.Errorf("logger init failed: %w", err)
		}
		defer log.Close()
		if err := app.ExecuteUninstall(ctx, plan, info.HomeDir, true, log); err != nil {
			return err
		}
		return nil
	}

	// Determine which items to uninstall
	var selectedIDs []string
	if flags.All {
		// --all: use all detected items
		for _, item := range items {
			selectedIDs = append(selectedIDs, item.ID)
		}
	} else if isInteractiveTerminal() {
		// Interactive: show multi-select form
		var formItems []ui.UninstallItemOption
		for _, item := range items {
			label := item.Name
			if item.Description != "" {
				label += " — " + item.Description
			}
			if item.PkgManager != "" {
				label += " (" + item.PkgManager + ")"
			}
			formItems = append(formItems, ui.UninstallItemOption{ID: item.ID, Label: label})
		}
		selectedIDs, err = ui.UninstallSelectForm(formItems)
		if err != nil {
			return err
		}
		if len(selectedIDs) == 0 {
			fmt.Println(i18n.T("uninstall_no_items"))
			return nil
		}
	} else {
		return fmt.Errorf("%s", i18n.T("uninstall_all_no_yes"))
	}

	// Filter plan to selected items only
	selectedSet := make(map[string]bool)
	for _, id := range selectedIDs {
		selectedSet[id] = true
	}
	var selectedItems []app.UninstallItem
	for _, item := range items {
		if selectedSet[item.ID] {
			selectedItems = append(selectedItems, item)
		}
	}
	plan = app.BuildUninstallPlan(selectedItems, info.HomeDir)
	if err := app.ValidateUninstallPlan(plan, info.HomeDir); err != nil {
		return err
	}

	// Show plan and confirm (interactive only, unless --yes)
	if isInteractiveTerminal() && !flags.Yes {
		planText := app.FormatUninstallPlan(plan)
		fmt.Println(i18n.T("uninstall_plan_title"))
		fmt.Print(planText)
		fmt.Println()

		confirmed, err := ui.ConfirmUninstall(planText)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Println(i18n.T("runner_cancelled"))
			return nil
		}
	}

	// Initialize logger
	log, err := logging.New(false)
	if err != nil {
		return fmt.Errorf("logger init failed: %w", err)
	}
	defer log.Close()

	// Execute uninstall
	if err := app.ExecuteUninstall(ctx, plan, info.HomeDir, false, log); err != nil {
		return err
	}

	log.Success(i18n.T("uninstall_done"))
	return nil
}

func isInteractiveTerminal() bool {
	return term.IsTerminal(os.Stdin.Fd()) &&
		term.IsTerminal(os.Stdout.Fd()) &&
		term.IsTerminal(os.Stderr.Fd())
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

// resolveAptMirror determines the APT mirror setting.
// Priority: SYS_BOOTSTRAP_APT_MIRROR env > settings config > interactive prompt.
// Returns (resolved bool, saveNeeded bool). If saveNeeded is true, the caller
// should persist the user's answer after collecting it interactively.
func resolveAptMirror(cfg *types.Config, st settings.Settings, interactive bool) (bool, bool) {
	// 1. Env var override (highest)
	if env := os.Getenv("SYS_BOOTSTRAP_APT_MIRROR"); env != "" {
		switch env {
		case settings.ValAptCernet:
			cfg.AptMirror = settings.ValAptCernet
			return true, false
		case settings.ValAptDefault:
			cfg.AptMirror = ""
			return true, false
		default:
			fmt.Fprintf(os.Stderr, "Warning: ignoring unknown SYS_BOOTSTRAP_APT_MIRROR=%q (valid: cernet, default)\n", env)
		}
	}

	// 2. Persisted config
	switch st.AptMirror {
	case settings.ValAptCernet:
		cfg.AptMirror = settings.ValAptCernet
		return true, false
	case settings.ValAptDefault:
		cfg.AptMirror = ""
		return true, false
	}

	// 3. Unset — ask interactively if possible
	if interactive {
		return false, true
	}
	return false, false
}

// aptMirrorForm shows an interactive form to ask about CERNET APT mirror.
// Returns the user's choice ("cernet" or "default").
func aptMirrorForm(cfg *types.Config) (string, error) {
	useCernet := false
	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(i18n.T("apt_mirror_form_title")).
				Description(i18n.T("apt_mirror_form_desc")).
				Value(&useCernet),
		),
	).Run(); err != nil {
		return "", err
	}
	if useCernet {
		cfg.AptMirror = settings.ValAptCernet
		return settings.ValAptCernet, nil
	}
	return settings.ValAptDefault, nil
}

// ConfigCmd handles the `config` command.
func ConfigCmd(args []string) error {
	st := settings.Load()

	// Direct set: config language <lang>
	if len(args) == 2 && args[0] == "language" {
		lang := settings.NormalizeLang(args[1])
		if lang == "en" && strings.ToLower(args[1]) != "en" {
			return fmt.Errorf(i18n.T("config_invalid_lang"), args[1])
		}
		st.Lang = lang
		if err := settings.SaveUser(st); err != nil {
			return fmt.Errorf(i18n.T("config_save_failed"), err)
		}
		fmt.Println(i18n.Tf("config_saved", settings.UserConfigPath()))
		i18n.SetLang(i18n.Lang(lang))
		return nil
	}

	// Direct set: config apt-mirror <mirror>
	if len(args) == 2 && args[0] == "apt-mirror" {
		switch args[1] {
		case settings.ValAptCernet, settings.ValAptDefault:
			st.AptMirror = args[1]
		default:
			return fmt.Errorf(i18n.T("config_invalid_mirror"), args[1])
		}
		if err := settings.SaveUser(st); err != nil {
			return fmt.Errorf(i18n.T("config_save_failed"), err)
		}
		fmt.Println(i18n.Tf("config_saved", settings.UserConfigPath()))
		return nil
	}

	// Interactive config menu
	if !isInteractiveTerminal() {
		fmt.Fprintln(os.Stderr, i18n.T("config_lang_usage"))
		fmt.Fprintln(os.Stderr, i18n.T("config_mirror_usage"))
		return fmt.Errorf("%s", i18n.T("config_needs_tty"))
	}

	return interactiveConfig(&st)
}

// interactiveConfig shows an interactive settings menu.
func interactiveConfig(st *settings.Settings) error {
	// Language selection
	langChoice := "en"
	if st.Lang == "zh-CN" {
		langChoice = "zh-CN"
	}
	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(i18n.T("settings_lang_select")).
				Description(i18n.T("settings_lang_desc")).
				Options(
					huh.NewOption("English", "en"),
					huh.NewOption("中文", "zh-CN"),
				).
				Value(&langChoice),
		),
	).Run(); err != nil {
		return err
	}
	st.Lang = langChoice
	i18n.SetLang(i18n.Lang(st.Lang))

	// APT mirror selection
	mirrorChoice := settings.ValAptDefault
	if st.AptMirror == settings.ValAptCernet {
		mirrorChoice = settings.ValAptCernet
	}
	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(i18n.T("settings_mirror_select")).
				Description(i18n.T("settings_mirror_desc")).
				Options(
					huh.NewOption(i18n.T("settings_mirror_default"), settings.ValAptDefault),
					huh.NewOption(i18n.T("settings_mirror_cernet"), settings.ValAptCernet),
				).
				Value(&mirrorChoice),
		),
	).Run(); err != nil {
		return err
	}
	st.AptMirror = mirrorChoice

	// The selected language is already active for the remaining forms.
	if err := settings.SaveUser(*st); err != nil {
		return fmt.Errorf(i18n.T("config_save_failed"), err)
	}
	fmt.Println(i18n.T("settings_saved"))
	return nil
}
