package system

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// WriteFileAtomically replaces path with data using a unique sibling file.
// Existing symlinks in the destination path are rejected before any write.
func WriteFileAtomically(path string, data []byte, mode os.FileMode) error {
	return writeReaderAtomically(path, bytes.NewReader(data), mode, false, nil, nil)
}

// WriteReaderAtomically replaces path with data read from reader using a
// unique sibling file. Existing symlinks in the destination path are rejected
// before any write, and the input is copied without buffering it in memory.
func WriteReaderAtomically(path string, reader io.Reader, mode os.FileMode) error {
	return writeReaderAtomically(path, reader, mode, false, nil, nil)
}

// WriteFileAtomicallyAsInvokingUser performs the same descriptor-relative
// replacement but gives a newly created user-scoped file to SUDO_USER.
func WriteFileAtomicallyAsInvokingUser(path string, data []byte, mode os.FileMode) error {
	return writeReaderAtomically(path, bytes.NewReader(data), mode, true, nil, nil)
}

// WriteFileAtomicallyWithOwner restores a file with the supplied Unix owner.
// It is used by rollback journals when the destination may have disappeared
// since the snapshot was captured. The owner is applied to the replacement
// inode before it becomes visible at path.
func WriteFileAtomicallyWithOwner(path string, data []byte, mode os.FileMode, uid, gid int) error {
	return WriteFileAtomicallyWithOwnerAndXattrs(path, data, mode, uid, gid, nil)
}

// WriteFileAtomicallyWithOwnerAndXattrs restores both Unix ownership and
// extended attributes captured from the original inode.
func WriteFileAtomicallyWithOwnerAndXattrs(path string, data []byte, mode os.FileMode, uid, gid int, xattrs map[string][]byte) error {
	if uid < 0 || gid < 0 {
		return fmt.Errorf("invalid file owner %d:%d", uid, gid)
	}
	return writeReaderAtomically(path, bytes.NewReader(data), mode, false, &fileOwner{uid: uid, gid: gid}, xattrs)
}

type fileOwner struct {
	uid int
	gid int
}

func writeReaderAtomically(path string, reader io.Reader, mode os.FileMode, preferInvokingUser bool, explicitOwner *fileOwner, xattrs map[string][]byte) error {
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
		if explicitOwner == nil {
			uid, gid = int(target.Uid), int(target.Gid)
		}
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
	if explicitOwner != nil {
		uid, gid = explicitOwner.uid, explicitOwner.gid
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
	if err := setFileXattrs(tmp, xattrs); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("restore extended attributes for %s: %w", path, err)
	}
	if _, err := io.Copy(tmp, reader); err != nil {
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
