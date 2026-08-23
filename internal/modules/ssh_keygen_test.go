package modules

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

func TestSSHKeygenRunChecksCommandBeforeCreatingSSHDirectory(t *testing.T) {
	originalCommandExists := sshKeygenCommandExistsFn
	sshKeygenCommandExistsFn = func(name string) bool {
		return false
	}
	t.Cleanup(func() { sshKeygenCommandExistsFn = originalCommandExists })

	home := filepath.Join(t.TempDir(), "target-home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatalf("create target home: %v", err)
	}
	sys := &system.Context{CurrentUser: &user.User{
		Username: "test-user",
		HomeDir:  home,
	}}
	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New failed: %v", err)
	}
	defer log.Close()

	err = NewSSHKeygenModule().Run(context.Background(), sys, &types.Config{}, log)
	if err == nil {
		t.Error("expected missing ssh-keygen to fail")
	} else if !strings.Contains(err.Error(), "openssh-client") {
		t.Errorf("error = %q, want openssh-client installation guidance", err)
	}

	sshDir := filepath.Join(home, ".ssh")
	if _, statErr := os.Stat(sshDir); !os.IsNotExist(statErr) {
		t.Errorf(".ssh was created before ssh-keygen prerequisite check (stat error: %v)", statErr)
	}
}

func TestSSHKeygenCheckTreatsOverwriteAsUnsatisfied(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519"), []byte("private"), 0o600); err != nil {
		t.Fatalf("write existing key: %v", err)
	}
	sys := &system.Context{CurrentUser: &user.User{HomeDir: home}}

	check := NewSSHKeygenModule().Check(context.Background(), sys, &types.Config{KeygenType: "ed25519", KeygenOverwrite: true})
	if check.Satisfied {
		t.Fatalf("Check() = %#v, want overwrite request to remain actionable", check)
	}
}

func TestSSHKeygenRecoversMissingPublicKey(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(sshDir, "id_ed25519")
	if err := os.WriteFile(privatePath, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	sys := &system.Context{CurrentUser: &user.User{Username: "test-user", HomeDir: home}}
	cfg := &types.Config{KeygenType: "ed25519"}
	module := NewSSHKeygenModule()

	if check := module.Check(context.Background(), sys, cfg); check.Satisfied {
		t.Fatalf("Check() = %#v, want missing public key to remain actionable", check)
	}
	steps, err := module.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Title != "Recover SSH public key" {
		t.Fatalf("Plan() = %#v, want public-key recovery step", steps)
	}

	binDir := t.TempDir()
	keygenPath := filepath.Join(binDir, "ssh-keygen")
	if err := os.WriteFile(keygenPath, []byte("#!/bin/sh\n[ \"$1\" = -y ] || exit 99\nprintf '%s\\n' 'ssh-ed25519 AAAATEST recovered'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	originalCommandExists := sshKeygenCommandExistsFn
	sshKeygenCommandExistsFn = func(name string) bool { return name == "ssh-keygen" }
	t.Cleanup(func() { sshKeygenCommandExistsFn = originalCommandExists })
	log, err := logging.New(true)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	if err := module.Run(context.Background(), sys, cfg, log); err != nil {
		t.Fatalf("Run() failed: %v", err)
	}
	public, err := os.ReadFile(privatePath + ".pub")
	if err != nil {
		t.Fatalf("read recovered public key: %v", err)
	}
	if string(public) != "ssh-ed25519 AAAATEST recovered\n" {
		t.Fatalf("recovered public key = %q", public)
	}
}
