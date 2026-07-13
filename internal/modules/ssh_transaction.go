package modules

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

var managedSSHDropIn = "/etc/ssh/sshd_config.d/00-sys-bootstrap.conf"
var effectiveSSHPortsFunc = effectiveSSHPorts

// sshTransactionJournal records state before the SSH module mutates anything,
// so rollback can reverse only the tool's own changes.
type sshTransactionJournal struct {
	// Managed drop-in prior state
	hadDropIn   bool
	dropInBytes []byte
	dropInMode  os.FileMode

	// Service state
	serviceName string
	oldPorts    []int

	// Firewall rules added by this run
	addedUFWRules []string

	// Whether prepare completed
	prepared bool
}

// detectSSHDServiceName identifies the SSH service name (ssh or sshd)
// by probing systemctl.
func detectSSHDServiceName() string {
	if res, err := system.Run("systemctl", "list-unit-files"); err == nil && res != nil {
		if strings.Contains(res.Stdout, "ssh.service") {
			return "ssh"
		}
	}
	return "sshd"
}

// captureJournal records pre-mutation state for rollback.
func captureJournal() (*sshTransactionJournal, error) {
	j := &sshTransactionJournal{}

	// Managed drop-in prior state
	if data, err := os.ReadFile(managedSSHDropIn); err == nil {
		j.hadDropIn = true
		j.dropInBytes = data
		if info, err := os.Stat(managedSSHDropIn); err == nil {
			j.dropInMode = info.Mode()
		} else {
			return nil, fmt.Errorf("cannot stat existing managed SSH drop-in: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("cannot read existing managed SSH drop-in: %w", err)
	}

	j.serviceName = detectSSHDServiceName()
	ports, err := effectiveSSHPortsFunc(context.Background())
	if err != nil {
		return nil, fmt.Errorf("cannot capture effective SSH ports: %w", err)
	}
	j.oldPorts = ports
	return j, nil
}

// sshdPreCheck verifies that the host's sshd_config is compatible with the
// managed drop-in approach. It must fail before any mutation if not.
func sshdPreCheck(ctx context.Context, sys *system.Context, log *logging.Logger) error {
	// Read main config
	data, err := os.ReadFile(sshConfigPath)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", sshConfigPath, err)
	}
	includeSeen := false
	inMatch := false
	for lineNumber, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		fields := strings.Fields(trimmed)
		keyword := strings.ToLower(fields[0])
		if keyword == "match" {
			inMatch = len(fields) < 2 || !strings.EqualFold(strings.Join(fields[1:], " "), "all")
			continue
		}
		if keyword == "include" && strings.Contains(strings.ToLower(trimmed), "sshd_config.d") {
			if inMatch {
				return fmt.Errorf("sshd_config.d Include at line %d is inside a Match block", lineNumber+1)
			}
			includeSeen = true
			continue
		}
		if !includeSeen && !inMatch && (keyword == "permitrootlogin" || keyword == "passwordauthentication" || keyword == "pubkeyauthentication") {
			return fmt.Errorf("%s appears before the managed drop-in Include at line %d", fields[0], lineNumber+1)
		}
	}
	if !includeSeen {
		return fmt.Errorf("%s does not have an active global Include for sshd_config.d", sshConfigPath)
	}
	return nil
}

// getEffectiveServiceState returns whether the service is currently active.
func getEffectiveServiceState(svc string) (active bool, enabled bool) {
	if res, err := system.Run("systemctl", "is-active", svc); err == nil && res != nil {
		active = strings.TrimSpace(res.Stdout) == "active"
	}
	if res, err := system.Run("systemctl", "is-enabled", svc); err == nil && res != nil {
		enabled = strings.TrimSpace(res.Stdout) == "enabled"
	}
	return
}

// writeManagedDropIn writes the tool's managed config section.
func writeManagedDropIn(port int, permitRootLogin, passwordAuth string) error {
	return writeManagedDropInPorts([]int{port}, permitRootLogin, passwordAuth)
}

func writeManagedDropInPorts(ports []int, permitRootLogin, passwordAuth string) error {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Managed by sys-bootstrap — do not edit by hand\n"))
	b.WriteString(fmt.Sprintf("# Created: %s\n", time.Now().Format(time.RFC3339)))
	seen := make(map[int]bool)
	for _, port := range ports {
		if port < 1 || port > 65535 || seen[port] {
			continue
		}
		seen[port] = true
		b.WriteString(fmt.Sprintf("Port %d\n", port))
	}
	if len(seen) == 0 {
		return fmt.Errorf("refusing to write managed SSH config without a valid port")
	}
	if permitRootLogin != "" {
		b.WriteString(fmt.Sprintf("PermitRootLogin %s\n", permitRootLogin))
	}
	if passwordAuth != "" {
		b.WriteString(fmt.Sprintf("PasswordAuthentication %s\n", passwordAuth))
	}
	return os.WriteFile(managedSSHDropIn, []byte(b.String()), 0o644)
}

func effectiveSSHPorts(ctx context.Context) ([]int, error) {
	opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := system.RunWithContext(opCtx, "sshd", "-T")
	if err != nil {
		return nil, err
	}
	if res == nil || res.ExitCode != 0 {
		return nil, fmt.Errorf("sshd -T failed: %s", resultStderr(res))
	}
	var ports []int
	seen := make(map[int]bool)
	for _, line := range strings.Split(res.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || !strings.EqualFold(fields[0], "port") {
			continue
		}
		port, convErr := strconv.Atoi(fields[1])
		if convErr == nil && port >= 1 && port <= 65535 && !seen[port] {
			seen[port] = true
			ports = append(ports, port)
		}
	}
	if len(ports) == 0 {
		return nil, fmt.Errorf("sshd -T returned no effective ports")
	}
	sort.Ints(ports)
	return ports, nil
}

func resultStderr(res *system.Result) string {
	if res == nil {
		return "no command result"
	}
	return strings.TrimSpace(res.Stderr)
}

func verifyEffectivePorts(ctx context.Context, wanted []int) error {
	ports, err := effectiveSSHPortsFunc(ctx)
	if err != nil {
		return err
	}
	for _, wantedPort := range wanted {
		found := false
		for _, port := range ports {
			if port == wantedPort {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("effective sshd configuration is missing port %d (effective ports: %v)", wantedPort, ports)
		}
	}
	return nil
}

func effectiveSSHSettings(ctx context.Context, username string) (map[string]string, error) {
	opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := []string{"-T"}
	if username != "" {
		args = append(args, "-C", "user="+username+",host=localhost,addr=127.0.0.1")
	}
	res, err := system.RunWithContext(opCtx, "sshd", args...)
	if err != nil || res == nil || res.ExitCode != 0 {
		return nil, fmt.Errorf("sshd effective-policy query failed for %q: %v (%s)", username, err, resultStderr(res))
	}
	settings := make(map[string]string)
	for _, line := range strings.Split(res.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			settings[strings.ToLower(fields[0])] = strings.ToLower(fields[1])
		}
	}
	return settings, nil
}

func verifyFinalAuthPolicy(ctx context.Context, sys *system.Context, cfg *types.Config) error {
	users := []string{"root"}
	if username := system.TargetUsername(sys); username != "" && username != "root" {
		users = append(users, username)
	}
	if cfg.NewUsername != "" && cfg.NewUsername != "root" && cfg.NewUsername != system.TargetUsername(sys) {
		users = append(users, cfg.NewUsername)
	}
	for _, username := range users {
		settings, err := effectiveSSHSettings(ctx, username)
		if err != nil {
			return err
		}
		if cfg.SSHDisableRoot && username == "root" && settings["permitrootlogin"] != "no" {
			return fmt.Errorf("effective PermitRootLogin for root is %q, want no", settings["permitrootlogin"])
		}
		if cfg.SSHDisablePass && settings["passwordauthentication"] != "no" {
			return fmt.Errorf("effective PasswordAuthentication for %s is %q, want no", username, settings["passwordauthentication"])
		}
	}
	return nil
}

func verifyListeningPorts(ctx context.Context, wanted []int) error {
	if !system.CommandExists("ss") {
		return fmt.Errorf("cannot verify SSH listeners: ss command is unavailable")
	}
	opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := system.RunWithContext(opCtx, "ss", "-ltnpH")
	if err != nil || res == nil || res.ExitCode != 0 {
		return fmt.Errorf("cannot inspect listening TCP ports: %v (%s)", err, resultStderr(res))
	}
	listening := make(map[int]bool)
	for _, line := range strings.Split(res.Stdout, "\n") {
		lowerLine := strings.ToLower(line)
		if !strings.Contains(lowerLine, "sshd") && !strings.Contains(lowerLine, "systemd") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		address := fields[3]
		idx := strings.LastIndex(address, ":")
		if idx < 0 {
			continue
		}
		if port, convErr := strconv.Atoi(strings.Trim(address[idx+1:], "[]")); convErr == nil {
			listening[port] = true
		}
	}
	for _, port := range wanted {
		if !listening[port] {
			return fmt.Errorf("SSH port %d is not listening after reload", port)
		}
	}
	return nil
}

// removeManagedDropIn removes the tool's managed config file if it exists.
func removeManagedDropIn() error {
	if err := os.Remove(managedSSHDropIn); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// readEffectivePorts reads the managed drop-in's port setting.
func readManagedPort() int {
	data, err := os.ReadFile(managedSSHDropIn)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Port ") {
			var p int
			fmt.Sscanf(line, "Port %d", &p)
			return p
		}
	}
	return 0
}

// runSSHDValidate runs sshd -t or sshd -T to check configuration.
func runSSHDValidate(tFlag string) error {
	if res, err := system.Run("sshd", tFlag); err != nil || res == nil || res.ExitCode != 0 {
		detail := ""
		if res != nil {
			detail = res.Stderr
		}
		return fmt.Errorf("sshd %s failed: %s", tFlag, detail)
	}
	return nil
}

// runSSHDReload reloads the SSH service.
func runSSHDReload(svc string) error {
	if res, err := system.Run("systemctl", "reload-or-restart", svc); err != nil || res == nil || res.ExitCode != 0 {
		return fmt.Errorf("failed to reload %s: %v", svc, err)
	}
	return nil
}

// ufwAllowIfMissing checks if the UFW rule exists before adding it, to avoid
// duplicate entries and track only the delta.
func ufwAllowIfMissing(port int, log *logging.Logger) (added bool, err error) {
	if res, err := system.Run("ufw", "status", "verbose"); err == nil && res != nil {
		if strings.Contains(res.Stdout, fmt.Sprintf("%d/tcp", port)) {
			return false, nil // Already allowed
		}
	}
	if res, err := system.Run("ufw", "allow", fmt.Sprintf("%d/tcp", port)); err != nil || res == nil || res.ExitCode != 0 {
		detail := ""
		if res != nil {
			detail = res.Stderr
		}
		return false, fmt.Errorf("ufw allow %d/tcp failed: %s", port, detail)
	}
	return true, nil
}

// prepareSSHPhase performs the prepare phase of SSH hardening:
// write permissive drop-in, validate, add UFW, reload, verify.
func prepareSSHPhase(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger, port int, j *sshTransactionJournal) error {
	log.Info("SSH prepare phase: writing managed drop-in with permissive settings...")

	// Write permissive drop-in: new port only; keep existing auth settings
	preparePorts := append(append([]int{}, j.oldPorts...), port)
	if err := writeManagedDropInPorts(preparePorts, "", ""); err != nil {
		return fmt.Errorf("cannot write managed sshd config: %w", err)
	}
	log.Successf("Managed SSH drop-in written: port %d", port)

	// Validate with sshd -t
	log.Info("Validating managed SSH configuration...")
	if err := runSSHDValidate("-t"); err != nil {
		return fmt.Errorf("managed sshd config validation failed: %w", err)
	}
	if err := verifyEffectivePorts(ctx, preparePorts); err != nil {
		return fmt.Errorf("managed sshd effective-port validation failed: %w", err)
	}
	log.Success("Managed SSH config validation passed")

	// UFW: allow new port if UFW is active and user requested it
	if sys.HasUFW && sys.UFWActive && cfg.SSHAllowUFW {
		log.Infof("Checking UFW for port %d...", port)
		added, err := ufwAllowIfMissing(port, log)
		if err != nil {
			return fmt.Errorf("UFW setup failed: %w", err)
		}
		if added {
			log.Successf("UFW rule added: allow %d/tcp", port)
			j.addedUFWRules = append(j.addedUFWRules, fmt.Sprintf("%d/tcp", port))
		} else {
			log.Infof("UFW rule for port %d already exists", port)
		}
	}

	// Reload sshd
	log.Infof("Reloading SSH service: %s...", j.serviceName)
	if err := runSSHDReload(j.serviceName); err != nil {
		return fmt.Errorf("SSH service reload failed: %w", err)
	}
	if err := verifyListeningPorts(ctx, preparePorts); err != nil {
		return fmt.Errorf("SSH listener validation failed: %w", err)
	}
	log.Successf("SSH service %s reloaded", j.serviceName)

	j.prepared = true
	return nil
}

// finalizeSSHPhase applies restrictive auth after operator confirmation.
func finalizeSSHPhase(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger, port int, j *sshTransactionJournal) error {
	log.Info("SSH finalize phase: applying restrictive auth...")

	// Compute what to write
	permitRootLogin := ""
	if cfg.SSHDisableRoot {
		permitRootLogin = "no"
	}
	passwordAuth := ""
	if cfg.SSHDisablePass {
		passwordAuth = "no"
	}

	// Check for unmanaged extra ports before finalizing
	if cfg.SSHDisableRoot || cfg.SSHDisablePass {
		// We need to ensure no unmanaged port directives exist that would
		// keep old ports open. The user's requested port is the only one
		// we want active.
		extraPorts, err := findUnmanagedPorts(sshConfigPath, port)
		if err != nil {
			return fmt.Errorf("cannot check for unmanaged ports: %w", err)
		}
		if len(extraPorts) > 0 {
			return fmt.Errorf(
				"cannot finalize: unmanaged extra ports found (%v). Remove them from %s and re-run finalization",
				extraPorts, sshConfigPath,
			)
		}
	}

	// Rewrite drop-in with restrictive auth
	if err := writeManagedDropIn(port, permitRootLogin, passwordAuth); err != nil {
		return fmt.Errorf("cannot write final managed sshd config: %w", err)
	}
	log.Success("Managed SSH drop-in updated with restrictive auth")

	// Validate
	log.Info("Validating finalized SSH configuration...")
	if err := runSSHDValidate("-t"); err != nil {
		return fmt.Errorf("finalized sshd config validation failed: %w", err)
	}
	if err := verifyEffectivePorts(ctx, []int{port}); err != nil {
		return fmt.Errorf("finalized sshd effective-port validation failed: %w", err)
	}
	if err := verifyFinalAuthPolicy(ctx, sys, cfg); err != nil {
		return fmt.Errorf("finalized sshd authentication-policy validation failed: %w", err)
	}
	log.Success("Finalized SSH config validation passed")

	// Reload
	log.Infof("Reloading SSH service: %s...", j.serviceName)
	if err := runSSHDReload(j.serviceName); err != nil {
		return fmt.Errorf("SSH service reload failed during finalization: %w", err)
	}
	if err := verifyListeningPorts(ctx, []int{port}); err != nil {
		return fmt.Errorf("final SSH listener validation failed: %w", err)
	}
	log.Successf("SSH service %s reloaded", j.serviceName)

	return nil
}

// findUnmanagedPorts scans the sshd_config for unmanaged Port directives
// that are not in the managed drop-in.
func findUnmanagedPorts(configPath string, managedPort int) ([]int, error) {
	paths := []string{configPath}
	dropIns, err := filepath.Glob(filepath.Join(filepath.Dir(configPath), "sshd_config.d", "*.conf"))
	if err != nil {
		return nil, err
	}
	paths = append(paths, dropIns...)
	var extra []int
	seen := make(map[int]bool)
	for _, path := range paths {
		if path == managedSSHDropIn {
			continue
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			fields := strings.Fields(trimmed)
			if len(fields) != 2 || strings.HasPrefix(trimmed, "#") || !strings.EqualFold(fields[0], "port") {
				continue
			}
			var p int
			if n, _ := fmt.Sscanf(fields[1], "%d", &p); n == 1 && p != managedPort && !seen[p] {
				seen[p] = true
				extra = append(extra, p)
			}
		}
	}
	sort.Ints(extra)
	return extra, nil
}

// rollbackPrepare reverses prepare-phase changes. It uses a separate context
// with timeout to avoid being cancelled with the parent operation.
func rollbackPrepare(ctx context.Context, j *sshTransactionJournal) error {
	// Create independent bounded context for cleanup
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_ = ctx // parent context is intentionally not used for cleanup

	var rollbackErrs []error
	// Restore managed drop-in
	if j.hadDropIn {
		if err := os.WriteFile(managedSSHDropIn, j.dropInBytes, j.dropInMode); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("restore managed sshd config: %w", err))
		}
	} else {
		if err := removeManagedDropIn(); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("remove managed sshd config: %w", err))
		}
	}

	// Remove firewall rules we added
	for _, rule := range j.addedUFWRules {
		if res, err := system.RunWithContext(cleanupCtx, "ufw", "delete", "allow", rule); err != nil || res == nil || res.ExitCode != 0 {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("remove UFW rule %s: %v (%s)", rule, err, resultStderr(res)))
		}
	}

	// Reload sshd to restore effective config
	if err := runSSHDValidate("-t"); err != nil {
		rollbackErrs = append(rollbackErrs, fmt.Errorf("validate restored sshd config: %w", err))
	} else if err := runSSHDReload(j.serviceName); err != nil {
		rollbackErrs = append(rollbackErrs, fmt.Errorf("reload restored sshd config: %w", err))
	} else if len(j.oldPorts) > 0 {
		if err := verifyEffectivePorts(cleanupCtx, j.oldPorts); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("verify restored SSH ports: %w", err))
		} else if err := verifyListeningPorts(cleanupCtx, j.oldPorts); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("verify restored SSH listeners: %w", err))
		}
	}
	return errors.Join(rollbackErrs...)
}
