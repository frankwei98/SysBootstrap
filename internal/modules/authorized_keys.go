package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

var githubUsernameRegex = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)

const authorizedKeysMaxRetries = 8

const authorizedKeysHelperEnv = "SYS_BOOTSTRAP_AUTHORIZED_KEYS_HELPER"

type authorizedKeysFileOps struct {
	acquireLease func(*os.File) error
	releaseLease func(*os.File) error
	write        func(*os.File, string) (int, error)
	sync         func(*os.File) error
	truncate     func(*os.File, int64) error
}

var defaultAuthorizedKeysFileOps = authorizedKeysFileOps{
	acquireLease: acquireAuthorizedKeysWriteLease,
	releaseLease: releaseAuthorizedKeysWriteLease,
	write: func(f *os.File, payload string) (int, error) {
		return f.WriteString(payload)
	},
	sync:     func(f *os.File) error { return f.Sync() },
	truncate: func(f *os.File, size int64) error { return f.Truncate(size) },
}

type authorizedKeysHelperRequest struct {
	Home string   `json:"home"`
	Dir  string   `json:"dir"`
	File string   `json:"file"`
	Keys []string `json:"keys"`
}

type authorizedKeysHelperResponse struct {
	Added   []string `json:"added,omitempty"`
	Changed bool     `json:"changed,omitempty"`
	Error   string   `json:"error,omitempty"`
}

func init() {
	mode := os.Getenv(authorizedKeysHelperEnv)
	if mode == "" {
		return
	}
	response := authorizedKeysHelperResponse{}
	if os.Geteuid() == 0 {
		response.Error = "authorized_keys helper refuses to run with root privileges"
	} else {
		var request authorizedKeysHelperRequest
		if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
			response.Error = fmt.Sprintf("decode helper request: %v", err)
		} else {
			switch mode {
			case "add":
				var err error
				response.Added, err = addAuthorizedKeysLocal(context.Background(), request.Home, request.Dir, request.File, request.Keys)
				response.Error = errorText(err)
			case "rollback":
				var err error
				response.Changed, err = rollbackAuthorizedKeyLines(request.File, request.Keys)
				response.Error = errorText(err)
			default:
				response.Error = fmt.Sprintf("unknown authorized_keys helper mode %q", mode)
			}
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(response)
	os.Exit(0)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// addAuthorizedKeys creates the user's SSH files if needed, then appends only
// keys absent from the file opened under an exclusive lock. The returned keys
// are the exact additions made to the inode still referenced by path.
func addAuthorizedKeys(ctx context.Context, username, home, dir, file string, keys []string) ([]string, error) {
	u, err := lookupUser(username)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve user %s: %w", username, err)
	}
	if u.Uid == "0" {
		return addAuthorizedKeysLocal(ctx, home, dir, file, keys)
	}
	response, err := runAuthorizedKeysHelper(ctx, username, home, "add", authorizedKeysHelperRequest{
		Home: home,
		Dir:  dir,
		File: file,
		Keys: keys,
	})
	return response.Added, err
}

func rollbackAuthorizedKeysForUser(ctx context.Context, username, home, file string, keys []string) (bool, error) {
	u, err := lookupUser(username)
	if err != nil {
		return false, fmt.Errorf("cannot resolve user %s: %w", username, err)
	}
	if u.Uid == "0" {
		return rollbackAuthorizedKeyLines(file, keys)
	}
	response, err := runAuthorizedKeysHelper(ctx, username, home, "rollback", authorizedKeysHelperRequest{
		Home: home,
		File: file,
		Keys: keys,
	})
	return response.Changed, err
}

func addAuthorizedKeysLocal(ctx context.Context, home, dir, file string, keys []string) ([]string, error) {
	wroteDetachedInode := false
	for attempt := 0; attempt < authorizedKeysMaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// A prior attempt may have observed a path replacement. Re-tighten the
		// directory and file reached by the current path before every retry.
		if err := ensureAuthorizedKeysFileLocal(home, dir, file); err != nil {
			return nil, err
		}
		added, retry, err := appendAuthorizedKeyLinesOnce(file, keys)
		if err != nil {
			return added, err
		}
		if retry {
			wroteDetachedInode = wroteDetachedInode || len(added) > 0
			continue
		}
		if wroteDetachedInode && len(added) == 0 {
			// The current inode already contains the key, but it may have been
			// supplied independently or copied from a detached inode. Attribution
			// is ambiguous, so fail without claiming it for rollback.
			return nil, fmt.Errorf("authorized_keys inode changed after a write and the final key ownership is ambiguous")
		}
		return added, nil
	}
	return nil, fmt.Errorf("authorized_keys path changed repeatedly while adding keys")
}

func runAuthorizedKeysHelper(ctx context.Context, username, home, mode string, request authorizedKeysHelperRequest) (authorizedKeysHelperResponse, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return authorizedKeysHelperResponse{}, err
	}
	executable, err := os.Executable()
	if err != nil {
		return authorizedKeysHelperResponse{}, fmt.Errorf("locate current executable: %w", err)
	}
	cmd := exec.CommandContext(ctx, "sudo", "-n", "-H", "-u", username,
		"env", "HOME="+home, "USER="+username, "LOGNAME="+username,
		authorizedKeysHelperEnv+"="+mode, executable)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return authorizedKeysHelperResponse{}, fmt.Errorf("authorized_keys helper failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	var response authorizedKeysHelperResponse
	if err := json.NewDecoder(&stdout).Decode(&response); err != nil {
		return authorizedKeysHelperResponse{}, fmt.Errorf("decode authorized_keys helper response: %w", err)
	}
	if response.Error != "" {
		return response, fmt.Errorf("%s", response.Error)
	}
	return response, nil
}

func ensureAuthorizedKeysFileLocal(home, dir, file string) error {
	if err := rejectAuthorizedKeysFullPath(home, ".ssh/authorized_keys"); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		if err == nil {
			err = fmt.Errorf("not a real directory")
		}
		return fmt.Errorf("cannot safely use %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("set permissions on %s: %w", dir, err)
	}

	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("create %s: %w", file, err)
		}
		f, err = os.OpenFile(file, os.O_WRONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return fmt.Errorf("open existing %s: %w", file, err)
		}
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		if err == nil {
			err = fmt.Errorf("not a regular file")
		}
		return fmt.Errorf("cannot safely use %s: %w", file, err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("set permissions on %s: %w", file, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", file, err)
	}
	return nil
}

func appendAuthorizedKeyLinesOnce(path string, keys []string) ([]string, bool, error) {
	return appendAuthorizedKeyLinesOnceWithOps(path, keys, defaultAuthorizedKeysFileOps)
}

func appendAuthorizedKeyLinesOnceWithOps(path string, keys []string, ops authorizedKeysFileOps) ([]string, bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, false, fmt.Errorf("lock %s: %w", path, err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = fmt.Errorf("not a regular file")
		}
		return nil, false, fmt.Errorf("cannot safely use %s: %w", path, err)
	}
	if err := f.Chmod(0o600); err != nil {
		return nil, false, fmt.Errorf("set permissions on %s: %w", path, err)
	}
	same, err := pathReferencesOpenFile(path, info)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, true, nil
		}
		return nil, false, err
	}
	if !same {
		return nil, true, nil
	}
	if err := ops.acquireLease(f); err != nil {
		return nil, false, fmt.Errorf("acquire exclusive write lease on %s: %w", path, err)
	}
	defer ops.releaseLease(f) //nolint:errcheck
	// A non-cooperating writer may have appended between the initial path
	// inspection and lease acquisition. Snapshot both identity and length only
	// after the lease excludes any further opens.
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = fmt.Errorf("not a regular file")
		}
		return nil, false, fmt.Errorf("cannot safely use %s after lease: %w", path, err)
	}
	same, err = pathReferencesOpenFile(path, info)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, true, nil
		}
		return nil, false, err
	}
	if !same {
		return nil, true, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, false, err
	}
	content, err := io.ReadAll(f)
	if err != nil {
		return nil, false, err
	}
	existing := authorizedKeyLines(content)
	var added []string
	for _, key := range keys {
		if containsKey(existing, key) {
			continue
		}
		added = append(added, key)
		existing = append(existing, key)
	}
	if len(added) == 0 {
		same, err = pathReferencesOpenFile(path, info)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, true, nil
			}
			return nil, false, err
		}
		return nil, !same, nil
	}
	payload := "\n" + strings.Join(added, "\n") + "\n"
	if err := appendAuthorizedKeysPayload(f, path, info.Size(), payload, ops); err != nil {
		return nil, false, fmt.Errorf("append keys to %s: %w", path, err)
	}
	same, err = pathReferencesOpenFile(path, info)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, true, nil
		}
		return added, false, err
	}
	if !same {
		return added, true, nil
	}
	return added, false, nil
}

// appendAuthorizedKeysPayload restores the original file length when the
// append or its durability sync fails. On Linux, the caller holds a kernel
// write lease, so even writers that ignore flock cannot open the file between
// the failed append and its restoration.
func appendAuthorizedKeysPayload(f *os.File, path string, originalSize int64, payload string, ops authorizedKeysFileOps) error {
	written, appendErr := ops.write(f, payload)
	if appendErr == nil && written != len(payload) {
		appendErr = io.ErrShortWrite
	}
	if appendErr == nil {
		appendErr = ops.sync(f)
	}
	if appendErr == nil {
		return nil
	}

	restoreErr := restoreFailedAuthorizedKeysAppend(f, path, originalSize, written, ops)
	return errors.Join(appendErr, restoreErr)
}

func restoreFailedAuthorizedKeysAppend(f *os.File, path string, originalSize int64, written int, ops authorizedKeysFileOps) error {
	if written < 0 {
		return fmt.Errorf("cannot restore %s after writer returned a negative byte count", path)
	}
	if err := ops.truncate(f, originalSize); err != nil {
		return fmt.Errorf("restore %s after failed append: %w", path, err)
	}
	if err := ops.sync(f); err != nil {
		return fmt.Errorf("sync restored %s: %w", path, err)
	}
	return nil
}

func pathReferencesOpenFile(path string, openInfo os.FileInfo) (bool, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular non-symlink file", path)
	}
	return os.SameFile(openInfo, pathInfo), nil
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
	data, err := readAuthorizedKeysContent(path)
	if err != nil {
		return nil, err
	}
	return authorizedKeyLines(data), nil
}

func readAuthorizedKeysContent(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	return data, nil
}

func authorizedKeyLines(data []byte) []string {
	var keys []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		keys = append(keys, line)
	}
	return keys
}

// containsKey reports whether the key slice contains a line matching k,
// comparing by (key type + base64 payload) ignoring trailing options/comments.
func containsKey(keys []string, k string) bool {
	keyID := authorizedKeyID(k)
	if keyID == "" {
		return false
	}
	for _, existing := range keys {
		if authorizedKeyID(existing) == keyID {
			return true
		}
	}
	return false
}

func authorizedKeyID(line string) string {
	key, ok := parseAuthorizedKeyLine(line)
	if !ok {
		return ""
	}
	return key
}

// parseAuthorizedKeyLine extracts the key type and base64 blob from either a
// bare public key or an authorized_keys line with quoted options. Comments and
// malformed entries are rejected. The returned value deliberately excludes
// options and comments so identity comparisons cannot add an unrestricted
// duplicate of an existing restricted key.
func parseAuthorizedKeyLine(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", false
	}
	fields := splitAuthorizedKeyFields(line)
	keyIndex := 0
	if len(fields) >= 3 && !supportedPublicKeyTypes[fields[0]] {
		if !validateAuthorizedKeyOptions(fields[0]) {
			return "", false
		}
		keyIndex = 1
	}
	if keyIndex+1 >= len(fields) || !supportedPublicKeyTypes[fields[keyIndex]] {
		return "", false
	}
	candidate := fields[keyIndex] + " " + fields[keyIndex+1]
	if ValidatePublicKey(candidate) {
		return candidate, true
	}
	return "", false
}

var authorizedKeyFlagOptions = map[string]bool{
	"agent-forwarding":    true,
	"cert-authority":      true,
	"no-agent-forwarding": true,
	"no-port-forwarding":  true,
	"no-pty":              true,
	"no-touch-required":   true,
	"no-user-rc":          true,
	"no-x11-forwarding":   true,
	"port-forwarding":     true,
	"pty":                 true,
	"restrict":            true,
	"user-rc":             true,
	"verify-required":     true,
	"x11-forwarding":      true,
}

var authorizedKeyValueOptions = map[string]bool{
	"command":      true,
	"environment":  true,
	"expiry-time":  true,
	"from":         true,
	"permitlisten": true,
	"permitopen":   true,
	"principals":   true,
	"tunnel":       true,
}

// validateAuthorizedKeyOptions accepts the option grammar documented by
// sshd(8): a comma-separated list of known flags or name="value" entries.
// Rejecting unknown prefixes is security-critical because OpenSSH ignores the
// entire line, which must not satisfy desired-state or access-path checks.
func validateAuthorizedKeyOptions(raw string) bool {
	options, ok := splitAuthorizedKeyOptions(raw)
	if !ok || len(options) == 0 {
		return false
	}
	for _, option := range options {
		name, value, hasValue := strings.Cut(option, "=")
		name = strings.ToLower(name)
		if hasValue {
			if !authorizedKeyValueOptions[name] || !validQuotedAuthorizedKeyOptionValue(value) {
				return false
			}
			continue
		}
		if !authorizedKeyFlagOptions[name] {
			return false
		}
	}
	return true
}

func splitAuthorizedKeyOptions(raw string) ([]string, bool) {
	var options []string
	start := 0
	inQuotes := false
	escaped := false
	for i, r := range raw {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inQuotes {
			escaped = true
			continue
		}
		if r == '"' {
			inQuotes = !inQuotes
			continue
		}
		if r == ',' && !inQuotes {
			if i == start {
				return nil, false
			}
			options = append(options, raw[start:i])
			start = i + 1
		}
	}
	if inQuotes || escaped || start >= len(raw) {
		return nil, false
	}
	options = append(options, raw[start:])
	return options, true
}

func validQuotedAuthorizedKeyOptionValue(value string) bool {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return false
	}
	escaped := false
	for i := 1; i < len(value)-1; i++ {
		switch value[i] {
		case '\\':
			escaped = !escaped
		case '"':
			if !escaped {
				return false
			}
			escaped = false
		default:
			escaped = false
		}
	}
	return !escaped
}

func selectAuthorizedKey(content []byte, preferred string) (string, error) {
	var keys []string
	for _, line := range strings.Split(string(content), "\n") {
		if key, ok := parseAuthorizedKeyLine(line); ok {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return "", fmt.Errorf("authorized_keys contains no valid public keys")
	}
	if strings.TrimSpace(preferred) == "" {
		return keys[0], nil
	}
	preferredLines, err := validateKeyLines(preferred)
	if err != nil {
		return "", fmt.Errorf("preferred public key is invalid: %w", err)
	}
	for _, preferredLine := range preferredLines {
		if containsKey(keys, preferredLine) {
			return authorizedKeyID(preferredLine), nil
		}
	}
	return "", fmt.Errorf("preferred public key is not present in authorized_keys")
}

func splitAuthorizedKeyFields(line string) []string {
	var fields []string
	start := -1
	inQuotes := false
	escaped := false
	for i, r := range line {
		if start < 0 {
			if r == ' ' || r == '\t' {
				continue
			}
			start = i
		}
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inQuotes {
			escaped = true
			continue
		}
		if r == '"' {
			inQuotes = !inQuotes
			continue
		}
		if (r == ' ' || r == '\t') && !inQuotes {
			fields = append(fields, line[start:i])
			start = -1
		}
	}
	if start >= 0 {
		fields = append(fields, line[start:])
	}
	return fields
}

// rollbackAuthorizedKeyLines disables only the exact lines installed by the
// current transaction. It edits one matching occurrence per recorded line in
// place as an equal-length comment, so another process can add the same public
// key with different restrictions or comments without having its line removed.
func rollbackAuthorizedKeyLines(path string, keys []string) (bool, error) {
	remove := make(map[string]int, len(keys))
	for _, key := range keys {
		line := strings.TrimSpace(key)
		if _, ok := parseAuthorizedKeyLine(line); ok {
			remove[line]++
		}
	}
	if len(remove) == 0 {
		return false, nil
	}
	for attempt := 0; attempt < authorizedKeysMaxRetries; attempt++ {
		remaining := make(map[string]int, len(remove))
		for line, count := range remove {
			remaining[line] = count
		}
		changed, retry, err := rollbackAuthorizedKeyLinesOnce(path, remaining)
		if err != nil {
			return changed, err
		}
		if !retry {
			return changed, nil
		}
	}
	return false, fmt.Errorf("authorized_keys path changed repeatedly during rollback")
}

func rollbackAuthorizedKeyLinesOnce(path string, remove map[string]int) (bool, bool, error) {
	return rollbackAuthorizedKeyLinesOnceWithOps(path, remove, defaultAuthorizedKeysFileOps)
}

func rollbackAuthorizedKeyLinesOnceWithOps(path string, remove map[string]int, ops authorizedKeysFileOps) (bool, bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return false, false, err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return false, false, fmt.Errorf("lock %s: %w", path, err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
	info, err := f.Stat()
	if err != nil {
		return false, false, err
	}
	if !info.Mode().IsRegular() {
		return false, false, fmt.Errorf("%s is not a regular file", path)
	}
	same, err := pathReferencesOpenFile(path, info)
	if err != nil {
		if os.IsNotExist(err) {
			return false, true, nil
		}
		return false, false, err
	}
	if !same {
		return false, true, nil
	}
	if err := ops.acquireLease(f); err != nil {
		return false, false, fmt.Errorf("acquire exclusive write lease on %s for rollback: %w", path, err)
	}
	defer ops.releaseLease(f) //nolint:errcheck
	// Revalidate the inode after the lease closes the window for writers that
	// do not cooperate with flock.
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = fmt.Errorf("not a regular file")
		}
		return false, false, fmt.Errorf("cannot safely use %s after lease: %w", path, err)
	}
	same, err = pathReferencesOpenFile(path, info)
	if err != nil {
		if os.IsNotExist(err) {
			return false, true, nil
		}
		return false, false, err
	}
	if !same {
		return false, true, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false, false, err
	}
	content, err := io.ReadAll(f)
	if err != nil {
		return false, false, err
	}
	offset := 0
	changed := false
	for _, segment := range strings.SplitAfter(string(content), "\n") {
		line := strings.TrimSuffix(segment, "\n")
		line = strings.TrimSuffix(line, "\r")
		if remove[line] > 0 {
			lineLength := len(segment)
			if strings.HasSuffix(segment, "\n") {
				lineLength--
			}
			original := []byte(segment[:lineLength])
			current := make([]byte, lineLength)
			if _, err := f.ReadAt(current, int64(offset)); err != nil {
				return changed, false, fmt.Errorf("recheck key line in %s: %w", path, err)
			}
			if !bytes.Equal(current, original) {
				return changed, false, fmt.Errorf("%s changed during authorized_keys rollback", path)
			}
			replacement := bytes.Repeat([]byte{' '}, lineLength)
			replacement[0] = '#'
			if _, err := f.WriteAt(replacement, int64(offset)); err != nil {
				return changed, false, fmt.Errorf("neutralize key line in %s: %w", path, err)
			}
			remove[line]--
			changed = true
		}
		offset += len(segment)
	}
	if changed {
		if err := f.Sync(); err != nil {
			return true, false, fmt.Errorf("sync %s: %w", path, err)
		}
	}
	same, err = pathReferencesOpenFile(path, info)
	if err != nil {
		if os.IsNotExist(err) {
			return changed, true, nil
		}
		return changed, false, err
	}
	if !same {
		return changed, true, nil
	}
	return changed, false, nil
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
		if id := authorizedKeyID(k); id != "" {
			seen[id] = true
		}
		b.WriteString(k)
		b.WriteString("\n")
	}

	for _, k := range newValid {
		if id := authorizedKeyID(k); id != "" && seen[id] {
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
