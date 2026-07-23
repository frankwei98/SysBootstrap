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

// ZellijModule installs the terminal multiplexer as a separate module, after
// base has supplied the system tools it needs. Its own failure is non-fatal.
type ZellijModule struct{}

func NewZellijModule() *ZellijModule { return &ZellijModule{} }

func (m *ZellijModule) ID() string             { return "zellij" }
func (m *ZellijModule) Name() string           { return "Zellij" }
func (m *ZellijModule) Description() string    { return "Terminal multiplexer via GitHub release binary" }
func (m *ZellijModule) DefaultEnabled() bool   { return true }
func (m *ZellijModule) RequiresRoot() bool     { return true }
func (m *ZellijModule) Dependencies() []string { return []string{"base"} }

func (m *ZellijModule) Check(ctx context.Context, sys *system.Context) CheckResult {
	if system.CommandExists("zellij") {
		return CheckResult{Satisfied: true, Message: "zellij installed"}
	}
	return CheckResult{Satisfied: false, Message: "zellij missing"}
}

func (m *ZellijModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	if system.CommandExists("zellij") {
		return nil, nil
	}
	return []types.Step{{
		Module: "zellij",
		Title:  "Install zellij",
		Detail: "Terminal multiplexer via GitHub release binary",
	}}, nil
}

func (m *ZellijModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	if system.CommandExists("zellij") {
		log.Info("zellij already installed, skipping")
		return nil
	}

	log.Info("Installing zellij...")
	if err := installZellij(ctx); err != nil {
		return err
	}
	log.Success("zellij installed")
	return nil
}

func installZellij(ctx context.Context) error {
	// Determine architecture from runtime.
	runtimeArch := system.RunQuietOutput("uname", "-m")
	goarch := "amd64"
	switch {
	case strings.Contains(runtimeArch, "aarch64"), strings.Contains(runtimeArch, "arm64"):
		goarch = "arm64"
	}

	assetName := zellijAssetForArch(goarch)
	expectedSHA256 := zellijSHA256ForArch(goarch)
	if assetName == "" || expectedSHA256 == "" {
		return fmt.Errorf("unsupported architecture for zellij: %s", runtimeArch)
	}

	url := fmt.Sprintf("https://github.com/zellij-org/zellij/releases/download/%s/%s", zellijVersion, assetName)
	tmpdir, err := os.MkdirTemp("", "zellij-*")
	if err != nil {
		return fmt.Errorf("cannot create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpdir)

	archivePath := filepath.Join(tmpdir, "zellij.tar.gz")
	curlArgs := []string{"-fsSL", "--connect-timeout", "10", "--max-time", "60", "--retry", "2", "-o", archivePath, url}
	if res, err := system.RunWithContext(ctx, "curl", curlArgs...); err != nil || res.ExitCode != 0 {
		detail := ""
		if res != nil {
			detail = res.Stderr
		}
		return fmt.Errorf("zellij download failed: %s", detail)
	}

	if err := verifyFileSHA256(archivePath, expectedSHA256); err != nil {
		return fmt.Errorf("zellij checksum verification failed: %w", err)
	}

	extractDir := filepath.Join(tmpdir, "extract")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return fmt.Errorf("cannot create extract directory: %w", err)
	}

	script := fmt.Sprintf(`set -euo pipefail
tar -xzf %s -C %s
install -m 0755 %s/zellij /usr/local/bin/zellij
`, shellQuote(archivePath), shellQuote(extractDir), shellQuote(extractDir))
	if res, err := system.RunWithContext(ctx, "bash", "-c", script); err != nil || res == nil || res.ExitCode != 0 {
		return system.FormatCommandError("zellij extraction failed", res, err)
	}

	if !system.CommandExists("zellij") {
		return fmt.Errorf("zellij installation completed but zellij is still not available on PATH")
	}
	return nil
}
