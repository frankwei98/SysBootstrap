package system

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// RejectSymlinkPath verifies that an existing path and every existing parent
// component are ordinary filesystem entries. It is intended for paths built
// from user-controlled home directories before a privileged operation such as
// MkdirAll, chown, rename, or recursive removal.
func RejectSymlinkPath(path string) error {
	if path == "" {
		return fmt.Errorf("path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("cannot resolve path %s: %w", path, err)
	}
	current := string(filepath.Separator)
	for _, component := range splitPathComponents(absolute) {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				// A missing component means all descendants are missing too;
				// callers may create them after this check.
				return nil
			}
			return fmt.Errorf("cannot inspect %s: %w", current, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if isPlatformManagedSymlink(current) {
				continue
			}
			return fmt.Errorf("refusing to use %s: path component is a symlink", current)
		}
	}
	return nil
}

func isPlatformManagedSymlink(path string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	switch filepath.Clean(path) {
	case "/home", "/tmp", "/var":
		// macOS commonly exposes these as links into its data volume. They are
		// system-managed roots; descendants are still checked normally.
		return true
	default:
		return false
	}
}

// RejectSymlinkPathBelow performs the same check for components beneath a
// trusted base directory. The base itself may be a platform-managed symlink
// (for example, macOS may link /home into its data volume), but user-created
// links below that directory are still rejected.
func RejectSymlinkPathBelow(base, path string) error {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return fmt.Errorf("cannot resolve base path %s: %w", base, err)
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("cannot resolve path %s: %w", path, err)
	}
	rel, err := filepath.Rel(filepath.Clean(absBase), filepath.Clean(absPath))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %s is outside base %s", path, base)
	}
	current := filepath.Clean(absBase)
	for _, component := range splitPathComponents(rel) {
		if component == "." || component == string(filepath.Separator) {
			continue
		}
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				return nil
			}
			return fmt.Errorf("cannot inspect %s: %w", current, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to use %s: path component is a symlink", current)
		}
	}
	return nil
}

func splitPathComponents(path string) []string {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	clean = clean[len(volume):]
	components := make([]string, 0)
	for clean != string(filepath.Separator) && clean != "." && clean != "" {
		component := filepath.Base(clean)
		components = append([]string{component}, components...)
		parent := filepath.Dir(clean)
		if parent == clean {
			break
		}
		clean = parent
	}
	return components
}

// ChownToInvokingUser gives files created by a sudo-launched process back to
// the non-root user who invoked it. Direct root runs intentionally retain root
// ownership. Callers should use this only for user-scoped state they created.
func ChownToInvokingUser(paths ...string) error {
	uid, gid, username, ok, err := invokingUserOwnership()
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return fmt.Errorf("cannot safely set ownership of %s: %w", path, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("cannot safely set ownership of %s: path is a symlink", path)
		}
		if err := RejectSymlinkPath(path); err != nil {
			return fmt.Errorf("cannot safely set ownership of %s: %w", path, err)
		}
		if err := os.Lchown(path, uid, gid); err != nil {
			return fmt.Errorf("cannot set ownership of %s to %s: %w", path, username, err)
		}
	}
	return nil
}

func invokingUserOwnership() (uid, gid int, username string, ok bool, err error) {
	if os.Geteuid() != 0 {
		return 0, 0, "", false, nil
	}
	username = os.Getenv("SUDO_USER")
	if username == "" || username == "root" {
		return 0, 0, "", false, nil
	}
	u, err := user.Lookup(username)
	if err != nil {
		return 0, 0, username, false, fmt.Errorf("cannot resolve invoking user %q: %w", username, err)
	}
	uid, err = strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, username, false, fmt.Errorf("invalid UID for invoking user %q: %w", username, err)
	}
	gid, err = strconv.Atoi(u.Gid)
	if err != nil {
		return 0, 0, username, false, fmt.Errorf("invalid GID for invoking user %q: %w", username, err)
	}
	return uid, gid, username, true, nil
}
