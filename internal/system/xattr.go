package system

import (
	"errors"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// CaptureFileXattrs snapshots extended attributes from an already-open file.
// Reading through the descriptor keeps the metadata tied to the same inode as
// the content snapshot. Filesystems without xattr support are treated as
// having no attributes, which keeps ordinary local files usable.
func CaptureFileXattrs(f *os.File) (map[string][]byte, error) {
	if f == nil {
		return nil, errors.New("file is nil")
	}
	size, err := unix.Flistxattr(int(f.Fd()), nil)
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, nil
	}
	namesBuf := make([]byte, size)
	if _, err := unix.Flistxattr(int(f.Fd()), namesBuf); err != nil {
		return nil, err
	}
	attrs := make(map[string][]byte)
	for _, rawName := range strings.Split(string(namesBuf), "\x00") {
		if rawName == "" {
			continue
		}
		valueSize, err := unix.Fgetxattr(int(f.Fd()), rawName, nil)
		if errors.Is(err, unix.ENODATA) {
			continue
		}
		if err != nil {
			return nil, err
		}
		value := make([]byte, valueSize)
		if _, err := unix.Fgetxattr(int(f.Fd()), rawName, value); err != nil {
			return nil, err
		}
		attrs[rawName] = value
	}
	return attrs, nil
}

func setFileXattrs(f *os.File, attrs map[string][]byte) error {
	if len(attrs) == 0 {
		return nil
	}
	for name, value := range attrs {
		if name == "" || strings.ContainsRune(name, '\x00') {
			return errors.New("invalid extended attribute name")
		}
		if err := unix.Fsetxattr(int(f.Fd()), name, value, 0); err != nil {
			return err
		}
	}
	return nil
}
