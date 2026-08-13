package app

import (
	"errors"
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

// UserInfo holds the effective user plus the user-scoped home directory that
// install and uninstall operations should target.
type UserInfo struct {
	Username string
	UID      string
	HomeDir  string
	IsRoot   bool
	SudoUser string // SUDO_USER env var, if set
}

// ResolveUserInfo determines the current effective user and target home
// directory. A sudo-launched uninstall operates on the invoking user's home,
// matching the install path used for user-level tools.
func ResolveUserInfo() (*UserInfo, error) {
	u, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("cannot determine current user: %w", err)
	}

	targetUser := u
	sudoUser := os.Getenv("SUDO_USER")
	if u.Uid == "0" && sudoUser != "" && sudoUser != "root" {
		invokingUser, lookupErr := user.Lookup(sudoUser)
		if lookupErr != nil {
			return nil, fmt.Errorf("cannot determine sudo invoking user %q: %w", sudoUser, lookupErr)
		}
		targetUser = invokingUser
	}

	info := &UserInfo{
		Username: targetUser.Username,
		UID:      targetUser.Uid,
		HomeDir:  targetUser.HomeDir,
		IsRoot:   u.Uid == "0",
		SudoUser: sudoUser,
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
	absHome, err := filepath.Abs(homeDir)
	if err != nil {
		return fmt.Errorf("cannot resolve home directory %s: %w", homeDir, err)
	}
	cleanHome := filepath.Clean(absHome)

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
	if err := system.RejectSymlinkPathBelow(cleanHome, cleanTarget); err != nil {
		return fmt.Errorf("unsafe target path %s: %w", cleanTarget, err)
	}
	// A home directory may itself be a platform-managed symlink. Resolve both
	// paths when possible so a user-controlled home link cannot make an inside
	// lexical path point outside the real home tree.
	if realHome, err := resolveExistingPath(cleanHome); err == nil {
		if realTarget, targetErr := resolveExistingPath(cleanTarget); targetErr == nil {
			if !strings.HasPrefix(realTarget+string(filepath.Separator), realHome+string(filepath.Separator)) {
				return fmt.Errorf("path %s resolves outside home directory %s", cleanTarget, cleanHome)
			}
		}
	}

	return nil
}

func resolveExistingPath(path string) (string, error) {
	current := filepath.Clean(path)
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

// ScanUninstallItems detects user-level software installed in the given home directory.
func ScanUninstallItems(homeDir string) []UninstallItem {
	var items []UninstallItem

	// 1. nvm / Node.js
	nvmDir := userScopedEnvDir("NVM_DIR", homeDir, filepath.Join(homeDir, ".nvm"))
	if dirExists(nvmDir) {
		items = append(items, UninstallItem{
			ID:          "nvm",
			Name:        "nvm / Node.js",
			Description: i18n.T("uninstall_item_nvm_desc"),
			Dirs:        []string{nvmDir},
		})
	}

	// 2. bun
	bunDir := userScopedEnvDir("BUN_INSTALL", homeDir, filepath.Join(homeDir, ".bun"))
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
	pm := detectAIPkgManager(homeDir)
	if system.NvmCommandExistsForHome(homeDir, "claude") {
		items = append(items, UninstallItem{
			ID:          "ai-claude",
			Name:        "Claude Code",
			Description: i18n.T("uninstall_item_claude_desc"),
			PkgManager:  pm,
			PkgName:     "@anthropic-ai/claude-code",
		})
	}
	if system.NvmCommandExistsForHome(homeDir, "codex") {
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
	pnpmHome := userScopedEnvDir("PNPM_HOME", homeDir, filepath.Join(homeDir, ".local", "share", "pnpm"))
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

// userScopedEnvDir only honors an environment override when it stays inside
// the resolved target home. sudo can preserve root's NVM_DIR/BUN_INSTALL,
// which must never redirect a user-targeted uninstall back to /root.
func userScopedEnvDir(envName, homeDir, fallback string) string {
	candidate := strings.TrimSpace(os.Getenv(envName))
	if candidate == "" {
		return fallback
	}
	absCandidate, err := filepath.Abs(candidate)
	if err != nil {
		return fallback
	}
	cleanHome := filepath.Clean(homeDir)
	cleanCandidate := filepath.Clean(absCandidate)
	if cleanCandidate != cleanHome && strings.HasPrefix(cleanCandidate+string(filepath.Separator), cleanHome+string(filepath.Separator)) {
		return absCandidate
	}
	return fallback
}

// detectAIPkgManager returns the best available package manager for AI CLI removal.
// Returns "pnpm" if available in nvm shell, "npm" if available, or "" if neither.
func detectAIPkgManager(homeDir string) string {
	if system.NvmCommandExistsForHome(homeDir, "pnpm") {
		return "pnpm"
	}
	if system.NvmCommandExistsForHome(homeDir, "npm") {
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
			// Keep even unsafe paths in the plan so ExecuteUninstall can report
			// the rejected operation instead of silently returning success with
			// zero deletions.
			plan.DirsToDelete = append(plan.DirsToDelete, d)
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
		regexp.MustCompile(`(?i)^\s*#\s*SYS_BOOTSTRAP_NODE_ENV\s*$`),
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
		regexp.MustCompile(`(?i)^\s*#\s*SYS_BOOTSTRAP_PNPM_HOME\s*$`),
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
		if fileExists(path) && system.RejectSymlinkPath(path) == nil {
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
	if err := system.RejectSymlinkPath(rcFile); err != nil {
		return 0, fmt.Errorf("refusing unsafe shell rc path %s: %w", rcFile, err)
	}
	// Read and inspect the same no-follow inode so FIFOs, devices, and path
	// replacement races cannot block or change the snapshot after validation.
	rc, rcInfo, err := system.OpenExistingFileNoFollow(rcFile)
	if err != nil {
		return 0, fmt.Errorf("cannot read %s: %w", rcFile, err)
	}
	data, err := io.ReadAll(rc)
	closeErr := rc.Close()
	if err != nil {
		return 0, fmt.Errorf("cannot read %s: %w", rcFile, err)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("cannot close %s: %w", rcFile, closeErr)
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
	if err := system.RejectSymlinkPath(backupPath); err != nil {
		return removed, fmt.Errorf("refusing unsafe backup path %s: %w", backupPath, err)
	}
	if err := copyFile(rcFile, backupPath); err != nil {
		return removed, fmt.Errorf("cannot backup %s: %w", rcFile, err)
	}
	if log != nil {
		log.Infof(i18n.T("uninstall_rc_backup"), backupPath)
	}

	// Write cleaned content atomically so a pre-existing symlink cannot redirect
	// the privileged write to another file.
	if err := system.WriteFileAtomicallyAsInvokingUser(rcFile, []byte(strings.Join(lines, "\n")+"\n"), rcInfo.Mode()); err != nil {
		return removed, fmt.Errorf("cannot write %s: %w", rcFile, err)
	}
	return removed, nil
}

// ValidateUninstallPlan performs every path-safety check that can reject an
// uninstall before the plan is displayed or any package command is run.
func ValidateUninstallPlan(plan UninstallPlan, homeDir string) error {
	var failures []error
	itemIDs := make([]string, len(plan.Items))
	for i, item := range plan.Items {
		itemIDs[i] = item.ID
	}
	for _, dir := range plan.DirsToDelete {
		if err := ValidatePathSafety(dir, homeDir); err != nil {
			failures = append(failures, fmt.Errorf("unsafe uninstall path %s: %w", dir, err))
		}
	}
	for _, rcFile := range plan.RCFiles {
		if err := ValidatePathSafety(rcFile, homeDir); err != nil {
			failures = append(failures, fmt.Errorf("unsafe shell rc path %s: %w", rcFile, err))
			continue
		}
		removed, err := CleanShellRC([]string{rcFile}, itemIDs, true, nil)
		if err != nil {
			failures = append(failures, fmt.Errorf("cannot preview shell rc cleanup for %s: %w", rcFile, err))
			continue
		}
		if removed > 0 {
			backupPath := rcFile + ".bak.sys-bootstrap"
			if err := ValidatePathSafety(backupPath, homeDir); err != nil {
				failures = append(failures, fmt.Errorf("unsafe shell rc backup path %s: %w", backupPath, err))
			}
		}
	}
	return errors.Join(failures...)
}

// ExecuteUninstall performs the actual uninstallation.
// If dryRun is true, only prints what would be done without making changes.
func ExecuteUninstall(plan UninstallPlan, homeDir string, dryRun bool, log *logging.Logger) error {
	if err := ValidateUninstallPlan(plan, homeDir); err != nil {
		return err
	}
	var failures []error
	// 1. Remove package manager packages
	for _, item := range plan.Items {
		if item.PkgName == "" {
			continue
		}

		if item.PkgManager == "" {
			log.Warnf(i18n.T("uninstall_cmd_skipped_no_pkgmgr"), item.Name, item.PkgName)
			if !dryRun {
				failures = append(failures, fmt.Errorf("cannot remove %s: no package manager available", item.PkgName))
			}
			continue
		}

		cmd := buildPkgRemoveCmd(item.PkgManager, item.PkgName)
		if dryRun {
			log.Infof(i18n.T("uninstall_dry_run_cmd"), cmd)
			continue
		}
		log.Infof(i18n.T("uninstall_running_cmd"), cmd)
		res, err := system.RunInNvmShellForHome(homeDir, cmd)
		if err != nil || res == nil || res.ExitCode != 0 {
			stderr := ""
			if res != nil {
				stderr = res.Stderr
			}
			log.Warnf(i18n.T("uninstall_cmd_failed"), cmd, stderr)
			failures = append(failures, fmt.Errorf("uninstall command %q failed: %v (%s)", cmd, err, stderr))
		} else {
			log.Successf(i18n.T("uninstall_cmd_done"), cmd)
		}
	}

	// 2. Remove directories
	for _, dir := range plan.DirsToDelete {
		if err := ValidatePathSafety(dir, homeDir); err != nil {
			log.Warnf(i18n.T("uninstall_path_unsafe"), dir, err)
			failures = append(failures, fmt.Errorf("unsafe uninstall path %s: %w", dir, err))
			continue
		}
		if dryRun {
			log.Infof(i18n.T("uninstall_dry_run_dir"), dir)
			continue
		}
		log.Infof(i18n.T("uninstall_removing_dir"), dir)
		if err := system.RemoveAllBeneath(homeDir, dir); err != nil {
			log.Warnf(i18n.T("uninstall_dir_failed"), dir, err)
			failures = append(failures, fmt.Errorf("remove %s: %w", dir, err))
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
		failures = append(failures, fmt.Errorf("clean shell rc files: %w", err))
	} else if removed > 0 {
		if dryRun {
			log.Infof(i18n.T("uninstall_dry_run_rc"), removed)
		} else {
			log.Successf(i18n.T("uninstall_rc_cleaned"), removed)
		}
	}

	return errors.Join(failures...)
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
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func copyFile(src, dst string) error {
	in, info, err := system.OpenExistingFileNoFollow(src)
	if err != nil {
		return err
	}
	defer in.Close()
	data, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("copy %s -> %s: %w", src, dst, err)
	}
	return system.WriteFileAtomicallyAsInvokingUser(dst, data, info.Mode())
}
