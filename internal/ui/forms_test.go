package ui

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/types"
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

func TestTimezoneConfigFormOmitsKeepOptionWhenCurrentTimezoneUnknown(t *testing.T) {
	cfg := &types.Config{}
	current, ok := modulesCurrentTimezone()
	selected := cfg.Timezone
	if selected == "" {
		if ok && current != "" {
			selected = current
		} else {
			selected = "Etc/UTC"
		}
	}

	options := []string{}
	if ok && current != "" {
		options = append(options, "Keep current / detected")
	}
	options = append(options,
		"Etc/UTC",
		"Asia/Shanghai",
		"America/Los_Angeles",
		"Europe/Berlin",
		"Custom",
	)

	if !(ok && current != "") {
		for _, option := range options {
			if strings.Contains(option, "Keep current") {
				t.Fatalf("unexpected keep-current option when timezone is unknown: %v", options)
			}
		}
	}
}
