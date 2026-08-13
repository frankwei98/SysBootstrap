package system

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveAllBeneathRemovesTreeWithoutFollowingDescendantSymlink(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "keep")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".local", "share", "pnpm")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "owned"), []byte("remove"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "outside")); err != nil {
		t.Fatal(err)
	}

	if err := RemoveAllBeneath(home, target); err != nil {
		t.Fatalf("RemoveAllBeneath failed: %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("target still exists: %v", err)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "keep" {
		t.Fatalf("outside sentinel changed: %q, %v", got, err)
	}
}

func TestRemoveAllBeneathRejectsSymlinkAncestor(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	targetOutside := filepath.Join(outside, "share", "pnpm")
	if err := os.MkdirAll(targetOutside, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(targetOutside, "keep")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".local")); err != nil {
		t.Fatal(err)
	}

	err := RemoveAllBeneath(home, filepath.Join(home, ".local", "share", "pnpm"))
	if err == nil {
		t.Fatal("symlink ancestor should be rejected")
	}
	if got, readErr := os.ReadFile(sentinel); readErr != nil || string(got) != "keep" {
		t.Fatalf("outside sentinel changed: %q, %v", got, readErr)
	}
}

func TestRemoveAllBeneathRejectsTargetSymlink(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "keep")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".nvm")
	if err := os.Symlink(outside, target); err != nil {
		t.Fatal(err)
	}

	if err := RemoveAllBeneath(home, target); err == nil {
		t.Fatal("target symlink should be rejected")
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "keep" {
		t.Fatalf("outside sentinel changed: %q, %v", got, err)
	}
}

func TestRemoveAllBeneathRejectsOutsideAndBase(t *testing.T) {
	home := t.TempDir()
	for _, target := range []string{home, filepath.Dir(home)} {
		if err := RemoveAllBeneath(home, target); err == nil {
			t.Fatalf("unsafe target %q was accepted", target)
		}
	}
}

func TestOpenPinnedRemovalDirRejectsAncestorSwapAfterResolution(t *testing.T) {
	root := t.TempDir()
	ancestor := filepath.Join(root, "ancestor")
	base := filepath.Join(ancestor, "home")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(filepath.Join(outside, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(ancestor, ancestor+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, ancestor); err != nil {
		t.Fatal(err)
	}

	f, _, err := openPinnedRemovalDir(resolved)
	if f != nil {
		_ = f.Close()
	}
	if err == nil {
		t.Fatal("opening a resolved base followed a swapped ancestor symlink")
	}
}
