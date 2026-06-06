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

const nvmVersion = "v0.40.4"

type NodeModule struct{}

func NewNodeModule() *NodeModule { return &NodeModule{} }

func (m *NodeModule) ID() string             { return "node" }
func (m *NodeModule) Name() string           { return "Node.js Environment" }
func (m *NodeModule) Description() string    { return "nvm, Node.js LTS, pnpm, and bun" }
func (m *NodeModule) DefaultEnabled() bool   { return false }
func (m *NodeModule) RequiresRoot() bool     { return false }
func (m *NodeModule) Dependencies() []string { return nil }

func (m *NodeModule) Check(ctx context.Context, sys *system.Context) CheckResult {
	nvmScript := filepath.Join(system.NvmDirForContext(sys), "nvm.sh")

	msg := ""
	allInstalled := true
	if _, err := os.Stat(nvmScript); err == nil {
		msg += "nvm installed. "
	} else {
		allInstalled = false
		msg += "nvm missing. "
	}
	if system.NvmCommandExistsForContext(sys, "node") {
		msg += "Node.js installed. "
	} else {
		allInstalled = false
		msg += "Node.js missing. "
	}
	if system.NvmCommandExistsForContext(sys, "pnpm") {
		msg += "pnpm installed. "
	} else {
		allInstalled = false
		msg += "pnpm missing. "
	}
	if system.NvmCommandExistsForContext(sys, "bun") {
		msg += "bun installed. "
	} else {
		allInstalled = false
		msg += "bun missing. "
	}
	if nodeShellPathConfigured(sys) {
		msg += "shell startup configured. "
	} else {
		allInstalled = false
		msg += "shell startup missing. "
	}
	if allInstalled {
		return CheckResult{Satisfied: true, Message: msg}
	}
	return CheckResult{Satisfied: false, Message: msg}
}

func (m *NodeModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	steps := []types.Step{
		{Module: "node", Title: "Install nvm", Detail: fmt.Sprintf("nvm %s", nvmVersion)},
		{Module: "node", Title: "Install Node.js LTS", Detail: "via nvm install --lts"},
		{Module: "node", Title: "Install pnpm", Detail: "via official installer"},
		{Module: "node", Title: "Install bun", Detail: "via official installer"},
	}
	return steps, nil
}

func (m *NodeModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	nvmScript := filepath.Join(system.NvmDirForContext(sys), "nvm.sh")

	// Install nvm
	if _, err := os.Stat(nvmScript); err == nil {
		log.Info("nvm already installed, skipping")
	} else {
		log.Infof("Installing nvm %s...", nvmVersion)
		url := fmt.Sprintf("https://raw.githubusercontent.com/nvm-sh/nvm/%s/install.sh", nvmVersion)
		cmd := fmt.Sprintf("curl -o- %s | bash", url)
		if res, err := system.RunAsUserWithInput(sys, "", "bash", "-c", cmd); err != nil || res.ExitCode != 0 {
			return fmt.Errorf("nvm installation failed")
		}
		log.Success("nvm installed")
	}

	if _, err := os.Stat(nvmScript); err != nil {
		return fmt.Errorf("nvm.sh not found at %s after installation", nvmScript)
	}
	if err := ensureNodeShellPath(sys); err != nil {
		return err
	}

	// Install Node.js LTS (must go through nvm-aware shell)
	if system.NvmCommandExistsForContext(sys, "node") {
		log.Info("Node.js already installed, skipping")
	} else {
		log.Info("Installing Node.js LTS...")
		script := "nvm install --lts && nvm use --lts && nvm alias default lts/*"
		if res, err := system.RunInNvmShellForContext(sys, script); err != nil || res.ExitCode != 0 {
			return fmt.Errorf("Node.js installation failed")
		}
		log.Success("Node.js LTS installed")
	}

	// Install pnpm
	if system.NvmCommandExistsForContext(sys, "pnpm") {
		log.Info("pnpm already installed, skipping")
	} else {
		log.Info("Installing pnpm...")
		script := `corepack enable
corepack prepare pnpm@latest --activate`
		if res, err := system.RunInNvmShellForContext(sys, script); err != nil || res.ExitCode != 0 {
			return fmt.Errorf("pnpm installation failed")
		}
		if !system.NvmCommandExistsForContext(sys, "pnpm") {
			return fmt.Errorf("pnpm installation completed but pnpm is still not available on PATH")
		}
		log.Success("pnpm installed")
	}

	// Install bun
	if system.NvmCommandExistsForContext(sys, "bun") {
		log.Info("bun already installed, skipping")
	} else {
		log.Info("Installing bun...")
		if res, err := system.RunAsUserWithInput(sys, "", "bash", "-c", "curl -fsSL https://bun.sh/install | bash"); err != nil || res.ExitCode != 0 {
			return fmt.Errorf("bun installation failed")
		}
		if !system.NvmCommandExistsForContext(sys, "bun") {
			return fmt.Errorf("bun installation completed but bun is still not available on PATH")
		}
		log.Success("bun installed")
	}

	return nil
}

func nodeShellPathConfigured(sys *system.Context) bool {
	rcFiles := nodeShellRCFiles(sys)
	if len(rcFiles) == 0 {
		return false
	}
	for _, rcFile := range rcFiles {
		content, err := os.ReadFile(rcFile)
		if err != nil {
			return false
		}
		if !nodeShellFileConfigured(string(content)) {
			return false
		}
	}
	return true
}

func nodeShellFileConfigured(content string) bool {
	return strings.Contains(content, "SYS_BOOTSTRAP_NODE_ENV") ||
		(strings.Contains(content, "nvm.sh") && strings.Contains(content, "BUN_INSTALL"))
}

func ensureNodeShellPath(sys *system.Context) error {
	home := system.TargetHomeDir(sys)
	if home == "" {
		return fmt.Errorf("cannot determine target home directory for node shell setup")
	}

	script := fmt.Sprintf(`set -e
export HOME=%q
marker="# SYS_BOOTSTRAP_NODE_ENV"
for rc in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.profile"; do
  touch "$rc"
  if ! grep -qF "$marker" "$rc"; then
    cat >> "$rc" <<'EOF'

# SYS_BOOTSTRAP_NODE_ENV
export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"
[ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"
export BUN_INSTALL="${BUN_INSTALL:-$HOME/.bun}"
export PATH="$BUN_INSTALL/bin:$PATH"
EOF
  fi
done`, home)
	res, err := system.RunAsUserWithInput(sys, "", "bash", "-c", script)
	if err != nil {
		return fmt.Errorf("failed to update shell startup files for node: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("failed to update shell startup files for node: %s", res.Stderr)
	}
	return nil
}

func nodeShellRCFiles(sys *system.Context) []string {
	home := system.TargetHomeDir(sys)
	if home == "" {
		return nil
	}
	return []string{
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".profile"),
	}
}
