package system

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// WriteFileAtomically replaces path with data using a unique sibling file.
// Existing symlinks in the destination path are rejected before any write.
func WriteFileAtomically(path string, data []byte, mode os.FileMode) error {
	return writeFileAtomically(path, data, mode, false)
}

// WriteFileAtomicallyAsInvokingUser performs the same descriptor-relative
// replacement but gives a newly created user-scoped file to SUDO_USER.
func WriteFileAtomicallyAsInvokingUser(path string, data []byte, mode os.FileMode) error {
	return writeFileAtomically(path, data, mode, true)
}

func writeFileAtomically(path string, data []byte, mode os.FileMode, preferInvokingUser bool) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	parent, err := openDirectoryFromRoot(filepath.Dir(abs))
	if err != nil {
		return fmt.Errorf("open parent directory for %s: %w", path, err)
	}
	defer parent.Close()
	name := filepath.Base(abs)

	uid, gid := -1, -1
	var target unix.Stat_t
	err = unix.Fstatat(int(parent.Fd()), name, &target, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		if target.Mode&unix.S_IFMT == unix.S_IFLNK {
			return fmt.Errorf("refusing to replace symlink %s", path)
		}
		uid, gid = int(target.Uid), int(target.Gid)
	} else if !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("inspect %s: %w", path, err)
	} else if preferInvokingUser {
		invokingUID, invokingGID, _, ok, lookupErr := invokingUserOwnership()
		if lookupErr != nil {
			return lookupErr
		}
		if ok {
			uid, gid = invokingUID, invokingGID
		}
	}

	tmpName, tmp, err := createAtomicTempAt(parent, mode.Perm())
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = unix.Unlinkat(int(parent.Fd()), tmpName, 0)
		}
	}()
	if uid >= 0 {
		if err := unix.Fchown(int(tmp.Fd()), uid, gid); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("set temporary file ownership for %s: %w", path, err)
		}
	}
	if err := tmp.Chmod(mode.Perm()); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set temporary file mode for %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary file for %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary file for %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file for %s: %w", path, err)
	}
	if err := unix.Renameat(int(parent.Fd()), tmpName, int(parent.Fd()), name); err != nil {
		return fmt.Errorf("replace %s atomically: %w", path, err)
	}
	removeTemp = false
	if err := unix.Fsync(int(parent.Fd())); err != nil {
		return fmt.Errorf("sync parent directory for %s: %w", path, err)
	}
	return nil
}

func createAtomicTempAt(parent *os.File, mode os.FileMode) (string, *os.File, error) {
	for attempt := 0; attempt < 16; attempt++ {
		var random [8]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, err
		}
		name := ".sys-bootstrap-write-" + hex.EncodeToString(random[:])
		fd, err := unix.Openat(int(parent.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(mode.Perm()))
		if err == nil {
			return name, os.NewFile(uintptr(fd), name), nil
		}
		if !errors.Is(err, unix.EEXIST) {
			return "", nil, err
		}
	}
	return "", nil, fmt.Errorf("could not allocate unique temporary file")
}
