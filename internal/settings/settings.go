package settings

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/frankwei98/sys-bootstrap/internal/system"
)

const (
	DefaultSystemConfigPath = "/etc/sys-bootstrap/config.env"

	KeyLang      = "lang"
	KeyAptMirror = "apt_mirror"

	ValAptCernet  = "cernet"
	ValAptDefault = "default"

	settingsUserHelperEnv = "SYS_BOOTSTRAP_SETTINGS_USER_HELPER"
)

var (
	settingsEffectiveUID   = os.Geteuid
	saveUserAsInvokingUser = runUserSettingsHelper
)

type settingsHelperResponse struct {
	Error string `json:"error,omitempty"`
}

func init() {
	if os.Getenv(settingsUserHelperEnv) == "" {
		return
	}
	response := settingsHelperResponse{}
	if os.Geteuid() == 0 {
		response.Error = "settings user helper refuses to run with root privileges"
	} else {
		var s Settings
		if err := json.NewDecoder(os.Stdin).Decode(&s); err != nil {
			response.Error = fmt.Sprintf("decode settings helper request: %v", err)
		} else if current, err := user.Current(); err != nil || current.HomeDir == "" {
			response.Error = fmt.Sprintf("resolve settings helper user: %v", err)
		} else {
			path := filepath.Join(current.HomeDir, ".config", "sys-bootstrap", "config.env")
			if err := writeConfig(path, s, true); err != nil {
				response.Error = err.Error()
			}
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(response)
	os.Exit(0)
}

// SystemConfigPath is the system-wide config file path. Overridable for tests.
var SystemConfigPath = DefaultSystemConfigPath

// defaultUserConfigPath computes the user config path from environment.
func defaultUserConfigPath() string {
	// sudo commonly preserves the root user's XDG_CONFIG_HOME. When a
	// non-root invoker is known, derive the path from that user's home instead.
	xdg := ""
	if sudoUser := os.Getenv("SUDO_USER"); sudoUser == "" || sudoUser == "root" {
		xdg = os.Getenv("XDG_CONFIG_HOME")
	}
	if xdg == "" {
		home := userHomeDir()
		if home == "" {
			return ""
		}
		xdg = filepath.Join(home, ".config")
	}
	return filepath.Join(xdg, "sys-bootstrap", "config.env")
}

func userHomeDir() string {
	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser != "" && sudoUser != "root" {
		u, err := user.Lookup(sudoUser)
		if err == nil && u.HomeDir != "" {
			return u.HomeDir
		}
	}
	home, _ := os.UserHomeDir()
	return home
}

// UserConfigPath returns the user-level config file path. Overridable for tests.
var UserConfigPath = defaultUserConfigPath

// Settings holds persisted configuration.
type Settings struct {
	Lang      string // "en", "zh-CN", or ""
	AptMirror string // "cernet", "default", or ""
}

// Load reads system then user config files and merges them.
// User config overrides system config for each key.
func Load() (Settings, error) {
	s := Settings{}
	if err := mergeFile(&s, SystemConfigPath); err != nil {
		return Settings{}, fmt.Errorf("load system settings: %w", err)
	}
	if err := mergeFile(&s, UserConfigPath()); err != nil {
		return Settings{}, fmt.Errorf("load user settings: %w", err)
	}
	return s, nil
}

// SaveUser writes settings to the user config file.
func SaveUser(s Settings) error {
	if settingsEffectiveUID() == 0 {
		if sudoUser := os.Getenv("SUDO_USER"); sudoUser != "" && sudoUser != "root" {
			return saveUserAsInvokingUser(s)
		}
	}
	path := UserConfigPath()
	if path == "" {
		return fmt.Errorf("cannot determine user config path")
	}
	return writeConfig(path, s, true)
}

func runUserSettingsHelper(s Settings) error {
	username := os.Getenv("SUDO_USER")
	if username == "" || username == "root" {
		return fmt.Errorf("cannot determine non-root invoking user")
	}
	invoker, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("cannot resolve invoking user %q: %w", username, err)
	}
	if invoker.Uid == "0" || invoker.HomeDir == "" {
		return fmt.Errorf("refusing invalid invoking user %q", username)
	}
	payload, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encode user settings: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current executable: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", "-H", "-u", username, "--",
		"/usr/bin/env", "-i", "HOME="+invoker.HomeDir, "USER="+username, "LOGNAME="+username,
		settingsUserHelperEnv+"=1", executable)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("user settings helper timed out: %w", ctx.Err())
		}
		return fmt.Errorf("user settings helper failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	var response settingsHelperResponse
	if err := json.NewDecoder(&stdout).Decode(&response); err != nil {
		return fmt.Errorf("decode user settings helper response: %w", err)
	}
	if response.Error != "" {
		return fmt.Errorf("user settings helper: %s", response.Error)
	}
	return nil
}

// SaveSystem writes settings to the system config file.
// Requires root privileges (used by installer).
func SaveSystem(s Settings) error {
	return writeConfig(SystemConfigPath, s, false)
}

// mergeFile reads a config file and overlays non-empty values onto s.
func mergeFile(s *Settings, path string) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := parseLine(line)
		if !ok {
			continue
		}
		switch key {
		case KeyLang:
			if isValidLang(val) {
				s.Lang = NormalizeLang(val)
			}
		case KeyAptMirror:
			if isValidAptMirror(val) {
				s.AptMirror = val
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

// parseLine splits "key=value" and returns (key, value, true) or ("", "", false).
func parseLine(line string) (string, string, bool) {
	idx := strings.Index(line, "=")
	if idx < 0 {
		return "", "", false
	}
	key := strings.TrimSpace(line[:idx])
	val := strings.TrimSpace(line[idx+1:])
	return key, val, true
}

func isValidLang(v string) bool {
	switch strings.ToLower(v) {
	case "en", "zh-cn", "zh_cn", "zh", "chinese":
		return true
	}
	return false
}

func isValidAptMirror(v string) bool {
	switch v {
	case ValAptCernet, ValAptDefault:
		return true
	}
	return false
}

// writeConfig atomically writes a settings file.
// Creates parent directories with 0755 and the file with 0644.
func writeConfig(path string, s Settings, userScoped bool) error {
	dir := filepath.Dir(path)
	if err := rejectConfigSymlinks(path, userScoped); err != nil {
		return fmt.Errorf("refusing unsafe config path %s: %w", path, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create config dir %s: %w", dir, err)
	}
	if err := rejectConfigSymlinks(path, userScoped); err != nil {
		return fmt.Errorf("refusing unsafe config path %s: %w", path, err)
	}
	if userScoped {
		// The parent (usually ~/.config) may have been created by MkdirAll as
		// root too. Both directories must be returned to the invoking user.
		if err := system.ChownToInvokingUser(filepath.Dir(dir), dir); err != nil {
			return fmt.Errorf("cannot set config directory ownership: %w", err)
		}
	}

	var b strings.Builder
	if s.Lang != "" {
		fmt.Fprintf(&b, "lang=%s\n", s.Lang)
	}
	if s.AptMirror != "" {
		fmt.Fprintf(&b, "apt_mirror=%s\n", s.AptMirror)
	}

	if err := writeConfigAtomically(path, []byte(b.String())); err != nil {
		return fmt.Errorf("cannot write config %s: %w", path, err)
	}
	if userScoped {
		if err := system.ChownToInvokingUser(path); err != nil {
			return fmt.Errorf("cannot set config file ownership: %w", err)
		}
	}
	return nil
}

func writeConfigAtomically(path string, data []byte) error {
	f, info, err := system.OpenExistingFileNoFollow(path)
	if os.IsNotExist(err) {
		return system.WriteFileAtomically(path, data, 0o644)
	}
	if err != nil {
		return err
	}
	xattrs, xattrErr := system.CaptureFileXattrs(f)
	closeErr := f.Close()
	if xattrErr != nil {
		return fmt.Errorf("capture existing extended attributes: %w", xattrErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close existing config: %w", closeErr)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("capture existing config ownership")
	}
	return system.WriteFileAtomicallyWithOwnerAndXattrs(path, data, 0o644, int(stat.Uid), int(stat.Gid), xattrs)
}

func rejectConfigSymlinks(path string, userScoped bool) error {
	if userScoped {
		home := userHomeDir()
		if home != "" {
			rel, err := filepath.Rel(filepath.Clean(home), filepath.Clean(path))
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return system.RejectSymlinkPathBelow(home, path)
			}
		}
	}
	return system.RejectSymlinkPath(path)
}

// NormalizeLang converts settings lang values to i18n-compatible form.
func NormalizeLang(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "zh", "zh-cn", "zh_cn", "zh_cn.utf-8", "zh_cn.utf8", "chinese":
		return "zh-CN"
	default:
		return "en"
	}
}
