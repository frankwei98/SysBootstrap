package modules

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
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
		{"malformed rsa payload", "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC...", false},
		{"malformed ecdsa payload", "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTY...", false},
		{"malformed sk payload", "sk-ssh-ed25519@openssh.com AAAA...", false},
		{"dss rejected", "ssh-dss AAAAC3NzaC1kc3MAAACB", false},
		{"bare key type", "ssh-ed25519", false},
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
	useFakeSSHEffectiveOutput(t, "port 22333\npermitrootlogin yes\npasswordauthentication yes\nkbdinteractiveauthentication yes")
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

func TestSSHCheckReportsMultipleEffectivePortsAsUnsatisfied(t *testing.T) {
	origPath := sshConfigPath
	origService := sshServiceReadyFn
	sshConfigPath = filepath.Join(t.TempDir(), "sshd_config")
	if err := os.WriteFile(sshConfigPath, []byte("Port 22122\n"), 0o644); err != nil {
		t.Fatalf("write sshd_config: %v", err)
	}
	sshServiceReadyFn = func() bool { return true }
	useFakeSSHEffectiveOutput(t, "port 22\nport 22122\npermitrootlogin no\npasswordauthentication no\nkbdinteractiveauthentication no")
	t.Cleanup(func() {
		sshConfigPath = origPath
		sshServiceReadyFn = origService
	})

	result := NewSSHModule().Check(context.Background(), &system.Context{HasSSHD: true})
	if result.Satisfied {
		t.Fatalf("multiple effective SSH ports should be unsatisfied: %#v", result)
	}
	if !strings.Contains(result.Message, "ports [22 22122]") {
		t.Fatalf("message = %q, want all effective ports", result.Message)
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

func useFakeSSHEffectiveOutput(t *testing.T, output string) {
	t.Helper()
	useTempSSHDRunDir(t)
	tempBin := t.TempDir()
	writeFakeCommand(t, tempBin, "sshd", "#!/bin/sh\ncat <<'EOF'\n"+output+"\nEOF\n")
	t.Setenv("PATH", tempBin+":"+os.Getenv("PATH"))
}

func useTempSSHDRunDir(t *testing.T) {
	t.Helper()
	original := sshdRuntimeDir
	sshdRuntimeDir = filepath.Join(t.TempDir(), "run", "sshd")
	t.Cleanup(func() { sshdRuntimeDir = original })
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

func TestSSHPlanRejectsOutOfRangeExistingPort(t *testing.T) {
	for _, value := range []string{"0", "-1", "65536"} {
		t.Run(value, func(t *testing.T) {
			origPath := sshConfigPath
			origDropIn := managedSSHDropIn
			origService := sshServiceReadyFn
			sshConfigPath = filepath.Join(t.TempDir(), "sshd_config")
			managedSSHDropIn = filepath.Join(t.TempDir(), "00-sys-bootstrap.conf")
			sshServiceReadyFn = func() bool { return true }
			t.Cleanup(func() {
				sshConfigPath = origPath
				managedSSHDropIn = origDropIn
				sshServiceReadyFn = origService
			})

			if err := os.WriteFile(sshConfigPath, []byte("Port "+value+"\n"), 0o644); err != nil {
				t.Fatalf("write sshd_config: %v", err)
			}
			_, err := NewSSHModule().Plan(context.Background(), &system.Context{
				HasSSHD:        true,
				HasSSHDService: true,
			}, &types.Config{SSHPort: 22122})
			if err == nil || !strings.Contains(err.Error(), "invalid Port value") {
				t.Fatalf("Plan error = %v, want invalid Port value for %s", err, value)
			}
		})
	}
}

func TestSSHPlanRejectsOutOfRangeRequestedPort(t *testing.T) {
	origPath := sshConfigPath
	origDropIn := managedSSHDropIn
	origService := sshServiceReadyFn
	sshConfigPath = filepath.Join(t.TempDir(), "sshd_config")
	managedSSHDropIn = filepath.Join(t.TempDir(), "00-sys-bootstrap.conf")
	sshServiceReadyFn = func() bool { return true }
	t.Cleanup(func() {
		sshConfigPath = origPath
		managedSSHDropIn = origDropIn
		sshServiceReadyFn = origService
	})
	if err := os.WriteFile(sshConfigPath, []byte("Port 22\n"), 0o644); err != nil {
		t.Fatalf("write sshd_config: %v", err)
	}

	for _, port := range []int{-1, 65536} {
		t.Run(strconv.Itoa(port), func(t *testing.T) {
			_, err := NewSSHModule().Plan(context.Background(), &system.Context{
				HasSSHD:        true,
				HasSSHDService: true,
			}, &types.Config{SSHPort: port})
			if err == nil || !strings.Contains(err.Error(), "between 1 and 65535") {
				t.Fatalf("Plan error = %v, want requested-port range guidance", err)
			}
		})
	}
}

func TestSSHPlanNoStepsWhenAlreadySatisfied(t *testing.T) {
	origPath := sshConfigPath
	origDropIn := managedSSHDropIn
	origService := sshServiceReadyFn
	origUFW := sshUFWAllowsPortFn
	sshDir := filepath.Join(t.TempDir(), "etc", "ssh")
	dropInDir := filepath.Join(sshDir, "sshd_config.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatalf("create SSH config directories: %v", err)
	}
	tmpFile := filepath.Join(sshDir, "sshd_config")
	managedSSHDropIn = filepath.Join(dropInDir, "00-sys-bootstrap.conf")
	if err := os.WriteFile(tmpFile, []byte("Include "+dropInDir+"/*.conf\n"), 0o644); err != nil {
		t.Fatalf("write sshd_config: %v", err)
	}
	if err := os.WriteFile(managedSSHDropIn, []byte("Port 22122\nPermitRootLogin no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\n"), 0o644); err != nil {
		t.Fatalf("write managed SSH drop-in: %v", err)
	}
	sshConfigPath = tmpFile
	sshServiceReadyFn = func() bool { return true }
	sshUFWAllowsPortFn = func(int) bool { return true }
	useFakeSSHEffectiveOutput(t, "port 22122\npermitrootlogin no\npasswordauthentication no\nkbdinteractiveauthentication no")
	t.Cleanup(func() {
		sshConfigPath = origPath
		managedSSHDropIn = origDropIn
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

	steps, err := m.Plan(context.Background(), sys, &types.Config{
		SSHPort:        22122,
		SSHAllowUFW:    true,
		SSHDisableRoot: true,
		SSHDisablePass: true,
	})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(steps) != 0 {
		t.Fatalf("unexpected steps when managed SSH config is already satisfied: %#v", steps)
	}
	check := m.Check(context.Background(), sys)
	if !strings.Contains(check.Message, "port 22122") {
		t.Fatalf("Check message = %q, want managed SSH port", check.Message)
	}
}

func TestSSHPlanUsesEffectiveDaemonState(t *testing.T) {
	useTempSSHDRunDir(t)
	origPath := os.Getenv("PATH")
	origConfigPath := sshConfigPath
	origDropIn := managedSSHDropIn
	origService := sshServiceReadyFn
	sshDir := filepath.Join(t.TempDir(), "etc", "ssh")
	dropInDir := filepath.Join(sshDir, "sshd_config.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatalf("create SSH config directories: %v", err)
	}
	sshConfigPath = filepath.Join(sshDir, "sshd_config")
	managedSSHDropIn = filepath.Join(dropInDir, "00-sys-bootstrap.conf")
	if err := os.WriteFile(sshConfigPath, []byte("Include "+dropInDir+"/*.conf\n"), 0o644); err != nil {
		t.Fatalf("write sshd_config: %v", err)
	}
	if err := os.WriteFile(managedSSHDropIn, []byte("Port 22122\nPermitRootLogin no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\n"), 0o644); err != nil {
		t.Fatalf("write managed SSH drop-in: %v", err)
	}
	tempBin := t.TempDir()
	writeFakeCommand(t, tempBin, "sshd", "#!/bin/sh\nprintf '%s\\n' 'port 22' 'port 22122' 'permitrootlogin yes' 'passwordauthentication yes' 'kbdinteractiveauthentication yes'\n")
	t.Setenv("PATH", tempBin+":"+origPath)
	sshServiceReadyFn = func() bool { return true }
	t.Cleanup(func() {
		sshConfigPath = origConfigPath
		managedSSHDropIn = origDropIn
		sshServiceReadyFn = origService
	})

	steps, err := NewSSHModule().Plan(context.Background(), &system.Context{
		HasSSHD:        true,
		HasSSHDService: true,
	}, &types.Config{SSHPort: 22122, SSHDisableRoot: true, SSHDisablePass: true})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	wantTitles := map[string]bool{
		"Configure SSH port":    false,
		"Disable root login":    false,
		"Disable password auth": false,
	}
	for _, step := range steps {
		if _, ok := wantTitles[step.Title]; ok {
			wantTitles[step.Title] = true
		}
	}
	for title, found := range wantTitles {
		if !found {
			t.Errorf("effective daemon state did not produce %q step: %#v", title, steps)
		}
	}
}

func TestSSHPlanAppliesConnectionContextToEffectiveState(t *testing.T) {
	useTempSSHDRunDir(t)
	origConfigPath := sshConfigPath
	origDropIn := managedSSHDropIn
	origService := sshServiceReadyFn
	sshDir := filepath.Join(t.TempDir(), "etc", "ssh")
	dropInDir := filepath.Join(sshDir, "sshd_config.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatalf("create SSH config directories: %v", err)
	}
	sshConfigPath = filepath.Join(sshDir, "sshd_config")
	managedSSHDropIn = filepath.Join(dropInDir, "00-sys-bootstrap.conf")
	if err := os.WriteFile(sshConfigPath, []byte("Include "+dropInDir+"/*.conf\n"), 0o644); err != nil {
		t.Fatalf("write sshd_config: %v", err)
	}
	if err := os.WriteFile(managedSSHDropIn, []byte("Port 22122\nPasswordAuthentication no\nKbdInteractiveAuthentication no\n"), 0o644); err != nil {
		t.Fatalf("write managed SSH drop-in: %v", err)
	}
	tempBin := t.TempDir()
	writeFakeCommand(t, tempBin, "sshd", `#!/bin/sh
echo "port 22122"
echo "permitrootlogin no"
if [ "$SYSBOOTSTRAP_TEST_MATCH_PASSWORD_LOCAL_PORT" = "yes" ]; then
  case " $* " in
    *"lport=22122"*)
      echo "passwordauthentication yes"
      echo "kbdinteractiveauthentication yes"
      exit 0
      ;;
  esac
fi
echo "passwordauthentication no"
echo "kbdinteractiveauthentication no"
`)
	t.Setenv("PATH", tempBin+":"+os.Getenv("PATH"))
	sshServiceReadyFn = func() bool { return true }
	t.Cleanup(func() {
		sshConfigPath = origConfigPath
		managedSSHDropIn = origDropIn
		sshServiceReadyFn = origService
	})

	m := NewSSHModule()
	sys := &system.Context{
		HasSSHD:        true,
		HasSSHDService: true,
	}
	cfg := &types.Config{SSHPort: 22122, SSHDisablePass: true}
	t.Setenv("SYSBOOTSTRAP_TEST_MATCH_PASSWORD_LOCAL_PORT", "yes")
	steps, err := m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	foundPasswordStep := false
	for _, step := range steps {
		if step.Title == "Disable password auth" {
			foundPasswordStep = true
		}
	}
	if !foundPasswordStep {
		t.Fatalf("expected Match LocalPort policy to keep password hardening pending: %#v", steps)
	}

	t.Setenv("SYSBOOTSTRAP_TEST_MATCH_PASSWORD_LOCAL_PORT", "")
	steps, err = m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("negative-control Plan failed: %v", err)
	}
	for _, step := range steps {
		if step.Title == "Disable password auth" {
			t.Fatalf("effective configuration fixture was not consumed by Plan: %#v", steps)
		}
	}
}

func TestSSHPlanChecksDefaultRequestedPortConnectionContext(t *testing.T) {
	useTempSSHDRunDir(t)
	origConfigPath := sshConfigPath
	origDropIn := managedSSHDropIn
	origService := sshServiceReadyFn
	sshDir := filepath.Join(t.TempDir(), "etc", "ssh")
	dropInDir := filepath.Join(sshDir, "sshd_config.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatalf("create SSH config directories: %v", err)
	}
	sshConfigPath = filepath.Join(sshDir, "sshd_config")
	managedSSHDropIn = filepath.Join(dropInDir, "00-sys-bootstrap.conf")
	if err := os.WriteFile(sshConfigPath, []byte("Include "+dropInDir+"/*.conf\n"), 0o644); err != nil {
		t.Fatalf("write sshd_config: %v", err)
	}
	tempBin := t.TempDir()
	t.Setenv("SYSBOOTSTRAP_TEST_DEFAULT_PORT", strconv.Itoa(DefaultSSHPort))
	writeFakeCommand(t, tempBin, "sshd", `#!/bin/sh
echo "port 22"
echo "permitrootlogin no"
if [ "$SYSBOOTSTRAP_TEST_MATCH_PASSWORD_LOCAL_PORT" = "yes" ]; then
  case " $* " in
    *"lport=$SYSBOOTSTRAP_TEST_DEFAULT_PORT"*)
      echo "passwordauthentication yes"
      echo "kbdinteractiveauthentication yes"
      exit 0
      ;;
  esac
fi
echo "passwordauthentication no"
echo "kbdinteractiveauthentication no"
`)
	t.Setenv("PATH", tempBin+":"+os.Getenv("PATH"))
	sshServiceReadyFn = func() bool { return true }
	t.Cleanup(func() {
		sshConfigPath = origConfigPath
		managedSSHDropIn = origDropIn
		sshServiceReadyFn = origService
	})

	m := NewSSHModule()
	sys := &system.Context{
		HasSSHD:        true,
		HasSSHDService: true,
	}
	cfg := &types.Config{SSHDisablePass: true}
	t.Setenv("SYSBOOTSTRAP_TEST_MATCH_PASSWORD_LOCAL_PORT", "yes")
	steps, err := m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	foundPasswordStep := false
	for _, step := range steps {
		if step.Title == "Disable password auth" {
			foundPasswordStep = true
		}
	}
	if !foundPasswordStep {
		t.Fatalf("expected default requested port Match policy to keep password hardening pending: %#v", steps)
	}

	t.Setenv("SYSBOOTSTRAP_TEST_MATCH_PASSWORD_LOCAL_PORT", "")
	steps, err = m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("negative-control Plan failed: %v", err)
	}
	for _, step := range steps {
		if step.Title == "Disable password auth" {
			t.Fatalf("effective configuration fixture was not consumed by default-port Plan: %#v", steps)
		}
	}
}

func TestQuerySSHEffectiveOutputErrorIncludesConnectionContext(t *testing.T) {
	useTempSSHDRunDir(t)
	tempBin := t.TempDir()
	writeFakeCommand(t, tempBin, "sshd", "#!/bin/sh\necho query failed >&2\nexit 1\n")
	t.Setenv("PATH", tempBin+":"+os.Getenv("PATH"))

	_, err := querySSHEffectiveOutput(context.Background(), "alice", 22122)
	if err == nil {
		t.Fatal("expected effective configuration query to fail")
	}
	for _, want := range []string{"alice", "22122"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("query error = %q, want connection context %q", err, want)
		}
	}
}

func TestSSHPlanDisablesKeyboardInteractiveAuthenticationWithPassword(t *testing.T) {
	origPath := sshConfigPath
	origDropIn := managedSSHDropIn
	origService := sshServiceReadyFn
	sshDir := filepath.Join(t.TempDir(), "etc", "ssh")
	dropInDir := filepath.Join(sshDir, "sshd_config.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatalf("create SSH config directories: %v", err)
	}
	sshConfigPath = filepath.Join(sshDir, "sshd_config")
	managedSSHDropIn = filepath.Join(dropInDir, "00-sys-bootstrap.conf")
	if err := os.WriteFile(sshConfigPath, []byte("Include "+dropInDir+"/*.conf\n"), 0o644); err != nil {
		t.Fatalf("write sshd_config: %v", err)
	}
	if err := os.WriteFile(managedSSHDropIn, []byte("Port 22122\nPasswordAuthentication no\nKbdInteractiveAuthentication yes\n"), 0o644); err != nil {
		t.Fatalf("write managed SSH drop-in: %v", err)
	}
	sshServiceReadyFn = func() bool { return true }
	t.Cleanup(func() {
		sshConfigPath = origPath
		managedSSHDropIn = origDropIn
		sshServiceReadyFn = origService
	})

	steps, err := NewSSHModule().Plan(context.Background(), &system.Context{
		HasSSHD:        true,
		HasSSHDService: true,
	}, &types.Config{SSHPort: 22122, SSHDisablePass: true})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	for _, step := range steps {
		if step.Title == "Disable password auth" && strings.Contains(step.Detail, "KbdInteractiveAuthentication no") {
			return
		}
	}
	t.Fatalf("expected password hardening step to disable keyboard-interactive authentication: %#v", steps)
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
	origJailPath := fail2banManagedJailPath
	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPath)
		fail2banManagedJailPath = origJailPath
	})

	tempBin := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "ssh-f2b-sync.log")
	fail2banManagedJailPath = filepath.Join(t.TempDir(), "jail.d", "99-sys-bootstrap.local")
	if err := os.MkdirAll(filepath.Dir(fail2banManagedJailPath), 0o755); err != nil {
		t.Fatalf("create managed jail directory: %v", err)
	}
	if err := os.WriteFile(fail2banManagedJailPath, []byte("[sshd]\nenabled = true\nbackend = systemd\nport = 2222\n"), 0o644); err != nil {
		t.Fatalf("write managed jail: %v", err)
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

	content, err := os.ReadFile(fail2banManagedJailPath)
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

type sshRunTestEnvironment struct {
	configPath     string
	dropInPath     string
	originalConfig string
}

func newSSHRunTestEnvironment(t *testing.T) *sshRunTestEnvironment {
	t.Helper()
	origPath := os.Getenv("PATH")
	origConfigPath := sshConfigPath
	origDropIn := managedSSHDropIn
	origRuntimeDir := sshdRuntimeDir
	origFail2banJailPath := fail2banManagedJailPath
	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPath)
		sshConfigPath = origConfigPath
		managedSSHDropIn = origDropIn
		sshdRuntimeDir = origRuntimeDir
		fail2banManagedJailPath = origFail2banJailPath
	})

	root := t.TempDir()
	sshDir := filepath.Join(root, "etc", "ssh")
	dropInDir := filepath.Join(sshDir, "sshd_config.d")
	if err := os.MkdirAll(dropInDir, 0o755); err != nil {
		t.Fatalf("create SSH config directories: %v", err)
	}
	sshConfigPath = filepath.Join(sshDir, "sshd_config")
	managedSSHDropIn = filepath.Join(dropInDir, "00-sys-bootstrap.conf")
	sshdRuntimeDir = filepath.Join(root, "run", "sshd")
	fail2banManagedJailPath = filepath.Join(root, "etc", "fail2ban", "jail.d", "99-sys-bootstrap.local")
	originalConfig := "Include " + dropInDir + "/*.conf\nPort 22\n"
	if err := os.WriteFile(sshConfigPath, []byte(originalConfig), 0o644); err != nil {
		t.Fatalf("write sshd_config: %v", err)
	}

	tempBin := t.TempDir()
	writeFakeCommand(t, tempBin, "sshd", `#!/bin/sh
case "$1" in
  -t)
    exit 0
    ;;
  -T)
    for file in "$SYSBOOTSTRAP_TEST_DROPIN" "$SYSBOOTSTRAP_TEST_SSHD_CONFIG"; do
      [ -f "$file" ] || continue
      awk 'BEGIN { IGNORECASE=1 } /^[[:space:]]*#/ { next } tolower($1) == "port" { print "port " $2 }' "$file"
    done | sort -u
    echo "pubkeyauthentication yes"
    echo "permitrootlogin yes"
    echo "passwordauthentication yes"
    echo "kbdinteractiveauthentication yes"
    for file in "$SYSBOOTSTRAP_TEST_DROPIN" "$SYSBOOTSTRAP_TEST_SSHD_CONFIG"; do
      [ -f "$file" ] || continue
      awk 'BEGIN { IGNORECASE=1 } /^[[:space:]]*#/ { next } tolower($1) ~ /^(permitrootlogin|passwordauthentication|kbdinteractiveauthentication)$/ { print tolower($1), tolower($2) }' "$file"
    done
    if [ "$SYSBOOTSTRAP_TEST_FORCE_KBD_INTERACTIVE" = "yes" ]; then
      echo "kbdinteractiveauthentication yes"
    fi
    if [ -n "$SYSBOOTSTRAP_TEST_FORCE_PASSWORD_LOCAL_PORT" ]; then
      case " $* " in
        *"lport=$SYSBOOTSTRAP_TEST_FORCE_PASSWORD_LOCAL_PORT"*)
          echo "passwordauthentication yes"
          echo "kbdinteractiveauthentication yes"
          ;;
      esac
    fi
    exit 0
    ;;
esac
exit 1
`)
	writeFakeCommand(t, tempBin, "ss", `#!/bin/sh
for file in "$SYSBOOTSTRAP_TEST_DROPIN" "$SYSBOOTSTRAP_TEST_SSHD_CONFIG"; do
  [ -f "$file" ] || continue
  awk 'BEGIN { IGNORECASE=1 } /^[[:space:]]*#/ { next } tolower($1) == "port" { print $2 }' "$file"
done | sort -nu | while read -r port; do
  echo "LISTEN 0 128 0.0.0.0:$port 0.0.0.0:* users:((\"sshd\",pid=100,fd=3))"
done
`)
	writeFakeCommand(t, tempBin, "systemctl", `#!/bin/sh
case "$1" in
  list-unit-files)
    echo "ssh.service enabled"
    exit 0
    ;;
  is-active)
    echo "inactive"
    exit 3
    ;;
  reload-or-restart)
	count=0
	if [ -f "$SYSBOOTSTRAP_TEST_RELOAD_COUNT" ]; then
	  count=$(awk 'NR == 1 { print $1 }' "$SYSBOOTSTRAP_TEST_RELOAD_COUNT")
	fi
	count=$((count + 1))
	echo "$count" > "$SYSBOOTSTRAP_TEST_RELOAD_COUNT"
	if [ "$SYSBOOTSTRAP_TEST_FAIL_FINAL_RELOAD" = "1" ] && [ "$count" -eq 2 ]; then
	  exit 1
	fi
    exit 0
    ;;
esac
exit 0
`)
	writeFakeCommand(t, tempBin, "dpkg", "#!/bin/sh\nexit 1\n")
	t.Setenv("SYSBOOTSTRAP_TEST_DROPIN", managedSSHDropIn)
	t.Setenv("SYSBOOTSTRAP_TEST_SSHD_CONFIG", sshConfigPath)
	t.Setenv("SYSBOOTSTRAP_TEST_RELOAD_COUNT", filepath.Join(root, "reload-count"))
	t.Setenv("PATH", tempBin+":"+origPath)

	return &sshRunTestEnvironment{
		configPath:     sshConfigPath,
		dropInPath:     managedSSHDropIn,
		originalConfig: originalConfig,
	}
}

func (e *sshRunTestEnvironment) run(t *testing.T) error {
	t.Helper()
	m := NewSSHModule()
	m.SetCheckpoint(func(context.Context, []types.AccessPath) (bool, error) { return true, nil })
	sys := &system.Context{CurrentUser: &user.User{Username: "sys-bootstrap-test-missing-user"}}
	return m.Run(context.Background(), sys, &types.Config{SSHPort: 22122}, newQuietLogger(t))
}

func newSSHRunTestAccessPath(t *testing.T) *system.Context {
	t.Helper()
	const username = "sys-bootstrap-test-user"
	const publicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq"
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.Mkdir(sshDir, 0o700); err != nil {
		t.Fatalf("create test .ssh directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "authorized_keys"), []byte(publicKey+"\n"), 0o600); err != nil {
		t.Fatalf("write test authorized_keys: %v", err)
	}

	testUser := &user.User{
		Username: username,
		Uid:      strconv.Itoa(os.Getuid()),
		Gid:      strconv.Itoa(os.Getgid()),
		HomeDir:  home,
	}
	origLookupUser := lookupUser
	lookupUser = func(string) (*user.User, error) { return testUser, nil }
	t.Cleanup(func() { lookupUser = origLookupUser })

	pathEntries := filepath.SplitList(os.Getenv("PATH"))
	if len(pathEntries) == 0 {
		t.Fatal("test PATH is empty")
	}
	writeFakeCommand(t, pathEntries[0], "ssh-keygen", "#!/bin/sh\necho '256 SHA256:test fingerprint (ED25519)'\n")
	return &system.Context{CurrentUser: testUser}
}

func TestSSHRunReplacesExplicitLegacyPort(t *testing.T) {
	env := newSSHRunTestEnvironment(t)
	if err := env.run(t); err != nil {
		t.Fatalf("SSH hardening should replace an explicit legacy port: %v", err)
	}
}

func TestSSHRunRejectsOutOfRangeRequestedPortBeforeMutation(t *testing.T) {
	for _, port := range []int{-1, 65536} {
		t.Run(strconv.Itoa(port), func(t *testing.T) {
			env := newSSHRunTestEnvironment(t)
			err := NewSSHModule().Run(context.Background(), &system.Context{
				CurrentUser: &user.User{Username: "sys-bootstrap-test-missing-user"},
			}, &types.Config{SSHPort: port}, newQuietLogger(t))
			if err == nil || !strings.Contains(err.Error(), "between 1 and 65535") {
				t.Fatalf("Run error = %v, want requested-port range guidance", err)
			}
			if _, statErr := os.Stat(env.dropInPath); !os.IsNotExist(statErr) {
				t.Fatalf("managed drop-in created before port validation: %v", statErr)
			}
			content, readErr := os.ReadFile(env.configPath)
			if readErr != nil {
				t.Fatalf("read sshd_config: %v", readErr)
			}
			if string(content) != env.originalConfig {
				t.Fatalf("sshd_config changed before port validation:\n%s", content)
			}
		})
	}
}

func TestSSHRunRequiresSSBeforeManagedConfigMutation(t *testing.T) {
	tests := []struct {
		name            string
		existingDropIn  string
		wantDropInExist bool
	}{
		{name: "new managed config remains absent"},
		{name: "existing managed config remains unchanged", existingDropIn: "Port 22022\n", wantDropInExist: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newSSHRunTestEnvironment(t)
			if tt.wantDropInExist {
				if err := os.WriteFile(env.dropInPath, []byte(tt.existingDropIn), 0o640); err != nil {
					t.Fatalf("write existing managed SSH drop-in: %v", err)
				}
			}
			origCommandExists := sshCommandExistsFn
			sshCommandExistsFn = func(name string) bool {
				if name == "ss" {
					return false
				}
				return origCommandExists(name)
			}
			t.Cleanup(func() { sshCommandExistsFn = origCommandExists })

			err := env.run(t)
			if err == nil {
				t.Fatal("expected missing ss prerequisite to reject SSH hardening")
			}
			if !strings.Contains(err.Error(), "required command ss") || !strings.Contains(err.Error(), "install iproute2") {
				t.Fatalf("missing ss error = %q, want required-command and iproute2 guidance", err)
			}

			content, readErr := os.ReadFile(env.dropInPath)
			if !tt.wantDropInExist {
				if !os.IsNotExist(readErr) {
					t.Fatalf("managed drop-in was created before prerequisite rejection: %v", readErr)
				}
				return
			}
			if readErr != nil {
				t.Fatalf("read existing managed SSH drop-in: %v", readErr)
			}
			if string(content) != tt.existingDropIn {
				t.Fatalf("managed drop-in changed before prerequisite rejection:\n%s", content)
			}
		})
	}
}

func TestSSHRunDisablesPasswordAndKeyboardInteractiveAuthentication(t *testing.T) {
	env := newSSHRunTestEnvironment(t)
	m := NewSSHModule()
	m.SetCheckpoint(func(context.Context, []types.AccessPath) (bool, error) { return true, nil })
	sys := newSSHRunTestAccessPath(t)
	if err := m.Run(context.Background(), sys, &types.Config{
		SSHPort:        22122,
		SSHDisablePass: true,
	}, newQuietLogger(t)); err != nil {
		t.Fatalf("disable SSH password authentication: %v", err)
	}

	content, err := os.ReadFile(env.dropInPath)
	if err != nil {
		t.Fatalf("read finalized managed SSH drop-in: %v", err)
	}
	for _, directive := range []string{"PasswordAuthentication no", "KbdInteractiveAuthentication no"} {
		if !strings.Contains(string(content), directive) {
			t.Errorf("finalized SSH config is missing %q:\n%s", directive, content)
		}
	}
}

func TestSSHRunRejectsEffectiveKeyboardInteractiveAuthentication(t *testing.T) {
	newSSHRunTestEnvironment(t)
	t.Setenv("SYSBOOTSTRAP_TEST_FORCE_KBD_INTERACTIVE", "yes")
	m := NewSSHModule()
	m.SetCheckpoint(func(context.Context, []types.AccessPath) (bool, error) { return true, nil })
	err := m.Run(context.Background(), newSSHRunTestAccessPath(t), &types.Config{
		SSHPort:        22122,
		SSHDisablePass: true,
	}, newQuietLogger(t))
	if err == nil {
		t.Fatal("expected effective keyboard-interactive authentication to fail final verification")
	}
	if !strings.Contains(err.Error(), "KbdInteractiveAuthentication") {
		t.Fatalf("unexpected final verification error: %v", err)
	}
}

func TestSSHRunRejectsPasswordAuthenticationMatchedByFinalPort(t *testing.T) {
	newSSHRunTestEnvironment(t)
	t.Setenv("SYSBOOTSTRAP_TEST_FORCE_PASSWORD_LOCAL_PORT", "22122")
	m := NewSSHModule()
	m.SetCheckpoint(func(context.Context, []types.AccessPath) (bool, error) { return true, nil })
	err := m.Run(context.Background(), newSSHRunTestAccessPath(t), &types.Config{
		SSHPort:        22122,
		SSHDisablePass: true,
	}, newQuietLogger(t))
	if err == nil {
		t.Fatal("expected Match LocalPort password authentication to fail final verification")
	}
	if !strings.Contains(err.Error(), "PasswordAuthentication") {
		t.Fatalf("unexpected final verification error: %v", err)
	}
}

func TestSSHRunPreservesManagedHardeningOnRerun(t *testing.T) {
	env := newSSHRunTestEnvironment(t)
	const existingHardening = "Port 22122\nPermitRootLogin no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\n"
	if err := os.WriteFile(env.dropInPath, []byte(existingHardening), 0o644); err != nil {
		t.Fatalf("write existing managed SSH drop-in: %v", err)
	}

	var preparedConfig string
	m := NewSSHModule()
	m.SetCheckpoint(func(context.Context, []types.AccessPath) (bool, error) {
		content, err := os.ReadFile(env.dropInPath)
		if err != nil {
			return false, err
		}
		preparedConfig = string(content)
		return true, nil
	})
	sys := &system.Context{CurrentUser: &user.User{Username: "sys-bootstrap-test-missing-user"}}
	if err := m.Run(context.Background(), sys, &types.Config{SSHPort: 22122}, newQuietLogger(t)); err != nil {
		t.Fatalf("rerun existing SSH hardening: %v", err)
	}

	for _, directive := range []string{"PermitRootLogin no", "PasswordAuthentication no", "KbdInteractiveAuthentication no"} {
		if !strings.Contains(preparedConfig, directive) {
			t.Errorf("prepare phase removed existing %q directive:\n%s", directive, preparedConfig)
		}
	}
	finalizedConfig, err := os.ReadFile(env.dropInPath)
	if err != nil {
		t.Fatalf("read finalized managed SSH drop-in: %v", err)
	}
	for _, directive := range []string{"PermitRootLogin no", "PasswordAuthentication no", "KbdInteractiveAuthentication no"} {
		if !strings.Contains(string(finalizedConfig), directive) {
			t.Errorf("finalize phase removed existing %q directive:\n%s", directive, finalizedConfig)
		}
	}
}

func TestSSHRunRestoresExplicitLegacyPortWhenFinalReloadFails(t *testing.T) {
	env := newSSHRunTestEnvironment(t)
	t.Setenv("SYSBOOTSTRAP_TEST_FAIL_FINAL_RELOAD", "1")
	if err := env.run(t); err == nil {
		t.Fatal("SSH hardening should report a failed final reload")
	}

	content, err := os.ReadFile(env.configPath)
	if err != nil {
		t.Fatalf("read restored sshd_config: %v", err)
	}
	if string(content) != env.originalConfig {
		t.Fatalf("sshd_config was not restored after rollback:\n%s", content)
	}
	if _, err := os.Stat(env.dropInPath); !os.IsNotExist(err) {
		t.Fatalf("managed drop-in should be removed after rollback: %v", err)
	}
}

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

func TestWriteManagedDropIn(t *testing.T) {
	tmpDir := t.TempDir()
	origDropIn := managedSSHDropIn
	managedSSHDropIn = filepath.Join(tmpDir, "00-sys-bootstrap.conf")
	t.Cleanup(func() { managedSSHDropIn = origDropIn })

	err := writeManagedDropIn(22122, "no", "no", "no")
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
	if !strings.Contains(content, "KbdInteractiveAuthentication no") {
		t.Errorf("missing KbdInteractiveAuthentication: %s", content)
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

func TestParseSSHListeningPortsRecognizesSocketButNotSystemdResolved(t *testing.T) {
	output := `LISTEN 0 4096 0.0.0.0:22333 0.0.0.0:* users:(("systemd",pid=1,fd=51))
LISTEN 0 4096 127.0.0.53:53 0.0.0.0:* users:(("systemd-resolved",pid=35,fd=14))`
	ports := parseSSHListeningPorts(output)
	if !ports[22333] {
		t.Fatalf("socket-activated SSH port was not detected: %v", ports)
	}
	if ports[53] {
		t.Fatalf("systemd-resolved listener must not satisfy SSH verification: %v", ports)
	}
}

func TestSSHListeningPortsMatchExactly(t *testing.T) {
	if !sshListeningPortsMatchExactly(map[int]bool{22122: true}, map[int]bool{22122: true}) {
		t.Fatal("identical port sets should match")
	}
	if sshListeningPortsMatchExactly(map[int]bool{22: true, 22122: true}, map[int]bool{22122: true}) {
		t.Fatal("legacy SSH listener must prevent an exact match")
	}
}

func TestVerifyOnlyEffectivePortsRejectsAdditionalListener(t *testing.T) {
	orig := effectiveSSHPortsFunc
	effectiveSSHPortsFunc = func(context.Context) ([]int, error) { return []int{22, 22333}, nil }
	t.Cleanup(func() { effectiveSSHPortsFunc = orig })

	if err := verifyOnlyEffectivePorts(context.Background(), []int{22333}); err == nil {
		t.Fatal("expected an unmanaged effective SSH port to be rejected")
	}
}

func TestReloadSSHSocketActivationUsesDaemonReloadAndSocketRestart(t *testing.T) {
	origPath := os.Getenv("PATH")
	t.Cleanup(func() { _ = os.Setenv("PATH", origPath) })
	tempBin := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "systemctl.log")
	writeFakeCommand(t, tempBin, "systemctl", `#!/bin/sh
echo "$*" >> "$SYSBOOTSTRAP_TEST_LOG"
exit 0
`)
	t.Setenv("SYSBOOTSTRAP_TEST_LOG", logFile)
	t.Setenv("PATH", tempBin+":"+origPath)

	if err := reloadSSH(sshReloadTarget{unit: "ssh.socket", socketActivated: true}); err != nil {
		t.Fatalf("reloadSSH failed: %v", err)
	}
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read systemctl log: %v", err)
	}
	if got := string(content); !strings.Contains(got, "daemon-reload") || !strings.Contains(got, "restart ssh.socket") {
		t.Fatalf("socket activation reload sequence was incomplete:\n%s", got)
	}
}

func TestEnsureSSHDRunDirCreatesMissingRuntimeDirectory(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/run/sshd is a Linux runtime convention")
	}
	orig := sshdRuntimeDir
	sshdRuntimeDir = filepath.Join(t.TempDir(), "sshd")
	t.Cleanup(func() { sshdRuntimeDir = orig })

	if err := ensureSSHDRunDir(); err != nil {
		t.Fatalf("ensureSSHDRunDir failed: %v", err)
	}
	info, err := os.Stat(sshdRuntimeDir)
	if err != nil || !info.IsDir() {
		t.Fatalf("runtime directory was not created: info=%v err=%v", info, err)
	}
}
