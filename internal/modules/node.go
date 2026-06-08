package modules

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
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
		downloadURL := fmt.Sprintf("https://raw.githubusercontent.com/nvm-sh/nvm/%s/install.sh", nvmVersion)
		tmpFile, err := os.CreateTemp("", "sys-bootstrap-nvm-*.sh")
		if err != nil {
			return fmt.Errorf("failed to create temp file for nvm install script: %w", err)
		}
		tmpPath := tmpFile.Name()
		tmpFile.Close()
		defer os.Remove(tmpPath)

		if res, err := system.Run("curl", "-fsSL", "-o", tmpPath, downloadURL); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("failed to download nvm install script", res, err)
		}
		if err := verifyFileSHA256(tmpPath, nvmInstallSHA256); err != nil {
			return fmt.Errorf("nvm install script verification failed: %w", err)
		}
		if err := os.Chmod(tmpPath, 0o644); err != nil {
			return fmt.Errorf("failed to make nvm install script readable: %w", err)
		}
		cmd := fmt.Sprintf("bash %s", shellQuote(tmpPath))
		if res, err := system.RunAsUserWithInput(sys, "", "bash", "-c", cmd); err != nil || res.ExitCode != 0 {
			return system.FormatCommandError("nvm installation failed", res, err)
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
			return system.FormatCommandError("Node.js installation failed", res, err)
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
			return system.FormatCommandError("pnpm installation failed", res, err)
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
		if err := installBun(sys, log); err != nil {
			return fmt.Errorf("bun installation failed: %w", err)
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
export HOME=%s
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
done`, shellQuote(home))
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

// installBun downloads the bun binary from GitHub releases, verifies its
// checksum, and extracts it to ~/.bun/bin.
func installBun(sys *system.Context, log *logging.Logger) error {
	home := system.TargetHomeDir(sys)
	if home == "" {
		return fmt.Errorf("cannot determine target home directory for bun")
	}

	arch := ""
	if sys != nil {
		arch = sys.Arch
	}
	if arch == "" {
		arch = runtime.GOOS + "/" + runtime.GOARCH
	}
	normalizedArch, err := normalizeBunArch(arch)
	if err != nil {
		return err
	}

	assetName, checksum, err := bunAssetForInstall(normalizedArch)
	if err != nil {
		return err
	}

	downloadURL := fmt.Sprintf("https://github.com/oven-sh/bun/releases/download/bun-%s/%s", bunVersion, assetName)
	tmpDir, err := os.MkdirTemp("", "sys-bootstrap-bun-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	zipPath := filepath.Join(tmpDir, assetName)
	log.Infof("Downloading bun %s...", bunVersion)
	if res, err := system.Run("curl", "-fsSL", "-o", zipPath, downloadURL); err != nil || res.ExitCode != 0 {
		// If x64 download fails, try baseline as fallback
		if normalizedArch == "amd64" && assetName != "bun-linux-x64-baseline.zip" {
			log.Warn("x64 bun download failed, trying baseline variant...")
			assetName = "bun-linux-x64-baseline.zip"
			checksum = bunLinuxX64BaselineSHA256
			downloadURL = fmt.Sprintf("https://github.com/oven-sh/bun/releases/download/bun-%s/%s", bunVersion, assetName)
			zipPath = filepath.Join(tmpDir, assetName)
			if res2, err2 := system.Run("curl", "-fsSL", "-o", zipPath, downloadURL); err2 != nil || res2.ExitCode != 0 {
				return fmt.Errorf("failed to download bun from GitHub releases")
			}
		} else {
			return fmt.Errorf("failed to download bun from GitHub releases")
		}
	}

	// Verify checksum
	if err := verifyFileSHA256(zipPath, checksum); err != nil {
		return fmt.Errorf("bun checksum verification failed: %w", err)
	}
	log.Info("bun checksum verified")

	// Extract zip using Go's native archive/zip (no unzip dependency)
	bunDir := filepath.Join(home, ".bun")
	bunBinDir := filepath.Join(home, ".bun", "bin")
	if err := rejectSymlinkPaths(bunDir, bunBinDir); err != nil {
		return err
	}
	if err := os.MkdirAll(bunBinDir, 0o755); err != nil {
		return fmt.Errorf("failed to create bun bin directory: %w", err)
	}

	destPath := filepath.Join(bunBinDir, "bun")
	if err := extractBunFromZip(zipPath, destPath); err != nil {
		return fmt.Errorf("failed to extract bun binary: %w", err)
	}
	if err := verifyBunBinary(sys, destPath); err != nil {
		if normalizedArch == "amd64" && assetName != "bun-linux-x64-baseline.zip" {
			log.Warnf("bun failed to run after install, trying baseline variant: %v", err)
			assetName = "bun-linux-x64-baseline.zip"
			checksum = bunLinuxX64BaselineSHA256
			downloadURL = fmt.Sprintf("https://github.com/oven-sh/bun/releases/download/bun-%s/%s", bunVersion, assetName)
			zipPath = filepath.Join(tmpDir, assetName)
			if res, err := system.Run("curl", "-fsSL", "-o", zipPath, downloadURL); err != nil || res.ExitCode != 0 {
				return fmt.Errorf("failed to download bun baseline from GitHub releases")
			}
			if err := verifyFileSHA256(zipPath, checksum); err != nil {
				return fmt.Errorf("bun baseline checksum verification failed: %w", err)
			}
			if err := extractBunFromZip(zipPath, destPath); err != nil {
				return fmt.Errorf("failed to extract bun baseline binary: %w", err)
			}
			if err := verifyBunBinary(sys, destPath); err != nil {
				return fmt.Errorf("bun baseline verification failed: %w", err)
			}
		} else {
			return fmt.Errorf("bun verification failed: %w", err)
		}
	}

	// Chown to target user when running as root
	if os.Geteuid() == 0 {
		username := system.TargetUsername(sys)
		if username != "" && username != "root" {
			targetUser := system.TargetUser(sys)
			owner := username
			if targetUser != nil && targetUser.Uid != "" {
				owner = targetUser.Uid
				if targetUser.Gid != "" {
					owner = fmt.Sprintf("%s:%s", targetUser.Uid, targetUser.Gid)
				}
			}
			if res, err := system.Run("chown", "-R", owner, bunDir); err != nil || res.ExitCode != 0 {
				return fmt.Errorf("failed to chown bun directory: %s", res.Stderr)
			}
		}
	}

	return nil
}

func verifyBunBinary(sys *system.Context, path string) error {
	res, err := system.RunAsUserWithInput(sys, "", path, "--version")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		output := strings.TrimSpace(res.Stderr)
		if output == "" {
			output = strings.TrimSpace(res.Stdout)
		}
		if output == "" {
			output = fmt.Sprintf("exited with code %d", res.ExitCode)
		}
		return fmt.Errorf("%s", output)
	}
	return nil
}

// extractBunFromZip extracts the bun binary from a zip archive to destPath.
func extractBunFromZip(zipPath, destPath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("cannot open zip: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		// Look for a file named "bun" (not a directory)
		if f.FileInfo().IsDir() || filepath.Base(f.Name) != "bun" {
			continue
		}

		if err := extractZipEntry(f, destPath); err != nil {
			return err
		}
		return nil
	}

	return fmt.Errorf("bun binary not found in zip archive")
}

// extractZipEntry extracts a single zip file entry to destPath.
func extractZipEntry(f *zip.File, destPath string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("cannot read entry %s in zip: %w", f.Name, err)
	}
	defer rc.Close()

	outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("cannot create destination file: %w", err)
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, rc); err != nil {
		return fmt.Errorf("cannot write %s: %w", f.Name, err)
	}
	return nil
}

// bunAssetForArch returns the asset filename and expected SHA256 for the given architecture.
func bunAssetForArch(arch string) (string, string, error) {
	arch, err := normalizeBunArch(arch)
	if err != nil {
		return "", "", err
	}
	return bunAssetForNormalizedArch(arch, false)
}

func bunAssetForInstall(arch string) (string, string, error) {
	arch, err := normalizeBunArch(arch)
	if err != nil {
		return "", "", err
	}
	return bunAssetForNormalizedArch(arch, arch == "amd64" && !cpuHasAVX2())
}

func bunAssetForNormalizedArch(arch string, baseline bool) (string, string, error) {
	switch arch {
	case "arm64":
		return "bun-linux-aarch64.zip", bunLinuxAarch64SHA256, nil
	case "amd64":
		if baseline {
			return "bun-linux-x64-baseline.zip", bunLinuxX64BaselineSHA256, nil
		}
		return "bun-linux-x64.zip", bunLinuxX64SHA256, nil
	default:
		return "", "", fmt.Errorf("unsupported architecture for bun: %s", arch)
	}
}

func normalizeBunArch(arch string) (string, error) {
	if strings.HasPrefix(arch, "linux/") {
		arch = strings.TrimPrefix(arch, "linux/")
	} else if strings.Contains(arch, "/") {
		return "", fmt.Errorf("unsupported platform for bun: %s", arch)
	}

	switch arch {
	case "amd64", "x86_64":
		return "amd64", nil
	case "arm64", "aarch64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported architecture for bun: %s", arch)
	}
}

func cpuHasAVX2() bool {
	content, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return true
	}
	return cpuInfoHasAVX2(string(content))
}

func cpuInfoHasAVX2(content string) bool {
	for _, field := range strings.Fields(content) {
		if field == "avx2" {
			return true
		}
	}
	return false
}

func rejectSymlinkPaths(paths ...string) error {
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to modify %s: it is a symlink", path)
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to inspect %s: %w", path, err)
		}
	}
	return nil
}
