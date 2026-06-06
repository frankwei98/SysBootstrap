package app

import (
	"os"
	"os/user"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/system"
)

func TestUserLevelModuleIDs(t *testing.T) {
	tests := []struct {
		name string
		ids  []string
		want int
	}{
		{"none", []string{"base", "ssh"}, 0},
		{"one", []string{"base", "node"}, 1},
		{"all", []string{"node", "ai", "ssh_keygen"}, 3},
		{"mixed", []string{"base", "ssh", "node", "user", "ai"}, 2},
		{"empty", []string{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UserLevelModuleIDs(tt.ids)
			if len(got) != tt.want {
				t.Errorf("UserLevelModuleIDs(%v) = %v (len %d), want len %d", tt.ids, got, len(got), tt.want)
			}
		})
	}
}

func TestCheckRootUserInstall_NotRoot(t *testing.T) {
	// When not root, should return nil without any check
	sys := &system.Context{IsRoot: false}
	err := CheckRootUserInstall(sys, []string{"node", "ai"}, true)
	if err != nil {
		t.Errorf("CheckRootUserInstall(non-root) = %v, want nil", err)
	}
}

func TestCheckRootUserInstall_NoUserLevelModules(t *testing.T) {
	// When root but no user-level modules selected, should return nil
	sys := &system.Context{IsRoot: true}
	err := CheckRootUserInstall(sys, []string{"base", "ssh"}, true)
	if err != nil {
		t.Errorf("CheckRootUserInstall(root, no user mods) = %v, want nil", err)
	}
}

func TestCheckRootUserInstall_NonInteractive_Override(t *testing.T) {
	// Non-interactive with override env var should succeed
	os.Setenv("SYS_BOOTSTRAP_CONFIRM_ROOT_USER_INSTALL", "1")
	defer os.Unsetenv("SYS_BOOTSTRAP_CONFIRM_ROOT_USER_INSTALL")

	sys := &system.Context{IsRoot: true}
	err := CheckRootUserInstall(sys, []string{"node"}, false)
	if err != nil {
		t.Errorf("CheckRootUserInstall(override=1) = %v, want nil", err)
	}
}

func TestCheckRootUserInstall_NonInteractive_NoOverride(t *testing.T) {
	// Non-interactive without override should fail
	os.Unsetenv("SYS_BOOTSTRAP_CONFIRM_ROOT_USER_INSTALL")

	sys := &system.Context{IsRoot: true}
	err := CheckRootUserInstall(sys, []string{"node"}, false)
	if err == nil {
		t.Error("CheckRootUserInstall(non-interactive, no override) = nil, want error")
	}
}

func TestCheckRootUserInstall_NonInteractive_SudoUser(t *testing.T) {
	// Non-interactive root via sudo targets the invoking user, not /root.
	os.Unsetenv("SYS_BOOTSTRAP_CONFIRM_ROOT_USER_INSTALL")

	sys := &system.Context{
		IsRoot: true,
		InvokingUser: &user.User{
			Username: "frank",
			HomeDir:  "/home/frank",
		},
	}
	err := CheckRootUserInstall(sys, []string{"ai", "ssh_keygen"}, false)
	if err != nil {
		t.Errorf("CheckRootUserInstall(non-interactive, sudo) = %v, want nil", err)
	}
}

func TestCheckRootUserInstall_CoversDeps(t *testing.T) {
	// When running "module ai" with missing "node" dep, root check should
	// see both ai and node as user-level modules and reject in non-interactive.
	os.Unsetenv("SYS_BOOTSTRAP_CONFIRM_ROOT_USER_INSTALL")

	sys := &system.Context{IsRoot: true}
	// Simulates the call: CheckRootUserInstall(sys, []string{"ai", "node"}, false)
	// Both "ai" and "node" are user-level modules.
	err := CheckRootUserInstall(sys, []string{"ai", "node"}, false)
	if err == nil {
		t.Error("CheckRootUserInstall(root, [ai, node], non-interactive) = nil, want error")
	}
}

func TestUserLevelModuleIDs_WithDeps(t *testing.T) {
	// Target "ai" + missing dep "node" — both are user-level.
	// This tests the "will actually execute" module set.
	got := UserLevelModuleIDs([]string{"ai", "node"})
	if len(got) != 2 {
		t.Errorf("UserLevelModuleIDs([ai, node]) = %v (len %d), want len 2", got, len(got))
	}
	// Verify both present
	hasAI, hasNode := false, false
	for _, id := range got {
		if id == "ai" {
			hasAI = true
		}
		if id == "node" {
			hasNode = true
		}
	}
	if !hasAI || !hasNode {
		t.Errorf("UserLevelModuleIDs([ai, node]) = %v, want both ai and node", got)
	}
}
