package modules

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

// TestLiveSSHTwoPhaseCutover mutates and restores the host SSH daemon. It is
// intentionally opt-in and must only run on a disposable machine with an
// independent recovery console.
func TestLiveSSHTwoPhaseCutover(t *testing.T) {
	if os.Getenv("SYS_BOOTSTRAP_LIVE_SSH") != "1" {
		t.Skip("set SYS_BOOTSTRAP_LIVE_SSH=1 on a disposable VM")
	}
	if os.Geteuid() != 0 {
		t.Fatal("live SSH integration test must run as root")
	}

	username := os.Getenv("SYS_BOOTSTRAP_LIVE_SSH_USER")
	privateKey := os.Getenv("SYS_BOOTSTRAP_LIVE_SSH_KEY")
	publicKeyPath := os.Getenv("SYS_BOOTSTRAP_LIVE_SSH_PUBKEY")
	port, err := strconv.Atoi(os.Getenv("SYS_BOOTSTRAP_LIVE_SSH_PORT"))
	if err != nil || port < 1 || port > 65535 || username == "" || privateKey == "" || publicKeyPath == "" {
		t.Fatal("set valid SYS_BOOTSTRAP_LIVE_SSH_USER, _KEY, _PUBKEY, and _PORT")
	}

	publicKey, err := os.ReadFile(publicKeyPath)
	if err != nil {
		t.Fatalf("read public key: %v", err)
	}
	target, err := user.Lookup(username)
	if err != nil {
		t.Fatalf("lookup test user: %v", err)
	}

	journal, err := captureJournal()
	if err != nil {
		t.Fatalf("capture original SSH state: %v", err)
	}
	t.Cleanup(func() {
		if err := rollbackPrepare(context.Background(), journal); err != nil {
			t.Errorf("restore original SSH state: %v", err)
		}
	})

	sys, err := system.NewContext()
	if err != nil {
		t.Fatalf("detect system: %v", err)
	}
	sys.InvokingUser = target

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("create logger: %v", err)
	}
	defer log.Close()

	module := NewSSHModule()
	module.SetCheckpoint(func(ctx context.Context, candidates []types.AccessPath) (bool, error) {
		if len(candidates) == 0 {
			return false, fmt.Errorf("no verified access candidates")
		}
		cmd := exec.CommandContext(ctx, "ssh",
			"-i", privateKey,
			"-o", "BatchMode=yes",
			"-o", "IdentitiesOnly=yes",
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "ConnectTimeout=5",
			"-p", strconv.Itoa(port),
			username+"@127.0.0.1", "true",
		)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return false, fmt.Errorf("fresh SSH login failed: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return true, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cfg := &types.Config{
		SSHPort:        port,
		SSHAddKey:      true,
		SSHPublicKey:   strings.TrimSpace(string(publicKey)),
		SSHDisableRoot: true,
		SSHDisablePass: true,
	}
	if err := module.Run(ctx, sys, cfg, log); err != nil {
		t.Fatalf("two-phase SSH cutover failed: %v", err)
	}

	settings, err := effectiveSSHSettings(ctx, username)
	if err != nil {
		t.Fatalf("read final effective settings: %v", err)
	}
	if settings["passwordauthentication"] != "no" {
		t.Fatalf("PasswordAuthentication=%q, want no", settings["passwordauthentication"])
	}
	if settings["kbdinteractiveauthentication"] != "no" {
		t.Fatalf("KbdInteractiveAuthentication=%q, want no", settings["kbdinteractiveauthentication"])
	}
	rootSettings, err := effectiveSSHSettings(ctx, "root")
	if err != nil {
		t.Fatalf("read root effective settings: %v", err)
	}
	if rootSettings["permitrootlogin"] != "no" {
		t.Fatalf("PermitRootLogin=%q, want no", rootSettings["permitrootlogin"])
	}
	if err := verifyOnlyListeningPorts(ctx, []int{port}); err != nil {
		t.Fatalf("final listener set is not exclusive to the requested port: %v", err)
	}
}

func TestLiveSSHDeclineKeepsDualPath(t *testing.T) {
	if os.Getenv("SYS_BOOTSTRAP_LIVE_SSH") != "1" {
		t.Skip("set SYS_BOOTSTRAP_LIVE_SSH=1 on a disposable VM")
	}
	if os.Geteuid() != 0 {
		t.Fatal("live SSH integration test must run as root")
	}
	username := os.Getenv("SYS_BOOTSTRAP_LIVE_SSH_USER")
	publicKeyPath := os.Getenv("SYS_BOOTSTRAP_LIVE_SSH_PUBKEY")
	port, err := strconv.Atoi(os.Getenv("SYS_BOOTSTRAP_LIVE_SSH_PORT"))
	if err != nil || username == "" || publicKeyPath == "" {
		t.Fatal("live SSH test environment is incomplete")
	}
	publicKey, err := os.ReadFile(publicKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	target, err := user.Lookup(username)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := captureJournal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := rollbackPrepare(context.Background(), journal); err != nil {
			t.Errorf("restore original SSH state: %v", err)
		}
	})
	sys, err := system.NewContext()
	if err != nil {
		t.Fatal(err)
	}
	sys.InvokingUser = target
	log, err := logging.New(true)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	module := NewSSHModule()
	module.SetCheckpoint(func(context.Context, []types.AccessPath) (bool, error) { return false, nil })
	cfg := &types.Config{
		SSHPort:        port,
		SSHAddKey:      true,
		SSHPublicKey:   strings.TrimSpace(string(publicKey)),
		SSHDisableRoot: true,
		SSHDisablePass: true,
	}
	err = module.Run(context.Background(), sys, cfg, log)
	if !errors.Is(err, types.ErrSSHPendingConfirmation) {
		t.Fatalf("declined confirmation returned %v, want pending sentinel", err)
	}
	wanted := append(append([]int{}, journal.oldPorts...), port)
	if err := verifyEffectivePorts(context.Background(), wanted); err != nil {
		t.Fatalf("decline did not retain old and new effective ports: %v", err)
	}
	if err := verifyListeningPorts(context.Background(), wanted); err != nil {
		t.Fatalf("decline did not retain old and new listeners: %v", err)
	}
}
