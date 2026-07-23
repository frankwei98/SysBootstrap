package modules

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

// Keep the tool's policy isolated from administrator-owned jail.local and
// other jail.d snippets. A managed drop-in can be safely rewritten without
// deleting unrelated jails or changing their [DEFAULT] values.
var fail2banManagedJailPath = "/etc/fail2ban/jail.d/99-sys-bootstrap.local"
var sshdConfigPath = "/etc/ssh/sshd_config"
var fail2banRunAptFn = system.RunApt
var fail2banDurationRegex = regexp.MustCompile(`^[1-9][0-9]*(?:[smhdw])?$`)

const fail2banEffectiveSSHPortFallbackWarning = "warning: sshd -T could not resolve effective SSH ports, so this preview may omit sshd_config.d drop-ins; run as root to verify"

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
	if err := validateFail2banPolicy(cfg); err != nil {
		return nil, err
	}
	var steps []types.Step
	if !system.DpkgInstalled("fail2ban") {
		steps = append(steps, types.Step{Module: "fail2ban", Title: "Install fail2ban", Detail: "apt-get install fail2ban"})
	}
	jailConfigured, _ := fail2banSSHDJailMatchesConfig(cfg)
	if !jailConfigured {
		ports, fellBack := resolveEffectiveSSHPortsForFail2ban(cfg)
		detail := fmt.Sprintf("%s (port %s, maxretry %d, findtime %s, bantime %s, backend %s%s)",
			fail2banManagedJailPath,
			fail2banSSHPortSettingForPorts(ports),
			fail2banMaxRetry(cfg),
			fail2banFindTime(cfg),
			fail2banBanTime(cfg),
			fail2banBackend(cfg),
			fail2banIgnoreIPSummary(cfg),
		)
		if fellBack {
			detail += "; " + fail2banEffectiveSSHPortFallbackWarning
		}
		steps = append(steps, types.Step{
			Module: "fail2ban",
			Title:  "Write sshd jail config",
			Detail: detail,
		})
	}
	if !fail2banServiceEnabled() {
		steps = append(steps, types.Step{Module: "fail2ban", Title: "Enable fail2ban service", Detail: "systemctl enable --now fail2ban"})
	}
	return steps, nil
}

func (m *Fail2banModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	if err := validateFail2banPolicy(cfg); err != nil {
		return err
	}
	if !system.DpkgInstalled("fail2ban") {
		log.Info("Installing fail2ban...")
		if res, err := fail2banRunAptFn(ctx, "update", "-y"); err != nil || res == nil || res.ExitCode != 0 {
			return system.FormatCommandError("apt-get update before fail2ban install failed", res, err)
		}
		if res, err := fail2banRunAptFn(ctx, "install", "-y", "fail2ban"); err != nil || res == nil || res.ExitCode != 0 {
			return system.FormatCommandError("fail2ban installation failed", res, err)
		}
		log.Success("fail2ban installed")
	} else {
		log.Info("fail2ban already installed, skipping package installation")
	}

	jailConfigured, _ := fail2banSSHDJailMatchesConfig(cfg)
	if !jailConfigured {
		log.Info("Writing fail2ban sshd jail configuration...")
		if err := writeFail2banManagedJail(cfg); err != nil {
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
	if err != nil || enabledRes == nil || enabledRes.ExitCode != 0 || strings.TrimSpace(enabledRes.Stdout) != "enabled" {
		return false
	}
	activeRes, err := system.Run("systemctl", "is-active", "fail2ban")
	return err == nil && activeRes != nil && activeRes.ExitCode == 0 && strings.TrimSpace(activeRes.Stdout) == "active"
}

func fail2banSSHDJailMatchesConfig(cfg *types.Config) (bool, string) {
	content, err := os.ReadFile(fail2banManagedJailPath)
	if err != nil {
		return false, "sshd jail missing"
	}
	text := string(content)
	if !strings.Contains(text, "[sshd]") || !strings.Contains(text, "enabled = true") {
		return false, "sshd jail missing"
	}
	ports, fellBack := resolveEffectiveSSHPortsForFail2ban(cfg)
	expectedPort := "port = " + fail2banSSHPortSettingForPorts(ports)
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
	summary := "sshd jail configured"
	if fellBack {
		summary += "; " + fail2banEffectiveSSHPortFallbackWarning
	}
	return true, summary
}

func writeFail2banManagedJail(cfg *types.Config) error {
	if err := os.MkdirAll(filepath.Dir(fail2banManagedJailPath), 0o755); err != nil {
		return fmt.Errorf("failed to create fail2ban config directory: %w", err)
	}
	maxRetry := fail2banMaxRetry(cfg)
	findTime := fail2banFindTime(cfg)
	banTime := fail2banBanTime(cfg)

	content := fmt.Sprintf(`# Managed by sys-bootstrap. Do not edit by hand.
[sshd]
enabled = true
bantime = %s
findtime = %s
maxretry = %d
ignoreip = %s
backend = %s
port = %s
`, banTime, findTime, maxRetry, fail2banIgnoreIP(cfg), fail2banBackend(cfg), fail2banSSHPortSetting(cfg))
	tmp, err := os.CreateTemp(filepath.Dir(fail2banManagedJailPath), ".sys-bootstrap-fail2ban-*")
	if err != nil {
		return fmt.Errorf("failed to create fail2ban jail temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to set fail2ban jail file permissions: %w", err)
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write fail2ban jail config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close fail2ban jail config: %w", err)
	}
	if err := os.Rename(tmpPath, fail2banManagedJailPath); err != nil {
		return fmt.Errorf("failed to install fail2ban jail config: %w", err)
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

func validateFail2banPolicy(cfg *types.Config) error {
	if cfg == nil {
		return nil
	}
	if cfg.Fail2banMaxRetry != 0 && cfg.Fail2banMaxRetry < 1 {
		return fmt.Errorf("fail2ban maxretry must be a positive whole number")
	}
	for label, value := range map[string]string{
		"findtime": cfg.Fail2banFindTime,
		"bantime":  cfg.Fail2banBanTime,
	} {
		if value != "" && !fail2banDurationRegex.MatchString(strings.TrimSpace(value)) {
			return fmt.Errorf("fail2ban %s must be a positive duration such as 600, 10m, 1h, 1d, or 1w", label)
		}
	}
	if backend := strings.TrimSpace(cfg.Fail2banBackend); backend != "" {
		switch backend {
		case "systemd", "auto", "polling", "gamin", "pyinotify":
		default:
			return fmt.Errorf("unsupported fail2ban backend %q", backend)
		}
	}
	for _, token := range strings.Fields(cfg.Fail2banIgnoreIP) {
		if _, err := netip.ParsePrefix(token); err == nil {
			continue
		}
		if _, err := netip.ParseAddr(token); err == nil {
			continue
		}
		return fmt.Errorf("invalid fail2ban ignoreip address %q", token)
	}
	return nil
}

func validateFail2banConfig(action string) error {
	if res, err := system.Run("fail2ban-client", "-d"); err != nil || res == nil || res.ExitCode != 0 {
		return system.FormatCommandError(action, res, err)
	}
	return nil
}

func effectiveSSHPort(cfg *types.Config) int {
	ports := effectiveSSHPortsForFail2ban(cfg)
	if len(ports) > 0 {
		return ports[0]
	}
	return 22
}

// effectiveSSHPortsForFail2ban resolves the daemon's actual port list via
// sshd -T, including sshd_config.d snippets. A requested SSH port takes
// precedence while the SSH module is configuring that exact target.
func effectiveSSHPortsForFail2ban(cfg *types.Config) []int {
	ports, _ := resolveEffectiveSSHPortsForFail2ban(cfg)
	return ports
}

// resolveEffectiveSSHPortsForFail2ban reports whether it had to fall back to
// parsing only the main sshd_config. That fallback cannot see config drop-ins,
// so plan output must not imply that its port list is authoritative.
func resolveEffectiveSSHPortsForFail2ban(cfg *types.Config) ([]int, bool) {
	if cfg != nil && cfg.SSHPort > 0 {
		return []int{cfg.SSHPort}, false
	}
	if ports, err := effectiveSSHPortsFunc(context.Background()); err == nil && len(ports) > 0 {
		return ports, false
	}
	if content, err := os.ReadFile(sshdConfigPath); err == nil {
		for _, line := range strings.Split(string(content), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 2 && strings.EqualFold(fields[0], "port") {
				if port, err := strconv.Atoi(fields[1]); err == nil && port >= 1 && port <= 65535 {
					return []int{port}, true
				}
			}
		}
	}
	return []int{22}, true
}

func fail2banSSHPortSetting(cfg *types.Config) string {
	return fail2banSSHPortSettingForPorts(effectiveSSHPortsForFail2ban(cfg))
}

func fail2banSSHPortSettingForPorts(ports []int) string {
	values := make([]string, 0, len(ports))
	seen := make(map[int]bool, len(ports))
	for _, port := range ports {
		if port < 1 || port > 65535 || seen[port] {
			continue
		}
		seen[port] = true
		values = append(values, strconv.Itoa(port))
	}
	if len(values) == 0 {
		return "22"
	}
	return strings.Join(values, ",")
}

// EffectiveSSHPortSetting exposes the port list that fail2ban will protect so
// the interactive form and plan can show the user the consequential value.
func EffectiveSSHPortSetting(cfg *types.Config) string {
	return fail2banSSHPortSetting(cfg)
}
