package system

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWriteFileAtomicallyReplacesContentAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomically(path, []byte("new"), 0o640); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new" {
		t.Fatalf("content = %q, want new", content)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o, want 640", info.Mode().Perm())
	}
}

func TestWriteFileAtomicallyRejectsSymlinkDestination(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	link := filepath.Join(dir, "config")
	if err := os.WriteFile(victim, []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomically(link, []byte("overwritten"), 0o600); err == nil {
		t.Fatal("expected symlink destination rejection")
	}
	content, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "safe" {
		t.Fatalf("victim was modified: %q", content)
	}
}

func TestWriteFileAtomicallyWithOwnerAndXattrsRestoresMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := "user.sys-bootstrap-test"
	if runtime.GOOS == "darwin" {
		name = "com.sys-bootstrap.test"
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	value := []byte("preserve-me")
	setErr := unix.Fsetxattr(int(f.Fd()), name, value, 0)
	_ = f.Close()
	if errors.Is(setErr, unix.ENOTSUP) || errors.Is(setErr, unix.EOPNOTSUPP) || errors.Is(setErr, unix.EPERM) {
		t.Skipf("extended attributes unavailable: %v", setErr)
	}
	if setErr != nil {
		t.Fatalf("set extended attribute: %v", setErr)
	}

	if err := WriteFileAtomicallyWithOwnerAndXattrs(path, []byte("new"), 0o640, os.Getuid(), os.Getgid(), map[string][]byte{name: value}); err != nil {
		t.Fatal(err)
	}
	f, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(value))
	n, getErr := unix.Fgetxattr(int(f.Fd()), name, got)
	_ = f.Close()
	if getErr != nil {
		t.Fatalf("read restored extended attribute: %v", getErr)
	}
	if string(got[:n]) != string(value) {
		t.Fatalf("restored xattr = %q, want %q", got[:n], value)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o, want 640", info.Mode().Perm())
	}
}
