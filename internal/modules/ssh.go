package modules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/FrankWiZe/sys-bootstrap/internal/logging"
	"github.com/FrankWiZe/sys-bootstrap/internal/system"
	"github.com/FrankWiZe/sys-bootstrap/internal/types"
)

const sshConfigPath = "/etc/ssh/sshd_config"
const defaultSSHPort = 22122

var pubKeyRegex = regexp.MustCompile(`^(ssh-(rsa|ed25519|dss)|ecdsa-sha2|sk-)`)

type SSHModule struct{}

func NewSSHModule() *SSHModule { return &SSHModule{} }

func (m *SSHModule) ID() string             { return "ssh" }
func (m *SSHModule) Name() string           { return "SSH Hardening" }
func (m *SSHModule) Description() string    { return "SSH port change and key management" }
func (m *SSHModule) DefaultEnabled() bool   { return false }
func (m *SSHModule) RequiresRoot() bool     { return true }
func (m *SSHModule) Dependencies() []string { return nil }

func (m *SSHModule) Check(ctx context.Context, sys *system.Context) CheckResult {
	if !sys.HasSSHD {
		return CheckResult{Satisfied: false, Message: "sshd not found"}
	}
	return CheckResult{Satisfied: false, Message: "SSH configuration not yet applied"}
}

func (m *SSHModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	port := cfg.SSHPort
	if port == 0 {
		port = defaultSSHPort
	}

	steps := []types.Step{
		{Module: "ssh", Title: "Configure SSH port", Detail: fmt.Sprintf("Set port to %d", port), Risk: "high"},
		{Module: "ssh", Title: "Validate sshd config", Detail: "Run sshd -t after changes"},
	}
	if cfg.SSHDisableRoot {
		steps = append(steps, types.Step{Module: "ssh", Title: "Disable root login", Detail: "PermitRootLogin no", Risk: "high"})
	}
	if cfg.SSHDisablePass {
		steps = append(steps, types.Step{Module: "ssh", Title: "Disable password auth", Detail: "PasswordAuthentication no", Risk: "high"})
	}
	if cfg.SSHAddKey && cfg.SSHPublicKey != "" {
		steps = append(steps, types.Step{Module: "ssh", Title: "Add SSH public key", Detail: "Write to authorized_keys"})
	}
	if sys.HasUFW && sys.UFWActive {
		if cfg.SSHAllowUFW {
			steps = append(steps, types.Step{Module: "ssh", Title: "Allow SSH port in UFW", Detail: fmt.Sprintf("ufw allow %d/tcp", port)})
		} else {
			steps = append(steps, types.Step{Module: "ssh", Title: "UFW firewall warning", Detail: fmt.Sprintf("Port %d may need manual UFW rule", port), Risk: "manual-step"})
		}
	}
	steps = append(steps, types.Step{Module: "ssh", Title: "Restart sshd", Detail: "Restart SSH service"})
	return steps, nil
}

func (m *SSHModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	port := cfg.SSHPort
	if port == 0 {
		port = defaultSSHPort
	}

	// Backup sshd_config (preserve original permissions)
	info, err := os.Stat(sshConfigPath)
	if err != nil {
		return fmt.Errorf("failed to stat sshd_config: %w", err)
	}
	origMode := info.Mode()

	backupFile := fmt.Sprintf("%s.bak.%s", sshConfigPath, time.Now().Format("20060102150405"))
	if err := copyFile(sshConfigPath, backupFile); err != nil {
		return fmt.Errorf("failed to backup sshd_config: %w", err)
	}
	log.Successf("Backed up sshd_config to %s", backupFile)

	// Read current config
	content, err := os.ReadFile(sshConfigPath)
	if err != nil {
		return fmt.Errorf("failed to read sshd_config: %w", err)
	}

	// Build new config: comment out existing Port lines, append new port
	lines := strings.Split(string(content), "\n")
	var newLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Port ") && !strings.HasPrefix(trimmed, "#") {
			newLines = append(newLines, "#"+line)
		} else {
			newLines = append(newLines, line)
		}
	}
	newLines = append(newLines, fmt.Sprintf("Port %d", port))

	// Disable root login if requested
	if cfg.SSHDisableRoot {
		newLines = appendSSHDOption(newLines, "PermitRootLogin", "no")
	}

	// Disable password authentication if requested
	if cfg.SSHDisablePass {
		newLines = appendSSHDOption(newLines, "PasswordAuthentication", "no")
	}

	if err := os.WriteFile(sshConfigPath, []byte(strings.Join(newLines, "\n")), origMode); err != nil {
		copyFileWithMode(backupFile, sshConfigPath, origMode)
		return fmt.Errorf("failed to write sshd_config: %w", err)
	}
	log.Successf("SSH port set to %d", port)

	// Validate with sshd -t
	log.Info("Validating sshd configuration...")
	if res, err := system.Run("sshd", "-t"); err != nil || res.ExitCode != 0 {
		copyFileWithMode(backupFile, sshConfigPath, origMode)
		return fmt.Errorf("sshd config validation failed, rolled back: %s", res.Stderr)
	}
	log.Success("sshd config validation passed")

	// UFW handling
	if sys.HasUFW && sys.UFWActive {
		if cfg.SSHAllowUFW {
			log.Infof("Allowing port %d in UFW...", port)
			if res, err := system.Run("ufw", "allow", fmt.Sprintf("%d/tcp", port)); err != nil || res.ExitCode != 0 {
				return fmt.Errorf("ufw allow failed: %s", res.Stderr)
			}
			log.Successf("UFW rule added: allow %d/tcp", port)
		} else {
			log.Warnf("UFW firewall is active — please manually verify port %d is allowed", port)
		}
	}

	// Restart sshd (detect service name)
	svc := "sshd"
	if res, _ := system.Run("systemctl", "list-unit-files"); res != nil && strings.Contains(res.Stdout, "ssh.service") {
		svc = "ssh"
	}
	if res, err := system.Run("systemctl", "restart", svc); err != nil || res.ExitCode != 0 {
		return fmt.Errorf("failed to restart %s: %v", svc, err)
	}
	log.Successf("Service %s restarted", svc)

	log.Warnf("⚠ Test new port before closing old connection: ssh -p %d user@host", port)

	// Write SSH public key to authorized_keys if requested
	if cfg.SSHAddKey && cfg.SSHPublicKey != "" {
		if !ValidatePublicKey(cfg.SSHPublicKey) {
			log.Error("Invalid public key format, skipping authorized_keys write")
		} else {
			home := sys.CurrentUser.HomeDir
			sshDir := filepath.Join(home, ".ssh")
			keyFile := filepath.Join(sshDir, "authorized_keys")

			if err := os.MkdirAll(sshDir, 0o700); err != nil {
				return fmt.Errorf("failed to create .ssh directory: %w", err)
			}
			f, err := os.OpenFile(keyFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				return fmt.Errorf("failed to open authorized_keys: %w", err)
			}
			key := strings.TrimSpace(cfg.SSHPublicKey)
			fmt.Fprintln(f, key)
			f.Close()
			log.Success("SSH public key written to authorized_keys")
		}
	}

	return nil
}

// appendSSHDOption adds or replaces an sshd_config directive.
func appendSSHDOption(lines []string, key, value string) []string {
	prefix := key + " "
	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) && !strings.HasPrefix(trimmed, "#") {
			lines[i] = key + " " + value
			found = true
		}
	}
	if !found {
		lines = append(lines, key+" "+value)
	}
	return lines
}

// ValidatePublicKey checks if a string looks like a valid SSH public key.
func ValidatePublicKey(key string) bool {
	key = strings.TrimSpace(key)
	return pubKeyRegex.MatchString(key)
}

func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, 0o644)
}

func copyFileWithMode(src, dst string, mode os.FileMode) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, in, mode)
}
