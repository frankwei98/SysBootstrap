package system

import (
	"runtime"
	"testing"
)

func TestNewContext(t *testing.T) {
	ctx, err := NewContext()
	if err != nil {
		// On macOS, /etc/os-release doesn't exist — this is expected
		if runtime.GOOS != "linux" {
			t.Skipf("Skipping on %s (no /etc/os-release): %v", runtime.GOOS, err)
		}
		t.Fatalf("NewContext() error: %v", err)
	}

	if ctx.CurrentUser == nil {
		t.Error("CurrentUser should not be nil")
	}

	if ctx.Arch == "" {
		t.Error("Arch should not be empty")
	}

	if runtime.GOOS == "linux" {
		if ctx.OSID == "" {
			t.Error("OSID should not be empty on Linux")
		}
	}
}

func TestCommandExists(t *testing.T) {
	if !CommandExists("go") {
		t.Error("expected 'go' command to exist")
	}

	if CommandExists("nonexistent_command_xyz_12345") {
		t.Error("expected nonexistent command to not exist")
	}
}
