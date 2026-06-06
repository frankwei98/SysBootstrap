package app

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/frankwei98/sys-bootstrap/internal/i18n"
	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
)

// UninstallItem represents a user-level software item that can be uninstalled.
type UninstallItem struct {
	ID          string   // e.g. "nvm", "bun", "pnpm", "ai-claude", "ai-codex"
	Name        string   // human-readable name
	Description string   // what it is
	Dirs        []string // absolute paths to delete (may be empty)
	PkgManager  string   // "pnpm", "npm", or ""
	PkgName     string   // npm package name, e.g. "@anthropic-ai/claude-code"
}

// UserInfo holds the resolved current effective user and home directory.
type UserInfo struct {
	Username string
	UID      string
	HomeDir  string
	IsRoot   bool
	SudoUser string // SUDO_USER env var, if set
}

// ResolveUserInfo determines the current effective user and home directory.
// If running as root or via sudo, the target home is still the effective user's
// home (typically /root), not SUDO_USER's home. This is intentional per spec.
func ResolveUserInfo() (*UserInfo, error) {
	u, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("cannot determine current user: %w", err)
	}

	info := &UserInfo{
		Username: u.Username,
		UID:      u.Uid,
		HomeDir:  u.HomeDir,
		IsRoot:   u.Uid == "0",
		SudoUser: os.Getenv("SUDO_USER"),
	}

	// Ensure home directory exists and is absolute
	if info.HomeDir == "" {
		return nil, fmt.Errorf("home directory is empty for user %s", info.Username)
	}
	absHome, err := filepath.Abs(info.HomeDir)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve home directory %s: %w", info.HomeDir, err)
	}
	info.HomeDir = absHome

	return info, nil
}

// ValidatePathSafety checks that the resolved target path is safe to delete:
// - Not empty
// - Not "/"
// - Not the home directory itself
// - Must be inside the home directory
func ValidatePathSafety(target, homeDir string) error {
	if target == "" {
		return fmt.Errorf("refusing to delete empty path")
	}

	absTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("cannot resolve path %s: %w", target, err)
	}

	// Clean both paths for comparison
	cleanTarget := filepath.Clean(absTarget)
	cleanHome := filepath.Clean(homeDir)

	if cleanTarget == "/" {
		return fmt.Errorf("refusing to delete root path /")
	}

	if cleanTarget == cleanHome {
		return fmt.Errorf("refusing to delete home directory itself: %s", cleanHome)
	}

	// Check that target is inside home directory
	if !strings.HasPrefix(cleanTarget+string(filepath.Separator), cleanHome+string(filepath.Separator)) {
		return fmt.Errorf("path %s is outside home directory %s", cleanTarget, cleanHome)
	}

	return nil
}

// ScanUninstallItems detects user-level software installed in the given home directory.
func ScanUninstallItems(homeDir string) []UninstallItem {
	var items []UninstallItem

	// 1. nvm / Node.js
	nvmDir := os.Getenv("NVM_DIR")
	if nvmDir == "" {
		nvmDir = filepath.Join(homeDir, ".nvm")
	}
	if dirExists(nvmDir) {
		items = append(items, UninstallItem{
			ID:          "nvm",
			Name:        "nvm / Node.js",
			Description: i18n.T("uninstall_item_nvm_desc"),
			Dirs:        []string{nvmDir},
		})
	}

	// 2. bun
	bunDir := os.Getenv("BUN_INSTALL")
	if bunDir == "" {
		bunDir = filepath.Join(homeDir, ".bun")
	}
	if dirExists(bunDir) {
		items = append(items, UninstallItem{
			ID:          "bun",
			Name:        "bun",
			Description: i18n.T("uninstall_item_bun_desc"),
			Dirs:        []string{bunDir},
		})
	}

	// 3. pnpm (user-level dirs)
	pnpmDirs := pnpmUserDirs(homeDir)
	if len(pnpmDirs) > 0 {
		items = append(items, UninstallItem{
			ID:          "pnpm",
			Name:        "pnpm (user-level)",
			Description: i18n.T("uninstall_item_pnpm_desc"),
			Dirs:        pnpmDirs,
		})
	}

	// 4-5. AI CLI: only include if actually installed (command exists in nvm shell)
	pm := detectAIPkgManager()
	if system.NvmCommandExists("claude") {
		items = append(items, UninstallItem{
			ID:          "ai-claude",
			Name:        "Claude Code",
			Description: i18n.T("uninstall_item_claude_desc"),
			PkgManager:  pm,
			PkgName:     "@anthropic-ai/claude-code",
		})
	}
	if system.NvmCommandExists("codex") {
		items = append(items, UninstallItem{
			ID:          "ai-codex",
			Name:        "Codex",
			Description: i18n.T("uninstall_item_codex_desc"),
			PkgManager:  pm,
			PkgName:     "@openai/codex",
		})
	}

	return items
}

// pnpmUserDirs returns pnpm-related directories that exist under the user's home.
func pnpmUserDirs(homeDir string) []string {
	var dirs []string

	// Primary: $PNPM_HOME or ~/.local/share/pnpm
	pnpmHome := os.Getenv("PNPM_HOME")
	if pnpmHome == "" {
		pnpmHome = filepath.Join(homeDir, ".local", "share", "pnpm")
	}
	if dirExists(pnpmHome) {
		dirs = append(dirs, pnpmHome)
	}

	// Also check ~/.config/pnpm
	configDir := filepath.Join(homeDir, ".config", "pnpm")
	if dirExists(configDir) && configDir != pnpmHome {
		dirs = append(dirs, configDir)
	}

	return dirs
}

// detectAIPkgManager returns the best available package manager for AI CLI removal.
// Returns "pnpm" if available in nvm shell, "npm" if available, or "" if neither.
func detectAIPkgManager() string {
	if system.NvmCommandExists("pnpm") {
		return "pnpm"
	}
	if system.NvmCommandExists("npm") {
		return "npm"
	}
	return ""
}

// FilterInstalledItems returns only items that have something to uninstall
// (either directories exist, a package is known, or a package manager is available).
func FilterInstalledItems(items []UninstallItem) []UninstallItem {
	var result []UninstallItem
	for _, item := range items {
		hasDir := len(item.Dirs) > 0
		hasPkg := item.PkgName != ""
		hasPkgManager := item.PkgManager != ""
		if hasDir || hasPkg || hasPkgManager {
			result = append(result, item)
		}
	}
	return result
}

// UninstallPlan describes what would happen during an uninstall.
type UninstallPlan struct {
	Items        []UninstallItem
	DirsToDelete []string
	Commands     []string
	RCFiles      []string
}

// BuildUninstallPlan creates a plan for the given uninstall items.
func BuildUninstallPlan(items []UninstallItem, homeDir string) UninstallPlan {
	plan := UninstallPlan{
		Items: items,
	}

	for _, item := range items {
		// Collect directories
		for _, d := range item.Dirs {
			if err := ValidatePathSafety(d, homeDir); err == nil {
				plan.DirsToDelete = append(plan.DirsToDelete, d)
			}
		}

		// Collect package removal commands
		if item.PkgName != "" && item.PkgManager != "" {
			cmd := buildPkgRemoveCmd(item.PkgManager, item.PkgName)
			plan.Commands = append(plan.Commands, cmd)
		}
	}

	// Detect rc files that need cleanup
	plan.RCFiles = detectRCFiles(homeDir)

	return plan
}

// buildPkgRemoveCmd constructs the package removal command.
func buildPkgRemoveCmd(manager, pkg string) string {
	switch manager {
	case "pnpm":
		return fmt.Sprintf("pnpm remove -g %s", pkg)
	case "npm":
		return fmt.Sprintf("npm uninstall -g %s", pkg)
	default:
		return ""
	}
}

// rcCleanupPatterns defines shell rc lines to remove for each tool.
// Patterns use (?i) for case-insensitive matching to handle variations like
// "Export NVM_DIR" or "export Nvm_Dir". The ^ anchor prevents matching
// mid-line occurrences (e.g. inside comments or case statements).
var rcCleanupPatterns = map[string][]*regexp.Regexp{
	"nvm": {
		regexp.MustCompile(`(?i)^\s*(export\s+)?NVM_DIR=`),
		regexp.MustCompile(`(?i)\[ -s "\$NVM_DIR/nvm\.sh" \]`),
		regexp.MustCompile(`(?i)source "\$NVM_DIR/nvm\.sh"`),
		regexp.MustCompile(`(?i)\[ -s "\$NVM_DIR/bash_completion" \]`),
		regexp.MustCompile(`(?i)source "\$NVM_DIR/bash_completion"`),
	},
	"bun": {
		regexp.MustCompile(`(?i)^\s*(export\s+)?BUN_INSTALL=`),
		regexp.MustCompile(`(?i)^\s*(export\s+)?PATH=.*\$BUN_INSTALL`), // PATH lines that add BUN_INSTALL
	},
	"pnpm": {
		regexp.MustCompile(`(?i)^\s*(export\s+)?PNPM_HOME=`),
		regexp.MustCompile(`(?i)^\s*(export\s+)?PATH=.*\$PNPM_HOME`), // PATH lines that add PNPM_HOME
	},
}

// detectRCFiles returns rc files in the home directory that exist.
func detectRCFiles(homeDir string) []string {
	candidates := []string{".bashrc", ".zshrc", ".profile", ".bash_profile"}
	var result []string
	for _, name := range candidates {
		path := filepath.Join(homeDir, name)
		if fileExists(path) {
			result = append(result, path)
		}
	}
	return result
}

// CleanShellRC removes shell rc lines matching nvm/bun/pnpm initialization patterns
// from the given rc files. Returns the number of lines removed.
// If dryRun is true, only counts lines without modifying files.
func CleanShellRC(rcFiles []string, itemIDs []string, dryRun bool, log *logging.Logger) (int, error) {
	totalRemoved := 0

	// Collect applicable patterns
	var patterns []*regexp.Regexp
	for _, id := range itemIDs {
		if pats, ok := rcCleanupPatterns[id]; ok {
			patterns = append(patterns, pats...)
		}
	}
	if len(patterns) == 0 {
		return 0, nil
	}

	for _, rcFile := range rcFiles {
		removed, err := cleanSingleRC(rcFile, patterns, dryRun, log)
		if err != nil {
			return totalRemoved, err
		}
		totalRemoved += removed
	}

	return totalRemoved, nil
}

// cleanSingleRC cleans a single rc file. Returns lines removed.
func cleanSingleRC(rcFile string, patterns []*regexp.Regexp, dryRun bool, log *logging.Logger) (int, error) {
	// Read and scan for matches
	data, err := os.ReadFile(rcFile)
	if err != nil {
		return 0, fmt.Errorf("cannot read %s: %w", rcFile, err)
	}

	rawLines := strings.Split(string(data), "\n")
	// If file ends with newline, Split produces a trailing empty string.
	// Drop it to avoid a spurious blank line on rewrite.
	if len(rawLines) > 0 && rawLines[len(rawLines)-1] == "" {
		rawLines = rawLines[:len(rawLines)-1]
	}

	var lines []string
	removed := 0
	for _, line := range rawLines {
		matched := false
		for _, pat := range patterns {
			if pat.MatchString(line) {
				matched = true
				break
			}
		}
		if matched {
			removed++
			if log != nil {
				log.Infof(i18n.T("uninstall_rc_remove_line"), rcFile, line)
			}
		} else {
			lines = append(lines, line)
		}
	}

	if removed == 0 {
		return 0, nil
	}

	if dryRun {
		return removed, nil
	}

	// Backup original before modifying
	backupPath := rcFile + ".bak.sys-bootstrap"
	if err := copyFile(rcFile, backupPath); err != nil {
		return removed, fmt.Errorf("cannot backup %s: %w", rcFile, err)
	}
	if log != nil {
		log.Infof(i18n.T("uninstall_rc_backup"), backupPath)
	}

	// Write cleaned content
	if err := os.WriteFile(rcFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return removed, fmt.Errorf("cannot write %s: %w", rcFile, err)
	}

	return removed, nil
}

// ExecuteUninstall performs the actual uninstallation.
// If dryRun is true, only prints what would be done without making changes.
func ExecuteUninstall(plan UninstallPlan, homeDir string, dryRun bool, log *logging.Logger) error {
	// 1. Remove package manager packages
	for _, item := range plan.Items {
		if item.PkgName == "" {
			continue
		}

		if item.PkgManager == "" {
			log.Warnf(i18n.T("uninstall_cmd_skipped_no_pkgmgr"), item.Name, item.PkgName)
			continue
		}

		cmd := buildPkgRemoveCmd(item.PkgManager, item.PkgName)
		if dryRun {
			log.Infof(i18n.T("uninstall_dry_run_cmd"), cmd)
			continue
		}
		log.Infof(i18n.T("uninstall_running_cmd"), cmd)
		res, err := system.RunInNvmShell(cmd)
		if err != nil || res.ExitCode != 0 {
			stderr := ""
			if res != nil {
				stderr = res.Stderr
			}
			log.Warnf(i18n.T("uninstall_cmd_failed"), cmd, stderr)
		} else {
			log.Successf(i18n.T("uninstall_cmd_done"), cmd)
		}
	}

	// 2. Remove directories
	for _, dir := range plan.DirsToDelete {
		if dryRun {
			log.Infof(i18n.T("uninstall_dry_run_dir"), dir)
			continue
		}
		if err := ValidatePathSafety(dir, homeDir); err != nil {
			log.Warnf(i18n.T("uninstall_path_unsafe"), dir, err)
			continue
		}
		log.Infof(i18n.T("uninstall_removing_dir"), dir)
		if err := os.RemoveAll(dir); err != nil {
			log.Warnf(i18n.T("uninstall_dir_failed"), dir, err)
		} else {
			log.Successf(i18n.T("uninstall_dir_removed"), dir)
		}
	}

	// 3. Clean shell rc files
	itemIDs := make([]string, len(plan.Items))
	for i, item := range plan.Items {
		itemIDs[i] = item.ID
	}
	removed, err := CleanShellRC(plan.RCFiles, itemIDs, dryRun, log)
	if err != nil {
		log.Warnf(i18n.T("uninstall_rc_failed"), err)
	} else if removed > 0 {
		if dryRun {
			log.Infof(i18n.T("uninstall_dry_run_rc"), removed)
		} else {
			log.Successf(i18n.T("uninstall_rc_cleaned"), removed)
		}
	}

	return nil
}

// PrintUserInfo displays the current effective user information.
func PrintUserInfo(info *UserInfo) {
	fmt.Println(i18n.Tf("uninstall_user_info", info.Username, info.UID, info.HomeDir))
	if info.IsRoot {
		fmt.Println(i18n.T("uninstall_root_warning"))
		if info.SudoUser != "" && info.SudoUser != "root" {
			fmt.Println(i18n.Tf("uninstall_sudo_user_info", info.SudoUser))
		}
	}
}

// FormatUninstallPlan returns the plan as a human-readable string.
func FormatUninstallPlan(plan UninstallPlan) string {
	var b strings.Builder

	if len(plan.DirsToDelete) > 0 {
		fmt.Fprintln(&b, i18n.T("uninstall_plan_dirs"))
		for _, d := range plan.DirsToDelete {
			fmt.Fprintf(&b, "  - %s\n", d)
		}
		fmt.Fprintln(&b)
	}

	if len(plan.Commands) > 0 {
		fmt.Fprintln(&b, i18n.T("uninstall_plan_cmds"))
		for _, c := range plan.Commands {
			fmt.Fprintf(&b, "  - %s\n", c)
		}
		fmt.Fprintln(&b)
	}

	if len(plan.RCFiles) > 0 {
		fmt.Fprintln(&b, i18n.T("uninstall_plan_rc"))
		for _, f := range plan.RCFiles {
			fmt.Fprintf(&b, "  - %s\n", f)
		}
	}

	return b.String()
}

// PrintUninstallPlan displays what the uninstall will do.
func PrintUninstallPlan(plan UninstallPlan) {
	fmt.Println(i18n.T("uninstall_plan_title"))
	fmt.Print(FormatUninstallPlan(plan))
}

// Helper functions

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	return nil
}
