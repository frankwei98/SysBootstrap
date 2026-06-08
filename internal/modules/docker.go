package modules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

type DockerModule struct{}

var dockerRepoPathsFn = dockerRepoPaths
var dockerInstalledFn = func() bool { return system.CommandExists("docker") }
var dockerComposePluginInstalledFn = dockerComposePluginInstalled
var dockerServiceEnabledFn = dockerServiceEnabled
var dockerGroupSatisfiedWithConfigFn = dockerGroupSatisfiedWithConfig
var dockerRepoConfiguredFn = dockerRepoConfigured
var ensureDockerRepoFn = ensureDockerRepo
var dockerRunWithContextFn = system.RunWithContext

func NewDockerModule() *DockerModule { return &DockerModule{} }

func (m *DockerModule) ID() string             { return "docker" }
func (m *DockerModule) Name() string           { return "Docker Environment" }
func (m *DockerModule) Description() string    { return "Docker Engine, CLI, and Compose plugin" }
func (m *DockerModule) DefaultEnabled() bool   { return false }
func (m *DockerModule) RequiresRoot() bool     { return true }
func (m *DockerModule) Dependencies() []string { return []string{"base"} }

func (m *DockerModule) Check(ctx context.Context, sys *system.Context) CheckResult {
	hasDocker := dockerInstalledFn()
	hasCompose := dockerComposePluginInstalledFn()
	serviceEnabled := dockerServiceEnabledFn()
	groupReady, targetUser := dockerGroupSatisfied(sys)

	if hasDocker && hasCompose && serviceEnabled && groupReady {
		return CheckResult{Satisfied: true, Message: buildDockerCheckMessage(hasDocker, hasCompose, serviceEnabled, targetUser, true)}
	}
	return CheckResult{Satisfied: false, Message: buildDockerCheckMessage(hasDocker, hasCompose, serviceEnabled, targetUser, groupReady)}
}

func (m *DockerModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	hasDocker := dockerInstalledFn()
	hasCompose := dockerComposePluginInstalledFn()
	serviceEnabled := dockerServiceEnabledFn()
	groupReady, targetUser := dockerGroupSatisfiedWithConfigFn(sys, cfg)
	repoReady := dockerRepoConfiguredFn(sys)
	needsRepo := dockerNeedsRepo(hasDocker, hasCompose, repoReady)

	var steps []types.Step
	if needsRepo {
		steps = append(steps, types.Step{Module: "docker", Title: "Configure Docker apt repository", Detail: "Docker official GPG key and apt source"})
	}
	if !hasDocker {
		steps = append(steps, types.Step{Module: "docker", Title: "Install Docker packages", Detail: "docker-ce, docker-ce-cli, containerd.io, docker-buildx-plugin, docker-compose-plugin"})
	}
	if hasDocker && !hasCompose {
		steps = append(steps, types.Step{Module: "docker", Title: "Install Compose plugin", Detail: "docker-compose-plugin"})
	}
	if !serviceEnabled {
		steps = append(steps, types.Step{Module: "docker", Title: "Enable docker service", Detail: "systemctl enable --now docker"})
	}
	if targetUser != "" && !groupReady {
		steps = append(steps, types.Step{Module: "docker", Title: "Add user to docker group", Detail: targetUser})
		steps = append(steps, types.Step{Module: "docker", Title: "Refresh shell access", Detail: "Re-login or run newgrp docker", Risk: "manual-step"})
	}
	return steps, nil
}

func (m *DockerModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	hasDocker := dockerInstalledFn()
	hasCompose := dockerComposePluginInstalledFn()
	repoReady := dockerRepoConfiguredFn(sys)

	if dockerNeedsRepo(hasDocker, hasCompose, repoReady) {
		log.Info("Configuring Docker official apt repository...")
		if err := ensureDockerRepoFn(ctx, sys); err != nil {
			return err
		}
		log.Success("Docker apt repository configured")
	}

	if !hasDocker {
		log.Info("Installing Docker packages...")
		if res, err := dockerRunWithContextFn(ctx, "apt-get", "update", "-y"); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("apt-get update before Docker install failed", res, err)
		}
		if res, err := dockerRunWithContextFn(ctx, "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y",
			"docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin"); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("Docker package installation failed", res, err)
		}
		log.Success("Docker packages installed")
	} else if !hasCompose {
		log.Info("Installing Docker Compose plugin...")
		if res, err := dockerRunWithContextFn(ctx, "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "docker-compose-plugin"); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("Docker Compose plugin installation failed", res, err)
		}
		log.Success("Docker Compose plugin installed")
	} else {
		log.Info("Docker and Compose plugin already installed, skipping package installation")
	}

	if !dockerServiceEnabledFn() {
		log.Info("Enabling Docker service...")
		if res, err := system.Run("systemctl", "enable", "--now", "docker"); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("failed to enable Docker service", res, err)
		}
		log.Success("Docker service enabled")
	} else {
		log.Info("Docker service already enabled, skipping")
	}

	groupReady, targetUser := dockerGroupSatisfiedWithConfigFn(sys, cfg)
	if targetUser != "" && !groupReady {
		log.Infof("Adding %s to docker group...", targetUser)
		if res, err := system.Run("usermod", "-aG", "docker", targetUser); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError(fmt.Sprintf("failed to add %s to docker group", targetUser), res, err)
		}
		log.Successf("%s added to docker group", targetUser)
		log.Warnf("Re-login or run 'newgrp docker' for %s to use Docker without sudo.", targetUser)
	}

	return nil
}

func dockerComposePluginInstalled() bool {
	res, err := system.Run("docker", "compose", "version")
	return err == nil && res.ExitCode == 0
}

func dockerRepoConfigured(sys *system.Context) bool {
	keyPath, repoPath := dockerRepoPathsFn(sys)
	if _, err := os.Stat(keyPath); err != nil {
		return false
	}
	content, err := os.ReadFile(repoPath)
	if err != nil {
		return false
	}
	return strings.Contains(string(content), "download.docker.com/linux/")
}

func dockerServiceEnabled() bool {
	if !system.CommandExists("systemctl") {
		return false
	}
	enabledRes, err := system.Run("systemctl", "is-enabled", "docker")
	if err != nil || enabledRes.ExitCode != 0 || strings.TrimSpace(enabledRes.Stdout) != "enabled" {
		return false
	}
	activeRes, err := system.Run("systemctl", "is-active", "docker")
	return err == nil && activeRes.ExitCode == 0 && strings.TrimSpace(activeRes.Stdout) == "active"
}

func dockerGroupSatisfied(sys *system.Context) (bool, string) {
	return dockerGroupSatisfiedWithConfigFn(sys, nil)
}

func dockerNeedsRepo(hasDocker, hasCompose, repoReady bool) bool {
	if repoReady {
		return false
	}
	return !hasDocker || !hasCompose
}

func dockerGroupSatisfiedWithConfig(sys *system.Context, cfg *types.Config) (bool, string) {
	targetUser := ""
	if cfg != nil && cfg.DockerUser != "" {
		targetUser = cfg.DockerUser
	} else {
		targetUser = system.TargetUsername(sys)
	}
	if targetUser == "" {
		return true, ""
	}
	res, err := system.Run("id", "-nG", targetUser)
	if err != nil || res.ExitCode != 0 {
		return false, targetUser
	}
	groups := strings.Fields(strings.TrimSpace(res.Stdout))
	for _, group := range groups {
		if group == "docker" {
			return true, targetUser
		}
	}
	return false, targetUser
}

func buildDockerCheckMessage(hasDocker, hasCompose, serviceEnabled bool, targetUser string, groupReady bool) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("docker %s", boolWord(hasDocker, "installed", "missing")))
	parts = append(parts, fmt.Sprintf("compose plugin %s", boolWord(hasCompose, "installed", "missing")))
	parts = append(parts, fmt.Sprintf("docker service %s", boolWord(serviceEnabled, "ready", "not ready")))
	if targetUser != "" {
		parts = append(parts, fmt.Sprintf("docker group for %s %s", targetUser, boolWord(groupReady, "ready", "missing")))
	}
	return strings.Join(parts, ". ")
}

func ensureDockerRepo(ctx context.Context, sys *system.Context) error {
	keyPath, repoPath := dockerRepoPathsFn(sys)
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o755); err != nil {
		return fmt.Errorf("failed to create Docker keyring directory: %w", err)
	}
	if !dockerRepoConfigured(sys) {
		if res, err := dockerRunWithContextFn(ctx, "apt-get", "update", "-y"); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("apt-get update before Docker repo setup failed", res, err)
		}
		if res, err := dockerRunWithContextFn(ctx, "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "ca-certificates", "curl"); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("failed to install Docker repo prerequisites", res, err)
		}
		script := fmt.Sprintf(`set -e
install -m 0755 -d %s
curl -fsSL https://download.docker.com/linux/%s/gpg -o /tmp/sys-bootstrap-docker.gpg
install -m 0644 /tmp/sys-bootstrap-docker.gpg %s
rm -f /tmp/sys-bootstrap-docker.gpg
cat > %s <<'EOF'
deb [arch=%s signed-by=%s] https://download.docker.com/linux/%s %s stable
EOF
`, shellQuote(filepath.Dir(keyPath)), dockerRepoOS(sys), shellQuote(keyPath), shellQuote(repoPath), dockerRepoArch(sys), keyPath, dockerRepoOS(sys), dockerRepoCodename(sys))
		if res, err := dockerRunWithContextFn(ctx, "bash", "-lc", script); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("failed to configure Docker apt repository", res, err)
		}
	}
	return nil
}

func dockerRepoPaths(sys *system.Context) (keyPath, repoPath string) {
	return "/etc/apt/keyrings/docker.asc", "/etc/apt/sources.list.d/docker.list"
}

func dockerRepoOS(sys *system.Context) string {
	if sys != nil && sys.OSID == "ubuntu" {
		return "ubuntu"
	}
	return "debian"
}

func dockerRepoCodename(sys *system.Context) string {
	if sys != nil && sys.OSCodename != "" {
		return sys.OSCodename
	}
	return "bookworm"
}

func dockerRepoArch(sys *system.Context) string {
	if sys == nil {
		return "amd64"
	}
	switch sys.Arch {
	case "linux/arm64":
		return "arm64"
	default:
		return "amd64"
	}
}
