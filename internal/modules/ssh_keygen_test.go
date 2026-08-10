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
