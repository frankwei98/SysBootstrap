package system

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRejectSymlinkPathBelowRejectsDescendantLink(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, ".config", "sys-bootstrap", "config.env")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.RemoveAll(filepath.Join(base, ".config")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, ".config")); err != nil {
		t.Fatal(err)
	}
	if err := RejectSymlinkPathBelow(base, target); err == nil {
		t.Fatal("expected symlinked descendant to be rejected")
	}
}

func TestRejectSymlinkPathAllowsMissingLeaf(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "state", "new-file")
	if err := RejectSymlinkPath(target); err != nil {
		t.Fatalf("missing leaf should be creatable: %v", err)
	}
}
