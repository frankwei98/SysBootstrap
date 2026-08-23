package logging

import (
	"bytes"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoggerRoutesErrorLevelToStderr(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	log := &Logger{stdout: &stdout, stderr: &stderr}

	log.Info("informational")
	log.Errorf("failure: %s", "boom")

	if strings.Contains(stdout.String(), "failure: boom") {
		t.Fatalf("error output leaked to stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "failure: boom") {
		t.Fatalf("stderr missing error output: %q", stderr.String())
	}
}

func TestLoggerKeepsNonErrorLevelsOnStdout(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	log := &Logger{stdout: &stdout, stderr: &stderr}

	log.Info("information")
	log.Success("success")
	log.Warn("warning")

	for _, want := range []string{"information", "success", "warning"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %q", want, stdout.String())
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("non-error output reached stderr: %q", stderr.String())
	}
}

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

func TestNewReturnsLogFileCreationError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SUDO_USER", "")
	t.Setenv("HOME", home)
	if err := os.Symlink(t.TempDir(), filepath.Join(home, ".local")); err != nil {
		t.Fatal(err)
	}

	log, err := New(true)
	if err == nil {
		if log != nil {
			log.Close()
		}
		t.Fatal("New() succeeded despite an unsafe log directory")
	}
}
