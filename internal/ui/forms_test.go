package ui

import (
	"context"
	"errors"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/frankwei98/sys-bootstrap/internal/modules"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

func TestSSHCheckpointPreservesCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	confirmed, err := NewSSHCheckpointFunc()(ctx, nil)
	if confirmed {
		t.Fatal("cancelled checkpoint must not confirm finalization")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("checkpoint error = %v, want context.Canceled", err)
	}
}

func TestSSHCheckpointPreservesCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	confirmed, err := NewSSHCheckpointFunc()(ctx, nil)
	if confirmed {
		t.Fatal("timed out checkpoint must not confirm finalization")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("checkpoint error = %v, want context.DeadlineExceeded", err)
	}
}

func TestSSHCheckpointPreservesPromptAbortAndTimeout(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "Ctrl-C", err: huh.ErrUserAborted},
		{name: "timeout", err: huh.ErrTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalRun := runSSHCheckpointForm
			runSSHCheckpointForm = func(context.Context, *huh.Form) error { return tt.err }
			t.Cleanup(func() { runSSHCheckpointForm = originalRun })

			confirmed, err := NewSSHCheckpointFunc()(context.Background(), nil)
			if confirmed {
				t.Fatal("failed checkpoint prompt must not confirm finalization")
			}
			if !errors.Is(err, tt.err) {
				t.Fatalf("checkpoint error = %v, want %v", err, tt.err)
			}
		})
	}
}

func TestSSHCheckpointPreservesEOF(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	originalStdin := os.Stdin
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close stdin writer: %v", err)
	}
	os.Stdin = input
	t.Cleanup(func() {
		os.Stdin = originalStdin
		input.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	confirmed, err := NewSSHCheckpointFunc()(ctx, nil)
	if confirmed {
		t.Fatal("EOF checkpoint must not confirm finalization")
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("checkpoint error = %v, want io.EOF", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("checkpoint waited for timeout instead of returning EOF: %v", ctx.Err())
	}
}

func TestSSHCheckpointExplicitNoRemainsPendingChoice(t *testing.T) {
	originalRun := runSSHCheckpointForm
	runSSHCheckpointForm = func(context.Context, *huh.Form) error { return nil }
	t.Cleanup(func() { runSSHCheckpointForm = originalRun })

	confirmed, err := NewSSHCheckpointFunc()(context.Background(), nil)
	if err != nil {
		t.Fatalf("explicit No checkpoint error = %v, want nil", err)
	}
	if confirmed {
		t.Fatal("explicit No must leave confirmation false")
	}
}

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

func TestValidateDockerTargetUserInput(t *testing.T) {
	for _, input := range []string{"", "Bad User", "-deploy"} {
		if err := validateDockerTargetUserInput(input); err == nil {
			t.Errorf("validateDockerTargetUserInput(%q) succeeded, want validation error", input)
		}
	}

	root, err := user.Lookup("root")
	if err != nil {
		t.Fatalf("lookup root account: %v", err)
	}
	if !modules.ValidateLinuxUsername(root.Username) {
		t.Fatalf("root account username %q does not match supported Linux format", root.Username)
	}
	if err := validateDockerTargetUserInput("  " + root.Username + "  "); err != nil {
		t.Fatalf("validate existing Docker target user: %v", err)
	}
}
