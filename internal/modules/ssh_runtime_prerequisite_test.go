package modules

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/system"
)

func TestSSHCheckPreparesRuntimeDirectoryBeforeEffectiveQuery(t *testing.T) {
	originalConfigPath := sshConfigPath
	originalDropInPath := managedSSHDropIn
	originalServiceReady := sshServiceReadyFn
	originalEnsureRunDir := ensureSSHDRunDirFn
	t.Cleanup(func() {
		sshConfigPath = originalConfigPath
		managedSSHDropIn = originalDropInPath
		sshServiceReadyFn = originalServiceReady
		ensureSSHDRunDirFn = originalEnsureRunDir
	})

	testRoot := t.TempDir()
	sshConfigPath = filepath.Join(testRoot, "sshd_config")
	managedSSHDropIn = filepath.Join(testRoot, "sshd_config.d", "00-sys-bootstrap.conf")
	if err := os.WriteFile(sshConfigPath, []byte("Port 22122\n"), 0o600); err != nil {
		t.Fatalf("write sshd_config: %v", err)
	}

	runtimeReady := filepath.Join(testRoot, "runtime-ready")
	ensureSSHDRunDirFn = func() error {
		return os.WriteFile(runtimeReady, []byte("ready\n"), 0o600)
	}

	fakeBin := t.TempDir()
	writeFakeCommand(t, fakeBin, "sshd", `#!/bin/sh
if [ ! -f "$SYS_BOOTSTRAP_TEST_SSHD_RUNTIME_READY" ]; then
  echo "sshd runtime directory is unavailable" >&2
  exit 1
fi
printf '%s\n' \
  'port 22122' \
  'permitrootlogin no' \
  'passwordauthentication no' \
  'kbdinteractiveauthentication no'
`)
	t.Setenv("SYS_BOOTSTRAP_TEST_SSHD_RUNTIME_READY", runtimeReady)
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	sshServiceReadyFn = func() bool { return true }

	result := NewSSHModule().Check(context.Background(), &system.Context{HasSSHD: true}, nil)
	if !result.Satisfied {
		t.Fatalf("SSH Check should prepare the runtime directory before querying sshd: %#v", result)
	}
}
