package modules

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

const nvmVersion = "v0.40.4"

var nodeCommandExistsFn = system.CommandExists

type NodeModule struct{}

func NewNodeModule() *NodeModule { return &NodeModule{} }

func (m *NodeModule) ID() string             { return "node" }
func (m *NodeModule) Name() string           { return "Node.js Environment" }
func (m *NodeModule) Description() string    { return "nvm, Node.js LTS, pnpm, and bun" }
func (m *NodeModule) DefaultEnabled() bool   { return false }
func (m *NodeModule) RequiresRoot() bool     { return false }
func (m *NodeModule) Dependencies() []string { return nil }

func (m *NodeModule) Check(ctx context.Context, sys *system.Context, cfg *types.Config) CheckResult {
	nvmScript := filepath.Join(system.NvmDirForContext(sys), "nvm.sh")

	msg := ""
	allInstalled := true
	if _, err := os.Stat(nvmScript); err == nil {
		msg += "nvm installed. "
	} else {
		allInstalled = false
		msg += "nvm missing. "
	}
	if system.NvmCommandExistsForContextWithContext(ctx, sys, "node") {
		msg += "Node.js installed. "
	} else {
		allInstalled = false
		msg += "Node.js missing. "
	}
	if system.NvmCommandExistsForContextWithContext(ctx, sys, "pnpm") {
		msg += "pnpm installed. "
	} else {
		allInstalled = false
		msg += "pnpm missing. "
	}
	if system.NvmCommandExistsForContextWithContext(ctx, sys, "bun") {
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var steps []types.Step
	nvmScript := filepath.Join(system.NvmDirForContext(sys), "nvm.sh")

	if _, err := os.Stat(nvmScript); err != nil {
		steps = append(steps, types.Step{Module: "node", Title: "Install nvm", Detail: fmt.Sprintf("nvm %s", nvmVersion)})
	}
	if !system.NvmCommandExistsForContextWithContext(ctx, sys, "node") {
		steps = append(steps, types.Step{Module: "node", Title: "Install Node.js LTS", Detail: "via nvm install --lts"})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !system.NvmCommandExistsForContextWithContext(ctx, sys, "pnpm") {
		steps = append(steps, types.Step{Module: "node", Title: "Install pnpm", Detail: "via corepack"})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !system.NvmCommandExistsForContextWithContext(ctx, sys, "bun") {
		steps = append(steps, types.Step{Module: "node", Title: "Install bun", Detail: "via official installer"})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !nodeShellPathConfigured(sys) {
		steps = append(steps, types.Step{Module: "node", Title: "Update shell startup", Detail: "Load nvm and bun paths from rc files"})
	}
	return steps, nil
}

func (m *NodeModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, command := range []string{"bash", "curl"} {
		if !nodeCommandExistsFn(command) {
			return fmt.Errorf("required command %s is unavailable; install %s before running the node module", command, command)
		}
	}

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

		if res, err := system.RunWithContext(ctx, "curl", "-fsSL", "-o", tmpPath, downloadURL); err != nil || res.ExitCode != 0 {
			return formatCommandErrorForContext(ctx, "failed to download nvm install script", res, err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := verifyFileSHA256(tmpPath, nvmInstallSHA256); err != nil {
			return fmt.Errorf("nvm install script verification failed: %w", err)
		}
		if err := os.Chmod(tmpPath, 0o644); err != nil {
			return fmt.Errorf("failed to make nvm install script readable: %w", err)
		}
		cmd := fmt.Sprintf("bash %s", shellQuote(tmpPath))
		if res, err := system.RunAsUserWithInputContext(ctx, sys, "", "bash", "-c", cmd); err != nil || res.ExitCode != 0 {
			return formatCommandErrorForContext(ctx, "nvm installation failed", res, err)
		}
		log.Success("nvm installed")
	}

	if _, err := os.Stat(nvmScript); err != nil {
		return fmt.Errorf("nvm.sh not found at %s after installation", nvmScript)
	}
	if err := ensureNodeShellPathContext(ctx, sys); err != nil {
		return err
	}

	// Install Node.js LTS (must go through nvm-aware shell)
	if system.NvmCommandExistsForContextWithContext(ctx, sys, "node") {
		log.Info("Node.js already installed, skipping")
	} else {
		if err := ctx.Err(); err != nil {
			return err
		}
		log.Info("Installing Node.js LTS...")
		script := "nvm install --lts && nvm use --lts && nvm alias default lts/*"
		if res, err := system.RunInNvmShellForContextWithContext(ctx, sys, script); err != nil || res.ExitCode != 0 {
			return formatCommandErrorForContext(ctx, "Node.js installation failed", res, err)
		}
		log.Success("Node.js LTS installed")
	}

	// Install pnpm
	if system.NvmCommandExistsForContextWithContext(ctx, sys, "pnpm") {
		log.Info("pnpm already installed, skipping")
	} else {
		if err := ctx.Err(); err != nil {
			return err
		}
		log.Info("Installing pnpm...")
		script := `corepack enable
corepack prepare pnpm@latest --activate`
		if res, err := system.RunInNvmShellForContextWithContext(ctx, sys, script); err != nil || res.ExitCode != 0 {
			return formatCommandErrorForContext(ctx, "pnpm installation failed", res, err)
		}
		if !system.NvmCommandExistsForContextWithContext(ctx, sys, "pnpm") {
			if err := ctx.Err(); err != nil {
				return err
			}
			return fmt.Errorf("pnpm installation completed but pnpm is still not available on PATH")
		}
		log.Success("pnpm installed")
	}

	// Install bun
	if system.NvmCommandExistsForContextWithContext(ctx, sys, "bun") {
		log.Info("bun already installed, skipping")
	} else {
		if err := ctx.Err(); err != nil {
			return err
		}
		log.Info("Installing bun...")
		if err := installBunContext(ctx, sys, log); err != nil {
			return fmt.Errorf("bun installation failed: %w", err)
		}
		if !system.NvmCommandExistsForContextWithContext(ctx, sys, "bun") {
			if err := ctx.Err(); err != nil {
				return err
			}
			return fmt.Errorf("bun installation completed but bun is still not available on PATH")
		}
		log.Success("bun installed")
	}

	return nil
}

func formatCommandErrorForContext(ctx context.Context, action string, res *system.Result, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return system.FormatCommandError(action, res, err)
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
	return strings.Contains(content, `export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"`) &&
		strings.Contains(content, "nvm.sh") &&
		strings.Contains(content, `export BUN_INSTALL="${BUN_INSTALL:-$HOME/.bun}"`) &&
		strings.Contains(content, `export PATH="$BUN_INSTALL/bin:$PATH"`)
}

func ensureNodeShellPath(sys *system.Context) error {
	return ensureNodeShellPathContext(context.Background(), sys)
}

func ensureNodeShellPathContext(ctx context.Context, sys *system.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	home := system.TargetHomeDir(sys)
	if home == "" {
		return fmt.Errorf("cannot determine target home directory for node shell setup")
	}

	script := fmt.Sprintf(`set -e
export HOME=%s
for rc in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.profile"; do
  touch "$rc"
  if ! grep -qF 'export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"' "$rc"; then
    if ! grep -qF '# SYS_BOOTSTRAP_NODE_ENV' "$rc"; then
      printf '\n# SYS_BOOTSTRAP_NODE_ENV\n' >> "$rc"
    fi
    printf '%%s\n' 'export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"' >> "$rc"
  fi
  if ! grep -qF '"$NVM_DIR/nvm.sh"' "$rc"; then
    printf '%%s\n' '[ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"' >> "$rc"
  fi
  if ! grep -qF 'export BUN_INSTALL="${BUN_INSTALL:-$HOME/.bun}"' "$rc"; then
    printf '%%s\n' 'export BUN_INSTALL="${BUN_INSTALL:-$HOME/.bun}"' >> "$rc"
  fi
  if ! grep -qF 'export PATH="$BUN_INSTALL/bin:$PATH"' "$rc"; then
    printf '%%s\n' 'export PATH="$BUN_INSTALL/bin:$PATH"' >> "$rc"
  fi
done`, shellQuote(home))
	res, err := system.RunAsUserWithInputContext(ctx, sys, "", "bash", "-c", script)
	if err != nil {
		return formatCommandErrorForContext(ctx, "failed to update shell startup files for node", res, err)
	}
	if res.ExitCode != 0 {
		return formatCommandErrorForContext(ctx, "failed to update shell startup files for node", res, nil)
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
	return installBunContext(context.Background(), sys, log)
}

func installBunContext(ctx context.Context, sys *system.Context, log *logging.Logger) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
	if res, err := system.RunWithContext(ctx, "curl", "-fsSL", "-o", zipPath, downloadURL); err != nil || res.ExitCode != 0 {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		// If x64 download fails, try baseline as fallback
		if normalizedArch == "amd64" && assetName != "bun-linux-x64-baseline.zip" {
			log.Warn("x64 bun download failed, trying baseline variant...")
			assetName = "bun-linux-x64-baseline.zip"
			checksum = bunLinuxX64BaselineSHA256
			downloadURL = fmt.Sprintf("https://github.com/oven-sh/bun/releases/download/bun-%s/%s", bunVersion, assetName)
			zipPath = filepath.Join(tmpDir, assetName)
			if res2, err2 := system.RunWithContext(ctx, "curl", "-fsSL", "-o", zipPath, downloadURL); err2 != nil || res2.ExitCode != 0 {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				return fmt.Errorf("failed to download bun from GitHub releases")
			}
		} else {
			return fmt.Errorf("failed to download bun from GitHub releases")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Verify checksum
	if err := verifyFileSHA256(zipPath, checksum); err != nil {
		return fmt.Errorf("bun checksum verification failed: %w", err)
	}
	log.Info("bun checksum verified")
	if err := ctx.Err(); err != nil {
		return err
	}

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
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := verifyBunBinaryContext(ctx, sys, destPath); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if normalizedArch == "amd64" && assetName != "bun-linux-x64-baseline.zip" {
			log.Warnf("bun failed to run after install, trying baseline variant: %v", err)
			assetName = "bun-linux-x64-baseline.zip"
			checksum = bunLinuxX64BaselineSHA256
			downloadURL = fmt.Sprintf("https://github.com/oven-sh/bun/releases/download/bun-%s/%s", bunVersion, assetName)
			zipPath = filepath.Join(tmpDir, assetName)
			if res, err := system.RunWithContext(ctx, "curl", "-fsSL", "-o", zipPath, downloadURL); err != nil || res.ExitCode != 0 {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				return fmt.Errorf("failed to download bun baseline from GitHub releases")
			}
			if err := verifyFileSHA256(zipPath, checksum); err != nil {
				return fmt.Errorf("bun baseline checksum verification failed: %w", err)
			}
			if err := extractBunFromZip(zipPath, destPath); err != nil {
				return fmt.Errorf("failed to extract bun baseline binary: %w", err)
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := verifyBunBinaryContext(ctx, sys, destPath); err != nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return ctxErr
				}
				return fmt.Errorf("bun baseline verification failed: %w", err)
			}
		} else {
			return fmt.Errorf("bun verification failed: %w", err)
		}
	}

	// Chown to target user when running as root
	if err := ctx.Err(); err != nil {
		return err
	}
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
			if res, err := system.RunWithContext(ctx, "chown", "-R", owner, bunDir); err != nil || res.ExitCode != 0 {
				return formatCommandErrorForContext(ctx, "failed to chown bun directory", res, err)
			}
		}
	}

	return nil
}

func verifyBunBinary(sys *system.Context, path string) error {
	return verifyBunBinaryContext(context.Background(), sys, path)
}

func verifyBunBinaryContext(ctx context.Context, sys *system.Context, path string) error {
	res, err := system.RunAsUserWithInputContext(ctx, sys, "", path, "--version")
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

	if err := system.WriteReaderAtomically(destPath, rc, 0o755); err != nil {
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
