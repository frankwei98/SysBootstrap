package app

import (
	"os"
	"os/user"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/system"
)

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

func TestIsUserLevelModule(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"node", true},
		{"ai", true},
		{"ssh_keygen", true},
		{"base", false},
		{"ssh", false},
		{"user", false},
		{"nonexistent", false},
	}
	for _, tt := range tests {
		if got := IsUserLevelModule(tt.id); got != tt.want {
			t.Errorf("IsUserLevelModule(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}

func TestUserLevelModuleSet_ReturnsCopy(t *testing.T) {
	s1 := UserLevelModuleSet()
	s2 := UserLevelModuleSet()

	// Verify contents
	expected := map[string]bool{"node": true, "ai": true, "ssh_keygen": true}
	for id := range expected {
		if !s1[id] {
			t.Errorf("UserLevelModuleSet() missing %q", id)
		}
	}
	for id := range s1 {
		if !expected[id] {
			t.Errorf("UserLevelModuleSet() has unexpected %q", id)
		}
	}

	// Verify independence
	s1["injected"] = true
	if s2["injected"] {
		t.Error("UserLevelModuleSet should return independent copies")
	}
}
