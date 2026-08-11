package modules

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
var sshCommandExistsFn = system.CommandExists
var sshdRuntimeDir = "/run/sshd"
var ensureSSHDRunDirFn = ensureSSHDRunDir

// sshTransactionJournal records state before the SSH module mutates anything,
// so rollback can reverse only the tool's own changes.
type sshTransactionJournal struct {
	// Managed drop-in prior state
	hadDropIn   bool
	dropInBytes []byte
	dropInMode  os.FileMode

	// Administrator-owned SSH config files whose legacy Port directives were
	// disabled during finalization. Keep exact snapshots so a failed cutover
	// restores the pre-run configuration byte for byte.
	legacyPortFiles []sshConfigFileSnapshot

	// SSH can be activated either by a traditional service unit or by a
	// systemd socket. Keep the pre-mutation activation target so rollback
	// applies the restored configuration through the same path.
	reloadTarget sshReloadTarget
	oldPorts     []int

	// Firewall rules added by this run
	addedUFWRules []string

	// Whether prepare completed
	prepared bool
}

type sshConfigFileSnapshot struct {
	path string
	data []byte
	mode os.FileMode
}

type sshReloadTarget struct {
	unit            string
	socketActivated bool
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

// systemdUnitActive reports whether a unit is currently active. Socket
// activation is usable while the socket is active even though ssh.service is
// intentionally disabled on Ubuntu 22.10+.
func systemdUnitActive(unit string) bool {
	res, err := system.Run("systemctl", "is-active", unit)
	return err == nil && res != nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) == "active"
}

// detectSSHReloadTarget selects the active socket unit before falling back to
// a traditional SSH service. Ubuntu's socket generator reads sshd_config
// snippets on modern releases, but only after daemon-reload and a socket
// restart.
func detectSSHReloadTarget() sshReloadTarget {
	for _, unit := range []string{"ssh.socket", "sshd.socket"} {
		if systemdUnitActive(unit) {
			return sshReloadTarget{unit: unit, socketActivated: true}
		}
	}
	return sshReloadTarget{unit: detectSSHDServiceName()}
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

	j.reloadTarget = detectSSHReloadTarget()
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
func writeManagedDropIn(port int, permitRootLogin, passwordAuth, kbdInteractiveAuth string) error {
	return writeManagedDropInPorts([]int{port}, permitRootLogin, passwordAuth, kbdInteractiveAuth)
}

func writeManagedDropInPorts(ports []int, permitRootLogin, passwordAuth, kbdInteractiveAuth string) error {
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
	if kbdInteractiveAuth != "" {
		b.WriteString(fmt.Sprintf("KbdInteractiveAuthentication %s\n", kbdInteractiveAuth))
	}
	return os.WriteFile(managedSSHDropIn, []byte(b.String()), 0o644)
}

func managedAuthPolicyBeforeRun(j *sshTransactionJournal) (string, string, string, error) {
	if !j.hadDropIn {
		return "", "", "", nil
	}
	var state sshConfigState
	if err := mergeSSHConfigState(&state, j.dropInBytes); err != nil {
		return "", "", "", fmt.Errorf("cannot parse existing managed SSH drop-in: %w", err)
	}
	return state.permitRootLogin, state.passwordAuthentication, state.kbdInteractiveAuthentication, nil
}

func effectiveSSHPorts(ctx context.Context) ([]int, error) {
	output, err := querySSHEffectiveOutput(ctx, "", 0)
	if err != nil {
		return nil, err
	}
	return parseEffectiveSSHPorts(output)
}

func querySSHEffectiveOutput(ctx context.Context, username string, localPort int) (string, error) {
	if err := ensureSSHDRunDirFn(); err != nil {
		return "", fmt.Errorf("cannot prepare sshd runtime directory: %w", err)
	}
	opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := []string{"-T", "-f", sshConfigPath}
	if username != "" {
		connection := "user=" + username + ",host=localhost,addr=127.0.0.1"
		if localPort > 0 {
			connection += fmt.Sprintf(",laddr=127.0.0.1,lport=%d", localPort)
		}
		args = append(args, "-C", connection)
	}
	res, err := system.RunWithContext(opCtx, "sshd", args...)
	if err != nil || res == nil || res.ExitCode != 0 {
		queryContext := "global configuration"
		if username != "" {
			queryContext = fmt.Sprintf("user %q", username)
			if localPort > 0 {
				queryContext += fmt.Sprintf(" on local port %d", localPort)
			}
		}
		return "", fmt.Errorf("sshd effective-configuration query failed for %s: %v (%s)", queryContext, err, resultStderr(res))
	}
	return res.Stdout, nil
}

func parseEffectiveSSHPorts(output string) ([]int, error) {
	var ports []int
	seen := make(map[int]bool)
	for _, line := range strings.Split(output, "\n") {
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

// verifyOnlyEffectivePorts ensures that the final daemon configuration has no
// listener beyond the port(s) explicitly requested by this transaction.
func verifyOnlyEffectivePorts(ctx context.Context, wanted []int) error {
	ports, err := effectiveSSHPortsFunc(ctx)
	if err != nil {
		return err
	}
	wantedSet := make(map[int]bool, len(wanted))
	for _, port := range wanted {
		wantedSet[port] = true
	}
	if len(ports) != len(wantedSet) {
		return fmt.Errorf("effective sshd ports %v are not exactly the requested ports %v", ports, wanted)
	}
	for _, port := range ports {
		if !wantedSet[port] {
			return fmt.Errorf("effective sshd configuration still exposes unmanaged port %d (effective ports: %v)", port, ports)
		}
	}
	return nil
}

func effectiveSSHSettings(ctx context.Context, username string) (map[string]string, error) {
	return effectiveSSHSettingsForPort(ctx, username, 0)
}

func effectiveSSHSettingsForPort(ctx context.Context, username string, localPort int) (map[string]string, error) {
	output, err := querySSHEffectiveOutput(ctx, username, localPort)
	if err != nil {
		return nil, err
	}
	settings := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			settings[strings.ToLower(fields[0])] = strings.ToLower(fields[1])
		}
	}
	return settings, nil
}

func verifyFinalAuthPolicy(ctx context.Context, sys *system.Context, cfg *types.Config) error {
	ports, err := effectiveSSHPortsFunc(ctx)
	if err != nil {
		return fmt.Errorf("cannot inspect effective SSH ports for authentication policy: %w", err)
	}
	users := []string{"root"}
	if username := system.TargetUsername(sys); username != "" && username != "root" {
		users = append(users, username)
	}
	if cfg.NewUsername != "" && cfg.NewUsername != "root" && cfg.NewUsername != system.TargetUsername(sys) {
		users = append(users, cfg.NewUsername)
	}
	for _, username := range users {
		for _, port := range ports {
			settings, err := effectiveSSHSettingsForPort(ctx, username, port)
			if err != nil {
				return err
			}
			if cfg.SSHDisableRoot && username == "root" && settings["permitrootlogin"] != "no" {
				return fmt.Errorf("effective PermitRootLogin for root on port %d is %q, want no", port, settings["permitrootlogin"])
			}
			if cfg.SSHDisablePass && settings["passwordauthentication"] != "no" {
				return fmt.Errorf("effective PasswordAuthentication for %s on port %d is %q, want no", username, port, settings["passwordauthentication"])
			}
			if cfg.SSHDisablePass && settings["kbdinteractiveauthentication"] != "no" {
				return fmt.Errorf("effective KbdInteractiveAuthentication for %s on port %d is %q, want no", username, port, settings["kbdinteractiveauthentication"])
			}
		}
	}
	return nil
}

func verifyListeningPorts(ctx context.Context, wanted []int) error {
	return waitForSSHListeningPorts(ctx, wanted, false)
}

// verifyOnlyListeningPorts waits until sshd/socket listeners exactly match the
// requested ports. Finalization must prove that the legacy listener is gone,
// not merely that the replacement port appeared.
func verifyOnlyListeningPorts(ctx context.Context, wanted []int) error {
	return waitForSSHListeningPorts(ctx, wanted, true)
}

func requireSSHListenerInspection() error {
	if !sshCommandExistsFn("ss") {
		return fmt.Errorf("required command ss is unavailable; install iproute2 before running SSH hardening")
	}
	return nil
}

func waitForSSHListeningPorts(ctx context.Context, wanted []int, requireExact bool) error {
	if err := requireSSHListenerInspection(); err != nil {
		return fmt.Errorf("cannot verify SSH listeners: %w", err)
	}
	opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	wantedSet := make(map[int]bool, len(wanted))
	for _, port := range wanted {
		wantedSet[port] = true
	}
	var lastListening map[int]bool
	for {
		res, err := system.RunWithContext(opCtx, "ss", "-ltnpH")
		if err != nil || res == nil || res.ExitCode != 0 {
			return fmt.Errorf("cannot inspect listening TCP ports: %v (%s)", err, resultStderr(res))
		}
		lastListening = parseSSHListeningPorts(res.Stdout)
		allListening := len(wantedSet) > 0
		for port := range wantedSet {
			if !lastListening[port] {
				allListening = false
				break
			}
		}
		if allListening && (!requireExact || sshListeningPortsMatchExactly(lastListening, wantedSet)) {
			return nil
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-opCtx.Done():
			timer.Stop()
			var observed []int
			for port := range lastListening {
				observed = append(observed, port)
			}
			sort.Ints(observed)
			if requireExact {
				return fmt.Errorf("SSH listeners did not converge exclusively to %v (observed SSH ports: %v): %w", wanted, observed, opCtx.Err())
			}
			return fmt.Errorf("SSH listeners %v did not become ready (observed SSH ports: %v): %w", wanted, observed, opCtx.Err())
		case <-timer.C:
		}
	}
}

func sshListeningPortsMatchExactly(observed, wanted map[int]bool) bool {
	if len(observed) != len(wanted) {
		return false
	}
	for port := range wanted {
		if !observed[port] {
			return false
		}
	}
	return true
}

func parseSSHListeningPorts(output string) map[int]bool {
	listening := make(map[int]bool)
	for _, line := range strings.Split(output, "\n") {
		lowerLine := strings.ToLower(line)
		// ssh.socket is owned by PID 1 and therefore appears as "systemd" in
		// ss output. Match that exact process name, rather than any process
		// whose name happens to contain "systemd" (for example
		// systemd-resolved).
		isSSHD := strings.Contains(lowerLine, `"sshd",`)
		isSystemdSocket := strings.Contains(lowerLine, `"systemd",pid=1,`)
		if !isSSHD && !isSystemdSocket {
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
		if port, err := strconv.Atoi(strings.Trim(address[idx+1:], "[]")); err == nil {
			listening[port] = true
		}
	}
	return listening
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
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.EqualFold(fields[0], "port") {
			if p, err := strconv.Atoi(fields[1]); err == nil && p >= 1 && p <= 65535 {
				return p
			}
		}
	}
	return 0
}

// runSSHDValidate runs sshd -t or sshd -T to check configuration.
func runSSHDValidate(tFlag string) error {
	if err := ensureSSHDRunDir(); err != nil {
		return err
	}
	if res, err := system.Run("sshd", tFlag); err != nil || res == nil || res.ExitCode != 0 {
		detail := ""
		if res != nil {
			detail = res.Stderr
		}
		return fmt.Errorf("sshd %s failed: %s", tFlag, detail)
	}
	return nil
}

// ensureSSHDRunDir covers socket-activated Ubuntu hosts where ssh.service has
// not run yet, so systemd has not created RuntimeDirectory=/run/sshd. sshd -t
// and sshd -T otherwise fail before the first incoming connection.
func ensureSSHDRunDir() error {
	if runtime.GOOS != "linux" {
		return nil
	}
	if info, err := os.Stat(sshdRuntimeDir); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("sshd runtime path %s is not a directory", sshdRuntimeDir)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("cannot inspect sshd runtime directory %s: %w", sshdRuntimeDir, err)
	}
	if err := os.MkdirAll(sshdRuntimeDir, 0o755); err != nil {
		return fmt.Errorf("cannot create sshd runtime directory %s: %w", sshdRuntimeDir, err)
	}
	return nil
}

// reloadSSH applies an SSH configuration through the active activation path.
// Socket-activated Ubuntu systems need daemon-reload so the sshd socket
// generator sees sshd_config.d changes, followed by a socket restart.
func reloadSSH(target sshReloadTarget) error {
	if target.socketActivated {
		if res, err := system.Run("systemctl", "daemon-reload"); err != nil || res == nil || res.ExitCode != 0 {
			return fmt.Errorf("failed to reload systemd units for %s: %v", target.unit, err)
		}
		if res, err := system.Run("systemctl", "restart", target.unit); err != nil || res == nil || res.ExitCode != 0 {
			return fmt.Errorf("failed to restart %s: %v", target.unit, err)
		}
		return nil
	}
	if res, err := system.Run("systemctl", "reload-or-restart", target.unit); err != nil || res == nil || res.ExitCode != 0 {
		return fmt.Errorf("failed to reload %s: %v", target.unit, err)
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

	// Keep the authentication policy already managed by this tool while adding
	// the new port. Omitting it here would temporarily relax a hardened host.
	permitRootLogin, passwordAuth, kbdInteractiveAuth, err := managedAuthPolicyBeforeRun(j)
	if err != nil {
		return err
	}
	preparePorts := append(append([]int{}, j.oldPorts...), port)
	if err := writeManagedDropInPorts(preparePorts, permitRootLogin, passwordAuth, kbdInteractiveAuth); err != nil {
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
	log.Infof("Reloading SSH activation unit: %s...", j.reloadTarget.unit)
	if err := reloadSSH(j.reloadTarget); err != nil {
		return fmt.Errorf("SSH service reload failed: %w", err)
	}
	if err := verifyListeningPorts(ctx, preparePorts); err != nil {
		return fmt.Errorf("SSH listener validation failed: %w", err)
	}
	log.Successf("SSH activation unit %s reloaded", j.reloadTarget.unit)

	j.prepared = true
	return nil
}

// finalizeSSHPhase applies restrictive auth after operator confirmation.
func finalizeSSHPhase(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger, port int, j *sshTransactionJournal) error {
	log.Info("SSH finalize phase: applying restrictive auth...")

	// Preserve restrictions from an earlier run unless this run explicitly
	// tightens them further.
	permitRootLogin, passwordAuth, kbdInteractiveAuth, err := managedAuthPolicyBeforeRun(j)
	if err != nil {
		return err
	}
	if cfg.SSHDisableRoot {
		permitRootLogin = "no"
	}
	if cfg.SSHDisablePass {
		passwordAuth = "no"
		kbdInteractiveAuth = "no"
	}

	// A port change is a replacement operation, even when the user leaves the
	// authentication policy unchanged. Disable explicit legacy Port directives
	// transactionally so stock images with `Port 22` can complete the cutover.
	disabledPorts, err := disableLegacyPortDirectives(sshConfigPath, port, j)
	if err != nil {
		return fmt.Errorf("cannot disable legacy SSH ports: %w", err)
	}
	if len(disabledPorts) > 0 {
		log.Infof("Disabled legacy SSH Port directives: %v", disabledPorts)
	}

	// Rewrite drop-in with restrictive auth
	if err := writeManagedDropIn(port, permitRootLogin, passwordAuth, kbdInteractiveAuth); err != nil {
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
	if err := verifyOnlyEffectivePorts(ctx, []int{port}); err != nil {
		return fmt.Errorf("finalized sshd exclusive-port validation failed: %w", err)
	}
	if err := verifyFinalAuthPolicy(ctx, sys, cfg); err != nil {
		return fmt.Errorf("finalized sshd authentication-policy validation failed: %w", err)
	}
	log.Success("Finalized SSH config validation passed")

	// Reload
	log.Infof("Reloading SSH activation unit: %s...", j.reloadTarget.unit)
	if err := reloadSSH(j.reloadTarget); err != nil {
		return fmt.Errorf("SSH service reload failed during finalization: %w", err)
	}
	if err := verifyOnlyListeningPorts(ctx, []int{port}); err != nil {
		return fmt.Errorf("final SSH exclusive-listener validation failed: %w", err)
	}
	log.Successf("SSH activation unit %s reloaded", j.reloadTarget.unit)

	return nil
}

func sshConfigPaths(configPath string) ([]string, error) {
	dropIns, err := filepath.Glob(filepath.Join(filepath.Dir(configPath), "sshd_config.d", "*.conf"))
	if err != nil {
		return nil, err
	}
	return append([]string{configPath}, dropIns...), nil
}

func disableLegacyPortDirectives(configPath string, managedPort int, j *sshTransactionJournal) ([]int, error) {
	paths, err := sshConfigPaths(configPath)
	if err != nil {
		return nil, err
	}

	seen := make(map[int]bool)
	for _, path := range paths {
		if path == managedSSHDropIn {
			continue
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		updated, ports := commentLegacyPortDirectives(string(data), managedPort)
		if len(ports) == 0 {
			continue
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			return nil, statErr
		}
		j.legacyPortFiles = append(j.legacyPortFiles, sshConfigFileSnapshot{
			path: path,
			data: append([]byte(nil), data...),
			mode: info.Mode(),
		})
		if writeErr := os.WriteFile(path, []byte(updated), info.Mode()); writeErr != nil {
			return nil, writeErr
		}
		for _, port := range ports {
			seen[port] = true
		}
	}

	var ports []int
	for port := range seen {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports, nil
}

func commentLegacyPortDirectives(content string, managedPort int) (string, []int) {
	lines := strings.Split(content, "\n")
	seen := make(map[int]bool)
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		fields := strings.Fields(trimmed)
		if len(fields) != 2 || strings.HasPrefix(trimmed, "#") || !strings.EqualFold(fields[0], "port") {
			continue
		}
		port, err := strconv.Atoi(fields[1])
		if err != nil || port < 1 || port > 65535 || port == managedPort {
			continue
		}
		lines[index] = "# sys-bootstrap: disabled legacy SSH port during managed cutover: " + line
		seen[port] = true
	}

	var ports []int
	for port := range seen {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return strings.Join(lines, "\n"), ports
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
	for _, snapshot := range j.legacyPortFiles {
		if err := os.WriteFile(snapshot.path, snapshot.data, snapshot.mode); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("restore SSH config %s: %w", snapshot.path, err))
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
	} else if err := reloadSSH(j.reloadTarget); err != nil {
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
