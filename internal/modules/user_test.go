package modules

import (
	"context"
	"strings"
	"testing"

	"github.com/FrankWiZe/sys-bootstrap/internal/system"
	"github.com/FrankWiZe/sys-bootstrap/internal/types"
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

func TestUserPlanNewUser(t *testing.T) {
	m := NewUserModule()
	cfg := &types.Config{
		NewUsername:   "nonexistent_user_12345",
		UserAddSudo:   true,
		UserAddKey:    true,
		UserPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq",
	}

	steps, err := m.Plan(context.Background(), &system.Context{}, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Should have: create user, add to sudo, write SSH key, set password
	if len(steps) < 3 {
		t.Fatalf("expected at least 3 steps, got %d", len(steps))
	}

	// First step should be "Create user" (user doesn't exist)
	if steps[0].Title != "Create user" {
		t.Errorf("first step = %q, want %q", steps[0].Title, "Create user")
	}

	// Password step should mention passwd command
	lastStep := steps[len(steps)-1]
	if lastStep.Risk != "manual-step" {
		t.Errorf("last step risk = %q, want manual-step", lastStep.Risk)
	}
	if !strings.Contains(lastStep.Detail, "passwd nonexistent_user_12345") {
		t.Errorf("password step should mention 'passwd nonexistent_user_12345', got %q", lastStep.Detail)
	}
	if !strings.Contains(lastStep.Detail, "No password set automatically") {
		t.Errorf("password step should say 'No password set automatically', got %q", lastStep.Detail)
	}
}

func TestUserPlanExistingUser(t *testing.T) {
	// "root" always exists in /etc/passwd
	m := NewUserModule()
	cfg := &types.Config{
		NewUsername: "root",
		UserAddSudo: true,
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

	// Password step should still be present
	lastStep := steps[len(steps)-1]
	if lastStep.Risk != "manual-step" {
		t.Errorf("last step risk = %q, want manual-step", lastStep.Risk)
	}
}

func TestUserPlanNoUsername(t *testing.T) {
	m := NewUserModule()
	cfg := &types.Config{NewUsername: ""}

	steps, err := m.Plan(context.Background(), &system.Context{}, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Should still produce steps (create user with empty name)
	if len(steps) == 0 {
		t.Error("expected at least one step")
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
