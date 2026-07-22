package modules

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
