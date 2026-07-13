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

func TestValidatePublicKey(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		valid bool
	}{
		{"ed25519", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq", true},
		{"rsa", "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC...", true},
		{"ecdsa", "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTY...", true},
		{"sk", "sk-ssh-ed25519@openssh.com AAAA...", true},
		{"empty", "", false},
		{"random text", "hello world", false},
		{"no prefix", "AAAAAC3NzaC1lZDI1NTE5AAAA...", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidatePublicKey(tt.key)
			if got != tt.valid {
				t.Errorf("ValidatePublicKey(%q) = %v, want %v", tt.key, got, tt.valid)
			}
		})
	}
}

func TestSSHPlanNoUFW(t *testing.T) {
	m := NewSSHModule()
	sys := &system.Context{HasUFW: false}
	cfg := &types.Config{SSHPort: 22122}

	steps, err := m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	for _, s := range steps {
		if s.Title == "Allow SSH port in UFW" || s.Title == "UFW firewall warning" {
			t.Errorf("unexpected UFW step when UFW not present: %s", s.Title)
		}
	}
}

func TestSSHPlanInstallsOpenSSHServerWhenMissing(t *testing.T) {
	m := NewSSHModule()
	sys := &system.Context{HasSSHD: false, HasSSHDService: false}
	cfg := &types.Config{SSHPort: 22122}

	steps, err := m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if len(steps) == 0 || steps[0].Title != "Install OpenSSH server" {
		t.Fatalf("first step = %#v, want Install OpenSSH server", steps)
	}
}

func TestSSHCheckMissingConfig(t *testing.T) {
	orig := sshConfigPath
	sshConfigPath = filepath.Join(t.TempDir(), "missing_sshd_config")
	t.Cleanup(func() {
		sshConfigPath = orig
	})

	m := NewSSHModule()
	result := m.Check(context.Background(), &system.Context{HasSSHD: true})
	if result.Satisfied {
		t.Fatal("expected missing sshd_config to be unsatisfied")
	}
	if result.Message != "sshd_config not found" {
		t.Fatalf("message = %q, want sshd_config not found", result.Message)
	}
}

func TestSSHCheckSatisfiedForConfiguredPortAndService(t *testing.T) {
	origPath := sshConfigPath
	origService := sshServiceReadyFn
	tmpFile := filepath.Join(t.TempDir(), "sshd_config")
	if err := os.WriteFile(tmpFile, []byte("Port 22333\nPermitRootLogin yes\nPasswordAuthentication yes\n"), 0o644); err != nil {
		t.Fatalf("write sshd_config: %v", err)
	}
	sshConfigPath = tmpFile
	sshServiceReadyFn = func() bool { return true }
	t.Cleanup(func() {
		sshConfigPath = origPath
		sshServiceReadyFn = origService
	})

	m := NewSSHModule()
	result := m.Check(context.Background(), &system.Context{HasSSHD: true})
	if !result.Satisfied {
		t.Fatalf("expected configured ssh to be satisfied, got %#v", result)
	}
	if !strings.Contains(result.Message, "port 22333") {
		t.Fatalf("message = %q, want current port detail", result.Message)
	}
}

func TestSSHPlanUFWActiveAllow(t *testing.T) {
	m := NewSSHModule()
	sys := &system.Context{HasUFW: true, UFWActive: true}
	cfg := &types.Config{SSHPort: 22122, SSHAllowUFW: true}

	steps, err := m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	found := false
	for _, s := range steps {
		if s.Title == "Allow SSH port in UFW" {
			found = true
			if s.Detail != "ufw allow 22122/tcp" {
				t.Errorf("UFW step detail = %q, want %q", s.Detail, "ufw allow 22122/tcp")
			}
		}
	}
	if !found {
		t.Error("expected 'Allow SSH port in UFW' step not found")
	}
}

func TestSSHPlanUFWActiveDeny(t *testing.T) {
	m := NewSSHModule()
	sys := &system.Context{HasUFW: true, UFWActive: true}
	cfg := &types.Config{SSHPort: 22122, SSHAllowUFW: false}

	steps, err := m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	foundWarning := false
	foundAllow := false
	for _, s := range steps {
		if s.Title == "UFW firewall warning" {
			foundWarning = true
			if s.Risk != "manual-step" {
				t.Errorf("UFW warning risk = %q, want manual-step", s.Risk)
			}
		}
		if s.Title == "Allow SSH port in UFW" {
			foundAllow = true
		}
	}
	if !foundWarning {
		t.Error("expected 'UFW firewall warning' step not found")
	}
	if foundAllow {
		t.Error("unexpected 'Allow SSH port in UFW' step when SSHAllowUFW=false")
	}
}

func TestSSHPlanUFWInactive(t *testing.T) {
	m := NewSSHModule()
	sys := &system.Context{HasUFW: true, UFWActive: false}
	cfg := &types.Config{SSHPort: 22122, SSHAllowUFW: true}

	steps, err := m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	for _, s := range steps {
		if s.Title == "Allow SSH port in UFW" || s.Title == "UFW firewall warning" {
			t.Errorf("unexpected UFW step when UFW inactive: %s", s.Title)
		}
	}
}

func newQuietLogger(t *testing.T) *logging.Logger {
	t.Helper()
	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New() failed: %v", err)
	}
	t.Cleanup(func() { log.Close() })
	return log
}

func TestSSHGuardDisableRootPassWithoutKey(t *testing.T) {
	m := NewSSHModule()
	sys := &system.Context{}
	cfg := &types.Config{
		SSHDisableRoot: true,
		SSHDisablePass: true,
		SSHPort:        22122,
	}

	err := m.Run(context.Background(), sys, cfg, newQuietLogger(t))
	if err == nil {
		t.Fatal("expected error when disabling root+pass without replacement key")
	}
	if !strings.Contains(err.Error(), "no replacement access path") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSSHGuardDisableRootPassEmptyKeyRejected(t *testing.T) {
	m := NewSSHModule()
	sys := &system.Context{}
	cfg := &types.Config{
		SSHDisableRoot: true,
		SSHDisablePass: true,
		SSHAddKey:      true,
		SSHPublicKey:   "",
		SSHPort:        22122,
	}

	err := m.Run(context.Background(), sys, cfg, newQuietLogger(t))
	if err == nil {
		t.Fatal("expected error when disabling root+pass with empty key")
	}
	if !strings.Contains(err.Error(), "no replacement access path") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSSHGuardDisableRootOnlyWithoutKeyOK(t *testing.T) {
	m := NewSSHModule()
	sys := &system.Context{}
	cfg := &types.Config{
		SSHDisableRoot: true,
		SSHPort:        22122,
	}

	err := m.Run(context.Background(), sys, cfg, newQuietLogger(t))
	// Guard allows root-only disable without key (password still works)
	if err != nil && strings.Contains(err.Error(), "no replacement access path") {
		t.Fatal("guard should not fire when password auth remains enabled")
	}
}

func TestSSHGuardDisablePassOnlyWithoutKeyOK(t *testing.T) {
	m := NewSSHModule()
	sys := &system.Context{}
	cfg := &types.Config{
		SSHDisablePass: true,
		SSHPort:        22122,
	}

	err := m.Run(context.Background(), sys, cfg, newQuietLogger(t))
	// Guard allows pass-only disable without key (root login still works)
	if err != nil && strings.Contains(err.Error(), "no replacement access path") {
		t.Fatal("guard should not fire when root login remains enabled")
	}
}

func TestSSHPlanDefaultPort(t *testing.T) {
	origPath := sshConfigPath
	origService := sshServiceReadyFn
	tmpFile := filepath.Join(t.TempDir(), "sshd_config")
	if err := os.WriteFile(tmpFile, []byte("Port 22\n"), 0o644); err != nil {
		t.Fatalf("write sshd_config: %v", err)
	}
	sshConfigPath = tmpFile
	sshServiceReadyFn = func() bool { return true }
	t.Cleanup(func() {
		sshConfigPath = origPath
		sshServiceReadyFn = origService
	})

	m := NewSSHModule()
	sys := &system.Context{HasSSHD: true, HasSSHDService: true}
	cfg := &types.Config{SSHPort: 0} // zero means default

	steps, err := m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if len(steps) == 0 {
		t.Fatal("expected plan steps when current ssh port differs from default preview port")
	}
	if steps[0].Detail != "Set port to 22122" {
		t.Errorf("default port detail = %q, want %q", steps[0].Detail, "Set port to 22122")
	}
}

func TestSSHPlanNoStepsWhenAlreadySatisfied(t *testing.T) {
	origPath := sshConfigPath
	origService := sshServiceReadyFn
	origUFW := sshUFWAllowsPortFn
	tmpFile := filepath.Join(t.TempDir(), "sshd_config")
	if err := os.WriteFile(tmpFile, []byte("Port 22122\n"), 0o644); err != nil {
		t.Fatalf("write sshd_config: %v", err)
	}
	sshConfigPath = tmpFile
	sshServiceReadyFn = func() bool { return true }
	sshUFWAllowsPortFn = func(int) bool { return true }
	t.Cleanup(func() {
		sshConfigPath = origPath
		sshServiceReadyFn = origService
		sshUFWAllowsPortFn = origUFW
	})

	m := NewSSHModule()
	sys := &system.Context{
		HasSSHD:        true,
		HasSSHDService: true,
		HasUFW:         true,
		UFWActive:      true,
	}

	steps, err := m.Plan(context.Background(), sys, &types.Config{SSHPort: 22122, SSHAllowUFW: true})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	for _, step := range steps {
		if step.Title == "Configure SSH port" || step.Title == "Restart sshd" || step.Title == "Allow SSH port in UFW" {
			t.Fatalf("unexpected step when ssh config is already satisfied: %#v", steps)
		}
	}
}

func TestSSHPlanSkipsAuthorizedKeyWhenAlreadyPresent(t *testing.T) {
	key := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq"
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "authorized_keys"), []byte(key+"\n"), 0o600); err != nil {
		t.Fatalf("write authorized_keys: %v", err)
	}

	origPath := sshConfigPath
	origService := sshServiceReadyFn
	tmpFile := filepath.Join(t.TempDir(), "sshd_config")
	if err := os.WriteFile(tmpFile, []byte("Port 22122\n"), 0o644); err != nil {
		t.Fatalf("write sshd_config: %v", err)
	}
	sshConfigPath = tmpFile
	sshServiceReadyFn = func() bool { return true }
	t.Cleanup(func() {
		sshConfigPath = origPath
		sshServiceReadyFn = origService
	})

	m := NewSSHModule()
	sys := &system.Context{
		HasSSHD:        true,
		HasSSHDService: true,
		CurrentUser: &user.User{
			Username: "tester",
			HomeDir:  home,
		},
	}
	steps, err := m.Plan(context.Background(), sys, &types.Config{
		SSHPort:      22122,
		SSHAddKey:    true,
		SSHPublicKey: key,
	})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	for _, step := range steps {
		if step.Title == "Add SSH public key" {
			t.Fatalf("unexpected key write step when key already exists: %#v", steps)
		}
	}
}

func TestRewriteFail2banSSHDPort(t *testing.T) {
	input := `[DEFAULT]
bantime = 1h

[sshd]
enabled = true
backend = systemd
port = 2222
`
	output, changed := rewriteFail2banSSHDPort(input, 22333)
	if !changed {
		t.Fatal("expected rewrite to report a change")
	}
	if !strings.Contains(output, "port = 22333") {
		t.Fatalf("rewritten output missing new port:\n%s", output)
	}
	if strings.Contains(output, "port = 2222") {
		t.Fatalf("rewritten output still contains old port:\n%s", output)
	}
}

func TestSyncExistingFail2banSSHDPortUpdatesConfigAndRestarts(t *testing.T) {
	origPath := os.Getenv("PATH")
	origJailPath := fail2banJailLocalPath
	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPath)
		fail2banJailLocalPath = origJailPath
	})

	tempBin := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "ssh-f2b-sync.log")
	fail2banJailLocalPath = filepath.Join(t.TempDir(), "jail.local")
	if err := os.WriteFile(fail2banJailLocalPath, []byte("[sshd]\nenabled = true\nbackend = systemd\nport = 2222\n"), 0o644); err != nil {
		t.Fatalf("write jail.local: %v", err)
	}

	writeFakeCommand(t, tempBin, "dpkg", `#!/bin/sh
case "$1" in
  -s)
    exit 0
    ;;
esac
exit 0
`)
	writeFakeCommand(t, tempBin, "systemctl", `#!/bin/sh
echo "systemctl $*" >> "$SYSBOOTSTRAP_TEST_LOG"
case "$1" in
  is-enabled)
    echo "enabled"
    exit 0
    ;;
  is-active)
    echo "active"
    exit 0
    ;;
  restart)
    exit 0
    ;;
esac
exit 0
`)
	writeFakeCommand(t, tempBin, "fail2ban-client", `#!/bin/sh
echo "fail2ban-client $*" >> "$SYSBOOTSTRAP_TEST_LOG"
exit 0
`)
	t.Setenv("SYSBOOTSTRAP_TEST_LOG", logFile)
	t.Setenv("PATH", tempBin+":"+origPath)

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New failed: %v", err)
	}
	defer log.Close()

	if err := syncExistingFail2banSSHDPort(context.Background(), 22333, log); err != nil {
		t.Fatalf("syncExistingFail2banSSHDPort failed: %v", err)
	}

	content, err := os.ReadFile(fail2banJailLocalPath)
	if err != nil {
		t.Fatalf("read jail.local: %v", err)
	}
	if !strings.Contains(string(content), "port = 22333") {
		t.Fatalf("expected jail.local to be rewritten:\n%s", string(content))
	}

	logContent, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	text := string(logContent)
	if !strings.Contains(text, "fail2ban-client -d") {
		t.Fatalf("expected fail2ban validation commands, got:\n%s", text)
	}
	if !strings.Contains(text, "systemctl restart fail2ban") {
		t.Fatalf("expected fail2ban restart, got:\n%s", text)
	}
}

// --- U2: SSH two-phase transaction tests ---

func TestSSHDPreCheck_StockConfig(t *testing.T) {
	origPath := sshConfigPath
	sshConfigPath = "testdata/sshd_config/stock_debian.conf"
	t.Cleanup(func() { sshConfigPath = origPath })

	err := sshdPreCheck(context.Background(), &system.Context{}, newQuietLogger(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSSHDPreCheck_NoInclude(t *testing.T) {
	origPath := sshConfigPath
	sshConfigPath = "testdata/sshd_config/no_include.conf"
	t.Cleanup(func() { sshConfigPath = origPath })

	err := sshdPreCheck(context.Background(), &system.Context{}, newQuietLogger(t))
	if err == nil {
		t.Fatal("expected error for config without Include")
	}
}

func TestFindUnmanagedPorts(t *testing.T) {
	extra, err := findUnmanagedPorts("testdata/sshd_config/stock_debian.conf", 22122)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// stock_debian has Port 22, managed port is 22122, so Port 22 is unmanaged
	if len(extra) != 1 || extra[0] != 22 {
		t.Fatalf("expected [22], got %v", extra)
	}
}

func TestWriteManagedDropIn(t *testing.T) {
	tmpDir := t.TempDir()
	origDropIn := managedSSHDropIn
	managedSSHDropIn = filepath.Join(tmpDir, "00-sys-bootstrap.conf")
	t.Cleanup(func() { managedSSHDropIn = origDropIn })

	err := writeManagedDropIn(22122, "no", "no")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(managedSSHDropIn)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "Port 22122") {
		t.Errorf("missing Port 22122 in: %s", content)
	}
	if !strings.Contains(content, "PermitRootLogin no") {
		t.Errorf("missing PermitRootLogin: %s", content)
	}
	if !strings.Contains(content, "PasswordAuthentication no") {
		t.Errorf("missing PasswordAuthentication: %s", content)
	}
}

func TestCaptureJournal(t *testing.T) {
	tmpDir := t.TempDir()
	origDropIn := managedSSHDropIn
	managedSSHDropIn = filepath.Join(tmpDir, "00-sys-bootstrap.conf")
	origEffectivePorts := effectiveSSHPortsFunc
	effectiveSSHPortsFunc = func(context.Context) ([]int, error) { return []int{22}, nil }
	t.Cleanup(func() {
		managedSSHDropIn = origDropIn
		effectiveSSHPortsFunc = origEffectivePorts
	})

	os.WriteFile(managedSSHDropIn, []byte("Port 22\n"), 0o644)

	j, err := captureJournal()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !j.hadDropIn {
		t.Fatal("expected hadDropIn to be true")
	}
	if string(j.dropInBytes) != "Port 22\n" {
		t.Errorf("unexpected dropInBytes: %q", string(j.dropInBytes))
	}
}

func TestRollbackRemovesNewDropIn(t *testing.T) {
	tmpDir := t.TempDir()
	origDropIn := managedSSHDropIn
	managedSSHDropIn = filepath.Join(tmpDir, "00-sys-bootstrap.conf")
	t.Cleanup(func() { managedSSHDropIn = origDropIn })

	// Original: no drop-in existed
	j := &sshTransactionJournal{hadDropIn: false}

	// Simulate prepare by writing a new drop-in
	os.WriteFile(managedSSHDropIn, []byte("Port 22122\n"), 0o644)

	// Rollback
	rollbackPrepare(context.Background(), j)

	if _, err := os.Stat(managedSSHDropIn); !os.IsNotExist(err) {
		t.Fatal("expected drop-in to be removed after rollback from no prior state")
	}
}

func TestRollbackRestoresPreviousContent(t *testing.T) {
	tmpDir := t.TempDir()
	origDropIn := managedSSHDropIn
	managedSSHDropIn = filepath.Join(tmpDir, "00-sys-bootstrap.conf")
	origEffectivePorts := effectiveSSHPortsFunc
	effectiveSSHPortsFunc = func(context.Context) ([]int, error) { return []int{22}, nil }
	t.Cleanup(func() {
		managedSSHDropIn = origDropIn
		effectiveSSHPortsFunc = origEffectivePorts
	})

	// Prior state: drop-in had content
	os.WriteFile(managedSSHDropIn, []byte("Port 22\nCustomOption yes\n"), 0o644)
	j, err := captureJournal()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Prepare overwrites
	os.WriteFile(managedSSHDropIn, []byte("Port 22122\n"), 0o644)

	// Rollback
	rollbackPrepare(context.Background(), j)

	data, _ := os.ReadFile(managedSSHDropIn)
	if string(data) != "Port 22\nCustomOption yes\n" {
		t.Errorf("expected original content restored, got: %q", string(data))
	}
}

func TestReadManagedPort(t *testing.T) {
	tmpDir := t.TempDir()
	origDropIn := managedSSHDropIn
	managedSSHDropIn = filepath.Join(tmpDir, "00-sys-bootstrap.conf")
	t.Cleanup(func() { managedSSHDropIn = origDropIn })

	os.WriteFile(managedSSHDropIn, []byte("Port 22122\n"), 0o644)
	p := readManagedPort()
	if p != 22122 {
		t.Errorf("expected 22122, got %d", p)
	}
}

func TestParseSSHListeningPorts(t *testing.T) {
	output := `LISTEN 0 128 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=190,fd=6))
LISTEN 0 128 [::]:22444 [::]:* users:(("sshd",pid=190,fd=7))
LISTEN 0 4096 127.0.0.1:8080 0.0.0.0:* users:(("other",pid=1,fd=1))`
	ports := parseSSHListeningPorts(output)
	if !ports[22] || !ports[22444] {
		t.Fatalf("expected SSH ports 22 and 22444, got %v", ports)
	}
	if ports[8080] {
		t.Fatal("non-SSH listener must not satisfy SSH listener verification")
	}
}
