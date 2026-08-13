package system

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const secureUserDirFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW

// OpenInvokingUserFileBeneath creates or opens a file relative to a pinned
// base directory. Every ancestor is opened with O_NOFOLLOW, so a privileged
// caller never follows a user-controlled symlink between validation and use.
// Newly created or updated entries are returned to SUDO_USER when applicable.
func OpenInvokingUserFileBeneath(baseDir, relativeFile string, mode os.FileMode) (*os.File, error) {
	components, err := safeRelativeComponents(relativeFile)
	if err != nil {
		return nil, err
	}
	base, err := openDirectoryFromRoot(baseDir)
	if err != nil {
		return nil, fmt.Errorf("open base directory %s: %w", baseDir, err)
	}
	defer base.Close()

	uid, gid, username, shouldChown, err := invokingUserOwnership()
	if err != nil {
		return nil, err
	}
	parent := base
	var closeParent func()
	closeParent = func() {
		if parent != base {
			_ = parent.Close()
		}
	}
	for _, component := range components[:len(components)-1] {
		child, err := openOrCreateDirectoryAt(parent, component)
		if err != nil {
			closeParent()
			return nil, fmt.Errorf("open directory %s: %w", component, err)
		}
		if shouldChown {
			if err := unix.Fchown(int(child.Fd()), uid, gid); err != nil {
				_ = child.Close()
				closeParent()
				return nil, fmt.Errorf("set ownership of %s to %s: %w", component, username, err)
			}
		}
		closeParent()
		parent = child
	}

	name := components[len(components)-1]
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_WRONLY|unix.O_APPEND|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(mode.Perm()))
	if err != nil {
		closeParent()
		return nil, fmt.Errorf("open file %s: %w", name, err)
	}
	f := os.NewFile(uintptr(fd), name)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		closeParent()
		if err == nil {
			err = fmt.Errorf("not a regular file")
		}
		return nil, fmt.Errorf("unsafe file %s: %w", name, err)
	}
	if err := f.Chmod(mode.Perm()); err != nil {
		_ = f.Close()
		closeParent()
		return nil, fmt.Errorf("set mode on %s: %w", name, err)
	}
	if shouldChown {
		if err := unix.Fchown(int(f.Fd()), uid, gid); err != nil {
			_ = f.Close()
			closeParent()
			return nil, fmt.Errorf("set ownership of %s to %s: %w", name, username, err)
		}
	}
	closeParent()
	return f, nil
}

// OpenExistingFileNoFollow opens an existing regular file by traversing every
// path component from a pinned root directory. It returns the open file and
// metadata from that same inode, avoiding read-then-stat races.
func OpenExistingFileNoFollow(path string) (*os.File, os.FileInfo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	base, err := openDirectoryFromRoot(filepath.Dir(abs))
	if err != nil {
		return nil, nil, err
	}
	defer base.Close()
	name := filepath.Base(abs)
	fd, err := unix.Openat(int(base.Fd()), name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		if err == nil {
			err = fmt.Errorf("not a regular file")
		}
		return nil, nil, err
	}
	return f, info, nil
}

func safeRelativeComponents(relative string) ([]string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return nil, fmt.Errorf("file path must be a non-empty relative path")
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("file path escapes base directory: %s", relative)
	}
	parts := strings.Split(clean, string(filepath.Separator))
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, fmt.Errorf("invalid file path component in %s", relative)
		}
	}
	return parts, nil
}

func openDirectoryFromRoot(path string) (*os.File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open(string(filepath.Separator), secureUserDirFlags, 0)
	if err != nil {
		return nil, err
	}
	current := os.NewFile(uintptr(fd), string(filepath.Separator))
	currentPath := string(filepath.Separator)
	for _, component := range splitPathComponents(abs) {
		componentPath := filepath.Join(currentPath, component)
		flags := secureUserDirFlags
		if isPlatformManagedSymlink(componentPath) {
			flags &^= unix.O_NOFOLLOW
		}
		childFD, err := unix.Openat(int(current.Fd()), component, flags, 0)
		if err != nil {
			_ = current.Close()
			return nil, err
		}
		child := os.NewFile(uintptr(childFD), component)
		if err := current.Close(); err != nil {
			_ = child.Close()
			return nil, err
		}
		current = child
		currentPath = componentPath
	}
	return current, nil
}

func openOrCreateDirectoryAt(parent *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, secureUserDirFlags, 0)
	if errors.Is(err, unix.ENOENT) {
		if mkdirErr := unix.Mkdirat(int(parent.Fd()), name, 0o755); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
			return nil, mkdirErr
		}
		fd, err = unix.Openat(int(parent.Fd()), name, secureUserDirFlags, 0)
	}
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}
