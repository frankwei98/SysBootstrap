package modules

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

func TestDockerModuleInterface(t *testing.T) {
	m := NewDockerModule()
	if m.ID() != "docker" {
		t.Fatalf("ID() = %q, want docker", m.ID())
	}
	if !m.RequiresRoot() {
		t.Fatal("docker module should require root")
	}
	if len(m.Dependencies()) != 1 || m.Dependencies()[0] != "base" {
		t.Fatalf("Dependencies() = %v, want [base]", m.Dependencies())
	}
}

func TestBuildDockerCheckMessage(t *testing.T) {
	msg := buildDockerCheckMessage(true, false, true, "frank", false)
	for _, part := range []string{"docker installed", "compose plugin missing", "docker service ready", "docker group for frank missing"} {
		if !strings.Contains(msg, part) {
			t.Fatalf("message %q missing %q", msg, part)
		}
	}
}

func TestDockerPlanUsesConfiguredUser(t *testing.T) {
	m := NewDockerModule()
	sys := &system.Context{
		CurrentUser: &user.User{Username: "root", HomeDir: "/root"},
		Arch:        "linux/amd64",
	}
	cfg := &types.Config{DockerUser: "deploy"}
	steps, err := m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	found := false
	for _, step := range steps {
		if step.Title == "Add user to docker group" && step.Detail == "deploy" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected docker plan to mention configured target user, got %#v", steps)
	}
}

func TestDockerRepoConfiguredDetectsOfficialRepo(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "docker.asc")
	repoPath := filepath.Join(tmpDir, "docker.list")

	if err := os.WriteFile(keyPath, []byte("key"), 0o644); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := os.WriteFile(repoPath, []byte("deb [arch=amd64 signed-by="+keyPath+"] https://download.docker.com/linux/debian bookworm stable\n"), 0o644); err != nil {
		t.Fatalf("write repo: %v", err)
	}

	origPaths := dockerRepoPathsFn
	dockerRepoPathsFn = func(_ *system.Context) (string, string) { return keyPath, repoPath }
	t.Cleanup(func() { dockerRepoPathsFn = origPaths })

	if !dockerRepoConfigured(&system.Context{OSID: "debian", OSCodename: "bookworm", Arch: "linux/amd64"}) {
		t.Fatal("expected Docker repo to be detected as configured")
	}
}

func TestDockerRepoConfiguredRejectsInactiveOrMismatchedEntries(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "docker.asc")
	repoPath := filepath.Join(tmpDir, "docker.list")
	origPaths := dockerRepoPathsFn
	dockerRepoPathsFn = func(_ *system.Context) (string, string) { return keyPath, repoPath }
	t.Cleanup(func() { dockerRepoPathsFn = origPaths })
	sys := &system.Context{OSID: "debian", OSCodename: "bookworm", Arch: "linux/amd64"}

	tests := []struct {
		name string
		key  string
		repo string
	}{
		{name: "empty key", repo: "deb [arch=amd64 signed-by=" + keyPath + "] https://download.docker.com/linux/debian bookworm stable\n"},
		{name: "commented source", key: "key", repo: "# deb [arch=amd64 signed-by=" + keyPath + "] https://download.docker.com/linux/debian bookworm stable\n"},
		{name: "wrong distribution", key: "key", repo: "deb [arch=amd64 signed-by=" + keyPath + "] https://download.docker.com/linux/ubuntu bookworm stable\n"},
		{name: "wrong codename", key: "key", repo: "deb [arch=amd64 signed-by=" + keyPath + "] https://download.docker.com/linux/debian trixie stable\n"},
		{name: "wrong key path", key: "key", repo: "deb [arch=amd64 signed-by=/tmp/other.asc] https://download.docker.com/linux/debian bookworm stable\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(keyPath, []byte(tt.key), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(repoPath, []byte(tt.repo), 0o644); err != nil {
				t.Fatal(err)
			}
			if dockerRepoConfigured(sys) {
				t.Fatalf("dockerRepoConfigured() = true for %s", tt.name)
			}
		})
	}
}

func TestDockerPlanNoStepsWhenAlreadySatisfied(t *testing.T) {
	origPaths := dockerRepoPathsFn
	origInstalled := dockerInstalledFn
	origCompose := dockerComposePluginInstalledFn
	origService := dockerServiceEnabledFn
	origGroup := dockerGroupSatisfiedWithConfigFn
	origRepo := dockerRepoConfiguredFn
	t.Cleanup(func() {
		dockerRepoPathsFn = origPaths
		dockerInstalledFn = origInstalled
		dockerComposePluginInstalledFn = origCompose
		dockerServiceEnabledFn = origService
		dockerGroupSatisfiedWithConfigFn = origGroup
		dockerRepoConfiguredFn = origRepo
	})

	dockerInstalledFn = func() bool { return true }
	dockerComposePluginInstalledFn = func() bool { return true }
	dockerServiceEnabledFn = func() bool { return true }
	dockerGroupSatisfiedWithConfigFn = func(_ *system.Context, cfg *types.Config) (bool, string) {
		if cfg != nil && cfg.DockerUser != "" {
			return true, cfg.DockerUser
		}
		return true, "deploy"
	}
	dockerRepoConfiguredFn = func(_ *system.Context) bool { return true }

	m := NewDockerModule()
	sys := &system.Context{
		CurrentUser: &user.User{Username: "root", HomeDir: "/root"},
	}
	cfg := &types.Config{DockerUser: "deploy"}

	steps, err := m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(steps) != 0 {
		t.Fatalf("steps len = %d, want 0 when docker state is fully satisfied: %#v", len(steps), steps)
	}
}

func TestDockerPlanSkipsRepoSetupWhenDockerAlreadyPresent(t *testing.T) {
	origInstalled := dockerInstalledFn
	origCompose := dockerComposePluginInstalledFn
	origService := dockerServiceEnabledFn
	origGroup := dockerGroupSatisfiedWithConfigFn
	origRepo := dockerRepoConfiguredFn
	t.Cleanup(func() {
		dockerInstalledFn = origInstalled
		dockerComposePluginInstalledFn = origCompose
		dockerServiceEnabledFn = origService
		dockerGroupSatisfiedWithConfigFn = origGroup
		dockerRepoConfiguredFn = origRepo
	})

	dockerInstalledFn = func() bool { return true }
	dockerComposePluginInstalledFn = func() bool { return true }
	dockerServiceEnabledFn = func() bool { return true }
	dockerGroupSatisfiedWithConfigFn = func(_ *system.Context, cfg *types.Config) (bool, string) {
		return true, "deploy"
	}
	dockerRepoConfiguredFn = func(_ *system.Context) bool { return false }

	m := NewDockerModule()
	steps, err := m.Plan(context.Background(), &system.Context{}, &types.Config{DockerUser: "deploy"})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(steps) != 0 {
		t.Fatalf("steps len = %d, want 0 when docker runtime is already present and repo is only missing: %#v", len(steps), steps)
	}
}

func TestDockerPlanRejectsUnsupportedRepoArchitectures(t *testing.T) {
	origInstalled := dockerInstalledFn
	origCompose := dockerComposePluginInstalledFn
	origService := dockerServiceEnabledFn
	origGroup := dockerGroupSatisfiedWithConfigFn
	origRepo := dockerRepoConfiguredFn
	t.Cleanup(func() {
		dockerInstalledFn = origInstalled
		dockerComposePluginInstalledFn = origCompose
		dockerServiceEnabledFn = origService
		dockerGroupSatisfiedWithConfigFn = origGroup
		dockerRepoConfiguredFn = origRepo
	})

	dockerInstalledFn = func() bool { return false }
	dockerComposePluginInstalledFn = func() bool { return false }
	dockerServiceEnabledFn = func() bool { return true }
	dockerGroupSatisfiedWithConfigFn = func(_ *system.Context, _ *types.Config) (bool, string) {
		return true, ""
	}
	dockerRepoConfiguredFn = func(_ *system.Context) bool { return false }

	for _, arch := range []string{"linux/ppc64le", "linux/riscv64", "linux/s390x"} {
		t.Run(arch, func(t *testing.T) {
			_, err := NewDockerModule().Plan(context.Background(), &system.Context{Arch: arch}, &types.Config{})
			if err == nil || !strings.Contains(err.Error(), arch) {
				t.Fatalf("Plan error = %v, want unsupported architecture %s", err, arch)
			}
		})
	}
}

func TestDockerRunInstallsAndConfiguresGroup(t *testing.T) {
	origPath := os.Getenv("PATH")
	origInstalled := dockerInstalledFn
	origCompose := dockerComposePluginInstalledFn
	origService := dockerServiceEnabledFn
	origGroup := dockerGroupSatisfiedWithConfigFn
	origRepo := dockerRepoConfiguredFn
	origEnsureRepo := ensureDockerRepoFn
	origRunWithContext := dockerRunWithContextFn
	origLookupUser := dockerLookupUserFn
	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPath)
		dockerInstalledFn = origInstalled
		dockerComposePluginInstalledFn = origCompose
		dockerServiceEnabledFn = origService
		dockerGroupSatisfiedWithConfigFn = origGroup
		dockerRepoConfiguredFn = origRepo
		ensureDockerRepoFn = origEnsureRepo
		dockerRunWithContextFn = origRunWithContext
		dockerLookupUserFn = origLookupUser
	})

	tempBin := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "docker-run.log")
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
exit 0
`)
	writeFakeCommand(t, tempBin, "usermod", `#!/bin/sh
echo "usermod $*" >> "$SYSBOOTSTRAP_TEST_LOG"
exit 0
`)
	t.Setenv("SYSBOOTSTRAP_TEST_LOG", logFile)
	t.Setenv("PATH", tempBin+":"+origPath)

	dockerInstalledFn = func() bool { return false }
	dockerComposePluginInstalledFn = func() bool { return false }
	dockerServiceEnabledFn = func() bool { return false }
	dockerGroupSatisfiedWithConfigFn = func(_ *system.Context, cfg *types.Config) (bool, string) {
		return false, cfg.DockerUser
	}
	dockerRepoConfiguredFn = func(_ *system.Context) bool { return false }
	dockerLookupUserFn = func(username string) (*user.User, error) {
		return &user.User{Username: username}, nil
	}
	ensureDockerRepoFn = func(context.Context, *system.Context) error {
		return os.WriteFile(logFile, []byte("ensureDockerRepo\n"), 0o644)
	}

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New failed: %v", err)
	}
	defer log.Close()

	m := NewDockerModule()
	cfg := &types.Config{DockerUser: "deploy"}
	if err := m.Run(context.Background(), &system.Context{}, cfg, log); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file failed: %v", err)
	}
	text := string(content)
	for _, want := range []string{
		"ensureDockerRepo",
		"DPkg::Lock::Timeout=120",
		"Acquire::Retries=3",
		"update -y",
		"install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin",
		"systemctl enable --now docker",
		"usermod -aG docker deploy",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("docker run log missing %q:\n%s", want, text)
		}
	}
}

func TestDockerRunPreflightsTargetUserBeforeSystemMutation(t *testing.T) {
	origInstalled := dockerInstalledFn
	origCompose := dockerComposePluginInstalledFn
	origRepo := dockerRepoConfiguredFn
	origEnsureRepo := ensureDockerRepoFn
	origLookupUser := dockerLookupUserFn
	t.Cleanup(func() {
		dockerInstalledFn = origInstalled
		dockerComposePluginInstalledFn = origCompose
		dockerRepoConfiguredFn = origRepo
		ensureDockerRepoFn = origEnsureRepo
		dockerLookupUserFn = origLookupUser
	})

	dockerInstalledFn = func() bool { return false }
	dockerComposePluginInstalledFn = func() bool { return false }
	dockerRepoConfiguredFn = func(_ *system.Context) bool { return false }
	dockerLookupUserFn = func(username string) (*user.User, error) {
		return nil, user.UnknownUserError(username)
	}

	tests := []struct {
		name     string
		username string
	}{
		{name: "empty", username: ""},
		{name: "invalid format", username: "Bad User"},
		{name: "unknown account", username: "sysbootstrapmissinguser"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mutationCount := 0
			ensureDockerRepoFn = func(context.Context, *system.Context) error {
				mutationCount++
				return errors.New("mutation sentinel")
			}

			log, err := logging.New(true)
			if err != nil {
				t.Fatalf("logging.New failed: %v", err)
			}
			defer log.Close()

			err = NewDockerModule().Run(
				context.Background(),
				&system.Context{},
				&types.Config{DockerUser: tt.username},
				log,
			)
			if err == nil {
				t.Fatal("Run succeeded for an invalid Docker target user")
			}
			if mutationCount != 0 {
				t.Fatalf("Docker system mutation ran %d time(s) before target-user validation", mutationCount)
			}
		})
	}
}

func TestEnsureDockerRepoWritesValidSourceEntry(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "docker.asc")
	repoPath := filepath.Join(tmpDir, "docker.list")
	origPaths := dockerRepoPathsFn
	origRunWithContext := dockerRunWithContextFn
	origRunApt := dockerRunAptFn
	t.Cleanup(func() {
		dockerRepoPathsFn = origPaths
		dockerRunWithContextFn = origRunWithContext
		dockerRunAptFn = origRunApt
	})

	dockerRepoPathsFn = func(_ *system.Context) (string, string) { return keyPath, repoPath }
	dockerRunAptFn = func(_ context.Context, _ ...string) (*system.Result, error) {
		return &system.Result{ExitCode: 0}, nil
	}
	dockerRunWithContextFn = func(_ context.Context, name string, args ...string) (*system.Result, error) {
		switch {
		case name == "bash" && len(args) >= 2 && args[0] == "-lc":
			script := args[1]
			if err := os.MkdirAll(filepath.Dir(keyPath), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(keyPath, []byte("dummy"), 0o644); err != nil {
				return nil, err
			}
			start := strings.Index(script, "cat > ")
			if start == -1 {
				t.Fatalf("script missing repo heredoc:\n%s", script)
			}
			if strings.Contains(script, "signed-by='") || strings.Contains(script, "signed-by=\"") {
				t.Fatalf("script should not quote signed-by value:\n%s", script)
			}
			if !strings.Contains(script, "mktemp") || strings.Contains(script, "/tmp/sys-bootstrap-docker.gpg") {
				t.Fatalf("script must use a private temporary GPG key path:\n%s", script)
			}
			repoLine := "deb [arch=arm64 signed-by=" + keyPath + "] https://download.docker.com/linux/debian trixie stable\n"
			if err := os.WriteFile(repoPath, []byte(repoLine), 0o644); err != nil {
				return nil, err
			}
			return &system.Result{ExitCode: 0}, nil
		default:
			t.Fatalf("unexpected command: %s %v", name, args)
			return nil, nil
		}
	}

	sys := &system.Context{OSID: "debian", OSCodename: "trixie", Arch: "linux/arm64"}
	if err := ensureDockerRepo(context.Background(), sys); err != nil {
		t.Fatalf("ensureDockerRepo failed: %v", err)
	}

	content, err := os.ReadFile(repoPath)
	if err != nil {
		t.Fatalf("read repo file: %v", err)
	}
	got := string(content)
	want := "deb [arch=arm64 signed-by=" + keyPath + "] https://download.docker.com/linux/debian trixie stable\n"
	if got != want {
		t.Fatalf("repo content = %q, want %q", got, want)
	}
}

func TestEnsureDockerRepoRejectsUnsupportedArchitectureBeforeMutation(t *testing.T) {
	tmpDir := t.TempDir()
	repoRoot := filepath.Join(tmpDir, "apt")
	keyPath := filepath.Join(repoRoot, "keyrings", "docker.asc")
	repoPath := filepath.Join(repoRoot, "sources.list.d", "docker.list")
	origPaths := dockerRepoPathsFn
	origRunWithContext := dockerRunWithContextFn
	origRunApt := dockerRunAptFn
	t.Cleanup(func() {
		dockerRepoPathsFn = origPaths
		dockerRunWithContextFn = origRunWithContext
		dockerRunAptFn = origRunApt
	})

	dockerRepoPathsFn = func(_ *system.Context) (string, string) { return keyPath, repoPath }
	aptCalled := false
	dockerRunAptFn = func(_ context.Context, _ ...string) (*system.Result, error) {
		aptCalled = true
		return &system.Result{ExitCode: 0}, nil
	}
	scriptCalled := false
	dockerRunWithContextFn = func(_ context.Context, _ string, _ ...string) (*system.Result, error) {
		scriptCalled = true
		return &system.Result{ExitCode: 0}, nil
	}

	sys := &system.Context{OSID: "debian", OSCodename: "bookworm", Arch: "linux/ppc64le"}
	err := ensureDockerRepo(context.Background(), sys)
	if err == nil || !strings.Contains(err.Error(), "linux/ppc64le") {
		t.Errorf("error = %v, want unsupported architecture error", err)
	}
	if aptCalled {
		t.Error("apt ran before Docker repository architecture validation")
	}
	if scriptCalled {
		t.Error("repository setup script ran before architecture validation")
	}
	if _, statErr := os.Stat(repoRoot); !os.IsNotExist(statErr) {
		t.Errorf("repository path was created before architecture validation; stat error = %v", statErr)
	}
}

func TestDockerRepoInfoRejectsUnknownOrIncompleteDistribution(t *testing.T) {
	if _, _, err := dockerRepoInfo(&system.Context{OSID: "linuxmint", OSCodename: "wilma"}); err == nil {
		t.Fatal("expected unknown Docker repository distribution to be rejected")
	}
	if _, _, err := dockerRepoInfo(&system.Context{OSID: "ubuntu"}); err == nil {
		t.Fatal("expected missing Docker repository codename to be rejected")
	}
}

func writeFakeCommand(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake command %s: %v", name, err)
	}
}

func TestDockerServiceEnabledRequiresActiveState(t *testing.T) {
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

	if dockerServiceEnabled() {
		t.Fatal("expected docker service to be not ready when active state is not active")
	}
}
