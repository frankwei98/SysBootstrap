package system

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// aptMirrorReplacements defines source -> mirror replacements.
// Security sources are intentionally excluded to avoid delayed security updates.
var aptMirrorReplacements = []struct {
	original string
	mirror   string
}{
	// Debian official
	{"deb http://deb.debian.org/debian", "deb https://mirrors.cernet.edu.cn/debian"},
	{"deb-src http://deb.debian.org/debian", "deb-src https://mirrors.cernet.edu.cn/debian"},
	{"deb https://deb.debian.org/debian", "deb https://mirrors.cernet.edu.cn/debian"},
	{"deb-src https://deb.debian.org/debian", "deb-src https://mirrors.cernet.edu.cn/debian"},
	// Ubuntu official (amd64)
	{"deb http://archive.ubuntu.com/ubuntu", "deb https://mirrors.cernet.edu.cn/ubuntu"},
	{"deb-src http://archive.ubuntu.com/ubuntu", "deb-src https://mirrors.cernet.edu.cn/ubuntu"},
	{"deb https://archive.ubuntu.com/ubuntu", "deb https://mirrors.cernet.edu.cn/ubuntu"},
	{"deb-src https://archive.ubuntu.com/ubuntu", "deb-src https://mirrors.cernet.edu.cn/ubuntu"},
	// Ubuntu ports (arm64 etc.)
	{"deb http://ports.ubuntu.com/ubuntu-ports", "deb https://mirrors.cernet.edu.cn/ubuntu-ports"},
	{"deb-src http://ports.ubuntu.com/ubuntu-ports", "deb-src https://mirrors.cernet.edu.cn/ubuntu-ports"},
	{"deb https://ports.ubuntu.com/ubuntu-ports", "deb https://mirrors.cernet.edu.cn/ubuntu-ports"},
	{"deb-src https://ports.ubuntu.com/ubuntu-ports", "deb-src https://mirrors.cernet.edu.cn/ubuntu-ports"},
}

// DEB822 URI replacements for .sources files.
var deb822URIReplacements = []struct {
	original string
	mirror   string
}{
	{"http://deb.debian.org/debian", "https://mirrors.cernet.edu.cn/debian"},
	{"https://deb.debian.org/debian", "https://mirrors.cernet.edu.cn/debian"},
	{"http://archive.ubuntu.com/ubuntu", "https://mirrors.cernet.edu.cn/ubuntu"},
	{"https://archive.ubuntu.com/ubuntu", "https://mirrors.cernet.edu.cn/ubuntu"},
	{"http://ports.ubuntu.com/ubuntu-ports", "https://mirrors.cernet.edu.cn/ubuntu-ports"},
	{"https://ports.ubuntu.com/ubuntu-ports", "https://mirrors.cernet.edu.cn/ubuntu-ports"},
}

// securityPatterns lists substrings that identify a security APT source.
// Any source line containing one of these is left untouched.
var securityPatterns = []string{
	"security.debian.org",
	"security.ubuntu.com",
	"deb.debian.org/debian-security",
}

// SwitchResult reports what was changed.
type SwitchResult struct {
	ChangedFiles []string
}

// SwitchAPTMirrorToCernet rewrites Debian/Ubuntu official APT sources to use
// the CERNET mirror. It backs up all modified files and returns a restore
// function that can be called if apt-get update fails.
//
// Only processes .list and .sources files. Security sources are kept as-is.
// Third-party sources are untouched.
func SwitchAPTMirrorToCernet() (*SwitchResult, func() error, error) {
	result := &SwitchResult{}
	var backups []backupEntry

	// Process /etc/apt/sources.list
	if changed, bk, err := switchListFile("/etc/apt/sources.list"); err != nil {
		return result, nil, fmt.Errorf("processing /etc/apt/sources.list: %w", err)
	} else if changed {
		result.ChangedFiles = append(result.ChangedFiles, "/etc/apt/sources.list")
		backups = append(backups, bk)
	}

	// Process /etc/apt/sources.list.d/*.list
	listFiles, _ := filepath.Glob("/etc/apt/sources.list.d/*.list")
	for _, f := range listFiles {
		if changed, bk, err := switchListFile(f); err != nil {
			processErr := fmt.Errorf("processing %s: %w", f, err)
			return result, nil, restoreAfterAPTSwitchFailure(processErr, backups)
		} else if changed {
			result.ChangedFiles = append(result.ChangedFiles, f)
			backups = append(backups, bk)
		}
	}

	// Process /etc/apt/sources.list.d/*.sources (DEB822 format)
	sourcesFiles, _ := filepath.Glob("/etc/apt/sources.list.d/*.sources")
	for _, f := range sourcesFiles {
		if changed, bk, err := switchSourcesFile(f); err != nil {
			processErr := fmt.Errorf("processing %s: %w", f, err)
			return result, nil, restoreAfterAPTSwitchFailure(processErr, backups)
		} else if changed {
			result.ChangedFiles = append(result.ChangedFiles, f)
			backups = append(backups, bk)
		}
	}

	restoreFunc := func() error {
		return restoreAll(backups)
	}

	return result, restoreFunc, nil
}

// APTMirrorNeedsSwitchToCernet reports whether any supported official APT
// source still points at Debian or Ubuntu upstream. It is read-only so module
// Check and Plan can agree before Run performs the switch.
func APTMirrorNeedsSwitchToCernet() (bool, error) {
	paths := []string{"/etc/apt/sources.list"}
	for _, pattern := range []string{
		"/etc/apt/sources.list.d/*.list",
		"/etc/apt/sources.list.d/*.sources",
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return false, fmt.Errorf("listing APT sources with %s: %w", pattern, err)
		}
		paths = append(paths, matches...)
	}
	return aptMirrorNeedsSwitchInFiles(paths)
}

func aptMirrorNeedsSwitchInFiles(paths []string) (bool, error) {
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, fmt.Errorf("reading %s: %w", path, err)
		}

		original := string(content)
		if filepath.Ext(path) == ".sources" {
			if rewriteSourcesContent(original) != original {
				return true, nil
			}
			continue
		}

		for _, line := range strings.Split(original, "\n") {
			if rewriteListLine(line) != line {
				return true, nil
			}
		}
	}
	return false, nil
}

type backupEntry struct {
	path    string
	content []byte
	mode    os.FileMode
	uid     int
	gid     int
	xattrs  map[string][]byte
}

func backupFile(path string) (backupEntry, error) {
	if err := RejectSymlinkPath(path); err != nil {
		return backupEntry{}, err
	}
	f, info, err := OpenExistingFileNoFollow(path)
	if err != nil {
		return backupEntry{}, err
	}
	content, readErr := io.ReadAll(f)
	xattrs, xattrErr := CaptureFileXattrs(f)
	closeErr := f.Close()
	if readErr != nil {
		return backupEntry{}, readErr
	}
	if xattrErr != nil {
		return backupEntry{}, fmt.Errorf("capturing extended attributes for %s: %w", path, xattrErr)
	}
	if closeErr != nil {
		return backupEntry{}, closeErr
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return backupEntry{}, fmt.Errorf("capturing ownership for %s", path)
	}
	return backupEntry{
		path:    path,
		content: content,
		mode:    info.Mode(),
		uid:     int(stat.Uid),
		gid:     int(stat.Gid),
		xattrs:  xattrs,
	}, nil
}

func restoreAll(backups []backupEntry) error {
	var failures []error
	for _, b := range backups {
		if err := WriteFileAtomicallyWithOwnerAndXattrs(b.path, b.content, b.mode, b.uid, b.gid, b.xattrs); err != nil {
			failures = append(failures, fmt.Errorf("restoring %s: %w", b.path, err))
		}
	}
	return errors.Join(failures...)
}

func restoreAfterAPTSwitchFailure(processErr error, backups []backupEntry) error {
	restoreErr := restoreAll(backups)
	if restoreErr == nil {
		return processErr
	}
	return errors.Join(processErr, fmt.Errorf("rolling back APT source changes: %w", restoreErr))
}

// isSecuritySource returns true if the line contains a security host or path
// that should not be redirected.
func isSecuritySource(line string) bool {
	for _, pat := range securityPatterns {
		if strings.Contains(line, pat) {
			return true
		}
	}
	return false
}

// switchListFile processes a traditional .list file. Returns true if any
// line was changed.
func switchListFile(path string) (bool, backupEntry, error) {
	if err := RejectSymlinkPath(path); err != nil {
		return false, backupEntry{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, backupEntry{}, nil
		}
		return false, backupEntry{}, err
	}

	var lines []string
	changed := false
	scanner := bufio.NewScanner(f)
	// APT options and signed-by values can make source lines larger than the
	// Scanner default. Accept reasonably large lines and fail before writing if
	// a malformed file exceeds the explicit bound.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		newLine := rewriteListLine(line)
		if newLine != line {
			changed = true
		}
		lines = append(lines, newLine)
	}
	if err := scanner.Err(); err != nil {
		_ = f.Close()
		return false, backupEntry{}, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return false, backupEntry{}, fmt.Errorf("closing %s: %w", path, err)
	}

	if !changed {
		return false, backupEntry{}, nil
	}

	bk, err := backupFile(path)
	if err != nil {
		return false, backupEntry{}, err
	}

	// Preserve original file mode
	info, _ := os.Stat(path)
	mode := os.FileMode(0o644)
	if info != nil {
		mode = info.Mode()
	}

	if err := WriteFileAtomically(path, []byte(strings.Join(lines, "\n")+"\n"), mode); err != nil {
		return false, backupEntry{}, err
	}

	return true, bk, nil
}

// rewriteListLine applies mirror replacements to a single .list line.
// Skips comments, blank lines, and security sources.
// Supports optional [key=value ...] options between deb/deb-src and the URI.
func rewriteListLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return line
	}
	if isSecuritySource(line) {
		return line
	}

	// Parse: deb [-src] [options] URI rest...
	// Extract the deb type prefix, optional [...] block, then the URI.
	rest := trimmed
	var debType string
	if strings.HasPrefix(rest, "deb-src ") {
		debType = "deb-src"
		rest = rest[len("deb-src "):]
	} else if strings.HasPrefix(rest, "deb ") {
		debType = "deb"
		rest = rest[len("deb "):]
	} else {
		return line
	}

	// Skip optional [...] options block.
	// If no closing "]" is found (malformed line), leave rest as-is;
	// it won't match any known URI and the line is returned unchanged.
	if strings.HasPrefix(rest, "[") {
		if idx := strings.Index(rest, "]"); idx >= 0 {
			rest = strings.TrimSpace(rest[idx+1:])
		}
	}

	// Now rest starts with the URI. Try matching against known mirrors.
	// Note: r.original is always "deb <URI>" or "deb-src <URI>", so
	// r.original[len(debType)+1:] extracts just the URI portion.
	for _, r := range aptMirrorReplacements {
		// Match if the URI starts with this replacement's original URI prefix.
		if strings.HasPrefix(rest, r.original[len(debType)+1:]) {
			// Build the expected original full prefix in the line.
			origURIPrefix := r.original[len(debType)+1:]
			return strings.Replace(line, origURIPrefix, r.mirror[len(debType)+1:], 1)
		}
	}

	return line
}

// switchSourcesFile processes a DEB822 .sources file.
// Parses stanza-by-stanza and rewrites URIs.
func switchSourcesFile(path string) (bool, backupEntry, error) {
	if err := RejectSymlinkPath(path); err != nil {
		return false, backupEntry{}, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, backupEntry{}, nil
		}
		return false, backupEntry{}, err
	}

	newContent := rewriteSourcesContent(string(content))
	if newContent == string(content) {
		return false, backupEntry{}, nil
	}

	info, _ := os.Stat(path)
	mode := os.FileMode(0o644)
	if info != nil {
		mode = info.Mode()
	}

	bk, err := backupFile(path)
	if err != nil {
		return false, backupEntry{}, err
	}

	if err := WriteFileAtomically(path, []byte(newContent), mode); err != nil {
		return false, backupEntry{}, err
	}

	return true, bk, nil
}

// rewriteSourcesContent rewrites DEB822 .sources content stanza by stanza.
// Preserves blank lines between stanzas and trailing newlines.
func rewriteSourcesContent(content string) string {
	lines := strings.Split(content, "\n")
	var result []string
	var stanzaLines []string

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			// End of stanza: rewrite and emit
			if len(stanzaLines) > 0 {
				result = append(result, rewriteStanzaLines(stanzaLines)...)
				stanzaLines = nil
			}
			result = append(result, line) // preserve blank line
		} else {
			stanzaLines = append(stanzaLines, line)
		}
	}
	// Handle trailing stanza without trailing blank line
	if len(stanzaLines) > 0 {
		result = append(result, rewriteStanzaLines(stanzaLines)...)
	}

	return strings.Join(result, "\n")
}

// rewriteStanzaLines rewrites URIs in a stanza represented as a slice of lines.
func rewriteStanzaLines(lines []string) []string {
	// Check if this stanza is a security source
	joined := strings.Join(lines, "\n")
	if isSecuritySource(joined) {
		return lines
	}

	var result []string
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "URIs:") {
			result = append(result, rewriteURILine(line))
		} else {
			result = append(result, line)
		}
	}
	return result
}

// rewriteURILine rewrites a DEB822 URIs: line.
func rewriteURILine(line string) string {
	for _, r := range deb822URIReplacements {
		if strings.Contains(line, r.original) {
			line = strings.ReplaceAll(line, r.original, r.mirror)
		}
	}
	return line
}
