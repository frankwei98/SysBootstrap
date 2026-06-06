package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/frankwei98/sys-bootstrap/internal/i18n"
	"github.com/frankwei98/sys-bootstrap/internal/modules"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

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
		port = "22122"
	}

	groups := []*huh.Group{
		huh.NewGroup(
			huh.NewInput().
				Title(i18n.T("form_ssh_port")).
				Description(i18n.T("form_ssh_port_desc")).
				Placeholder("22122").
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

	fmt.Sscanf(port, "%d", &cfg.SSHPort)

	if cfg.SSHAddKey {
		key := ""
		keyForm := huh.NewForm(
			huh.NewGroup(
				huh.NewText().
					Title(i18n.T("form_ssh_pubkey")).
					Description(i18n.T("form_ssh_pubkey_desc")).
					Placeholder("ssh-ed25519 AAAA...").
					Value(&key),
			),
		)
		if err := keyForm.Run(); err != nil {
			return err
		}
		cfg.SSHPublicKey = strings.TrimSpace(key)
	}

	return nil
}

// UserConfigForm collects user module configuration.
func UserConfigForm(cfg *types.Config) error {
	shell := "zsh"
	passwordlessSudo := true

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title(i18n.T("form_username")).
				Description(i18n.T("form_username_desc")).
				Placeholder("deploy").
				Value(&cfg.NewUsername),
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
					Description(i18n.T("form_passwordless_sudo_desc")).
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
						Value(&ghUser),
				),
			)
			if err := ghForm.Run(); err != nil {
				return err
			}
			cfg.UserGitHubUser = ghUser
		}
	}

	return nil
}

// SSHKeygenForm collects ssh_keygen module configuration.
func SSHKeygenForm(cfg *types.Config) error {
	keyType := "ed25519"
	comment := ""

	hostname, _ := os.Hostname()
	placeholder := cfg.NewUsername + "@" + hostname
	if placeholder == "@" {
		placeholder = "user@host"
	}

	// Check if key already exists
	home, _ := os.UserHomeDir()
	keyFile := filepath.Join(home, ".ssh", "id_"+keyType)
	keyExists := false
	if _, err := os.Stat(keyFile); err == nil {
		keyExists = true
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

	// Ask about overwrite if key exists
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
