package modules

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const writeAuthKeysAsUserScript = `set -e
dir=$1; file=$2
mkdir -p -- "$dir"
chmod 700 -- "$dir"
tmp="${file}.tmp.$$"
cat > "$tmp"
chmod 600 -- "$tmp"
mv -f -- "$tmp" "$file"
`

// writeAuthorizedKeysAsUser atomically writes key content to the given file
// as the specified target user (via sudo -n -H -u), creating directories as needed.
// caller must pre-validate paths for symlink safety.
func writeAuthorizedKeysAsUser(ctx context.Context, username, home, dir, file, content string) error {
	cmd := exec.CommandContext(ctx, "sudo", "-n", "-H", "-u", username,
		"env", "HOME="+home, "USER="+username, "LOGNAME="+username,
		"sh", "-e", "-c", writeAuthKeysAsUserScript, "--", dir, file)
	cmd.Stdin = strings.NewReader(content)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		return fmt.Errorf("failed to write authorized_keys as user %s: %w (stderr: %s)", username, err, detail)
	}
	return nil
}

// resolveHome returns the passwd-resolved home directory for username.
func resolveHome(username string) (string, error) {
	u, err := lookupUser(username)
	if err != nil {
		return "", fmt.Errorf("cannot resolve user %s: %w", username, err)
	}
	return u.HomeDir, nil
}

// resolveUID returns the numeric UID and GID for username.
func resolveUID(username string) (int, int, error) {
	u, err := lookupUser(username)
	if err != nil {
		return 0, 0, fmt.Errorf("cannot resolve user %s: %w", username, err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return uid, gid, nil
}

// rejectAuthorizedKeysPath verifies that each path component beneath home
// is not a symlink. Path components are passed separately (not sub-paths).
// Non-existent components are acceptable.
func rejectAuthorizedKeysPath(home string, components ...string) error {
	current := home
	for _, c := range components {
		current = filepath.Join(current, c)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("cannot inspect %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to modify %s: it is a symlink", current)
		}
	}
	return nil
}

// rejectAuthorizedKeysFullPath verifies that the full path beneath home
// (e.g. ".ssh/authorized_keys") has no symlinks in any component.
func rejectAuthorizedKeysFullPath(home, relPath string) error {
	parts := strings.Split(relPath, string(filepath.Separator))
	return rejectAuthorizedKeysPath(home, parts...)
}

// readExistingKeys reads authorized_keys lines from a file, returning
// each non-empty, non-comment line as a trimmed string.
func readExistingKeys(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	var keys []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		keys = append(keys, line)
	}
	return keys, nil
}

// containsKey reports whether the key slice contains a line matching k,
// comparing by (key type + base64 payload) ignoring trailing options/comments.
func containsKey(keys []string, k string) bool {
	keyFields := strings.Fields(k)
	if len(keyFields) < 2 {
		return false
	}
	keyPayload := keyFields[0] + " " + keyFields[1]
	for _, existing := range keys {
		existingFields := strings.Fields(existing)
		if len(existingFields) >= 2 && existingFields[0]+" "+existingFields[1] == keyPayload {
			return true
		}
	}
	return false
}

// validateKeyLines validates every non-blank line in content and returns
// valid lines. Returns an error if any line fails validation or no valid lines.
func validateKeyLines(content string) ([]string, error) {
	var valid []string
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !ValidatePublicKey(line) {
			return nil, fmt.Errorf("invalid SSH public key: %q", line)
		}
		valid = append(valid, line)
	}
	if len(valid) == 0 {
		return nil, fmt.Errorf("no valid SSH public keys provided")
	}
	return valid, nil
}

// buildAuthorizedKeysContent merges existing keys with new valid keys,
// deduplicating by (key type + base64 payload). Returns the complete content.
func buildAuthorizedKeysContent(existing, newValid []string) string {
	seen := make(map[string]bool)
	var b strings.Builder

	for _, k := range existing {
		keyFields := strings.Fields(k)
		if len(keyFields) >= 2 {
			seen[keyFields[0]+" "+keyFields[1]] = true
		}
		b.WriteString(k)
		b.WriteString("\n")
	}

	for _, k := range newValid {
		keyFields := strings.Fields(k)
		if len(keyFields) >= 2 && seen[keyFields[0]+" "+keyFields[1]] {
			continue
		}
		b.WriteString(k)
		b.WriteString("\n")
	}

	return b.String()
}

// fetchGitHubKeys fetches public keys from github.com/<user>.keys with
// bounded response size, timeout, and per-line validation.
func fetchGitHubKeys(username string) (string, error) {
	url := fmt.Sprintf("https://github.com/%s.keys", username)

	client := &http.Client{
		Timeout: 15e9, // 15 seconds
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub returned status %d for user %s", resp.StatusCode, username)
	}

	// Bounded read: max 1MB
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if len(body) == 0 {
		return "", fmt.Errorf("no public keys found for GitHub user %s", username)
	}

	// Validate every non-blank line
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !ValidatePublicKey(line) {
			return "", fmt.Errorf("invalid key line from GitHub for user %s: %q", username, line)
		}
	}

	return string(body), nil
}
