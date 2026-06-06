package modules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
