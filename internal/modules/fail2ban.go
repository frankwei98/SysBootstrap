package modules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

var fail2banJailLocalPath = "/etc/fail2ban/jail.local"
var sshdConfigPath = "/etc/ssh/sshd_config"

type Fail2banModule struct{}

func NewFail2banModule() *Fail2banModule { return &Fail2banModule{} }

func (m *Fail2banModule) ID() string             { return "fail2ban" }
func (m *Fail2banModule) Name() string           { return "Fail2ban Protection" }
func (m *Fail2banModule) Description() string    { return "Install fail2ban and enable SSH jail" }
func (m *Fail2banModule) DefaultEnabled() bool   { return false }
func (m *Fail2banModule) RequiresRoot() bool     { return true }
func (m *Fail2banModule) Dependencies() []string { return nil }

func (m *Fail2banModule) Check(ctx context.Context, sys *system.Context) CheckResult {
	installed := system.DpkgInstalled("fail2ban")
	serviceEnabled := fail2banServiceEnabled()
	jailConfigured, summary := fail2banSSHDJailMatchesConfig(nil)
	if installed && serviceEnabled && jailConfigured {
		return CheckResult{Satisfied: true, Message: "fail2ban installed. service enabled. " + summary}
	}
	return CheckResult{
		Satisfied: false,
		Message: fmt.Sprintf(
			"fail2ban %s. service %s. %s",
			boolWord(installed, "installed", "missing"),
			boolWord(serviceEnabled, "enabled", "disabled"),
			summary,
		),
	}
}

func (m *Fail2banModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	var steps []types.Step
	if !system.DpkgInstalled("fail2ban") {
		steps = append(steps, types.Step{Module: "fail2ban", Title: "Install fail2ban", Detail: "apt-get install fail2ban"})
	}
	jailConfigured, _ := fail2banSSHDJailMatchesConfig(cfg)
	if !jailConfigured {
		steps = append(steps, types.Step{
			Module: "fail2ban",
			Title:  "Write sshd jail config",
			Detail: fmt.Sprintf("%s (port %d, maxretry %d, findtime %s, bantime %s, backend %s%s)",
				fail2banJailLocalPath,
				effectiveSSHPort(cfg),
				fail2banMaxRetry(cfg),
				fail2banFindTime(cfg),
				fail2banBanTime(cfg),
				fail2banBackend(cfg),
				fail2banIgnoreIPSummary(cfg),
			),
		})
	}
	if !fail2banServiceEnabled() {
		steps = append(steps, types.Step{Module: "fail2ban", Title: "Enable fail2ban service", Detail: "systemctl enable --now fail2ban"})
	}
	return steps, nil
}

func (m *Fail2banModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	if !system.DpkgInstalled("fail2ban") {
		log.Info("Installing fail2ban...")
		if res, err := system.RunWithContext(ctx, "apt-get", "update", "-y"); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("apt-get update before fail2ban install failed", res, err)
		}
		if res, err := system.RunWithContext(ctx, "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "fail2ban"); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("fail2ban installation failed", res, err)
		}
		log.Success("fail2ban installed")
	} else {
		log.Info("fail2ban already installed, skipping package installation")
	}

	jailConfigured, _ := fail2banSSHDJailMatchesConfig(cfg)
	if !jailConfigured {
		log.Info("Writing fail2ban sshd jail configuration...")
		if err := writeFail2banJailLocal(cfg); err != nil {
			return err
		}
		log.Success("fail2ban sshd jail configured")
	}

	log.Info("Validating fail2ban configuration before service changes...")
	if err := validateFail2banConfig("fail2ban configuration validation failed before service changes"); err != nil {
		return err
	}

	if !fail2banServiceEnabled() {
		log.Info("Enabling fail2ban service...")
		if res, err := system.Run("systemctl", "enable", "--now", "fail2ban"); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("failed to enable fail2ban service", res, err)
		}
		log.Success("fail2ban service enabled")
	} else {
		log.Info("fail2ban service already enabled, restarting")
		if res, err := system.Run("systemctl", "restart", "fail2ban"); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("failed to restart fail2ban service", res, err)
		}
		log.Success("fail2ban service restarted")
	}

	log.Info("Validating fail2ban configuration after service changes...")
	if err := validateFail2banConfig("fail2ban configuration validation failed after service changes"); err != nil {
		return err
	}

	return nil
}

func fail2banServiceEnabled() bool {
	if !system.CommandExists("systemctl") {
		return false
	}
	enabledRes, err := system.Run("systemctl", "is-enabled", "fail2ban")
	if err != nil || enabledRes.ExitCode != 0 || strings.TrimSpace(enabledRes.Stdout) != "enabled" {
		return false
	}
	activeRes, err := system.Run("systemctl", "is-active", "fail2ban")
	return err == nil && activeRes.ExitCode == 0 && strings.TrimSpace(activeRes.Stdout) == "active"
}

func fail2banSSHDJailMatchesConfig(cfg *types.Config) (bool, string) {
	content, err := os.ReadFile(fail2banJailLocalPath)
	if err != nil {
		return false, "sshd jail missing"
	}
	text := string(content)
	if !strings.Contains(text, "[sshd]") || !strings.Contains(text, "enabled = true") {
		return false, "sshd jail missing"
	}
	expectedPort := fmt.Sprintf("port = %d", effectiveSSHPort(cfg))
	expectedMaxRetry := fmt.Sprintf("maxretry = %d", fail2banMaxRetry(cfg))
	expectedFindTime := fmt.Sprintf("findtime = %s", fail2banFindTime(cfg))
	expectedBanTime := fmt.Sprintf("bantime = %s", fail2banBanTime(cfg))
	expectedBackend := fmt.Sprintf("backend = %s", fail2banBackend(cfg))
	expectedIgnoreIP := fmt.Sprintf("ignoreip = %s", fail2banIgnoreIP(cfg))
	missing := []string{}
	for _, item := range []string{expectedPort, expectedMaxRetry, expectedFindTime, expectedBanTime, expectedBackend, expectedIgnoreIP} {
		if !strings.Contains(text, item) {
			missing = append(missing, item)
		}
	}
	if len(missing) > 0 {
		return false, "sshd jail differs from target policy"
	}
	return true, "sshd jail configured"
}

func writeFail2banJailLocal(cfg *types.Config) error {
	if err := os.MkdirAll(filepath.Dir(fail2banJailLocalPath), 0o755); err != nil {
		return fmt.Errorf("failed to create fail2ban config directory: %w", err)
	}
	port := effectiveSSHPort(cfg)
	maxRetry := fail2banMaxRetry(cfg)
	findTime := fail2banFindTime(cfg)
	banTime := fail2banBanTime(cfg)

	content := fmt.Sprintf(`[DEFAULT]
bantime = %s
findtime = %s
maxretry = %d
ignoreip = %s

[sshd]
enabled = true
backend = %s
port = %d
`, banTime, findTime, maxRetry, fail2banIgnoreIP(cfg), fail2banBackend(cfg), port)
	if err := os.WriteFile(fail2banJailLocalPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("failed to write fail2ban jail config: %w", err)
	}
	return nil
}

func fail2banMaxRetry(cfg *types.Config) int {
	if cfg != nil && cfg.Fail2banMaxRetry > 0 {
		return cfg.Fail2banMaxRetry
	}
	return 5
}

func fail2banFindTime(cfg *types.Config) string {
	if cfg != nil && strings.TrimSpace(cfg.Fail2banFindTime) != "" {
		return strings.TrimSpace(cfg.Fail2banFindTime)
	}
	return "10m"
}

func fail2banBanTime(cfg *types.Config) string {
	if cfg != nil && strings.TrimSpace(cfg.Fail2banBanTime) != "" {
		return strings.TrimSpace(cfg.Fail2banBanTime)
	}
	return "1h"
}

func fail2banBackend(cfg *types.Config) string {
	if cfg != nil && strings.TrimSpace(cfg.Fail2banBackend) != "" {
		return strings.TrimSpace(cfg.Fail2banBackend)
	}
	return "systemd"
}

func fail2banIgnoreIP(cfg *types.Config) string {
	if cfg != nil && strings.TrimSpace(cfg.Fail2banIgnoreIP) != "" {
		return strings.TrimSpace(cfg.Fail2banIgnoreIP)
	}
	return "127.0.0.1/8 ::1"
}

func fail2banIgnoreIPSummary(cfg *types.Config) string {
	if cfg == nil || strings.TrimSpace(cfg.Fail2banIgnoreIP) == "" {
		return ""
	}
	return fmt.Sprintf(", ignoreip %s", fail2banIgnoreIP(cfg))
}

func validateFail2banConfig(action string) error {
	if res, err := system.Run("fail2ban-client", "-d"); err != nil || res.ExitCode != 0 {
		return system.FormatCommandError(action, res, err)
	}
	return nil
}

func effectiveSSHPort(cfg *types.Config) int {
	if cfg != nil && cfg.SSHPort > 0 {
		return cfg.SSHPort
	}
	if content, err := os.ReadFile(sshdConfigPath); err == nil {
		for _, line := range strings.Split(string(content), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, "Port ") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if port, err := strconv.Atoi(fields[1]); err == nil && port > 0 {
						return port
					}
				}
			}
		}
	}
	return 22
}
