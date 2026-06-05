package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/FrankWiZe/sys-bootstrap/internal/modules"
	"github.com/FrankWiZe/sys-bootstrap/internal/system"
	"github.com/FrankWiZe/sys-bootstrap/internal/types"
	"github.com/charmbracelet/huh"
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
				Title("Select modules to run").
				Description("Use space to select, enter to confirm").
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
				Title("SSH Port").
				Description("Port for sshd to listen on").
				Placeholder("22122").
				Value(&port),
			huh.NewConfirm().
				Title("Disable root login?").
				Description("Set PermitRootLogin no").
				Value(&cfg.SSHDisableRoot),
			huh.NewConfirm().
				Title("Disable password authentication?").
				Description("Set PasswordAuthentication no (key-only login)").
				Value(&cfg.SSHDisablePass),
			huh.NewConfirm().
				Title("Add SSH public key?").
				Description("Paste a public key to add to authorized_keys").
				Value(&cfg.SSHAddKey),
		),
	}

	// Ask about UFW only when UFW is present and active
	if sys != nil && sys.HasUFW && sys.UFWActive {
		cfg.SSHAllowUFW = true // default to true
		groups = append(groups, huh.NewGroup(
			huh.NewConfirm().
				Title("Allow new SSH port in UFW firewall?").
				Description("Runs ufw allow <port>/tcp to ensure the new port is reachable").
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
					Title("SSH Public Key").
					Description("Paste your public key (ssh-ed25519 AAAA...)").
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

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Username").
				Description("New system user to create").
				Placeholder("deploy").
				Value(&cfg.NewUsername),
			huh.NewSelect[string]().
				Title("Default Shell").
				Options(
					huh.NewOption("bash", "bash"),
					huh.NewOption("zsh", "zsh"),
				).
				Value(&shell),
			huh.NewConfirm().
				Title("Add to sudo group?").
				Value(&cfg.UserAddSudo),
			huh.NewConfirm().
				Title("Add SSH public key?").
				Value(&cfg.UserAddKey),
		),
	)
	if err := form.Run(); err != nil {
		return err
	}
	cfg.UserShell = shell

	if cfg.UserAddKey {
		source := "paste"
		sourceForm := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Public key source").
					Options(
						huh.NewOption("Paste public key", "paste"),
						huh.NewOption("Fetch from GitHub", "github"),
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
						Title("SSH Public Key").
						Description("Paste your public key").
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
						Title("GitHub Username").
						Description("Public keys will be fetched from github.com/<user>.keys").
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
				Title("Key Algorithm").
				Options(
					huh.NewOption("ed25519 (recommended)", "ed25519"),
					huh.NewOption("RSA 4096", "rsa"),
				).
				Value(&keyType),
			huh.NewInput().
				Title("Key Comment").
				Description("Optional comment for the key").
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
					Title(fmt.Sprintf("Key %s already exists. Overwrite?", keyFile)).
					Description("This will replace the existing key").
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
				Title("Execution Plan").
				Description(planText),
			huh.NewConfirm().
				Title("Proceed with execution?").
				Description("This will modify your system").
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
	var selected []string

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("AI CLI Tools").
				Description("Select tools to install").
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
