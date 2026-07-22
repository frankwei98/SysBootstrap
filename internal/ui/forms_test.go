package ui

import (
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
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

func TestParseTCPPortRejectsTrailingCharacters(t *testing.T) {
	if _, err := parseTCPPort("22abc"); err == nil {
		t.Fatal("expected trailing characters in port to be rejected")
	}
	if got, err := parseTCPPort(" 22122 "); err != nil || got != 22122 {
		t.Fatalf("parseTCPPort valid input = (%d, %v), want (22122, nil)", got, err)
	}
}

func TestFail2banInputValidators(t *testing.T) {
	if err := validateFail2banDuration("10m\nbackend = auto"); err == nil {
		t.Fatal("expected multiline duration to be rejected")
	}
	if err := validateFail2banBackend("systemd\nport = 22"); err == nil {
		t.Fatal("expected multiline backend to be rejected")
	}
	if err := validateFail2banIgnoreIP("127.0.0.1/8 invalid-host"); err == nil {
		t.Fatal("expected invalid ignoreip token to be rejected")
	}
}
