package modules

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var githubUsernameRegex = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)

const writeAuthKeysAsUserScript = `set -e
dir=$1; file=$2
mkdir -p -- "$dir"
chmod 700 -- "$dir"
umask 077
tmp=$(mktemp "${file}.tmp.XXXXXX")
trap 'rm -f -- "$tmp"' EXIT
cat > "$tmp"
chmod 600 -- "$tmp"
mv -f -- "$tmp" "$file"
trap - EXIT
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

// validatedKeyLinesAndFingerprints validates every non-blank line in content
// and computes its canonical SHA256 fingerprint. It returns an error if any
// line fails validation or no valid lines are present.
func validatedKeyLinesAndFingerprints(content string) ([]string, []string, error) {
	var valid []string
	var fingerprints []string
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !ValidatePublicKey(line) {
			return nil, nil, fmt.Errorf("invalid SSH public key: %q", line)
		}
		fingerprint, err := canonicalKeyFingerprint(context.Background(), line)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid SSH public key %q: %w", line, err)
		}
		valid = append(valid, line)
		fingerprints = append(fingerprints, fingerprint)
	}
	if len(valid) == 0 {
		return nil, nil, fmt.Errorf("no valid SSH public keys provided")
	}
	return valid, fingerprints, nil
}

// validateKeyLines validates every non-blank line in content and returns
// valid lines. Returns an error if any line fails validation or no valid lines.
func validateKeyLines(content string) ([]string, error) {
	valid, _, err := validatedKeyLinesAndFingerprints(content)
	return valid, err
}

// PublicKeyFingerprints returns the canonical SHA256 fingerprints for every
// validated key in content. It is used to let operators review fetched keys
// before authorizing their installation.
func PublicKeyFingerprints(content string) ([]string, error) {
	_, fingerprints, err := validatedKeyLinesAndFingerprints(content)
	return fingerprints, err
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

// ValidateGitHubUsername checks GitHub's documented username shape before it
// is interpolated into an HTTP path or presented to the operator.
func ValidateGitHubUsername(username string) bool {
	username = strings.TrimSpace(username)
	return githubUsernameRegex.MatchString(username) && !strings.Contains(username, "--")
}

// FetchGitHubKeys fetches public keys from github.com/<user>.keys with
// bounded response size, timeout, and per-line validation.
func FetchGitHubKeys(username string) (string, error) {
	username = strings.TrimSpace(username)
	if !ValidateGitHubUsername(username) {
		return "", fmt.Errorf("invalid GitHub username %q", username)
	}
	keysURL := fmt.Sprintf("https://github.com/%s.keys", url.PathEscape(username))

	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	resp, err := client.Get(keysURL)
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

	if _, err := validateKeyLines(string(body)); err != nil {
		return "", fmt.Errorf("invalid key line from GitHub for user %s: %w", username, err)
	}

	return string(body), nil
}
