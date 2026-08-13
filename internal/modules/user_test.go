package modules

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

func TestUserModuleInterface(t *testing.T) {
	m := NewUserModule()

	if m.ID() != "user" {
		t.Errorf("ID() = %q, want user", m.ID())
	}
	if !m.RequiresRoot() {
		t.Error("user module should require root")
	}
	if m.Dependencies() != nil {
		t.Errorf("Dependencies() = %v, want nil", m.Dependencies())
	}
}

func TestUserCheckUsesRequestedConfig(t *testing.T) {
	originalInspect := inspectUserStateFn
	inspectUserStateFn = func(username string) (userState, error) {
		if username != "alice" {
			t.Fatalf("inspected username = %q, want alice", username)
		}
		return userState{
			Exists:           true,
			Shell:            "/bin/bash",
			InSudoGroup:      true,
			PasswordlessSudo: true,
		}, nil
	}
	t.Cleanup(func() { inspectUserStateFn = originalInspect })

	check := NewUserModule().Check(context.Background(), &system.Context{}, &types.Config{
		NewUsername:          "alice",
		UserShell:            "bash",
		UserAddSudo:          true,
		UserPasswordlessSudo: true,
	})
	if !check.Satisfied {
		t.Fatalf("requested user configuration should be satisfied: %#v", check)
	}
}

func TestUserPasswordStateReportsShadowReadFailure(t *testing.T) {
	missingShadow := filepath.Join(t.TempDir(), "missing-shadow")
	known, usable, err := userPasswordStateFromPath(missingShadow, "alice")
	if err == nil {
		t.Fatalf("userPasswordStateFromPath() = (%v, %v, nil), want read error", known, usable)
	}
	if !strings.Contains(err.Error(), missingShadow) {
		t.Fatalf("error = %q, want shadow path context", err)
	}
}

func TestUserPasswordStateReportsShadowScanFailure(t *testing.T) {
	shadowPath := filepath.Join(t.TempDir(), "shadow")
	if err := os.WriteFile(shadowPath, []byte(strings.Repeat("x", 70*1024)), 0o600); err != nil {
		t.Fatalf("write oversized shadow fixture: %v", err)
	}

	_, _, err := userPasswordStateFromPath(shadowPath, "alice")
	if err == nil || !strings.Contains(err.Error(), "cannot scan password state") {
		t.Fatalf("userPasswordStateFromPath() error = %v, want scan failure", err)
	}
}

func TestSetPasswordStopsWhenUserStateCannotBeRead(t *testing.T) {
	originalInspect := inspectUserStateFn
	inspectUserStateFn = func(string) (userState, error) {
		return userState{}, errors.New("cannot read /etc/shadow")
	}
	t.Cleanup(func() { inspectUserStateFn = originalInspect })

	tempBin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "passwd-called")
	writeFakeCommand(t, tempBin, "passwd", "#!/bin/sh\n: > \"$SYSBOOTSTRAP_TEST_PASSWD_MARKER\"\n")
	t.Setenv("SYSBOOTSTRAP_TEST_PASSWD_MARKER", marker)
	t.Setenv("PATH", tempBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New failed: %v", err)
	}
	defer log.Close()

	err = NewUserModule().setPasswordIfNeeded(context.Background(), "alice", &types.Config{
		UserAddSudo:          true,
		UserPasswordlessSudo: false,
	}, log)
	if err == nil || !strings.Contains(err.Error(), "/etc/shadow") {
		t.Fatalf("setPasswordIfNeeded() error = %v, want user-state read failure", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("passwd ran after user-state read failure: %v", statErr)
	}
}

func TestUserRunRejectsUnavailableLoginShellBeforeCreatingAccount(t *testing.T) {
	originalShellAvailable := loginShellAvailableFn
	loginShellAvailableFn = func(path string) bool {
		return path != "/bin/zsh"
	}
	t.Cleanup(func() { loginShellAvailableFn = originalShellAvailable })

	tempBin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "useradd-called")
	useradd := filepath.Join(tempBin, "useradd")
	if err := os.WriteFile(useradd, []byte("#!/bin/sh\n: > \""+marker+"\"\n"), 0o755); err != nil {
		t.Fatalf("write fake useradd: %v", err)
	}
	t.Setenv("PATH", tempBin)

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New failed: %v", err)
	}
	t.Cleanup(log.Close)

	err = NewUserModule().Run(context.Background(), &system.Context{}, &types.Config{
		NewUsername: "shellcheck_user_12345",
		UserShell:   "zsh",
	}, log)
	if err == nil {
		t.Fatal("expected unavailable zsh to be rejected")
	}
	if !strings.Contains(err.Error(), "/bin/zsh") || !strings.Contains(err.Error(), "install zsh") {
		t.Fatalf("error = %q, want unavailable shell path and installation guidance", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("useradd ran before shell validation; marker stat error = %v", statErr)
	}
}

func TestValidateLinuxUsername(t *testing.T) {
	for _, username := range []string{"deploy", "deploy_1", "user-name"} {
		if !ValidateLinuxUsername(username) {
			t.Errorf("expected %q to be valid", username)
		}
	}
	for _, username := range []string{"-deploy", "Deploy", "", "name with space", " deploy "} {
		if ValidateLinuxUsername(username) {
			t.Errorf("expected %q to be invalid", username)
		}
	}
}

func TestUserPlanNewUser(t *testing.T) {
	m := NewUserModule()
	cfg := &types.Config{
		NewUsername:          "nonexistent_user_12345",
		UserAddSudo:          true,
		UserPasswordlessSudo: true,
		UserAddKey:           true,
		UserPublicKey:        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq",
	}

	steps, err := m.Plan(context.Background(), &system.Context{}, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Should have: create user, add to sudo, passwordless sudo, write SSH key
	if len(steps) < 3 {
		t.Fatalf("expected at least 3 steps, got %d", len(steps))
	}

	// First step should be "Create user" (user doesn't exist)
	if steps[0].Title != "Create user" {
		t.Errorf("first step = %q, want %q", steps[0].Title, "Create user")
	}

	hasPasswordlessSudo := false
	for _, s := range steps {
		if s.Title == "Enable passwordless sudo" {
			hasPasswordlessSudo = true
			if !strings.Contains(s.Detail, "sys-bootstrap-nonexistent_user_12345") {
				t.Errorf("passwordless sudo detail should mention sudoers file, got %q", s.Detail)
			}
		}
		if s.Title == "Set password" {
			t.Errorf("did not expect password step when passwordless sudo is enabled: %#v", s)
		}
	}
	if !hasPasswordlessSudo {
		t.Error("expected 'Enable passwordless sudo' step")
	}
}

func TestUserPlanExistingUser(t *testing.T) {
	tmpDir := t.TempDir()
	oldSudoersDir := sudoersDir
	sudoersDir = tmpDir
	t.Cleanup(func() { sudoersDir = oldSudoersDir })
	originalInspect := inspectUserStateFn
	inspectUserStateFn = func(username string) (userState, error) {
		return userState{Exists: true, HomeDir: "/root", Shell: "/bin/bash"}, nil
	}
	t.Cleanup(func() { inspectUserStateFn = originalInspect })

	m := NewUserModule()
	cfg := &types.Config{
		NewUsername:          "root",
		UserAddSudo:          true,
		UserPasswordlessSudo: true,
	}

	steps, err := m.Plan(context.Background(), &system.Context{}, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// First step should indicate supplement, not create
	if steps[0].Title != "Supplement existing user" {
		t.Errorf("first step = %q, want %q", steps[0].Title, "Supplement existing user")
	}
	if !strings.Contains(steps[0].Detail, "already exists") {
		t.Errorf("supplement step detail should mention 'already exists', got %q", steps[0].Detail)
	}

	// Should still have sudo step
	hasSudo := false
	for _, s := range steps {
		if s.Title == "Add to sudo group" {
			hasSudo = true
		}
	}
	if !hasSudo {
		t.Error("expected 'Add to sudo group' step for existing user")
	}

	hasPasswordlessSudo := false
	for _, s := range steps {
		if s.Title == "Enable passwordless sudo" {
			hasPasswordlessSudo = true
		}
	}
	if !hasPasswordlessSudo {
		t.Error("expected 'Enable passwordless sudo' step for existing user")
	}
}

func TestUserPlanExistingUserNoOpWhenAlreadySatisfied(t *testing.T) {
	tmpDir := t.TempDir()
	oldSudoersDir := sudoersDir
	sudoersDir = tmpDir
	t.Cleanup(func() { sudoersDir = oldSudoersDir })
	origInspect := inspectUserStateFn
	inspectUserStateFn = func(username string) (userState, error) {
		if username != "root" {
			return userState{}, nil
		}
		return userState{Exists: true, HomeDir: "/root", Shell: "/bin/bash"}, nil
	}
	t.Cleanup(func() { inspectUserStateFn = origInspect })

	m := NewUserModule()
	cfg := &types.Config{
		NewUsername:          "root",
		UserShell:            "bash",
		UserAddSudo:          false,
		UserPasswordlessSudo: false,
	}

	steps, err := m.Plan(context.Background(), &system.Context{}, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if len(steps) != 0 {
		t.Fatalf("expected no steps for already-satisfied existing user, got %#v", steps)
	}
}

func TestUserPlanExistingUserShellChangeAndDisablePasswordless(t *testing.T) {
	tmpDir := t.TempDir()
	oldSudoersDir := sudoersDir
	sudoersDir = tmpDir
	t.Cleanup(func() { sudoersDir = oldSudoersDir })
	if err := os.WriteFile(filepath.Join(tmpDir, "sys-bootstrap-root"), []byte("root ALL=(ALL) NOPASSWD: ALL\n"), 0o440); err != nil {
		t.Fatalf("write sudoers file: %v", err)
	}
	originalInspect := inspectUserStateFn
	inspectUserStateFn = func(username string) (userState, error) {
		return userState{
			Exists:            true,
			HomeDir:           "/root",
			Shell:             "/bin/bash",
			InSudoGroup:       true,
			PasswordlessSudo:  true,
			PasswordKnown:     true,
			HasUsablePassword: true,
		}, nil
	}
	t.Cleanup(func() { inspectUserStateFn = originalInspect })

	m := NewUserModule()
	cfg := &types.Config{
		NewUsername:          "root",
		UserShell:            "zsh",
		UserAddSudo:          true,
		UserPasswordlessSudo: false,
	}

	steps, err := m.Plan(context.Background(), &system.Context{}, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	var hasShellUpdate bool
	var hasDisablePasswordless bool
	for _, s := range steps {
		if s.Title == "Update login shell" {
			hasShellUpdate = true
		}
		if s.Title == "Disable passwordless sudo" {
			hasDisablePasswordless = true
		}
	}

	if !hasShellUpdate {
		t.Fatal("expected shell update step for existing user when target shell differs")
	}
	if !hasDisablePasswordless {
		t.Fatal("expected passwordless sudo disable step when switching existing user away from NOPASSWD")
	}
}

func TestEvaluateUserDesiredStateSkipsPasswordWhenExistingPasswordUsable(t *testing.T) {
	cfg := &types.Config{
		NewUsername:          "alice",
		UserAddSudo:          true,
		UserPasswordlessSudo: false,
	}
	state := userState{
		Exists:            true,
		InSudoGroup:       true,
		PasswordKnown:     true,
		HasUsablePassword: true,
	}

	desired := evaluateUserDesiredState(cfg, state)
	if desired.NeedsPassword {
		t.Fatal("expected usable existing password to avoid passwd prompt")
	}
}

func TestUserPlanPasswordSudo(t *testing.T) {
	m := NewUserModule()
	cfg := &types.Config{
		NewUsername:          "nonexistent_user_12345",
		UserAddSudo:          true,
		UserPasswordlessSudo: false,
	}

	steps, err := m.Plan(context.Background(), &system.Context{}, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	lastStep := steps[len(steps)-1]
	if lastStep.Title != "Set password" {
		t.Fatalf("last step = %q, want Set password", lastStep.Title)
	}
	if lastStep.Risk != "interactive" {
		t.Errorf("password step risk = %q, want interactive", lastStep.Risk)
	}
	if !strings.Contains(lastStep.Detail, "passwd nonexistent_user_12345") {
		t.Errorf("password step should mention 'passwd nonexistent_user_12345', got %q", lastStep.Detail)
	}
}

func TestUserPlanNoUsername(t *testing.T) {
	m := NewUserModule()
	cfg := &types.Config{NewUsername: ""}

	steps, err := m.Plan(context.Background(), &system.Context{}, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if len(steps) != 0 {
		t.Errorf("steps len = %d, want 0 when username is empty", len(steps))
	}
}

func TestUserPlanGitHubKeys(t *testing.T) {
	m := NewUserModule()
	cfg := &types.Config{
		NewUsername:    "nonexistent_user_12345",
		UserAddKey:     true,
		UserKeySource:  "github",
		UserGitHubUser: "torvalds",
	}

	steps, err := m.Plan(context.Background(), &system.Context{}, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	hasGitHub := false
	for _, s := range steps {
		if s.Title == "Fetch SSH keys from GitHub" {
			hasGitHub = true
			if s.Detail != "github.com/torvalds" {
				t.Errorf("GitHub step detail = %q, want %q", s.Detail, "github.com/torvalds")
			}
		}
	}
	if !hasGitHub {
		t.Error("expected 'Fetch SSH keys from GitHub' step")
	}
}

func TestUserPlanUsesConfirmedGitHubKeys(t *testing.T) {
	m := NewUserModule()
	cfg := &types.Config{
		NewUsername:    "nonexistent_user_12345",
		UserAddKey:     true,
		UserKeySource:  "github",
		UserGitHubUser: "torvalds",
		UserGitHubKeys: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq reviewed",
	}

	steps, err := m.Plan(context.Background(), &system.Context{}, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	for _, s := range steps {
		if s.Title == "Install confirmed SSH keys from GitHub" {
			return
		}
	}
	t.Fatalf("expected plan to install the reviewed GitHub keys, got %#v", steps)
}

func TestPasswordPlanDetailExistingUser(t *testing.T) {
	got := passwordPlanDetail("alice", true, false)
	if !strings.Contains(got, "confirm or rotate") {
		t.Fatalf("detail = %q, want confirm/rotate wording", got)
	}
}

func TestPasswordPlanDetailExistingUserWithoutUsablePassword(t *testing.T) {
	got := passwordPlanDetail("alice", true, true)
	if !strings.Contains(got, "does not currently have a usable password") {
		t.Fatalf("detail = %q, want unusable-password wording", got)
	}
}

func TestPasswordlessSudoEnabledRejectsNonRootOwner(t *testing.T) {
	originalDir := sudoersDir
	sudoersDir = t.TempDir()
	t.Cleanup(func() { sudoersDir = originalDir })

	path := sudoersFile("alice")
	if err := os.WriteFile(path, []byte("alice ALL=(ALL) NOPASSWD: ALL\n"), 0o440); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(path, 12345, 12345); err != nil {
			t.Fatal(err)
		}
	}
	if passwordlessSudoEnabled("alice") {
		t.Fatal("non-root-owned sudoers rule must not satisfy desired state")
	}
}

func TestDescribeUserTargetStateExistingReadySudoUser(t *testing.T) {
	cfg := &types.Config{
		NewUsername:          "alice",
		UserAddSudo:          true,
		UserPasswordlessSudo: false,
	}
	state := userState{
		Exists:            true,
		Shell:             "/bin/bash",
		InSudoGroup:       true,
		PasswordKnown:     true,
		HasUsablePassword: true,
	}
	desired := evaluateUserDesiredState(cfg, state)

	got := describeUserTargetState(cfg, state, desired)
	if !strings.Contains(got, "sudo password ready") {
		t.Fatalf("state description = %q, want sudo password ready", got)
	}
}

func TestDescribeUserCheckForConfigNoUsername(t *testing.T) {
	check, err := DescribeUserCheckForConfig(&types.Config{})
	if err != nil {
		t.Fatalf("DescribeUserCheckForConfig failed: %v", err)
	}
	if check.Satisfied {
		t.Fatal("expected no-username check to be unsatisfied")
	}
	if !strings.Contains(check.Message, "No target username configured yet") {
		t.Fatalf("unexpected check message: %q", check.Message)
	}
}
