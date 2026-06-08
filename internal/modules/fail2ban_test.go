package modules

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

func TestFail2banModuleInterface(t *testing.T) {
	m := NewFail2banModule()
	if m.ID() != "fail2ban" {
		t.Fatalf("ID() = %q, want fail2ban", m.ID())
	}
	if !m.RequiresRoot() {
		t.Fatal("fail2ban module should require root")
	}
	if len(m.Dependencies()) != 0 {
		t.Fatalf("Dependencies() = %v, want none", m.Dependencies())
	}
}

func TestFail2banPlanIncludesInstallOrConfig(t *testing.T) {
	m := NewFail2banModule()
	steps, err := m.Plan(context.Background(), &system.Context{}, &types.Config{
		SSHPort:          22000,
		Fail2banMaxRetry: 3,
		Fail2banFindTime: "5m",
		Fail2banBanTime:  "30m",
	})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(steps) == 0 {
		t.Fatal("expected non-empty fail2ban plan")
	}
	found := false
	for _, step := range steps {
		if strings.Contains(step.Title, "fail2ban") || strings.Contains(step.Title, "sshd jail") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("unexpected fail2ban steps: %#v", steps)
	}
	detailFound := false
	for _, step := range steps {
		if step.Title == "Write sshd jail config" && strings.Contains(step.Detail, "port 22000") && strings.Contains(step.Detail, "maxretry 3") {
			detailFound = true
		}
	}
	if !detailFound {
		t.Fatalf("expected fail2ban plan detail to include ssh port and policy, got %#v", steps)
	}
}

func TestEffectiveSSHPortUsesConfigValue(t *testing.T) {
	port := effectiveSSHPort(&types.Config{SSHPort: 22122})
	if port != 22122 {
		t.Fatalf("port = %d, want 22122", port)
	}
}

func TestEffectiveSSHPortFallsBackToSSHDConfig(t *testing.T) {
	origPath := sshdConfigPath
	tmpFile := filepath.Join(t.TempDir(), "sshd_config")
	if err := os.WriteFile(tmpFile, []byte("Port 2222\n"), 0o644); err != nil {
		t.Fatalf("write sshd_config: %v", err)
	}
	sshdConfigPath = tmpFile
	t.Cleanup(func() {
		sshdConfigPath = origPath
	})

	port := effectiveSSHPort(&types.Config{})
	if port != 2222 {
		t.Fatalf("port = %d, want 2222 from sshd_config", port)
	}
}

func TestWriteFail2banJailLocalIncludesSSHPort(t *testing.T) {
	origPath := fail2banJailLocalPath
	tmpFile := t.TempDir() + "/jail.local"
	fail2banJailLocalPath = tmpFile
	t.Cleanup(func() {
		fail2banJailLocalPath = origPath
	})

	cfg := &types.Config{
		SSHPort:          22000,
		Fail2banMaxRetry: 3,
		Fail2banFindTime: "5m",
		Fail2banBanTime:  "30m",
		Fail2banBackend:  "auto",
		Fail2banIgnoreIP: "127.0.0.1/8 ::1 10.0.0.0/8",
	}
	if err := writeFail2banJailLocal(cfg); err != nil {
		t.Fatalf("writeFail2banJailLocal failed: %v", err)
	}
	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("reading jail.local failed: %v", err)
	}
	text := string(content)
	for _, part := range []string{
		"port = 22000",
		"maxretry = 3",
		"findtime = 5m",
		"bantime = 30m",
		"backend = auto",
		"ignoreip = 127.0.0.1/8 ::1 10.0.0.0/8",
	} {
		if !strings.Contains(text, part) {
			t.Fatalf("content missing %q:\n%s", part, text)
		}
	}
}

func TestFail2banSSHDJailMatchesConfig(t *testing.T) {
	origPath := fail2banJailLocalPath
	tmpFile := t.TempDir() + "/jail.local"
	fail2banJailLocalPath = tmpFile
	t.Cleanup(func() {
		fail2banJailLocalPath = origPath
	})

	cfg := &types.Config{
		SSHPort:          22000,
		Fail2banMaxRetry: 3,
		Fail2banFindTime: "5m",
		Fail2banBanTime:  "30m",
		Fail2banBackend:  "auto",
		Fail2banIgnoreIP: "127.0.0.1/8 ::1 10.0.0.0/8",
	}
	if err := writeFail2banJailLocal(cfg); err != nil {
		t.Fatalf("writeFail2banJailLocal failed: %v", err)
	}

	ok, summary := fail2banSSHDJailMatchesConfig(cfg)
	if !ok {
		t.Fatalf("expected jail to match config, got summary %q", summary)
	}

	otherCfg := &types.Config{
		SSHPort:          22122,
		Fail2banMaxRetry: 3,
		Fail2banFindTime: "5m",
		Fail2banBanTime:  "30m",
		Fail2banBackend:  "auto",
		Fail2banIgnoreIP: "127.0.0.1/8 ::1 10.0.0.0/8",
	}
	ok, summary = fail2banSSHDJailMatchesConfig(otherCfg)
	if ok {
		t.Fatalf("expected jail mismatch when ssh port differs")
	}
	if !strings.Contains(summary, "differs") {
		t.Fatalf("unexpected mismatch summary: %q", summary)
	}
}

func TestFail2banRunInstallsWritesAndValidates(t *testing.T) {
	origPath := os.Getenv("PATH")
	origJailPath := fail2banJailLocalPath
	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPath)
		fail2banJailLocalPath = origJailPath
	})

	tempBin := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "fail2ban-run.log")
	fail2banJailLocalPath = filepath.Join(t.TempDir(), "jail.local")

	writeFakeCommand(t, tempBin, "dpkg", `#!/bin/sh
echo "dpkg $*" >> "$SYSBOOTSTRAP_TEST_LOG"
exit 1
`)
	writeFakeCommand(t, tempBin, "apt-get", `#!/bin/sh
echo "apt-get $*" >> "$SYSBOOTSTRAP_TEST_LOG"
exit 0
`)
	writeFakeCommand(t, tempBin, "env", `#!/bin/sh
shift
exec "$@"
`)
	writeFakeCommand(t, tempBin, "systemctl", `#!/bin/sh
echo "systemctl $*" >> "$SYSBOOTSTRAP_TEST_LOG"
case "$1" in
  is-enabled|is-active)
    exit 1
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

	m := NewFail2banModule()
	cfg := &types.Config{
		SSHPort:          22000,
		Fail2banMaxRetry: 3,
		Fail2banFindTime: "5m",
		Fail2banBanTime:  "30m",
		Fail2banBackend:  "auto",
		Fail2banIgnoreIP: "127.0.0.1/8 ::1 10.0.0.0/8",
	}
	if err := m.Run(context.Background(), &system.Context{}, cfg, log); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file failed: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"apt-get update -y",
		"apt-get install -y fail2ban",
		"fail2ban-client -d",
		"systemctl enable --now fail2ban",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("fail2ban run log missing %q:\n%s", want, text)
		}
	}
	if count := strings.Count(text, "fail2ban-client -d"); count != 2 {
		t.Fatalf("expected fail2ban-client -d twice, got %d:\n%s", count, text)
	}

	jailContent, err := os.ReadFile(fail2banJailLocalPath)
	if err != nil {
		t.Fatalf("read jail.local failed: %v", err)
	}
	jailText := string(jailContent)
	for _, want := range []string{"port = 22000", "maxretry = 3", "backend = auto"} {
		if !strings.Contains(jailText, want) {
			t.Fatalf("jail.local missing %q:\n%s", want, jailText)
		}
	}
}

func TestFail2banServiceEnabledRequiresActiveState(t *testing.T) {
	origPath := os.Getenv("PATH")
	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPath)
	})

	tempBin := t.TempDir()
	writeFakeCommand(t, tempBin, "systemctl", `#!/bin/sh
case "$1" in
  is-enabled)
    echo "enabled"
    exit 0
    ;;
  is-active)
    echo "inactive"
    exit 3
    ;;
esac
exit 1
`)
	t.Setenv("PATH", tempBin+":"+origPath)

	if fail2banServiceEnabled() {
		t.Fatal("expected fail2ban service to be not ready when active state is not active")
	}
}
