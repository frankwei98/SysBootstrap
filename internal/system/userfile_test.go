package system

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenInvokingUserFileBeneathRejectsAncestorSymlink(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(base, "state")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenInvokingUserFileBeneath(base, "state/logs/app.log", 0o644); err == nil {
		t.Fatal("ancestor symlink should be rejected")
	}
	if _, err := os.Stat(filepath.Join(outside, "logs")); !os.IsNotExist(err) {
		t.Fatalf("outside tree was touched: %v", err)
	}
}

func TestOpenInvokingUserFileBeneathCreatesAndReopens(t *testing.T) {
	base := t.TempDir()
	f, err := OpenInvokingUserFileBeneath(base, "state/logs/app.log", 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("one\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	f, err = OpenInvokingUserFileBeneath(base, "state/logs/app.log", 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("two\n"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(base, "state/logs/app.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "one\ntwo\n" {
		t.Fatalf("unexpected contents: %q", data)
	}
}
