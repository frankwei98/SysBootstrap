package modules

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

type UserModule struct{}

func NewUserModule() *UserModule { return &UserModule{} }

func (m *UserModule) ID() string             { return "user" }
func (m *UserModule) Name() string           { return "Create User" }
func (m *UserModule) Description() string    { return "Create a new system user" }
func (m *UserModule) DefaultEnabled() bool   { return false }
func (m *UserModule) RequiresRoot() bool     { return true }
func (m *UserModule) Dependencies() []string { return nil }

func (m *UserModule) Check(ctx context.Context, sys *system.Context) CheckResult {
	return CheckResult{Satisfied: false, Message: "User creation not yet performed"}
}

func (m *UserModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	var steps []types.Step

	if cfg.NewUsername == "" {
		return steps, nil
	}

	exists := userExists(cfg.NewUsername)
	if exists {
		steps = append(steps, types.Step{Module: "user", Title: "Supplement existing user", Detail: fmt.Sprintf("%s already exists — will apply supplemental updates only", cfg.NewUsername)})
	} else {
		steps = append(steps, types.Step{Module: "user", Title: "Create user", Detail: cfg.NewUsername})
	}
	if cfg.UserAddSudo {
		steps = append(steps, types.Step{Module: "user", Title: "Add to sudo group", Detail: cfg.NewUsername})
		if cfg.UserPasswordlessSudo {
			steps = append(steps, types.Step{Module: "user", Title: "Enable passwordless sudo", Detail: "/etc/sudoers.d/sys-bootstrap-" + cfg.NewUsername})
		} else {
			steps = append(steps, types.Step{Module: "user", Title: "Set password", Detail: "Run passwd " + cfg.NewUsername + " so sudo can prompt for a password", Risk: "interactive"})
		}
	}
	if cfg.UserAddKey {
		if cfg.UserKeySource == "github" && cfg.UserGitHubUser != "" {
			steps = append(steps, types.Step{Module: "user", Title: "Fetch SSH keys from GitHub", Detail: "github.com/" + cfg.UserGitHubUser})
		} else if cfg.UserPublicKey != "" {
			steps = append(steps, types.Step{Module: "user", Title: "Write SSH public key", Detail: "authorized_keys"})
		}
	}
	if !cfg.UserAddSudo && !exists {
		steps = append(steps, types.Step{Module: "user", Title: "Set password", Detail: "No password set automatically — run passwd " + cfg.NewUsername + " manually", Risk: "manual-step"})
	}
	return steps, nil
}

func (m *UserModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	username := cfg.NewUsername
	if username == "" {
		return fmt.Errorf("username is required")
	}

	// Check if user already exists
	if userExists(username) {
		log.Warnf("User %s already exists — applying supplemental updates only", username)
		return m.supplementUser(ctx, username, cfg, log)
	}

	shell := "/bin/bash"
	if cfg.UserShell == "zsh" {
		shell = "/bin/zsh"
	}

	// Create user
	log.Infof("Creating user %s (shell: %s)...", username, shell)
	if res, err := system.Run("useradd", "-m", "-s", shell, username); err != nil || res.ExitCode != 0 {
		return system.FormatCommandError(fmt.Sprintf("failed to create user %s", username), res, err)
	}
	log.Successf("User %s created", username)

	// Add to sudo group
	if cfg.UserAddSudo {
		if res, err := system.Run("usermod", "-aG", "sudo", username); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError(fmt.Sprintf("failed to add %s to sudo group", username), res, err)
		}
		log.Successf("%s added to sudo group", username)
		if err := m.configureSudo(username, cfg, log); err != nil {
			return err
		}
	}

	// Write SSH public key
	if err := m.writeSSHKey(username, cfg, log); err != nil {
		return err
	}

	if err := m.setPasswordIfNeeded(ctx, username, cfg, log); err != nil {
		return err
	}

	return nil
}

// supplementUser applies sudo and SSH key updates to an existing user.
func (m *UserModule) supplementUser(ctx context.Context, username string, cfg *types.Config, log *logging.Logger) error {
	if cfg.UserAddSudo {
		if res, err := system.Run("usermod", "-aG", "sudo", username); err != nil || res.ExitCode != 0 {
			log.Warn(system.FormatCommandError(fmt.Sprintf("could not add %s to sudo group (may already be a member)", username), res, err).Error())
		} else {
			log.Successf("%s added to sudo group", username)
		}
		if err := m.configureSudo(username, cfg, log); err != nil {
			return err
		}
	}

	if err := m.writeSSHKey(username, cfg, log); err != nil {
		return err
	}

	return m.setPasswordIfNeeded(ctx, username, cfg, log)
}

func (m *UserModule) configureSudo(username string, cfg *types.Config, log *logging.Logger) error {
	if cfg.UserPasswordlessSudo {
		if err := writePasswordlessSudo(username); err != nil {
			return err
		}
		log.Successf("Passwordless sudo enabled for %s", username)
		return nil
	}

	if err := removePasswordlessSudo(username); err != nil {
		return err
	}
	log.Infof("Passwordless sudo disabled for %s", username)
	return nil
}

func (m *UserModule) setPasswordIfNeeded(ctx context.Context, username string, cfg *types.Config, log *logging.Logger) error {
	if cfg.UserAddSudo && !cfg.UserPasswordlessSudo {
		log.Warnf("Set a password for %s so sudo can authenticate this user.", username)
		if err := system.RunInteractiveContext(ctx, "passwd", username); err != nil {
			return fmt.Errorf("failed to set password for %s: %w", username, err)
		}
		return nil
	}

	if !cfg.UserAddSudo {
		log.Warnf("⚠ Please set password manually: passwd %s", username)
	}
	return nil
}

func sudoersFile(username string) string {
	return filepath.Join("/etc/sudoers.d", "sys-bootstrap-"+username)
}

func writePasswordlessSudo(username string) error {
	path := sudoersFile(username)
	tmp := path + ".tmp"
	content := fmt.Sprintf("%s ALL=(ALL) NOPASSWD: ALL\n", username)

	if err := os.WriteFile(tmp, []byte(content), 0o440); err != nil {
		return fmt.Errorf("failed to write sudoers rule: %w", err)
	}
	if res, err := system.Run("chmod", "0440", tmp); err != nil || res.ExitCode != 0 {
		os.Remove(tmp)
		return system.FormatCommandError("failed to chmod sudoers rule", res, err)
	}
	if system.CommandExists("visudo") {
		if res, err := system.Run("visudo", "-cf", tmp); err != nil || res.ExitCode != 0 {
			os.Remove(tmp)
			return system.FormatCommandError(fmt.Sprintf("invalid sudoers rule for %s", username), res, err)
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("failed to install sudoers rule: %w", err)
	}
	return nil
}

func removePasswordlessSudo(username string) error {
	path := sudoersFile(username)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove passwordless sudo rule: %w", err)
	}
	return nil
}

// writeSSHKey handles writing SSH public keys for a user.
func (m *UserModule) writeSSHKey(username string, cfg *types.Config, log *logging.Logger) error {
	if !cfg.UserAddKey {
		return nil
	}

	var publicKey string

	if cfg.UserKeySource == "github" && cfg.UserGitHubUser != "" {
		log.Infof("Fetching SSH keys for GitHub user %s...", cfg.UserGitHubUser)
		keys, err := fetchGitHubKeys(cfg.UserGitHubUser)
		if err != nil {
			log.Errorf("Failed to fetch GitHub keys: %v", err)
		} else if keys == "" {
			log.Warnf("No public keys found for GitHub user %s", cfg.UserGitHubUser)
		} else {
			publicKey = keys
			log.Successf("Fetched SSH keys from GitHub user %s", cfg.UserGitHubUser)
		}
	} else if cfg.UserPublicKey != "" {
		publicKey = cfg.UserPublicKey
	}

	if publicKey == "" {
		return nil
	}

	if !ValidatePublicKey(strings.Split(publicKey, "\n")[0]) {
		log.Error("Invalid public key format, skipping")
		return nil
	}

	home := filepath.Join("/home", username)
	sshDir := filepath.Join(home, ".ssh")
	keyFile := filepath.Join(sshDir, "authorized_keys")

	os.MkdirAll(sshDir, 0o700)
	f, err := os.OpenFile(keyFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open authorized_keys: %w", err)
	}
	fmt.Fprint(f, publicKey)
	if !strings.HasSuffix(publicKey, "\n") {
		fmt.Fprintln(f)
	}
	f.Close()

	system.Run("chown", "-R", fmt.Sprintf("%s:%s", username, username), sshDir)
	log.Success("SSH public key(s) written")
	return nil
}

// fetchGitHubKeys fetches public keys from github.com/<user>.keys.
func fetchGitHubKeys(username string) (string, error) {
	url := fmt.Sprintf("https://github.com/%s.keys", username)
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	return string(body), nil
}

func userExists(username string) bool {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), ":")
		if len(parts) > 0 && parts[0] == username {
			return true
		}
	}
	return false
}
