package ui

import (
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/system"
)

func TestSelectedSSHKeyPathUsesSelectedAlgorithm(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_rsa"), []byte("dummy"), 0o600); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	sys := &system.Context{
		CurrentUser: &user.User{
			Username: "testuser",
			HomeDir:  home,
		},
	}

	rsaPath, rsaExists := selectedSSHKeyPath(sys, "rsa")
	if !rsaExists {
		t.Fatal("expected RSA key to be detected")
	}
	if want := filepath.Join(sshDir, "id_rsa"); rsaPath != want {
		t.Fatalf("rsa path = %q, want %q", rsaPath, want)
	}

	edPath, edExists := selectedSSHKeyPath(sys, "ed25519")
	if edExists {
		t.Fatal("did not expect ed25519 key to be detected")
	}
	if want := filepath.Join(sshDir, "id_ed25519"); edPath != want {
		t.Fatalf("ed25519 path = %q, want %q", edPath, want)
	}
}

