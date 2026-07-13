package modules

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

type UserModule struct{}

type userState struct {
	Exists               bool
	HomeDir              string
	Shell                string
	InSudoGroup          bool
	PasswordlessSudo     bool
	PasswordKnown        bool
	HasUsablePassword    bool
	AuthorizedKeysExists bool
}

var sudoersDir = "/etc/sudoers.d"

type userDesiredState struct {
	Username             string
	Exists               bool
	NeedsCreate          bool
	NeedsShellUpdate     bool
	NeedsSudoGroup       bool
	NeedsEnableNOPASSWD  bool
	NeedsDisableNOPASSWD bool
	NeedsPassword        bool
	NeedsSSHKey          bool
	NeedsManualPassword  bool
	PasswordReason       string
}

type UserStateInfo struct {
	Exists            bool
	Shell             string
	InSudoGroup       bool
	PasswordlessSudo  bool
	PasswordKnown     bool
	HasUsablePassword bool
}

func NewUserModule() *UserModule { return &UserModule{} }

func (m *UserModule) ID() string             { return "user" }
func (m *UserModule) Name() string           { return "Create User" }
func (m *UserModule) Description() string    { return "Create a new system user" }
func (m *UserModule) DefaultEnabled() bool   { return false }
func (m *UserModule) RequiresRoot() bool     { return true }
func (m *UserModule) Dependencies() []string { return nil }

func (m *UserModule) Check(ctx context.Context, sys *system.Context) CheckResult {
	username := ""
	if sys != nil && sys.InvokingUser != nil {
		username = sys.InvokingUser.Username
	}
	if username == "" {
		return CheckResult{Satisfied: false, Message: "No target username configured yet"}
	}

	cfg := &types.Config{
		NewUsername:          username,
		UserShell:            "bash",
		UserAddSudo:          true,
		UserPasswordlessSudo: false,
	}
	state, err := inspectUserState(username)
	if err != nil {
		return CheckResult{Satisfied: false, Message: fmt.Sprintf("failed to inspect user %s: %v", username, err)}
	}
	desired := evaluateUserDesiredState(cfg, state)
	satisfied := state.Exists && !desired.NeedsCreate && !desired.NeedsShellUpdate && !desired.NeedsSudoGroup && !desired.NeedsDisableNOPASSWD && !desired.NeedsPassword
	return CheckResult{
		Satisfied: satisfied,
		Message:   describeUserTargetState(cfg, state, desired),
	}
}

func (m *UserModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	if cfg.NewUsername == "" {
		return nil, nil
	}

	state, err := inspectUserState(cfg.NewUsername)
	if err != nil {
		return nil, err
	}
	desired := evaluateUserDesiredState(cfg, state)

	var steps []types.Step

	targetShell := desiredUserShell(cfg)
	if desired.NeedsShellUpdate && state.Shell != "" && state.Shell != targetShell {
		steps = append(steps, types.Step{Module: "user", Title: "Update login shell", Detail: fmt.Sprintf("%s -> %s", state.Shell, targetShell)})
	}

	if desired.NeedsSudoGroup {
		steps = append(steps, types.Step{Module: "user", Title: "Add to sudo group", Detail: cfg.NewUsername})
	}
	if cfg.UserAddSudo {
		if desired.NeedsEnableNOPASSWD {
			steps = append(steps, types.Step{Module: "user", Title: "Enable passwordless sudo", Detail: sudoersFile(cfg.NewUsername)})
		}
		if !cfg.UserPasswordlessSudo {
			if desired.NeedsDisableNOPASSWD {
				steps = append(steps, types.Step{Module: "user", Title: "Disable passwordless sudo", Detail: sudoersFile(cfg.NewUsername)})
			}
			if desired.NeedsPassword {
				steps = append(steps, types.Step{Module: "user", Title: "Set password", Detail: desired.PasswordReason, Risk: "interactive"})
			}
		}
	}
	if cfg.UserAddKey {
		if cfg.UserKeySource == "github" && cfg.UserGitHubUser != "" {
			steps = append(steps, types.Step{Module: "user", Title: "Fetch SSH keys from GitHub", Detail: "github.com/" + cfg.UserGitHubUser})
		} else if desired.NeedsSSHKey {
			steps = append(steps, types.Step{Module: "user", Title: "Write SSH public key", Detail: "authorized_keys"})
		}
	}
	if desired.NeedsManualPassword {
		steps = append(steps, types.Step{Module: "user", Title: "Set password", Detail: "No password set automatically — run passwd " + cfg.NewUsername + " manually", Risk: "manual-step"})
	}

	if desired.Exists {
		if len(steps) == 0 {
			return nil, nil
		}
		steps = append([]types.Step{{
			Module: "user",
			Title:  "Supplement existing user",
			Detail: fmt.Sprintf("%s already exists — will apply supplemental updates only", cfg.NewUsername),
		}}, steps...)
		return steps, nil
	}

	steps = append([]types.Step{{
		Module: "user",
		Title:  "Create user",
		Detail: cfg.NewUsername,
	}}, steps...)
	return steps, nil
}

func (m *UserModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	username := cfg.NewUsername
	if username == "" {
		return fmt.Errorf("username is required")
	}

	if userExists(username) {
		log.Warnf("User %s already exists — applying supplemental updates only", username)
		return m.supplementUser(ctx, username, cfg, log)
	}

	shell := desiredUserShell(cfg)

	log.Infof("Creating user %s (shell: %s)...", username, shell)
	if res, err := system.Run("useradd", "-m", "-s", shell, username); err != nil || res.ExitCode != 0 {
		return system.FormatCommandError(fmt.Sprintf("failed to create user %s", username), res, err)
	}
	log.Successf("User %s created", username)

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
	if err := m.writeSSHKey(ctx, username, cfg, log); err != nil {
		return err
	}

	if err := m.setPasswordIfNeeded(ctx, username, cfg, log); err != nil {
		return err
	}

	return nil
}

// supplementUser applies sudo, shell, and SSH key updates to an existing user.
func (m *UserModule) supplementUser(ctx context.Context, username string, cfg *types.Config, log *logging.Logger) error {
	state, err := inspectUserState(username)
	if err != nil {
		return err
	}
	desired := evaluateUserDesiredState(cfg, state)

	shell := desiredUserShell(cfg)
	if desired.NeedsShellUpdate && state.Shell != "" && state.Shell != shell {
		log.Infof("Updating login shell for %s to %s...", username, shell)
		if res, err := system.Run("usermod", "-s", shell, username); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError(fmt.Sprintf("failed to update login shell for %s", username), res, err)
		}
		log.Successf("Login shell updated for %s", username)
	}

	if cfg.UserAddSudo {
		if !desired.NeedsSudoGroup {
			log.Info("User already in sudo group, skipping")
		} else if res, err := system.Run("usermod", "-aG", "sudo", username); err != nil || res.ExitCode != 0 {
			log.Warn(system.FormatCommandError(fmt.Sprintf("could not add %s to sudo group", username), res, err).Error())
		} else {
			log.Successf("%s added to sudo group", username)
		}
		if err := m.configureSudo(username, cfg, log); err != nil {
			return err
		}
	}

	if err := m.writeSSHKey(ctx, username, cfg, log); err != nil {
		return err
	}

	if desired.NeedsPassword {
		return m.setPasswordIfNeeded(ctx, username, cfg, log)
	}
	return nil
}

func (m *UserModule) configureSudo(username string, cfg *types.Config, log *logging.Logger) error {
	if cfg.UserPasswordlessSudo {
		if passwordlessSudoEnabled(username) {
			log.Info("Passwordless sudo already enabled, skipping")
			return nil
		}
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
		if state, err := inspectUserState(username); err == nil && state.Exists && state.PasswordKnown && state.HasUsablePassword {
			log.Info("Existing password already usable for sudo, skipping passwd")
			return nil
		}
		log.Warnf("Set a password for %s so sudo can authenticate this user.", username)
		if err := system.RunInteractiveContext(ctx, "passwd", username); err != nil {
			return system.FormatCommandError(fmt.Sprintf("failed to set password for %s", username), system.ResultFromError(err), err)
		}
		return nil
	}

	if !cfg.UserAddSudo {
		log.Warnf("Please set password manually: passwd %s", username)
	}
	return nil
}

func sudoersFile(username string) string {
	return filepath.Join(sudoersDir, "sys-bootstrap-"+username)
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

// writeSSHKey handles writing SSH public keys for a user with safety:
// passwd-resolved home, symlink rejection, per-line validation,
// deduplication, and target-UID atomic write.
func (m *UserModule) writeSSHKey(ctx context.Context, username string, cfg *types.Config, log *logging.Logger) error {
	if !cfg.UserAddKey {
		return nil
	}

	var keyContent string
	var err error

	if cfg.UserKeySource == "github" && cfg.UserGitHubUser != "" {
		log.Infof("Fetching SSH keys for GitHub user %s...", cfg.UserGitHubUser)
		keyContent, err = fetchGitHubKeys(cfg.UserGitHubUser)
		if err != nil {
			return fmt.Errorf("failed to fetch GitHub keys for %s: %w", cfg.UserGitHubUser, err)
		}
		log.Successf("Fetched %d SSH key(s) from GitHub user %s", len(strings.Split(strings.TrimSpace(keyContent), "\n")), cfg.UserGitHubUser)
	} else if cfg.UserPublicKey != "" {
		keyContent = cfg.UserPublicKey
	} else {
		return fmt.Errorf("SSH key installation was requested for %s but the selected key source is incomplete", username)
	}

	// Validate every non-blank line
	validLines, err := validateKeyLines(keyContent)
	if err != nil {
		return fmt.Errorf("key validation failed: %w", err)
	}

	// Resolve home from passwd
	home, err := resolveHome(username)
	if err != nil {
		return fmt.Errorf("cannot determine home for %s: %w", username, err)
	}

	sshDir := filepath.Join(home, ".ssh")
	keyFile := filepath.Join(sshDir, "authorized_keys")

	// Reject symlinks in path components
	if err := rejectAuthorizedKeysFullPath(home, ".ssh/authorized_keys"); err != nil {
		return fmt.Errorf("path rejected for %s: %w", username, err)
	}

	// Existing keys for deduplication
	existingKeys, err := readExistingKeys(keyFile)
	if err != nil {
		return fmt.Errorf("cannot read existing keys for %s: %w", username, err)
	}

	content := buildAuthorizedKeysContent(existingKeys, validLines)

	// Write atomically as the target user
	if err := writeAuthorizedKeysAsUser(ctx, username, home, sshDir, keyFile, content); err != nil {
		return fmt.Errorf("failed to write authorized_keys for %s: %w", username, err)
	}

	// Verify ownership (chown via sudo may not set correctly; double-check)
	// The write runs as the target user via sudo, so ownership is correct.
	log.Successf("SSH public key(s) written for %s", username)
	return nil
}

func userExists(username string) bool {
	state, err := inspectUserState(username)
	return err == nil && state.Exists
}

func inspectUserState(username string) (userState, error) {
	f, err := os.Open("/etc/passwd")
	if err != nil {
		return userState{}, err
	}
	defer f.Close()

	state := userState{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), ":")
		if len(parts) >= 7 && parts[0] == username {
			state.Exists = true
			state.HomeDir = parts[5]
			state.Shell = parts[6]
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return userState{}, err
	}
	if !state.Exists {
		return state, nil
	}

	if res, err := system.Run("id", "-nG", username); err == nil && res.ExitCode == 0 {
		for _, group := range strings.Fields(strings.TrimSpace(res.Stdout)) {
			if group == "sudo" {
				state.InSudoGroup = true
				break
			}
		}
	}

	state.PasswordlessSudo = passwordlessSudoEnabled(username)
	state.PasswordKnown, state.HasUsablePassword = userPasswordState(username)
	state.AuthorizedKeysExists = authorizedKeysExists(state.HomeDir)
	return state, nil
}

func evaluateUserDesiredState(cfg *types.Config, state userState) userDesiredState {
	desired := userDesiredState{
		Exists:      state.Exists,
		Username:    cfg.NewUsername,
		NeedsCreate: !state.Exists,
	}

	targetShell := desiredUserShell(cfg)
	desired.NeedsShellUpdate = state.Exists && state.Shell != "" && state.Shell != targetShell
	desired.NeedsSudoGroup = cfg.UserAddSudo && !state.InSudoGroup
	desired.NeedsEnableNOPASSWD = cfg.UserAddSudo && cfg.UserPasswordlessSudo && !state.PasswordlessSudo
	desired.NeedsDisableNOPASSWD = cfg.UserAddSudo && !cfg.UserPasswordlessSudo && state.PasswordlessSudo
	desired.NeedsPassword = cfg.UserAddSudo && !cfg.UserPasswordlessSudo && (!state.Exists || !state.PasswordKnown || !state.HasUsablePassword)
	desired.NeedsManualPassword = !cfg.UserAddSudo && !state.Exists

	if cfg.UserAddKey {
		if cfg.UserKeySource == "github" && cfg.UserGitHubUser != "" {
			desired.NeedsSSHKey = true
		} else if cfg.UserPublicKey != "" && !authorizedKeyContains(state.HomeDir, cfg.UserPublicKey) {
			desired.NeedsSSHKey = true
		}
	}

	if desired.NeedsPassword {
		desired.PasswordReason = passwordPlanDetail(cfg.NewUsername, state.Exists, state.PasswordKnown)
	}

	return desired
}

func desiredUserShell(cfg *types.Config) string {
	if cfg != nil && cfg.UserShell == "zsh" {
		return "/bin/zsh"
	}
	return "/bin/bash"
}

func describeUserTargetState(cfg *types.Config, state userState, desired userDesiredState) string {
	if cfg == nil || strings.TrimSpace(cfg.NewUsername) == "" {
		return "No target username configured yet"
	}
	if desired.NeedsCreate {
		return fmt.Sprintf("user %s does not exist yet", cfg.NewUsername)
	}

	var parts []string
	parts = append(parts, fmt.Sprintf("user %s exists", cfg.NewUsername))
	if state.Shell != "" {
		parts = append(parts, "shell "+state.Shell)
	}
	if cfg.UserAddSudo {
		parts = append(parts, "sudo group "+boolWord(!desired.NeedsSudoGroup, "ready", "missing"))
		if cfg.UserPasswordlessSudo {
			parts = append(parts, "passwordless sudo "+boolWord(!desired.NeedsEnableNOPASSWD, "ready", "disabled"))
		} else {
			if desired.NeedsPassword {
				parts = append(parts, "sudo password needs confirmation")
			} else {
				parts = append(parts, "sudo password ready")
			}
		}
	}
	if cfg.UserAddKey {
		if cfg.UserKeySource == "github" && cfg.UserGitHubUser != "" {
			parts = append(parts, "GitHub SSH keys will be refreshed")
		} else {
			parts = append(parts, "authorized_keys "+boolWord(!desired.NeedsSSHKey, "ready", "needs update"))
		}
	}
	return strings.Join(parts, ". ")
}

func DescribeUserCheckForConfig(cfg *types.Config) (CheckResult, error) {
	if cfg == nil || strings.TrimSpace(cfg.NewUsername) == "" {
		return CheckResult{Satisfied: false, Message: "No target username configured yet"}, nil
	}
	state, err := inspectUserState(cfg.NewUsername)
	if err != nil {
		return CheckResult{}, err
	}
	desired := evaluateUserDesiredState(cfg, state)
	satisfied := !desired.NeedsCreate &&
		!desired.NeedsShellUpdate &&
		!desired.NeedsSudoGroup &&
		!desired.NeedsEnableNOPASSWD &&
		!desired.NeedsDisableNOPASSWD &&
		!desired.NeedsPassword &&
		!desired.NeedsSSHKey &&
		!desired.NeedsManualPassword
	return CheckResult{
		Satisfied: satisfied,
		Message:   describeUserTargetState(cfg, state, desired),
	}, nil
}

func LookupUserState(username string) (UserStateInfo, error) {
	state, err := inspectUserState(username)
	if err != nil {
		return UserStateInfo{}, err
	}
	return UserStateInfo{
		Exists:            state.Exists,
		Shell:             state.Shell,
		InSudoGroup:       state.InSudoGroup,
		PasswordlessSudo:  state.PasswordlessSudo,
		PasswordKnown:     state.PasswordKnown,
		HasUsablePassword: state.HasUsablePassword,
	}, nil
}

func passwordPlanDetail(username string, exists bool, passwordKnown bool) string {
	if exists && passwordKnown {
		return "Run passwd " + username + " because the existing account does not currently have a usable password for sudo"
	}
	if exists {
		return "Run passwd " + username + " to confirm or rotate the sudo password for the existing user"
	}
	return "Run passwd " + username + " so sudo can prompt for a password"
}

func passwordlessSudoEnabled(username string) bool {
	_, err := os.Stat(sudoersFile(username))
	return err == nil
}

func userHomeDir(username string) string {
	state, err := inspectUserState(username)
	if err == nil && state.HomeDir != "" {
		return state.HomeDir
	}
	return filepath.Join("/home", username)
}

func authorizedKeysExists(home string) bool {
	if strings.TrimSpace(home) == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(home, ".ssh", "authorized_keys"))
	return err == nil
}

func authorizedKeyContains(home, publicKey string) bool {
	if strings.TrimSpace(home) == "" || strings.TrimSpace(publicKey) == "" {
		return false
	}
	content, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if err != nil {
		return false
	}
	want := strings.TrimSpace(publicKey)
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

func userPasswordState(username string) (known bool, usable bool) {
	f, err := os.Open("/etc/shadow")
	if err != nil {
		return false, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), ":")
		if len(parts) < 2 || parts[0] != username {
			continue
		}
		hash := parts[1]
		if hash == "" || strings.HasPrefix(hash, "!") || strings.HasPrefix(hash, "*") {
			return true, false
		}
		return true, true
	}
	return false, false
}
