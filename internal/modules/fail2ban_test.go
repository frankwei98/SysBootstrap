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
	origEffectivePorts := effectiveSSHPortsFunc
	tmpFile := filepath.Join(t.TempDir(), "sshd_config")
	if err := os.WriteFile(tmpFile, []byte("Port 2222\n"), 0o644); err != nil {
		t.Fatalf("write sshd_config: %v", err)
	}
	sshdConfigPath = tmpFile
	t.Cleanup(func() {
		sshdConfigPath = origPath
		effectiveSSHPortsFunc = origEffectivePorts
	})
	effectiveSSHPortsFunc = func(context.Context) ([]int, error) {
		return nil, os.ErrNotExist
	}

	port := effectiveSSHPort(&types.Config{})
	if port != 2222 {
		t.Fatalf("port = %d, want 2222 from sshd_config", port)
	}
}

func TestFail2banUsesEffectiveSSHDPortsFromDropIns(t *testing.T) {
	origEffectivePorts := effectiveSSHPortsFunc
	effectiveSSHPortsFunc = func(context.Context) ([]int, error) { return []int{22122}, nil }
	t.Cleanup(func() { effectiveSSHPortsFunc = origEffectivePorts })

	if got := fail2banSSHPortSetting(&types.Config{}); got != "22122" {
		t.Fatalf("fail2ban port setting = %q, want effective sshd port 22122", got)
	}
}

func TestFail2banPlanWarnsWhenEffectivePortsFallback(t *testing.T) {
	origPath := sshdConfigPath
	origEffectivePorts := effectiveSSHPortsFunc
	sshdConfigPath = filepath.Join(t.TempDir(), "sshd_config")
	if err := os.WriteFile(sshdConfigPath, []byte("Port 22\n"), 0o644); err != nil {
		t.Fatalf("write sshd config: %v", err)
	}
	effectiveSSHPortsFunc = func(context.Context) ([]int, error) {
		return nil, os.ErrPermission
	}
	t.Cleanup(func() {
		sshdConfigPath = origPath
		effectiveSSHPortsFunc = origEffectivePorts
	})

	steps, err := NewFail2banModule().Plan(context.Background(), &system.Context{}, &types.Config{})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	for _, step := range steps {
		if step.Title == "Write sshd jail config" {
			if !strings.Contains(step.Detail, "warning: sshd -T could not resolve effective SSH ports") {
				t.Fatalf("expected fallback warning in plan detail, got %q", step.Detail)
			}
			return
		}
	}
	t.Fatalf("expected sshd jail config step, got %#v", steps)
}

func TestFail2banCheckWarnsWhenExistingJailMatchesFallbackPort(t *testing.T) {
	origPath := os.Getenv("PATH")
	origJailPath := fail2banManagedJailPath
	origEffectivePorts := effectiveSSHPortsFunc
	t.Cleanup(func() {
		fail2banManagedJailPath = origJailPath
		effectiveSSHPortsFunc = origEffectivePorts
	})

	tempBin := t.TempDir()
	t.Setenv("PATH", tempBin+":"+origPath)
	writeFakeCommand(t, tempBin, "dpkg", "#!/bin/sh\nexit 0\n")
	writeFakeCommand(t, tempBin, "systemctl", `#!/bin/sh
case "$1" in
  is-enabled) echo enabled ;;
  is-active) echo active ;;
esac
`)
	fail2banManagedJailPath = filepath.Join(t.TempDir(), "jail.d", "99-sys-bootstrap.local")
	effectiveSSHPortsFunc = func(context.Context) ([]int, error) {
		return nil, os.ErrPermission
	}
	if err := writeFail2banManagedJail(&types.Config{}); err != nil {
		t.Fatalf("write managed jail: %v", err)
	}

	result := NewFail2banModule().Check(context.Background(), &system.Context{})
	if !result.Satisfied {
		t.Fatalf("expected matching jail to satisfy check, got %#v", result)
	}
	if !strings.Contains(result.Message, "warning: sshd -T could not resolve effective SSH ports") {
		t.Fatalf("expected fallback warning in check message, got %q", result.Message)
	}
}

func TestFail2banRejectsUnsafePolicyValues(t *testing.T) {
	m := NewFail2banModule()
	_, err := m.Plan(context.Background(), &system.Context{}, &types.Config{Fail2banFindTime: "10m\nport = 22"})
	if err == nil {
		t.Fatal("expected newline-containing fail2ban value to be rejected")
	}
}

func TestWriteFail2banManagedJailPreservesAdministratorJailLocal(t *testing.T) {
	dir := t.TempDir()
	adminJail := filepath.Join(dir, "jail.local")
	managedJail := filepath.Join(dir, "jail.d", "99-sys-bootstrap.local")
	adminContent := "[nginx-botsearch]\nenabled = true\n"
	if err := os.WriteFile(adminJail, []byte(adminContent), 0o644); err != nil {
		t.Fatalf("write administrator jail: %v", err)
	}

	origPath := fail2banManagedJailPath
	fail2banManagedJailPath = managedJail
	t.Cleanup(func() { fail2banManagedJailPath = origPath })

	if err := writeFail2banManagedJail(&types.Config{SSHPort: 22000}); err != nil {
		t.Fatalf("writeFail2banManagedJail failed: %v", err)
	}
	gotAdmin, err := os.ReadFile(adminJail)
	if err != nil {
		t.Fatalf("read administrator jail: %v", err)
	}
	if string(gotAdmin) != adminContent {
		t.Fatalf("administrator jail.local was modified:\n%s", gotAdmin)
	}
	managed, err := os.ReadFile(managedJail)
	if err != nil {
		t.Fatalf("read managed jail: %v", err)
	}
	if !strings.Contains(string(managed), "port = 22000") {
		t.Fatalf("managed jail missing SSH port:\n%s", managed)
	}
}

func TestWriteFail2banManagedJailIncludesSSHPort(t *testing.T) {
	origPath := fail2banManagedJailPath
	tmpFile := t.TempDir() + "/jail.d/99-sys-bootstrap.local"
	fail2banManagedJailPath = tmpFile
	t.Cleanup(func() {
		fail2banManagedJailPath = origPath
	})

	cfg := &types.Config{
		SSHPort:          22000,
		Fail2banMaxRetry: 3,
		Fail2banFindTime: "5m",
		Fail2banBanTime:  "30m",
		Fail2banBackend:  "auto",
		Fail2banIgnoreIP: "127.0.0.1/8 ::1 10.0.0.0/8",
	}
	if err := writeFail2banManagedJail(cfg); err != nil {
		t.Fatalf("writeFail2banManagedJail failed: %v", err)
	}
	content, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("reading managed jail failed: %v", err)
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
	origPath := fail2banManagedJailPath
	tmpFile := t.TempDir() + "/jail.d/99-sys-bootstrap.local"
	fail2banManagedJailPath = tmpFile
	t.Cleanup(func() {
		fail2banManagedJailPath = origPath
	})

	cfg := &types.Config{
		SSHPort:          22000,
		Fail2banMaxRetry: 3,
		Fail2banFindTime: "5m",
		Fail2banBanTime:  "30m",
		Fail2banBackend:  "auto",
		Fail2banIgnoreIP: "127.0.0.1/8 ::1 10.0.0.0/8",
	}
	if err := writeFail2banManagedJail(cfg); err != nil {
		t.Fatalf("writeFail2banManagedJail failed: %v", err)
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
	origJailPath := fail2banManagedJailPath
	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPath)
		fail2banManagedJailPath = origJailPath
	})

	tempBin := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "fail2ban-run.log")
	fail2banManagedJailPath = filepath.Join(t.TempDir(), "jail.d", "99-sys-bootstrap.local")

	writeFakeCommand(t, tempBin, "dpkg", `#!/bin/sh
echo "dpkg $*" >> "$SYSBOOTSTRAP_TEST_LOG"
exit 1
`)
	writeFakeCommand(t, tempBin, "apt-get", `#!/bin/sh
echo "apt-get $*" >> "$SYSBOOTSTRAP_TEST_LOG"
exit 0
`)
	writeFakeCommand(t, tempBin, "env", `#!/bin/sh
while [ "$#" -gt 0 ]; do
  case "$1" in
    *=*) shift ;;
    *) break ;;
  esac
done
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
		"DPkg::Lock::Timeout=120",
		"Acquire::Retries=3",
		"update -y",
		"install -y fail2ban",
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

	jailContent, err := os.ReadFile(fail2banManagedJailPath)
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
