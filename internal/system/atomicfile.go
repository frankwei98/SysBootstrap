package system

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomically replaces path with data using a unique sibling file.
// Existing symlinks in the destination path are rejected before any write.
func WriteFileAtomically(path string, data []byte, mode os.FileMode) error {
	if err := RejectSymlinkPath(path); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sys-bootstrap-write-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
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
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace %s atomically: %w", path, err)
	}
	return nil
}
