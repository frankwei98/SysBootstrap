package modules

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
	"golang.org/x/sys/unix"
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

	result := NewFail2banModule().Check(context.Background(), &system.Context{}, nil)
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

func TestFail2banSSHDJailMatchesConfigDoesNotUsePortSubstring(t *testing.T) {
	origPath := fail2banManagedJailPath
	tmpFile := filepath.Join(t.TempDir(), "jail.d", "99-sys-bootstrap.local")
	fail2banManagedJailPath = tmpFile
	t.Cleanup(func() { fail2banManagedJailPath = origPath })

	if err := os.MkdirAll(filepath.Dir(tmpFile), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "[sshd]\nenabled = true\nport = 22122\nmaxretry = 5\nfindtime = 10m\nbantime = 1h\nbackend = systemd\nignoreip = 127.0.0.1/8 ::1\n"
	if err := os.WriteFile(tmpFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ok, _ := fail2banSSHDJailMatchesConfig(&types.Config{SSHPort: 22})
	if ok {
		t.Fatal("port 22122 must not satisfy target port 22")
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

func TestFail2banRunRollsBackExistingJailOnValidationFailure(t *testing.T) {
	origPath := os.Getenv("PATH")
	origJailPath := fail2banManagedJailPath
	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPath)
		fail2banManagedJailPath = origJailPath
	})

	tempBin := t.TempDir()
	commandLog := filepath.Join(t.TempDir(), "commands.log")
	fail2banManagedJailPath = filepath.Join(t.TempDir(), "jail.d", "99-sys-bootstrap.local")
	if err := os.MkdirAll(filepath.Dir(fail2banManagedJailPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("[sshd]\nenabled = false\n# administrator state\n")
	if err := os.WriteFile(fail2banManagedJailPath, original, 0o640); err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(fail2banManagedJailPath)
	if err != nil {
		t.Fatal(err)
	}
	originalStat, ok := originalInfo.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("cannot inspect original jail ownership")
	}
	xattrName := "user.sys-bootstrap-fail2ban-test"
	if runtime.GOOS == "darwin" {
		xattrName = "com.sys-bootstrap.fail2ban-test"
	}
	xattrValue := []byte("preserve-me")
	f, err := os.OpenFile(fail2banManagedJailPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	xattrErr := unix.Fsetxattr(int(f.Fd()), xattrName, xattrValue, 0)
	_ = f.Close()
	xattrSupported := xattrErr == nil
	if xattrErr != nil && !errors.Is(xattrErr, unix.ENOTSUP) && !errors.Is(xattrErr, unix.EOPNOTSUPP) && !errors.Is(xattrErr, unix.EPERM) {
		t.Fatalf("set test xattr: %v", xattrErr)
	}

	writeFakeCommand(t, tempBin, "dpkg", "#!/bin/sh\nexit 0\n")
	writeFakeCommand(t, tempBin, "systemctl", `#!/bin/sh
echo "systemctl $*" >> "$SYSBOOTSTRAP_TEST_LOG"
case "$1" in
  is-enabled) echo enabled; exit 0 ;;
  is-active) echo active; exit 0 ;;
  restart) exit 0 ;;
esac
exit 0
`)
	writeFakeCommand(t, tempBin, "fail2ban-client", "#!/bin/sh\nexit 1\n")
	t.Setenv("SYSBOOTSTRAP_TEST_LOG", commandLog)
	t.Setenv("PATH", tempBin+":"+origPath)

	log, err := logging.New(true)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	err = NewFail2banModule().Run(context.Background(), &system.Context{}, &types.Config{SSHPort: 22000}, log)
	if err == nil {
		t.Fatal("Run succeeded despite fail2ban-client validation failure")
	}

	got, readErr := os.ReadFile(fail2banManagedJailPath)
	if readErr != nil {
		t.Fatalf("read restored jail: %v", readErr)
	}
	if string(got) != string(original) {
		t.Fatalf("managed jail was not restored:\n%s", got)
	}
	info, statErr := os.Stat(fail2banManagedJailPath)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("restored jail mode = %o, want 640", info.Mode().Perm())
	}
	restoredStat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("cannot inspect restored jail ownership")
	}
	if restoredStat.Uid != originalStat.Uid || restoredStat.Gid != originalStat.Gid {
		t.Fatalf("restored jail owner = %d:%d, want %d:%d", restoredStat.Uid, restoredStat.Gid, originalStat.Uid, originalStat.Gid)
	}
	if xattrSupported {
		f, err = os.Open(fail2banManagedJailPath)
		if err != nil {
			t.Fatal(err)
		}
		gotXattr := make([]byte, len(xattrValue))
		n, getErr := unix.Fgetxattr(int(f.Fd()), xattrName, gotXattr)
		_ = f.Close()
		if getErr != nil {
			t.Fatalf("read restored xattr: %v", getErr)
		}
		if string(gotXattr[:n]) != string(xattrValue) {
			t.Fatalf("restored xattr = %q, want %q", gotXattr[:n], xattrValue)
		}
	}
	commands, readErr := os.ReadFile(commandLog)
	if readErr != nil {
		t.Fatalf("read command log: %v", readErr)
	}
	if !strings.Contains(string(commands), "systemctl restart fail2ban") {
		t.Fatalf("rollback did not restore the originally active service:\n%s", commands)
	}
}

func TestFail2banRunRollsBackExistingJailAfterRestartFailure(t *testing.T) {
	origPath := os.Getenv("PATH")
	origJailPath := fail2banManagedJailPath
	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPath)
		fail2banManagedJailPath = origJailPath
	})

	tempBin := t.TempDir()
	stateDir := t.TempDir()
	commandLog := filepath.Join(stateDir, "commands.log")
	restartCount := filepath.Join(stateDir, "restart-count")
	fail2banManagedJailPath = filepath.Join(t.TempDir(), "jail.d", "99-sys-bootstrap.local")
	if err := os.MkdirAll(filepath.Dir(fail2banManagedJailPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("[sshd]\nenabled = false\n# original before restart\n")
	if err := os.WriteFile(fail2banManagedJailPath, original, 0o600); err != nil {
		t.Fatal(err)
	}

	writeFakeCommand(t, tempBin, "dpkg", "#!/bin/sh\nexit 0\n")
	writeFakeCommand(t, tempBin, "fail2ban-client", "#!/bin/sh\nexit 0\n")
	writeFakeCommand(t, tempBin, "systemctl", `#!/bin/sh
echo "systemctl $*" >> "$SYSBOOTSTRAP_TEST_LOG"
case "$1" in
  is-enabled) echo enabled; exit 0 ;;
  is-active) echo active; exit 0 ;;
  restart)
    count=0
    [ ! -f "$SYSBOOTSTRAP_RESTART_COUNT" ] || count=$(cat "$SYSBOOTSTRAP_RESTART_COUNT")
    count=$((count + 1))
    echo "$count" > "$SYSBOOTSTRAP_RESTART_COUNT"
    [ "$count" -gt 1 ]
    exit $?
    ;;
esac
exit 0
`)
	t.Setenv("SYSBOOTSTRAP_TEST_LOG", commandLog)
	t.Setenv("SYSBOOTSTRAP_RESTART_COUNT", restartCount)
	t.Setenv("PATH", tempBin+":"+origPath)

	log, err := logging.New(true)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	err = NewFail2banModule().Run(context.Background(), &system.Context{}, &types.Config{SSHPort: 22000}, log)
	if err == nil {
		t.Fatal("Run succeeded despite restart failure")
	}
	got, readErr := os.ReadFile(fail2banManagedJailPath)
	if readErr != nil {
		t.Fatalf("read restored jail: %v", readErr)
	}
	if string(got) != string(original) {
		t.Fatalf("managed jail was not restored after restart failure:\n%s", got)
	}
	count, readErr := os.ReadFile(restartCount)
	if readErr != nil {
		t.Fatalf("read restart count: %v", readErr)
	}
	if strings.TrimSpace(string(count)) != "2" {
		t.Fatalf("restart count = %q, want primary attempt plus rollback attempt", count)
	}
}

func TestFail2banRunRestoresServiceWhenMatchingJailRestartFails(t *testing.T) {
	origPath := os.Getenv("PATH")
	origJailPath := fail2banManagedJailPath
	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPath)
		fail2banManagedJailPath = origJailPath
	})

	tempBin := t.TempDir()
	stateDir := t.TempDir()
	restartCount := filepath.Join(stateDir, "restart-count")
	fail2banManagedJailPath = filepath.Join(t.TempDir(), "jail.d", "99-sys-bootstrap.local")
	cfg := &types.Config{SSHPort: 22000}
	if err := writeFail2banManagedJail(cfg); err != nil {
		t.Fatal(err)
	}

	writeFakeCommand(t, tempBin, "dpkg", "#!/bin/sh\nexit 0\n")
	writeFakeCommand(t, tempBin, "fail2ban-client", "#!/bin/sh\nexit 0\n")
	writeFakeCommand(t, tempBin, "systemctl", `#!/bin/sh
case "$1" in
  is-enabled) echo enabled; exit 0 ;;
  is-active) echo active; exit 0 ;;
  restart)
    count=0
    [ ! -f "$SYSBOOTSTRAP_RESTART_COUNT" ] || count=$(cat "$SYSBOOTSTRAP_RESTART_COUNT")
    count=$((count + 1))
    echo "$count" > "$SYSBOOTSTRAP_RESTART_COUNT"
    [ "$count" -gt 1 ]
    exit $?
    ;;
esac
exit 0
`)
	t.Setenv("SYSBOOTSTRAP_RESTART_COUNT", restartCount)
	t.Setenv("PATH", tempBin+":"+origPath)

	log, err := logging.New(true)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	if err := NewFail2banModule().Run(context.Background(), &system.Context{}, cfg, log); err == nil {
		t.Fatal("Run succeeded despite primary restart failure")
	}
	count, err := os.ReadFile(restartCount)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(count)) != "2" {
		t.Fatalf("restart count = %q, want primary attempt plus rollback attempt", count)
	}
}

func TestFail2banRunRemovesNewJailOnValidationFailure(t *testing.T) {
	origPath := os.Getenv("PATH")
	origJailPath := fail2banManagedJailPath
	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPath)
		fail2banManagedJailPath = origJailPath
	})

	tempBin := t.TempDir()
	fail2banManagedJailPath = filepath.Join(t.TempDir(), "jail.d", "99-sys-bootstrap.local")
	writeFakeCommand(t, tempBin, "dpkg", "#!/bin/sh\nexit 0\n")
	writeFakeCommand(t, tempBin, "fail2ban-client", "#!/bin/sh\nexit 1\n")
	writeFakeCommand(t, tempBin, "systemctl", `#!/bin/sh
case "$1" in
  is-enabled) echo disabled; exit 1 ;;
  is-active) echo inactive; exit 3 ;;
esac
exit 0
`)
	t.Setenv("PATH", tempBin+":"+origPath)

	log, err := logging.New(true)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	err = NewFail2banModule().Run(context.Background(), &system.Context{}, &types.Config{SSHPort: 22000}, log)
	if err == nil {
		t.Fatal("Run succeeded despite fail2ban-client validation failure")
	}
	if _, statErr := os.Stat(fail2banManagedJailPath); !os.IsNotExist(statErr) {
		t.Fatalf("new managed jail remains after rollback: %v", statErr)
	}
}

func TestFail2banRollbackErrorIsCombinedWithPrimaryFailure(t *testing.T) {
	origPath := os.Getenv("PATH")
	origJailPath := fail2banManagedJailPath
	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPath)
		fail2banManagedJailPath = origJailPath
	})

	tempBin := t.TempDir()
	fail2banManagedJailPath = filepath.Join(t.TempDir(), "99-sys-bootstrap.local")
	writeFakeCommand(t, tempBin, "systemctl", `#!/bin/sh
case "$1" in
	  restart) echo "rollback restart failed" >&2; exit 1 ;;
	  start) echo "rollback start failed" >&2; exit 1 ;;
	  is-enabled) echo disabled; exit 1 ;;
  is-active) echo inactive; exit 3 ;;
esac
exit 0
`)
	t.Setenv("PATH", tempBin+":"+origPath)

	primaryErr := errors.New("primary validation failed")
	err := fail2banErrorWithRollback(primaryErr, &fail2banTransactionJournal{
		serviceWasActive: true,
	})
	if err == nil {
		t.Fatal("expected combined primary and rollback failure")
	}
	for _, want := range []string{"primary validation failed", "fail2ban rollback failed", "restore active fail2ban service", "rollback restart failed", "rollback start failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("combined error %q missing %q", err, want)
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
