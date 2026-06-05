package logging

import (
	"os"
	"os/user"
	"testing"
)

func TestStateHomeDirUsesCurrentUserByDefault(t *testing.T) {
	t.Setenv("SUDO_USER", "")

	want, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() failed: %v", err)
	}

	got, err := stateHomeDir()
	if err != nil {
		t.Fatalf("stateHomeDir() failed: %v", err)
	}
	if got != want {
		t.Fatalf("stateHomeDir() = %q, want %q", got, want)
	}
}

func TestStateHomeDirUsesSudoUserHome(t *testing.T) {
	current, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current() failed: %v", err)
	}
	if current.Username == "root" {
		t.Skip("test requires a non-root current user to validate sudo-user override")
	}

	t.Setenv("SUDO_USER", current.Username)

	got, err := stateHomeDir()
	if err != nil {
		t.Fatalf("stateHomeDir() failed: %v", err)
	}
	if got != current.HomeDir {
		t.Fatalf("stateHomeDir() = %q, want %q", got, current.HomeDir)
	}
}

func TestStateHomeDirIgnoresRootSudoUser(t *testing.T) {
	t.Setenv("SUDO_USER", "root")

	want, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() failed: %v", err)
	}

	got, err := stateHomeDir()
	if err != nil {
		t.Fatalf("stateHomeDir() failed: %v", err)
	}
	if got != want {
		t.Fatalf("stateHomeDir() = %q, want %q", got, want)
	}
}
