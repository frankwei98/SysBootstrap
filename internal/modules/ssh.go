package modules

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

// DefaultSSHPort is the non-standard SSH port proposed by the interactive
// hardening flow. Keeping it exported avoids subtly diverging defaults in the
// CLI, plan, and module implementations.
const DefaultSSHPort = 22122

var sshConfigPath = "/etc/ssh/sshd_config"
var sshServiceReadyFn = sshServiceReady
var sshUFWAllowsPortFn = sshUFWAllowsPort

var supportedPublicKeyTypes = map[string]bool{
	"ssh-ed25519":                        true,
	"ssh-rsa":                            true,
	"ecdsa-sha2-nistp256":                true,
	"ecdsa-sha2-nistp384":                true,
	"ecdsa-sha2-nistp521":                true,
	"sk-ssh-ed25519@openssh.com":         true,
	"sk-ecdsa-sha2-nistp256@openssh.com": true,
}

type sshConfigState struct {
	ports                        []int
	permitRootLogin              string
	passwordAuthentication       string
	kbdInteractiveAuthentication string
	effectiveKnown               bool
	effectiveError               string
}

type SSHModule struct {
	checkpoint types.CheckpointFunc
}

func NewSSHModule() *SSHModule { return &SSHModule{} }

func (m *SSHModule) SetCheckpoint(f types.CheckpointFunc) { m.checkpoint = f }

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

	state, err := readSSHConfigState(ctx, sys, nil)
	if err != nil {
		return CheckResult{Satisfied: false, Message: fmt.Sprintf("failed to parse sshd_config: %v", err)}
	}
	if !state.effectiveKnown {
		return CheckResult{Satisfied: false, Message: "effective sshd configuration unavailable: " + state.effectiveError}
	}

	serviceReady := sshServiceReadyFn()
	parts := []string{
		describeSSHPorts(state),
		fmt.Sprintf("service %s", boolWord(serviceReady, "ready", "not ready")),
	}
	if state.permitRootLogin != "" {
		parts = append(parts, "PermitRootLogin "+state.permitRootLogin)
	}
	if state.passwordAuthentication != "" {
		parts = append(parts, "PasswordAuthentication "+state.passwordAuthentication)
	}
	if state.kbdInteractiveAuthentication != "" {
		parts = append(parts, "KbdInteractiveAuthentication "+state.kbdInteractiveAuthentication)
	}

	return CheckResult{
		Satisfied: len(state.ports) == 1 && serviceReady,
		Message:   strings.Join(parts, ". "),
	}
}

func (m *SSHModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	port := cfg.SSHPort
	if port == 0 {
		port = DefaultSSHPort
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("SSH port %d must be between 1 and 65535", port)
	}
	if err := rejectAddressDependentSSHAuthPolicy(cfg); err != nil {
		return nil, err
	}

	normalizedCfg := *cfg
	normalizedCfg.SSHPort = port
	state, err := readSSHConfigState(ctx, sys, &normalizedCfg)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	serviceReady := sshServiceReadyFn()

	var steps []types.Step
	if !sys.HasSSHD || !sys.HasSSHDService || os.IsNotExist(err) {
		steps = append(steps, types.Step{Module: "ssh", Title: "Install OpenSSH server", Detail: "apt-get install openssh-server"})
	}

	needsReload := false
	if !sshStateUsesOnlyPort(state, port) {
		steps = append(steps, types.Step{Module: "ssh", Title: "Configure SSH port", Detail: fmt.Sprintf("Set port to %d", port), Risk: "high"})
		needsReload = true
	}
	if cfg.SSHDisableRoot && (!state.effectiveKnown || !strings.EqualFold(state.permitRootLogin, "no")) {
		steps = append(steps, types.Step{Module: "ssh", Title: "Disable root login", Detail: "PermitRootLogin no", Risk: "high"})
		needsReload = true
	}
	if cfg.SSHDisablePass && (!state.effectiveKnown || !strings.EqualFold(state.passwordAuthentication, "no") || !strings.EqualFold(state.kbdInteractiveAuthentication, "no")) {
		steps = append(steps, types.Step{Module: "ssh", Title: "Disable password auth", Detail: "PasswordAuthentication no; KbdInteractiveAuthentication no", Risk: "high"})
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
		port = DefaultSSHPort
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("SSH port %d must be between 1 and 65535", port)
	}

	// Guard: requesting to disable both root login and password auth without
	// a requested replacement key is unsafe. Fail before any mutation.
	if cfg.SSHDisableRoot && cfg.SSHDisablePass {
		hasKey := (cfg.SSHAddKey && cfg.SSHPublicKey != "") ||
			(cfg.UserAddKey && (cfg.UserPublicKey != "" || cfg.UserGitHubUser != "" || cfg.UserKeySource == "github"))
		if !hasKey {
			return fmt.Errorf("cannot disable both root login and password authentication without providing an SSH public key — no replacement access path")
		}
	}
	// Reject an existing unsupported policy before package installation or
	// any other mutation. A second check after installation covers fresh hosts.
	if err := rejectAddressDependentSSHAuthPolicy(cfg); err != nil {
		return err
	}
	if err := requireSSHListenerInspection(); err != nil {
		return err
	}

	if err := ensureOpenSSHServer(ctx, log); err != nil {
		return err
	}
	if err := rejectAddressDependentSSHAuthPolicy(cfg); err != nil {
		return err
	}

	// Pre-check: verify the host's sshd_config is compatible with the
	// managed drop-in approach before any mutation.
	if err := sshdPreCheck(ctx, sys, log); err != nil {
		return fmt.Errorf("SSH pre-check failed: %w", err)
	}

	// Capture pre-mutation state for rollback
	journal, err := captureJournal()
	if err != nil {
		return fmt.Errorf("cannot capture pre-mutation state: %w", err)
	}

	// A requested replacement key must be installed before any SSH daemon or
	// firewall mutation. Otherwise a key-write failure can strand a prepared
	// port without the access path the operator was told to test.
	if cfg.SSHAddKey {
		if strings.TrimSpace(cfg.SSHPublicKey) == "" {
			return fmt.Errorf("SSH key installation was requested but no public key was provided")
		}
		if err := m.writeAuthorizedKeys(ctx, sys, cfg, log); err != nil {
			return err
		}
	}

	// === PREPARE PHASE ===
	// Write permissive drop-in (add new port, keep existing auth),
	// validate, add UFW rule, reload, verify.
	log.Info("=== SSH Prepare Phase ===")
	if err := prepareSSHPhase(ctx, sys, cfg, log, port, journal); err != nil {
		return joinSSHRollbackError(fmt.Errorf("SSH prepare failed: %w", err), rollbackPrepare(ctx, journal))
	}
	log.Success("SSH prepare phase complete — dual-path access active")

	// === CONFIRMATION CHECKPOINT ===
	// If a checkpoint function is set (interactive two-phase mode),
	// pause and wait for operator attestation. If not set (noninteractive
	// or single-phase mode), skip directly.
	if m.checkpoint != nil {
		candidates := m.computeAccessPaths(ctx, sys, cfg)
		if (cfg.SSHDisableRoot || cfg.SSHDisablePass) && len(candidates) == 0 {
			return joinSSHRollbackError(
				fmt.Errorf("cannot restrict SSH authentication: no verified replacement access path is available"),
				rollbackPrepare(ctx, journal),
			)
		}
		confirmed, cperr := m.checkpoint(ctx, candidates)
		if cperr != nil {
			return joinSSHRollbackError(fmt.Errorf("SSH confirmation failed: %w", cperr), rollbackPrepare(ctx, journal))
		}
		if !confirmed {
			return types.ErrSSHPendingConfirmation
		}

		// === FINALIZE PHASE ===
		// Apply restrictive auth after operator confirmation.
		log.Info("=== SSH Finalize Phase ===")
		if err := finalizeSSHPhase(ctx, sys, cfg, log, port, journal); err != nil {
			return joinSSHRollbackError(fmt.Errorf("SSH finalize failed: %w", err), rollbackPrepare(ctx, journal))
		}
		log.Success("SSH hardening complete — restrictive auth applied")

		// Keep an existing fail2ban sshd jail aligned with the now-finalized
		// listener. A synchronization failure must not roll SSH back after the
		// operator has already verified and selected the new access path.
		if err := syncExistingFail2banSSHDPort(ctx, port, log); err != nil {
			return fmt.Errorf("SSH finalized, but fail2ban sync failed: %w", err)
		}
	} else {
		return types.ErrSSHPendingConfirmation
	}

	return nil
}

func joinSSHRollbackError(original, rollbackErr error) error {
	if rollbackErr == nil {
		return original
	}
	return fmt.Errorf("%w; EMERGENCY: SSH rollback could not be verified: %v", original, rollbackErr)
}

// writeAuthorizedKeys handles writing the SSH public key from config to the
// invoking user's authorized_keys using shared safety helpers.
func (m *SSHModule) writeAuthorizedKeys(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	username := system.TargetUsername(sys)
	if username == "" {
		log.Warn("No target user for authorized_keys write")
		return nil
	}

	validLines, err := validateKeyLines(cfg.SSHPublicKey)
	if err != nil {
		return fmt.Errorf("key validation failed: %w", err)
	}

	home, err := resolveHome(username)
	if err != nil {
		return fmt.Errorf("cannot determine home for %s: %w", username, err)
	}

	sshDir := filepath.Join(home, ".ssh")
	keyFile := filepath.Join(sshDir, "authorized_keys")

	if err := rejectAuthorizedKeysFullPath(home, ".ssh/authorized_keys"); err != nil {
		return fmt.Errorf("path rejected for %s: %w", username, err)
	}

	existingKeys, err := readExistingKeys(keyFile)
	if err != nil {
		return fmt.Errorf("cannot read existing keys for %s: %w", username, err)
	}

	content := buildAuthorizedKeysContent(existingKeys, validLines)

	if err := writeAuthorizedKeysAsUser(ctx, username, home, sshDir, keyFile, content); err != nil {
		return fmt.Errorf("failed to write authorized_keys for %s: %w", username, err)
	}

	log.Success("SSH public key written to authorized_keys")
	return nil
}

func readSSHConfigState(ctx context.Context, sys *system.Context, cfg *types.Config) (sshConfigState, error) {
	content, err := os.ReadFile(sshConfigPath)
	if err != nil {
		return sshConfigState{}, err
	}

	var state sshConfigState
	if err := mergeSSHConfigState(&state, content); err != nil {
		return sshConfigState{}, err
	}

	managedContent, err := os.ReadFile(managedSSHDropIn)
	if err != nil {
		if !os.IsNotExist(err) {
			return sshConfigState{}, err
		}
	} else if err := mergeSSHConfigState(&state, managedContent); err != nil {
		return sshConfigState{}, err
	}

	effective, effectiveErr := readEffectiveSSHConfigState(ctx, sys, cfg)
	if effectiveErr != nil {
		state.effectiveError = effectiveErr.Error()
		return state, nil
	}
	return effective, nil
}

func readEffectiveSSHConfigState(ctx context.Context, sys *system.Context, cfg *types.Config) (sshConfigState, error) {
	if sys == nil || !sys.HasSSHD {
		return sshConfigState{}, fmt.Errorf("openssh-server is not available")
	}
	users := []string{"root"}
	seenUsers := map[string]bool{"root": true}
	for _, username := range []string{system.TargetUsername(sys), configuredSSHUsername(cfg)} {
		if username != "" && !seenUsers[username] {
			seenUsers[username] = true
			users = append(users, username)
		}
	}

	output, err := querySSHEffectiveOutput(ctx, "", 0)
	if err != nil {
		return sshConfigState{}, err
	}
	var effective sshConfigState
	if err := mergeSSHConfigState(&effective, []byte(output)); err != nil {
		return sshConfigState{}, fmt.Errorf("cannot parse global effective sshd configuration: %w", err)
	}
	if len(effective.ports) == 0 {
		return sshConfigState{}, fmt.Errorf("sshd -T returned no effective ports")
	}
	ports := append([]int{}, effective.ports...)
	if cfg != nil && cfg.SSHPort > 0 && !containsSSHPort(ports, cfg.SSHPort) {
		ports = append(ports, cfg.SSHPort)
	}
	effective.permitRootLogin = ""
	effective.passwordAuthentication = ""
	effective.kbdInteractiveAuthentication = ""
	for _, username := range users {
		for _, port := range ports {
			output, err := querySSHEffectiveOutput(ctx, username, port)
			if err != nil {
				return sshConfigState{}, err
			}
			var userState sshConfigState
			if err := mergeSSHConfigState(&userState, []byte(output)); err != nil {
				return sshConfigState{}, fmt.Errorf("cannot parse effective sshd configuration for %s on port %d: %w", username, port, err)
			}
			if len(userState.ports) == 0 {
				return sshConfigState{}, fmt.Errorf("sshd -T returned no effective ports for %s on port %d", username, port)
			}
			if username == "root" {
				effective.permitRootLogin = lessRestrictiveSSHSetting(effective.permitRootLogin, userState.permitRootLogin)
			}
			effective.passwordAuthentication = lessRestrictiveSSHSetting(effective.passwordAuthentication, userState.passwordAuthentication)
			effective.kbdInteractiveAuthentication = lessRestrictiveSSHSetting(effective.kbdInteractiveAuthentication, userState.kbdInteractiveAuthentication)
		}
	}
	effective.effectiveKnown = true
	return effective, nil
}

func configuredSSHUsername(cfg *types.Config) string {
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.NewUsername)
}

func lessRestrictiveSSHSetting(current, candidate string) string {
	if current == "" {
		return candidate
	}
	if candidate == "" || !strings.EqualFold(current, "no") {
		return current
	}
	if !strings.EqualFold(candidate, "no") {
		return candidate
	}
	return current
}

func sshStateUsesOnlyPort(state sshConfigState, wanted int) bool {
	return state.effectiveKnown && len(state.ports) == 1 && state.ports[0] == wanted
}

func describeSSHPorts(state sshConfigState) string {
	if len(state.ports) == 1 {
		return fmt.Sprintf("port %d", state.ports[0])
	}
	return fmt.Sprintf("ports %v", state.ports)
}

func containsSSHPort(ports []int, wanted int) bool {
	for _, port := range ports {
		if port == wanted {
			return true
		}
	}
	return false
}

func mergeSSHConfigState(state *sshConfigState, content []byte) error {
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
			if convErr != nil || port < 1 || port > 65535 {
				return fmt.Errorf("invalid Port value %q", fields[1])
			}
			if !containsSSHPort(state.ports, port) {
				state.ports = append(state.ports, port)
			}
		case "permitrootlogin":
			state.permitRootLogin = value
		case "passwordauthentication":
			state.passwordAuthentication = value
		case "kbdinteractiveauthentication":
			state.kbdInteractiveAuthentication = value
		}
	}
	return nil
}

func sshServiceReady() bool {
	if !system.CommandExists("systemctl") {
		return false
	}

	// On socket-activated systems, ssh.service is deliberately disabled. The
	// active socket is the thing that makes SSH available, so checking only the
	// service reports a healthy Ubuntu 24.04 host as broken.
	if target := detectSSHReloadTarget(); target.socketActivated {
		return systemdUnitActive(target.unit)
	}

	for _, svc := range []string{"sshd", "ssh"} {
		enabledRes, err := system.Run("systemctl", "is-enabled", svc)
		if err != nil || enabledRes == nil || enabledRes.ExitCode != 0 || strings.TrimSpace(enabledRes.Stdout) != "enabled" {
			continue
		}
		if systemdUnitActive(svc) {
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

	content, err := os.ReadFile(fail2banManagedJailPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read %s: %w", fail2banManagedJailPath, err)
	}

	updated, changed := rewriteFail2banSSHDPort(string(content), port)
	if !changed {
		return nil
	}

	log.Infof("Syncing fail2ban sshd jail to port %d...", port)
	if err := os.WriteFile(fail2banManagedJailPath, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("failed to update %s: %w", fail2banManagedJailPath, err)
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
	if res, err := system.RunApt(ctx, "update", "-y"); err != nil || res == nil || res.ExitCode != 0 {
		return system.FormatCommandError("apt-get update before openssh-server install failed", res, err)
	}

	if res, err := system.RunApt(ctx, "install", "-y", "openssh-server"); err != nil || res == nil || res.ExitCode != 0 {
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

// ValidatePublicKey performs a cheap structural check for a single OpenSSH
// public-key line. validateKeyLines additionally asks ssh-keygen to parse the
// key before any authorized_keys write, which catches malformed wire formats
// that can otherwise look like valid base64.
func ValidatePublicKey(key string) bool {
	fields := strings.Fields(strings.TrimSpace(key))
	if len(fields) < 2 || !supportedPublicKeyTypes[fields[0]] {
		return false
	}

	blob, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil || len(blob) < 5 {
		return false
	}
	declaredLength := binary.BigEndian.Uint32(blob[:4])
	if declaredLength == 0 || declaredLength > uint32(len(blob)-4) {
		return false
	}
	typeEnd := 4 + int(declaredLength)
	declaredType := string(blob[4:typeEnd])
	return declaredType == fields[0] && len(blob) > typeEnd
}

// computeAccessPaths builds the list of replacement access paths from
// filesystem state and user configuration.
func (m *SSHModule) computeAccessPaths(ctx context.Context, sys *system.Context, cfg *types.Config) []types.AccessPath {
	var candidates []types.AccessPath
	port := cfg.SSHPort
	if port == 0 {
		port = DefaultSSHPort
	}

	seen := make(map[string]bool)
	if username := system.TargetUsername(sys); username != "" {
		if candidate, err := verifiedAccessPath(ctx, username, port); err == nil {
			candidates = append(candidates, candidate)
			seen[username] = true
		}
	}

	if cfg.NewUsername != "" && !seen[cfg.NewUsername] {
		if candidate, err := verifiedAccessPath(ctx, cfg.NewUsername, port); err == nil {
			candidates = append(candidates, candidate)
		}
	}

	return candidates
}

func verifiedAccessPath(ctx context.Context, username string, port int) (types.AccessPath, error) {
	u, err := lookupUser(username)
	if err != nil {
		return types.AccessPath{}, err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return types.AccessPath{}, fmt.Errorf("invalid UID for %s: %w", username, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return types.AccessPath{}, fmt.Errorf("invalid GID for %s: %w", username, err)
	}
	if err := rejectAuthorizedKeysFullPath(u.HomeDir, ".ssh/authorized_keys"); err != nil {
		return types.AccessPath{}, err
	}
	keyFile := filepath.Join(u.HomeDir, ".ssh", "authorized_keys")
	info, err := os.Lstat(keyFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return types.AccessPath{}, fmt.Errorf("authorized_keys for %s is missing or has unsafe type/mode", username)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != uid {
		return types.AccessPath{}, fmt.Errorf("authorized_keys for %s is not owned by UID %d", username, uid)
	}
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return types.AccessPath{}, err
	}
	keys, err := validateKeyLines(string(data))
	if err != nil {
		return types.AccessPath{}, err
	}
	if err := verifyPubkeyAuthentication(ctx, username); err != nil {
		return types.AccessPath{}, err
	}
	if err := verifyEffectivePorts(ctx, []int{port}); err != nil {
		return types.AccessPath{}, err
	}
	fingerprint, err := canonicalKeyFingerprint(ctx, keys[0])
	if err != nil {
		return types.AccessPath{}, err
	}
	return types.AccessPath{Username: username, UID: uid, GID: gid, HomeDir: u.HomeDir, KeyFingerprint: fingerprint, PreparedPort: port}, nil
}

func verifyPubkeyAuthentication(ctx context.Context, username string) error {
	opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := system.RunWithContext(opCtx, "sshd", "-T", "-C", "user="+username+",host=localhost,addr=127.0.0.1")
	if err != nil || res == nil || res.ExitCode != 0 {
		return fmt.Errorf("cannot verify effective SSH policy for %s: %v (%s)", username, err, resultStderr(res))
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "pubkeyauthentication yes") {
			return nil
		}
	}
	return fmt.Errorf("effective SSH policy does not enable public-key authentication for %s", username)
}

func canonicalKeyFingerprint(ctx context.Context, key string) (string, error) {
	opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := system.RunWithInputContext(opCtx, key+"\n", "ssh-keygen", "-lf", "-")
	if err != nil || res == nil || res.ExitCode != 0 {
		return "", fmt.Errorf("cannot compute SSH key fingerprint: %v (%s)", err, resultStderr(res))
	}
	fields := strings.Fields(res.Stdout)
	if len(fields) < 2 {
		return "", fmt.Errorf("ssh-keygen returned an invalid fingerprint")
	}
	return fields[1], nil
}

// extractFingerprint takes authorized_keys content and returns the fingerprint
// of the first valid key, or "<unknown>" if parsing fails.
func extractFingerprint(content string) string {
	lines := strings.SplitN(content, "\n", 2)
	if len(lines) == 0 || lines[0] == "" {
		return "<unknown>"
	}
	if !ValidatePublicKey(lines[0]) {
		return "<unknown>"
	}
	// Return a shortened representation of the key
	parts := strings.Fields(lines[0])
	if len(parts) >= 2 {
		key := parts[1]
		if len(key) > 40 {
			key = key[:40] + "..."
		}
		return parts[0] + " " + key
	}
	return "<unknown>"
}

// lookupUser wraps os/user.Lookup for mockability in tests.
var lookupUser = func(username string) (*user.User, error) {
	return user.Lookup(username)
}
