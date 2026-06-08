package modules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

const defaultSSHPort = 22122

var sshConfigPath = "/etc/ssh/sshd_config"
var sshServiceReadyFn = sshServiceReady
var sshUFWAllowsPortFn = sshUFWAllowsPort

var pubKeyRegex = regexp.MustCompile(`^(ssh-(rsa|ed25519|dss)|ecdsa-sha2|sk-)`)

type sshConfigState struct {
	port                   int
	portSet                bool
	permitRootLogin        string
	passwordAuthentication string
}

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
		return CheckResult{Satisfied: false, Message: "openssh-server not installed"}
	}
	if _, err := os.Stat(sshConfigPath); err != nil {
		return CheckResult{Satisfied: false, Message: "sshd_config not found"}
	}

	state, err := readSSHConfigState()
	if err != nil {
		return CheckResult{Satisfied: false, Message: fmt.Sprintf("failed to parse sshd_config: %v", err)}
	}

	serviceReady := sshServiceReadyFn()
	parts := []string{
		fmt.Sprintf("port %d", currentSSHPort(state)),
		fmt.Sprintf("service %s", boolWord(serviceReady, "ready", "not ready")),
	}
	if state.permitRootLogin != "" {
		parts = append(parts, "PermitRootLogin "+state.permitRootLogin)
	}
	if state.passwordAuthentication != "" {
		parts = append(parts, "PasswordAuthentication "+state.passwordAuthentication)
	}

	return CheckResult{
		Satisfied: currentSSHPort(state) > 0 && serviceReady,
		Message:   strings.Join(parts, ". "),
	}
}

func (m *SSHModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	port := cfg.SSHPort
	if port == 0 {
		port = defaultSSHPort
	}

	state, err := readSSHConfigState()
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	serviceReady := sshServiceReadyFn()

	var steps []types.Step
	if !sys.HasSSHD || !sys.HasSSHDService || os.IsNotExist(err) {
		steps = append(steps, types.Step{Module: "ssh", Title: "Install OpenSSH server", Detail: "apt-get install openssh-server"})
	}

	needsReload := false
	if currentSSHPort(state) != port {
		steps = append(steps, types.Step{Module: "ssh", Title: "Configure SSH port", Detail: fmt.Sprintf("Set port to %d", port), Risk: "high"})
		needsReload = true
	}
	if cfg.SSHDisableRoot && !strings.EqualFold(state.permitRootLogin, "no") {
		steps = append(steps, types.Step{Module: "ssh", Title: "Disable root login", Detail: "PermitRootLogin no", Risk: "high"})
		needsReload = true
	}
	if cfg.SSHDisablePass && !strings.EqualFold(state.passwordAuthentication, "no") {
		steps = append(steps, types.Step{Module: "ssh", Title: "Disable password auth", Detail: "PasswordAuthentication no", Risk: "high"})
		needsReload = true
	}
	if cfg.SSHAddKey && cfg.SSHPublicKey != "" && !sshAuthorizedKeyPresent(sys, cfg.SSHPublicKey) {
		steps = append(steps, types.Step{Module: "ssh", Title: "Add SSH public key", Detail: "Write to authorized_keys"})
	}
	if sys.HasUFW && sys.UFWActive {
		if cfg.SSHAllowUFW {
			if !sshUFWAllowsPortFn(port) {
				steps = append(steps, types.Step{Module: "ssh", Title: "Allow SSH port in UFW", Detail: fmt.Sprintf("ufw allow %d/tcp", port)})
			}
		} else {
			steps = append(steps, types.Step{Module: "ssh", Title: "UFW firewall warning", Detail: fmt.Sprintf("Port %d may need manual UFW rule", port), Risk: "manual-step"})
		}
	}
	if needsReload {
		steps = append(steps, types.Step{Module: "ssh", Title: "Validate sshd config", Detail: "Run sshd -t after changes"})
	}
	if needsReload || !serviceReady {
		steps = append(steps, types.Step{Module: "ssh", Title: "Restart sshd", Detail: "Restart SSH service"})
	}
	return steps, nil
}

func (m *SSHModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	port := cfg.SSHPort
	if port == 0 {
		port = defaultSSHPort
	}

	if err := ensureOpenSSHServer(ctx, log); err != nil {
		return err
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
		return system.FormatCommandError("sshd config validation failed, rolled back", res, err)
	}
	log.Success("sshd config validation passed")

	// UFW handling
	if sys.HasUFW && sys.UFWActive {
		if cfg.SSHAllowUFW {
			log.Infof("Allowing port %d in UFW...", port)
			if res, err := system.Run("ufw", "allow", fmt.Sprintf("%d/tcp", port)); err != nil || res.ExitCode != 0 {
				return system.FormatCommandError("ufw allow failed", res, err)
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
		return system.FormatCommandError(fmt.Sprintf("failed to restart %s", svc), res, err)
	}
	log.Successf("Service %s restarted", svc)

	if err := syncExistingFail2banSSHDPort(ctx, port, log); err != nil {
		return fmt.Errorf("SSH port changed, but fail2ban sync failed: %w", err)
	}

	log.Warnf("⚠ Test new port before closing old connection: ssh -p %d user@host", port)

	// Write SSH public key to authorized_keys if requested
	if cfg.SSHAddKey && cfg.SSHPublicKey != "" {
		if !ValidatePublicKey(cfg.SSHPublicKey) {
			log.Error("Invalid public key format, skipping authorized_keys write")
		} else {
			home := system.TargetHomeDir(sys)
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
			if sys != nil && sys.InvokingUser != nil {
				system.Run("chown", "-R", fmt.Sprintf("%s:%s", sys.InvokingUser.Username, sys.InvokingUser.Username), sshDir)
			}
			log.Success("SSH public key written to authorized_keys")
		}
	}

	return nil
}

func readSSHConfigState() (sshConfigState, error) {
	content, err := os.ReadFile(sshConfigPath)
	if err != nil {
		return sshConfigState{}, err
	}

	var state sshConfigState
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.ToLower(fields[0])
		value := strings.Join(fields[1:], " ")
		switch key {
		case "port":
			port, convErr := strconv.Atoi(fields[1])
			if convErr != nil {
				return sshConfigState{}, fmt.Errorf("invalid Port value %q", fields[1])
			}
			state.port = port
			state.portSet = true
		case "permitrootlogin":
			state.permitRootLogin = value
		case "passwordauthentication":
			state.passwordAuthentication = value
		}
	}

	return state, nil
}

func currentSSHPort(state sshConfigState) int {
	if state.portSet && state.port > 0 {
		return state.port
	}
	return 22
}

func sshServiceReady() bool {
	if !system.CommandExists("systemctl") {
		return false
	}
	for _, svc := range []string{"sshd", "ssh"} {
		enabledRes, err := system.Run("systemctl", "is-enabled", svc)
		if err != nil || enabledRes.ExitCode != 0 || strings.TrimSpace(enabledRes.Stdout) != "enabled" {
			continue
		}
		activeRes, err := system.Run("systemctl", "is-active", svc)
		if err == nil && activeRes.ExitCode == 0 && strings.TrimSpace(activeRes.Stdout) == "active" {
			return true
		}
	}
	return false
}

func sshUFWAllowsPort(port int) bool {
	if !system.CommandExists("ufw") {
		return false
	}
	res, err := system.Run("ufw", "status")
	if err != nil || res.ExitCode != 0 {
		return false
	}
	needle := fmt.Sprintf("%d/tcp", port)
	for _, line := range strings.Split(res.Stdout, "\n") {
		if strings.Contains(line, needle) && strings.Contains(line, "ALLOW") {
			return true
		}
	}
	return false
}

func sshAuthorizedKeyPresent(sys *system.Context, key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	authorizedKeys := filepath.Join(system.TargetHomeDir(sys), ".ssh", "authorized_keys")
	content, err := os.ReadFile(authorizedKeys)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == key {
			return true
		}
	}
	return false
}

func syncExistingFail2banSSHDPort(ctx context.Context, port int, log *logging.Logger) error {
	if !system.DpkgInstalled("fail2ban") {
		return nil
	}

	content, err := os.ReadFile(fail2banJailLocalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read %s: %w", fail2banJailLocalPath, err)
	}

	updated, changed := rewriteFail2banSSHDPort(string(content), port)
	if !changed {
		return nil
	}

	log.Infof("Syncing fail2ban sshd jail to port %d...", port)
	if err := os.WriteFile(fail2banJailLocalPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("failed to update %s: %w", fail2banJailLocalPath, err)
	}
	if err := validateFail2banConfig("fail2ban configuration validation failed after SSH port change"); err != nil {
		return err
	}
	if fail2banServiceEnabled() {
		if res, err := system.Run("systemctl", "restart", "fail2ban"); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("failed to restart fail2ban after SSH port change", res, err)
		}
		if err := validateFail2banConfig("fail2ban configuration validation failed after restart"); err != nil {
			return err
		}
	}
	log.Successf("Fail2ban sshd jail synced to port %d", port)
	return nil
}

func rewriteFail2banSSHDPort(content string, port int) (string, bool) {
	lines := strings.Split(content, "\n")
	var updated []string
	inSSHD := false
	portSet := false
	changed := false

	flushPort := func() {
		if inSSHD && !portSet {
			updated = append(updated, fmt.Sprintf("port = %d", port))
			portSet = true
			changed = true
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			flushPort()
			inSSHD = strings.EqualFold(trimmed, "[sshd]")
			portSet = false
		}

		if inSSHD && strings.HasPrefix(trimmed, "port") && strings.Contains(trimmed, "=") && !strings.HasPrefix(trimmed, "#") {
			expected := fmt.Sprintf("port = %d", port)
			if trimmed != expected {
				updated = append(updated, expected)
				changed = true
			} else {
				updated = append(updated, line)
			}
			portSet = true
			continue
		}

		updated = append(updated, line)
	}

	flushPort()
	return strings.Join(updated, "\n"), changed
}

func ensureOpenSSHServer(ctx context.Context, log *logging.Logger) error {
	if system.CommandExists("sshd") {
		if _, err := os.Stat(sshConfigPath); err == nil {
			return nil
		}
	}

	log.Info("OpenSSH server not found, installing openssh-server...")
	if res, err := system.RunWithContext(ctx, "apt-get", "update", "-y"); err != nil || res.ExitCode != 0 {
		return system.FormatCommandError("apt-get update before openssh-server install failed", res, err)
	}

	if res, err := system.RunWithContext(ctx, "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "openssh-server"); err != nil || res.ExitCode != 0 {
		return system.FormatCommandError("openssh-server installation failed", res, err)
	}

	if !system.CommandExists("sshd") {
		return fmt.Errorf("openssh-server installation completed but sshd is still not available on PATH")
	}
	if _, err := os.Stat(sshConfigPath); err != nil {
		return fmt.Errorf("openssh-server installation completed but sshd_config is still missing: %w", err)
	}

	log.Success("openssh-server installed")
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
