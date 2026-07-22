package ui

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/frankwei98/sys-bootstrap/internal/i18n"
	"github.com/frankwei98/sys-bootstrap/internal/modules"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

var fail2banDurationRegex = regexp.MustCompile(`^[1-9][0-9]*(?:[smhdw])?$`)

// ModuleSelect shows a multi-select for optional modules.
func ModuleSelect(registry *modules.Registry) ([]string, error) {
	var options []huh.Option[string]
	for _, m := range registry.All() {
		if m.DefaultEnabled() {
			continue // base is always included
		}
		options = append(options, huh.NewOption(m.Name(), m.ID()))
	}

	var selected []string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title(i18n.T("form_select_modules")).
				Description(i18n.T("form_select_modules_desc")).
				Options(options...).
				Value(&selected),
		),
	)
	if err := form.Run(); err != nil {
		return nil, err
	}
	return selected, nil
}

// ModuleSelectFiltered shows a multi-select for optional modules, filtered to
// only include modules whose IDs are in allowedIDs. Used for user-level mode
// where root-required modules should not appear.
func ModuleSelectFiltered(registry *modules.Registry, allowedIDs map[string]bool) ([]string, error) {
	var options []huh.Option[string]
	for _, m := range registry.All() {
		if m.DefaultEnabled() {
			continue
		}
		if !allowedIDs[m.ID()] {
			continue
		}
		options = append(options, huh.NewOption(m.Name(), m.ID()))
	}

	var selected []string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title(i18n.T("form_select_modules")).
				Description(i18n.T("form_select_modules_desc")).
				Options(options...).
				Value(&selected),
		),
	)
	if err := form.Run(); err != nil {
		return nil, err
	}
	return selected, nil
}

// SSHConfigForm collects SSH module configuration.
func SSHConfigForm(cfg *types.Config, sys *system.Context) error {
	port := fmt.Sprintf("%d", cfg.SSHPort)
	if port == "0" {
		port = strconv.Itoa(modules.DefaultSSHPort)
	}

	groups := []*huh.Group{
		huh.NewGroup(
			huh.NewInput().
				Title(i18n.T("form_ssh_port")).
				Description(i18n.T("form_ssh_port_desc")).
				Placeholder("22122").
				Validate(func(s string) error {
					_, err := parseTCPPort(s)
					return err
				}).
				Value(&port),
			huh.NewConfirm().
				Title(i18n.T("form_disable_root")).
				Description(i18n.T("form_disable_root_desc")).
				Value(&cfg.SSHDisableRoot),
			huh.NewConfirm().
				Title(i18n.T("form_disable_pass")).
				Description(i18n.T("form_disable_pass_desc")).
				Value(&cfg.SSHDisablePass),
			huh.NewConfirm().
				Title(i18n.T("form_add_ssh_key")).
				Description(i18n.T("form_add_ssh_key_desc")).
				Value(&cfg.SSHAddKey),
		),
	}

	// Ask about UFW only when UFW is present and active
	if sys != nil && sys.HasUFW && sys.UFWActive {
		cfg.SSHAllowUFW = true // default to true
		groups = append(groups, huh.NewGroup(
			huh.NewConfirm().
				Title(i18n.T("form_allow_ufw")).
				Description(i18n.T("form_allow_ufw_desc")).
				Value(&cfg.SSHAllowUFW),
		))
	}

	form := huh.NewForm(groups...)
	if err := form.Run(); err != nil {
		return err
	}

	parsedPort, err := parseTCPPort(port)
	if err != nil {
		return err
	}
	cfg.SSHPort = parsedPort

	if cfg.SSHAddKey {
		key := ""
		keyForm := huh.NewForm(
			huh.NewGroup(
				huh.NewText().
					Title(i18n.T("form_ssh_pubkey")).
					Description(i18n.T("form_ssh_pubkey_desc")).
					Placeholder("ssh-ed25519 AAAA...").
					Validate(func(s string) error {
						return validatePublicKeyContent(s)
					}).
					Value(&key),
			),
		)
		if err := keyForm.Run(); err != nil {
			return err
		}
		cfg.SSHPublicKey = strings.TrimSpace(key)
	}
	hasSSHKey := cfg.SSHAddKey && strings.TrimSpace(cfg.SSHPublicKey) != ""
	hasUserKey := cfg.UserAddKey && (strings.TrimSpace(cfg.UserPublicKey) != "" || strings.TrimSpace(cfg.UserGitHubUser) != "")
	if cfg.SSHDisableRoot && cfg.SSHDisablePass && !hasSSHKey && !hasUserKey {
		return fmt.Errorf("cannot disable both root login and password authentication without a replacement SSH public key")
	}

	return nil
}

// UserConfigForm collects user module configuration.
func UserConfigForm(cfg *types.Config) error {
	usernameForm := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(i18n.T("form_username")).
				Description(i18n.T("form_username_desc") + "\n" + i18n.T("form_user_existing_desc")).
				Placeholder("deploy").
				Validate(func(s string) error {
					if !modules.ValidateLinuxUsername(s) {
						return fmt.Errorf("username must be 1-32 lowercase letters, digits, hyphens, or underscores, starting with a letter")
					}
					return nil
				}).
				Value(&cfg.NewUsername),
		),
	)
	if err := usernameForm.Run(); err != nil {
		return err
	}

	shell := "zsh"
	passwordlessSudo := true
	if state, err := modules.LookupUserState(cfg.NewUsername); err == nil && state.Exists {
		switch state.Shell {
		case "/bin/bash":
			shell = "bash"
		case "/bin/zsh":
			shell = "zsh"
		}
		cfg.UserAddSudo = state.InSudoGroup
		passwordlessSudo = state.PasswordlessSudo
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(i18n.T("form_shell")).
				Options(
					huh.NewOption("bash", "bash"),
					huh.NewOption("zsh", "zsh"),
				).
				Value(&shell),
			huh.NewConfirm().
				Title(i18n.T("form_add_sudo")).
				Value(&cfg.UserAddSudo),
			huh.NewConfirm().
				Title(i18n.T("form_add_user_key")).
				Value(&cfg.UserAddKey),
		),
	)
	if err := form.Run(); err != nil {
		return err
	}
	cfg.UserShell = shell

	if cfg.UserAddSudo {
		sudoForm := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(i18n.T("form_passwordless_sudo")).
					Description(i18n.T("form_passwordless_sudo_desc") + "\n" + i18n.T("form_user_password_note")).
					Value(&passwordlessSudo),
			),
		)
		if err := sudoForm.Run(); err != nil {
			return err
		}
		cfg.UserPasswordlessSudo = passwordlessSudo
	}

	if cfg.UserAddKey {
		source := "paste"
		sourceForm := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title(i18n.T("form_key_source")).
					Options(
						huh.NewOption(i18n.T("form_key_source_paste"), "paste"),
						huh.NewOption(i18n.T("form_key_source_github"), "github"),
					).
					Value(&source),
			),
		)
		if err := sourceForm.Run(); err != nil {
			return err
		}
		cfg.UserKeySource = source

		if source == "paste" {
			key := ""
			keyForm := huh.NewForm(
				huh.NewGroup(
					huh.NewText().
						Title(i18n.T("form_ssh_pubkey")).
						Description(i18n.T("form_ssh_pubkey_desc")).
						Placeholder("ssh-ed25519 AAAA...").
						Validate(validatePublicKeyContent).
						Value(&key),
				),
			)
			if err := keyForm.Run(); err != nil {
				return err
			}
			cfg.UserPublicKey = strings.TrimSpace(key)
		} else {
			ghUser := ""
			ghForm := huh.NewForm(
				huh.NewGroup(
					huh.NewInput().
						Title(i18n.T("form_github_user")).
						Description(i18n.T("form_github_user_desc")).
						Validate(func(s string) error {
							if !modules.ValidateGitHubUsername(s) {
								return fmt.Errorf("GitHub username must be 1-39 letters, digits, or single hyphens")
							}
							return nil
						}).
						Value(&ghUser),
				),
			)
			if err := ghForm.Run(); err != nil {
				return err
			}
			cfg.UserGitHubUser = strings.TrimSpace(ghUser)

			keyContent, err := modules.FetchGitHubKeys(cfg.UserGitHubUser)
			if err != nil {
				return fmt.Errorf("failed to fetch GitHub keys for %s: %w", cfg.UserGitHubUser, err)
			}
			fingerprints, err := modules.PublicKeyFingerprints(keyContent)
			if err != nil {
				return fmt.Errorf("failed to fingerprint GitHub keys for %s: %w", cfg.UserGitHubUser, err)
			}

			confirmed := false
			fingerprintSummary := strings.Join(fingerprints, "\n")
			confirmationForm := huh.NewForm(
				huh.NewGroup(
					huh.NewNote().
						Title(i18n.T("form_github_keys_title")).
						Description(fmt.Sprintf(i18n.T("form_github_keys_desc"), len(fingerprints), cfg.UserGitHubUser, fingerprintSummary)),
					huh.NewConfirm().
						Title(i18n.T("form_github_keys_confirm")).
						Value(&confirmed),
				),
			)
			if err := confirmationForm.Run(); err != nil {
				return err
			}
			if !confirmed {
				return fmt.Errorf("GitHub SSH key import cancelled")
			}
			// Keep the reviewed bytes so the subsequent run cannot install keys
			// that changed at the remote endpoint after confirmation.
			cfg.UserGitHubKeys = keyContent
		}
	}

	summary := cfg.NewUsername
	if summary == "" {
		summary = i18n.T("form_username_desc")
	} else {
		if check, err := modules.DescribeUserCheckForConfig(cfg); err == nil && check.Message != "" {
			summary = check.Message
		}
	}
	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title(i18n.T("form_user_state_title")).
				Description(summary),
		),
	).Run(); err != nil {
		return err
	}

	return nil
}

// SSHKeygenForm collects ssh_keygen module configuration.
func SSHKeygenForm(cfg *types.Config, sys *system.Context) error {
	keyType := "ed25519"
	comment := ""

	hostname, _ := os.Hostname()
	placeholder := cfg.NewUsername + "@" + hostname
	if placeholder == "@" {
		placeholder = "user@host"
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(i18n.T("form_keygen_algo")).
				Options(
					huh.NewOption("ed25519 (recommended)", "ed25519"),
					huh.NewOption("RSA 4096", "rsa"),
				).
				Value(&keyType),
			huh.NewInput().
				Title(i18n.T("form_keygen_comment")).
				Description(i18n.T("form_keygen_comment_desc")).
				Placeholder(placeholder).
				Value(&comment),
		),
	)
	if err := form.Run(); err != nil {
		return err
	}

	cfg.KeygenType = keyType
	cfg.KeygenComment = comment

	keyFile, keyExists := selectedSSHKeyPath(sys, keyType)

	// Ask about overwrite if the selected key path already exists
	if keyExists {
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf(i18n.T("form_keygen_overwrite"), keyFile)).
					Description(i18n.T("form_keygen_overwrite_desc")).
					Value(&cfg.KeygenOverwrite),
			),
		).Run(); err != nil {
			return err
		}
	}

	return nil
}

func selectedSSHKeyPath(sys *system.Context, keyType string) (string, bool) {
	home := system.TargetHomeDir(sys)
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	keyFile := filepath.Join(home, ".ssh", "id_"+keyType)
	if _, err := os.Stat(keyFile); err == nil {
		return keyFile, true
	}
	return keyFile, false
}

// NewSSHCheckpointFunc returns a types.CheckpointFunc that displays the
// replacement access paths and asks the operator to test a new login from
// another terminal before confirming finalization.
func NewSSHCheckpointFunc() types.CheckpointFunc {
	return func(ctx context.Context, candidates []types.AccessPath) (bool, error) {
		fmt.Println()
		fmt.Println("╔══════════════════════════════════════════════════════════════╗")
		fmt.Println("║   SSH Hardening — Prepare Phase Complete                    ║")
		fmt.Println("╚══════════════════════════════════════════════════════════════╝")
		fmt.Println()
		fmt.Println("Replacement access path(s) ready. Before finalizing restrictive")
		fmt.Println("SSH policy, you must test that a NEW login works from another")
		fmt.Println("terminal using the information below.")
		fmt.Println()

		for i, c := range candidates {
			maybeNew := ""
			if i == len(candidates)-1 {
				maybeNew = " ← preferred"
			}
			fmt.Println("  Access path", i+1, maybeNew)
			fmt.Println("    Account:   ", c.Username)
			fmt.Println("    Port:      ", c.PreparedPort)
			fmt.Println("    Key:       ", c.KeyFingerprint)
			fmt.Printf("    Test cmd:   ssh -p %d %s@<host>\n", c.PreparedPort, c.Username)
			fmt.Println()
		}

		fmt.Println("Verify you can log in successfully with the key shown above.")
		fmt.Println("Finalization will disable password authentication and/or root")
		fmt.Println("login as you requested.")
		fmt.Println()

		// Use a bounded, context-aware yes/no prompt. Timeout/cancellation keeps
		// the prepared dual-path state and never enters finalization.
		var confirmed bool
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("SSH login from another terminal succeeded?").
					Description("Only select Yes if you have successfully logged in with the new configuration from a separate terminal.").
					Affirmative("Yes, finalize SSH hardening").
					Negative("No, leave dual-path state").
					Value(&confirmed),
			),
		)
		if err := form.WithTimeout(15 * time.Minute).RunWithContext(ctx); err != nil {
			// Cancellation, EOF, timeout: leave dual-path
			return false, nil
		}
		return confirmed, nil
	}
}

// ConfirmRun shows the execution plan and asks for confirmation.
func ConfirmRun(planText string) (bool, error) {
	var confirm bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title(i18n.T("plan_title")).
				Description(planText),
			huh.NewConfirm().
				Title(i18n.T("form_confirm_run")).
				Description(i18n.T("form_confirm_run_desc")).
				Value(&confirm),
		),
	)
	if err := form.Run(); err != nil {
		return false, err
	}
	return confirm, nil
}

// UninstallItemOption represents a selectable uninstall item for the form.
type UninstallItemOption struct {
	ID    string
	Label string
}

// UninstallSelectForm shows a multi-select for uninstallable items.
// items is a slice of (id, label) pairs pre-formatted by the caller.
func UninstallSelectForm(items []UninstallItemOption) ([]string, error) {
	var options []huh.Option[string]
	for _, item := range items {
		options = append(options, huh.NewOption(item.Label, item.ID))
	}

	var selected []string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title(i18n.T("uninstall_select_items")).
				Description(i18n.T("uninstall_select_desc")).
				Options(options...).
				Value(&selected),
		),
	)
	if err := form.Run(); err != nil {
		return nil, err
	}
	return selected, nil
}

// ConfirmUninstall shows the uninstall plan and asks for confirmation.
func ConfirmUninstall(planText string) (bool, error) {
	var confirm bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title(i18n.T("uninstall_plan_title")).
				Description(planText),
			huh.NewConfirm().
				Title(i18n.T("uninstall_confirm")).
				Description(i18n.T("uninstall_confirm_desc")).
				Value(&confirm),
		),
	)
	if err := form.Run(); err != nil {
		return false, err
	}
	return confirm, nil
}

// AIConfigForm collects AI module configuration.
func AIConfigForm(cfg *types.Config) error {
	selected := []string{"claude-code", "codex"}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title(i18n.T("form_ai_tools")).
				Description(i18n.T("form_ai_tools_desc")).
				Options(
					huh.NewOption("Claude Code", "claude-code"),
					huh.NewOption("Codex", "codex"),
				).
				Value(&selected),
		),
	)
	if err := form.Run(); err != nil {
		return err
	}

	cfg.AISelectionSet = true
	cfg.InstallClaudeCode = false
	cfg.InstallCodex = false
	for _, s := range selected {
		switch s {
		case "claude-code":
			cfg.InstallClaudeCode = true
		case "codex":
			cfg.InstallCodex = true
		}
	}
	return nil
}

// DockerConfigForm collects docker module configuration.
func DockerConfigForm(cfg *types.Config, sys *system.Context) error {
	targetUser := cfg.DockerUser
	if targetUser == "" {
		targetUser = system.TargetUsername(sys)
	}

	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(i18n.T("form_docker_user")).
				Description(i18n.T("form_docker_user_desc")).
				Placeholder(system.TargetUsername(sys)).
				Value(&targetUser),
		),
	).Run(); err != nil {
		return err
	}
	cfg.DockerUser = strings.TrimSpace(targetUser)
	return nil
}

// TimezoneConfigForm collects timezone module configuration.
func TimezoneConfigForm(cfg *types.Config, sys *system.Context) error {
	current, ok := modulesCurrentTimezone()
	selected := cfg.Timezone
	if selected == "" {
		if ok && current != "" {
			selected = current
		} else {
			selected = "Etc/UTC"
		}
	}

	custom := selected
	options := []huh.Option[string]{}
	if ok && current != "" {
		options = append(options, huh.NewOption("Keep current / detected", "__keep__"))
	}
	options = append(options,
		huh.NewOption("Etc/UTC", "Etc/UTC"),
		huh.NewOption("Asia/Shanghai", "Asia/Shanghai"),
		huh.NewOption("America/Los_Angeles", "America/Los_Angeles"),
		huh.NewOption("Europe/Berlin", "Europe/Berlin"),
		huh.NewOption("Custom", "__custom__"),
	)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title(i18n.T("form_timezone")).
				Description(i18n.T("form_timezone_desc")).
				Options(options...).
				Value(&selected),
		),
	)
	if err := form.Run(); err != nil {
		return err
	}

	switch selected {
	case "__keep__":
		if ok && current != "" {
			cfg.Timezone = current
		}
		return nil
	case "__custom__":
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title(i18n.T("form_timezone_custom")).
					Description(i18n.T("form_timezone_custom_desc")).
					Placeholder("Etc/UTC").
					Validate(func(s string) error {
						if _, err := time.LoadLocation(strings.TrimSpace(s)); err != nil {
							return fmt.Errorf("unknown timezone %q", strings.TrimSpace(s))
						}
						return nil
					}).
					Value(&custom),
			),
		).Run(); err != nil {
			return err
		}
		cfg.Timezone = strings.TrimSpace(custom)
	default:
		cfg.Timezone = selected
	}
	return nil
}

// Fail2banConfigForm collects fail2ban module configuration.
func Fail2banConfigForm(cfg *types.Config) error {
	maxRetry := fmt.Sprintf("%d", maxInt(cfg.Fail2banMaxRetry, 5))
	findTime := defaultString(cfg.Fail2banFindTime, "10m")
	banTime := defaultString(cfg.Fail2banBanTime, "1h")
	backend := defaultString(cfg.Fail2banBackend, "systemd")
	ignoreIP := strings.TrimSpace(cfg.Fail2banIgnoreIP)

	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Protected SSH port(s)").
				Description("fail2ban will use the effective sshd port setting: "+modules.EffectiveSSHPortSetting(cfg)),
			huh.NewInput().
				Title(i18n.T("form_fail2ban_maxretry")).
				Description(i18n.T("form_fail2ban_maxretry_desc")).
				Placeholder("5").
				Validate(func(s string) error {
					_, err := parsePositiveInt(s, "maxretry")
					return err
				}).
				Value(&maxRetry),
			huh.NewInput().
				Title(i18n.T("form_fail2ban_findtime")).
				Description(i18n.T("form_fail2ban_findtime_desc")).
				Placeholder("10m").
				Validate(validateFail2banDuration).
				Value(&findTime),
			huh.NewInput().
				Title(i18n.T("form_fail2ban_bantime")).
				Description(i18n.T("form_fail2ban_bantime_desc")).
				Placeholder("1h").
				Validate(validateFail2banDuration).
				Value(&banTime),
			huh.NewInput().
				Title(i18n.T("form_fail2ban_backend")).
				Description(i18n.T("form_fail2ban_backend_desc")).
				Placeholder("systemd").
				Validate(validateFail2banBackend).
				Value(&backend),
			huh.NewInput().
				Title(i18n.T("form_fail2ban_ignoreip")).
				Description(i18n.T("form_fail2ban_ignoreip_desc")).
				Placeholder("127.0.0.1/8 ::1").
				Validate(validateFail2banIgnoreIP).
				Value(&ignoreIP),
		),
	).Run(); err != nil {
		return err
	}

	parsedMaxRetry, err := parsePositiveInt(maxRetry, "maxretry")
	if err != nil {
		return err
	}
	cfg.Fail2banMaxRetry = parsedMaxRetry
	cfg.Fail2banFindTime = strings.TrimSpace(findTime)
	cfg.Fail2banBanTime = strings.TrimSpace(banTime)
	cfg.Fail2banBackend = strings.TrimSpace(backend)
	cfg.Fail2banIgnoreIP = strings.TrimSpace(ignoreIP)
	return nil
}

func modulesCurrentTimezone() (string, bool) {
	res, err := system.Run("timedatectl", "show", "--property=Timezone", "--value")
	if err != nil || res.ExitCode != 0 {
		return "", false
	}
	value := strings.TrimSpace(res.Stdout)
	return value, value != ""
}

func maxInt(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func parseTCPPort(value string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("port must be a number between 1 and 65535")
	}
	return port, nil
}

func parsePositiveInt(value, label string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < 1 {
		return 0, fmt.Errorf("%s must be a positive whole number", label)
	}
	return parsed, nil
}

func validatePublicKeyContent(value string) error {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if len(lines) == 0 || strings.TrimSpace(value) == "" {
		return fmt.Errorf("an SSH public key is required")
	}
	for _, line := range lines {
		if !modules.ValidatePublicKey(line) {
			return fmt.Errorf("invalid SSH public key format")
		}
	}
	return nil
}

func validateFail2banDuration(value string) error {
	if !fail2banDurationRegex.MatchString(strings.TrimSpace(value)) {
		return fmt.Errorf("use a positive duration such as 600, 10m, 1h, 1d, or 1w")
	}
	return nil
}

func validateFail2banBackend(value string) error {
	switch strings.TrimSpace(value) {
	case "systemd", "auto", "polling", "gamin", "pyinotify":
		return nil
	default:
		return fmt.Errorf("backend must be one of systemd, auto, polling, gamin, or pyinotify")
	}
}

func validateFail2banIgnoreIP(value string) error {
	for _, token := range strings.Fields(strings.TrimSpace(value)) {
		if _, err := netip.ParsePrefix(token); err == nil {
			continue
		}
		if _, err := netip.ParseAddr(token); err == nil {
			continue
		}
		return fmt.Errorf("ignoreip contains an invalid IP address or CIDR: %s", token)
	}
	return nil
}
