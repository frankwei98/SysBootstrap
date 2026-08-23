package modules

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

func TestSSHPlanRejectsAddressDependentPasswordPolicy(t *testing.T) {
	env := newSSHAddressPlanEnvironment(t)
	nestedDir := filepath.Join(env.sshDir, "policy")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("create nested policy directory: %v", err)
	}
	policyPath := filepath.Join(nestedDir, "password.conf")
	if err := os.WriteFile(policyPath, []byte("Match User deploy\n  PasswordAuthentication yes\n"), 0o644); err != nil {
		t.Fatalf("write address policy: %v", err)
	}
	addressMatchPath := filepath.Join(env.dropInDir, "10-address-policy.conf")
	if err := os.WriteFile(addressMatchPath, []byte("Match Address 192.0.2.0/24\n  Include "+policyPath+"\n"), 0o644); err != nil {
		t.Fatalf("write address Match policy: %v", err)
	}

	_, err := env.plan(&types.Config{SSHPort: 22122, SSHDisablePass: true})
	if err == nil {
		t.Fatal("expected address-dependent password policy to be rejected")
	}
	for _, want := range []string{"Match Address", "PasswordAuthentication", policyPath} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Plan error = %q, want %q", err, want)
		}
	}
}

func TestSSHPlanRejectsLocalAddressDependentPasswordPolicy(t *testing.T) {
	env := newSSHAddressPlanEnvironment(t)
	policyPath := filepath.Join(env.dropInDir, "10-local-address-policy.conf")
	if err := os.WriteFile(
		policyPath,
		[]byte("Match LocalAddress 203.0.113.10\n  PasswordAuthentication yes\n"),
		0o644,
	); err != nil {
		t.Fatalf("write local-address policy: %v", err)
	}

	_, err := env.plan(&types.Config{SSHPort: 22122, SSHDisablePass: true})
	if err == nil {
		t.Fatal("expected local-address-dependent password policy to be rejected")
	}
	for _, want := range []string{"Match LocalAddress", "PasswordAuthentication", policyPath} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Plan error = %q, want %q", err, want)
		}
	}
}

func TestSSHPlanRejectsUnverifiableMatchPasswordPolicy(t *testing.T) {
	for _, match := range []struct {
		criterion string
		pattern   string
	}{
		{criterion: "User", pattern: "deploy"},
		{criterion: "Group", pattern: "operators"},
		{criterion: "Host", pattern: "bastion.example"},
		{criterion: "Version", pattern: "OpenSSH_9.*"},
		{criterion: "RDomain", pattern: "0"},
	} {
		t.Run(match.criterion, func(t *testing.T) {
			env := newSSHAddressPlanEnvironment(t)
			policyPath := filepath.Join(env.dropInDir, "10-identity-policy.conf")
			content := "Match " + match.criterion + " " + match.pattern + "\n  PasswordAuthentication yes\n"
			if err := os.WriteFile(policyPath, []byte(content), 0o644); err != nil {
				t.Fatalf("write %s policy: %v", match.criterion, err)
			}

			_, err := env.plan(&types.Config{SSHPort: 22122, SSHDisablePass: true})
			if err == nil {
				t.Fatalf("expected Match %s password policy to be rejected", match.criterion)
			}
			for _, want := range []string{"Match " + match.criterion, "PasswordAuthentication", policyPath} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Plan error = %q, want %q", err, want)
				}
			}
		})
	}
}

func TestSSHPlanRejectsAddressPolicyUsingEqualsSeparators(t *testing.T) {
	env := newSSHAddressPlanEnvironment(t)
	policyPath := filepath.Join(env.dropInDir, "10-address-policy.conf")
	if err := os.WriteFile(
		policyPath,
		[]byte("Match=Address 192.0.2.0/24\n  PasswordAuthentication=yes\n"),
		0o644,
	); err != nil {
		t.Fatalf("write equals-separated address policy: %v", err)
	}
	if err := os.WriteFile(sshConfigPath, []byte("Include="+policyPath+"\nPort 22122\n"), 0o644); err != nil {
		t.Fatalf("write equals-separated Include: %v", err)
	}

	_, err := env.plan(&types.Config{SSHPort: 22122, SSHDisablePass: true})
	if err == nil {
		t.Fatal("expected equals-separated address policy to be rejected")
	}
	for _, want := range []string{"Match Address", "PasswordAuthentication", policyPath} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Plan error = %q, want %q", err, want)
		}
	}
}

func TestSSHPlanRestoresMatchStateBetweenIncludedFiles(t *testing.T) {
	env := newSSHAddressPlanEnvironment(t)
	if err := os.WriteFile(
		filepath.Join(env.dropInDir, "10-address-only.conf"),
		[]byte("Match Address 192.0.2.0/24\n  X11Forwarding no\n"),
		0o644,
	); err != nil {
		t.Fatalf("write address-only policy: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(env.dropInDir, "20-global-auth.conf"),
		[]byte("PasswordAuthentication no\n"),
		0o644,
	); err != nil {
		t.Fatalf("write global authentication policy: %v", err)
	}

	if _, err := env.plan(&types.Config{SSHPort: 22122, SSHDisablePass: true}); err != nil {
		t.Fatalf("sibling Include inherited another file's Match state: %v", err)
	}
}

func TestSSHPlanIgnoresHiddenFilesExcludedByOpenSSHGlob(t *testing.T) {
	env := newSSHAddressPlanEnvironment(t)
	policyPath := filepath.Join(env.dropInDir, ".hidden-policy.conf")
	if err := os.WriteFile(
		policyPath,
		[]byte("Match Address 192.0.2.0/24\n  PasswordAuthentication yes\n"),
		0o644,
	); err != nil {
		t.Fatalf("write hidden address policy: %v", err)
	}

	if _, err := env.plan(&types.Config{SSHPort: 22122, SSHDisablePass: true}); err != nil {
		t.Fatalf("OpenSSH wildcard should not include a leading-dot policy file: %v", err)
	}
}

func TestSSHPlanInspectsExplicitlyIncludedHiddenFiles(t *testing.T) {
	env := newSSHAddressPlanEnvironment(t)
	policyPath := filepath.Join(env.dropInDir, ".hidden-policy.conf")
	if err := os.WriteFile(
		policyPath,
		[]byte("Match Address 192.0.2.0/24\n  PasswordAuthentication yes\n"),
		0o644,
	); err != nil {
		t.Fatalf("write hidden address policy: %v", err)
	}
	if err := os.WriteFile(sshConfigPath, []byte("Include "+policyPath+"\nPort 22122\n"), 0o644); err != nil {
		t.Fatalf("write explicit hidden-file Include: %v", err)
	}

	_, err := env.plan(&types.Config{SSHPort: 22122, SSHDisablePass: true})
	if err == nil {
		t.Fatal("expected explicitly included hidden policy to be inspected")
	}
	if !strings.Contains(err.Error(), policyPath) {
		t.Fatalf("Plan error = %q, want hidden policy path", err)
	}
}

func TestSSHPlanRejectsAddressDependentChallengeResponseAlias(t *testing.T) {
	env := newSSHAddressPlanEnvironment(t)
	policyPath := filepath.Join(env.dropInDir, "10-address-policy.conf")
	if err := os.WriteFile(
		policyPath,
		[]byte("Match Address 192.0.2.0/24\n  ChallengeResponseAuthentication yes\n"),
		0o644,
	); err != nil {
		t.Fatalf("write challenge-response policy: %v", err)
	}

	_, err := env.plan(&types.Config{SSHPort: 22122, SSHDisablePass: true})
	if err == nil {
		t.Fatal("expected address-dependent challenge-response policy to be rejected")
	}
	if !strings.Contains(err.Error(), "ChallengeResponseAuthentication") {
		t.Fatalf("Plan error = %q, want deprecated keyboard-interactive alias", err)
	}
}

func TestSSHPlanRejectsAddressDependentSKeyAlias(t *testing.T) {
	env := newSSHAddressPlanEnvironment(t)
	policyPath := filepath.Join(env.dropInDir, "10-address-policy.conf")
	if err := os.WriteFile(
		policyPath,
		[]byte("Match Address 192.0.2.0/24\n  SKeyAuthentication yes\n"),
		0o644,
	); err != nil {
		t.Fatalf("write S/Key policy: %v", err)
	}

	_, err := env.plan(&types.Config{SSHPort: 22122, SSHDisablePass: true})
	if err == nil {
		t.Fatal("expected address-dependent S/Key policy to be rejected")
	}
	if !strings.Contains(err.Error(), "SKeyAuthentication") {
		t.Fatalf("Plan error = %q, want S/Key keyboard-interactive alias", err)
	}
}

func TestSSHPlanRejectsAddressPolicyWithInvalidUserCriterion(t *testing.T) {
	for _, matchLine := range []string{
		"Match Address 192.0.2.0/24 Invalid-User",
		"Match Invalid-User Address 192.0.2.0/24",
	} {
		t.Run(matchLine, func(t *testing.T) {
			env := newSSHAddressPlanEnvironment(t)
			policyPath := filepath.Join(env.dropInDir, "10-address-policy.conf")
			content := matchLine + "\n  PasswordAuthentication yes\n"
			if err := os.WriteFile(policyPath, []byte(content), 0o644); err != nil {
				t.Fatalf("write invalid-user address policy: %v", err)
			}

			_, err := env.plan(&types.Config{SSHPort: 22122, SSHDisablePass: true})
			if err == nil {
				t.Fatal("expected address-dependent policy with invalid-user criterion to be rejected")
			}
			for _, want := range []string{"Match Address", "PasswordAuthentication", policyPath} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Plan error = %q, want %q", err, want)
				}
			}
		})
	}
}

func TestSSHPlanFailsClosedWhenIncludedPolicyCannotBeRead(t *testing.T) {
	env := newSSHAddressPlanEnvironment(t)
	brokenPath := filepath.Join(env.dropInDir, "10-broken-policy.conf")
	if err := os.Symlink(filepath.Join(env.sshDir, "missing-policy.conf"), brokenPath); err != nil {
		t.Fatalf("create broken policy symlink: %v", err)
	}

	_, err := env.plan(&types.Config{SSHPort: 22122, SSHDisablePass: true})
	if err == nil {
		t.Fatal("expected unreadable included policy to fail closed")
	}
	if !strings.Contains(err.Error(), "cannot inspect") || !strings.Contains(err.Error(), brokenPath) {
		t.Fatalf("Plan error = %q, want unreadable included-policy context", err)
	}
}

func TestSSHPlanFailsClosedForTildeBasedIncludes(t *testing.T) {
	env := newSSHAddressPlanEnvironment(t)
	if err := os.WriteFile(sshConfigPath, []byte("Include ~/sshd-policy.conf\nPort 22122\n"), 0o644); err != nil {
		t.Fatalf("write tilde Include: %v", err)
	}

	_, err := env.plan(&types.Config{SSHPort: 22122, SSHDisablePass: true})
	if err == nil {
		t.Fatal("expected tilde-based Include to fail closed")
	}
	if !strings.Contains(err.Error(), "tilde") {
		t.Fatalf("Plan error = %q, want unsupported tilde Include guidance", err)
	}
}

func TestSSHPlanFailsClosedForBrokenRootConfigSymlink(t *testing.T) {
	env := newSSHAddressPlanEnvironment(t)
	if err := os.Remove(sshConfigPath); err != nil {
		t.Fatalf("remove test root config: %v", err)
	}
	if err := os.Symlink(filepath.Join(env.sshDir, "missing-root.conf"), sshConfigPath); err != nil {
		t.Fatalf("create broken root config symlink: %v", err)
	}

	_, err := env.plan(&types.Config{SSHPort: 22122, SSHDisablePass: true})
	if err == nil {
		t.Fatal("expected broken root config symlink to fail closed")
	}
	if !strings.Contains(err.Error(), "cannot inspect SSH configuration") {
		t.Fatalf("Plan error = %q, want root-config inspection context", err)
	}
}

type sshAddressPlanEnvironment struct {
	sshDir    string
	dropInDir string
}

func newSSHAddressPlanEnvironment(t *testing.T) *sshAddressPlanEnvironment {
	t.Helper()
	origRuntimeDir := sshdRuntimeDir
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
	if err := os.WriteFile(sshConfigPath, []byte("Include "+dropInDir+"/*.conf\nPort 22122\n"), 0o644); err != nil {
		t.Fatalf("write sshd_config: %v", err)
	}
	tempBin := t.TempDir()
	writeFakeCommand(t, tempBin, "sshd", "#!/bin/sh\nprintf '%s\\n' 'port 22122' 'permitrootlogin no' 'passwordauthentication no' 'kbdinteractiveauthentication no'\n")
	t.Setenv("PATH", tempBin+":"+os.Getenv("PATH"))
	sshServiceReadyFn = func() bool { return true }
	t.Cleanup(func() {
		sshdRuntimeDir = origRuntimeDir
		sshConfigPath = origConfigPath
		managedSSHDropIn = origDropIn
		sshServiceReadyFn = origService
	})
	sshdRuntimeDir = filepath.Join(t.TempDir(), "run", "sshd")
	return &sshAddressPlanEnvironment{sshDir: sshDir, dropInDir: dropInDir}
}

func (e *sshAddressPlanEnvironment) plan(cfg *types.Config) ([]types.Step, error) {
	return NewSSHModule().Plan(context.Background(), &system.Context{
		HasSSHD:        true,
		HasSSHDService: true,
	}, cfg)
}

func TestSSHRunRejectsAddressDependentPasswordPolicyBeforeManagedConfigMutation(t *testing.T) {
	env := newSSHRunTestEnvironment(t)
	policyPath := filepath.Join(filepath.Dir(env.dropInPath), "10-address-policy.conf")
	if err := os.WriteFile(policyPath, []byte("Match Address 192.0.2.0/24\n  PasswordAuthentication yes\n"), 0o644); err != nil {
		t.Fatalf("write address policy: %v", err)
	}
	m := NewSSHModule()
	m.SetCheckpoint(func(context.Context, []types.AccessPath) (bool, error) { return true, nil })

	err := m.Run(context.Background(), newSSHRunTestAccessPath(t), &types.Config{
		SSHPort:        22122,
		SSHDisablePass: true,
	}, newQuietLogger(t))
	if err == nil {
		t.Fatal("expected address-dependent password policy to be rejected")
	}
	if !strings.Contains(err.Error(), "Match Address") {
		t.Fatalf("Run error = %q, want address-policy guidance", err)
	}
	if _, statErr := os.Stat(env.dropInPath); !os.IsNotExist(statErr) {
		t.Fatalf("managed SSH config mutated before address-policy rejection: %v", statErr)
	}
}

func TestSSHRunRechecksAddressDependentPasswordPolicyBeforeFinalizing(t *testing.T) {
	env := newSSHRunTestEnvironment(t)
	policyPath := filepath.Join(filepath.Dir(env.dropInPath), "10-address-policy.conf")
	m := NewSSHModule()
	m.SetCheckpoint(func(context.Context, []types.AccessPath) (bool, error) {
		if err := os.WriteFile(policyPath, []byte("Match Address 192.0.2.0/24\n  PasswordAuthentication yes\n"), 0o644); err != nil {
			return false, err
		}
		return true, nil
	})

	err := m.Run(context.Background(), newSSHRunTestAccessPath(t), &types.Config{
		SSHPort:        22122,
		SSHDisablePass: true,
	}, newQuietLogger(t))
	if err == nil {
		t.Fatal("expected address-dependent policy added during checkpoint to be rejected")
	}
	if !strings.Contains(err.Error(), "Match Address") {
		t.Fatalf("Run error = %q, want address-policy guidance", err)
	}
	if _, statErr := os.Stat(env.dropInPath); !os.IsNotExist(statErr) {
		t.Fatalf("managed SSH config was not rolled back after final policy rejection: %v", statErr)
	}
}

func TestSSHRunRechecksLocalAddressDependentPasswordPolicyBeforeFinalizing(t *testing.T) {
	env := newSSHRunTestEnvironment(t)
	policyPath := filepath.Join(filepath.Dir(env.dropInPath), "10-local-address-policy.conf")
	m := NewSSHModule()
	m.SetCheckpoint(func(context.Context, []types.AccessPath) (bool, error) {
		if err := os.WriteFile(
			policyPath,
			[]byte("Match LocalAddress 203.0.113.10\n  PasswordAuthentication yes\n"),
			0o644,
		); err != nil {
			return false, err
		}
		return true, nil
	})

	err := m.Run(context.Background(), newSSHRunTestAccessPath(t), &types.Config{
		SSHPort:        22122,
		SSHDisablePass: true,
	}, newQuietLogger(t))
	if err == nil {
		t.Fatal("expected local-address-dependent policy added during checkpoint to be rejected")
	}
	if !strings.Contains(err.Error(), "Match LocalAddress") {
		t.Fatalf("Run error = %q, want local-address-policy guidance", err)
	}
	if _, statErr := os.Stat(env.dropInPath); !os.IsNotExist(statErr) {
		t.Fatalf("managed SSH config was not rolled back after final local-address policy rejection: %v", statErr)
	}
}
