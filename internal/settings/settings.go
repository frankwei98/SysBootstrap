package settings

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultSystemConfigPath = "/etc/sys-bootstrap/config.env"

	KeyLang      = "lang"
	KeyAptMirror = "apt_mirror"

	ValAptCernet  = "cernet"
	ValAptDefault = "default"
)

// SystemConfigPath is the system-wide config file path. Overridable for tests.
var SystemConfigPath = DefaultSystemConfigPath

// defaultUserConfigPath computes the user config path from environment.
func defaultUserConfigPath() string {
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		home, _ := os.UserHomeDir()
		if home == "" {
			return ""
		}
		xdg = filepath.Join(home, ".config")
	}
	return filepath.Join(xdg, "sys-bootstrap", "config.env")
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
func Load() Settings {
	s := Settings{}
	mergeFile(&s, SystemConfigPath)
	mergeFile(&s, UserConfigPath())
	return s
}

// SaveUser writes settings to the user config file.
func SaveUser(s Settings) error {
	path := UserConfigPath()
	if path == "" {
		return fmt.Errorf("cannot determine user config path")
	}
	return writeConfig(path, s)
}

// SaveSystem writes settings to the system config file.
// Requires root privileges (used by installer).
func SaveSystem(s Settings) error {
	return writeConfig(SystemConfigPath, s)
}

// mergeFile reads a config file and overlays non-empty values onto s.
func mergeFile(s *Settings, path string) {
	if path == "" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
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
func writeConfig(path string, s Settings) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot create config dir %s: %w", dir, err)
	}

	var b strings.Builder
	if s.Lang != "" {
		fmt.Fprintf(&b, "lang=%s\n", s.Lang)
	}
	if s.AptMirror != "" {
		fmt.Fprintf(&b, "apt_mirror=%s\n", s.AptMirror)
	}

	// Write to temp file then rename for atomicity
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("cannot write config %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("cannot rename config %s -> %s: %w", tmp, path, err)
	}
	return nil
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
