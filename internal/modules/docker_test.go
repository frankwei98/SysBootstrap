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
	if err := os.WriteFile(repoPath, []byte("deb [arch=amd64 signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian bookworm stable\n"), 0o644); err != nil {
		t.Fatalf("write repo: %v", err)
	}

	origPaths := dockerRepoPathsFn
	dockerRepoPathsFn = func(_ *system.Context) (string, string) { return keyPath, repoPath }
	t.Cleanup(func() { dockerRepoPathsFn = origPaths })

	if !dockerRepoConfigured(&system.Context{}) {
		t.Fatal("expected Docker repo to be detected as configured")
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

func TestDockerRunInstallsAndConfiguresGroup(t *testing.T) {
	origPath := os.Getenv("PATH")
	origInstalled := dockerInstalledFn
	origCompose := dockerComposePluginInstalledFn
	origService := dockerServiceEnabledFn
	origGroup := dockerGroupSatisfiedWithConfigFn
	origRepo := dockerRepoConfiguredFn
	origEnsureRepo := ensureDockerRepoFn
	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPath)
		dockerInstalledFn = origInstalled
		dockerComposePluginInstalledFn = origCompose
		dockerServiceEnabledFn = origService
		dockerGroupSatisfiedWithConfigFn = origGroup
		dockerRepoConfiguredFn = origRepo
		ensureDockerRepoFn = origEnsureRepo
	})

	tempBin := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "docker-run.log")
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
		"apt-get update -y",
		"apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin",
		"systemctl enable --now docker",
		"usermod -aG docker deploy",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("docker run log missing %q:\n%s", want, text)
		}
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
