package system

import (
	"os"
	"path/filepath"
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

func TestCommandExistsFallsBackToSbinPaths(t *testing.T) {
	dir := t.TempDir()
	cmdPath := filepath.Join(dir, "sshd")
	if err := os.WriteFile(cmdPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	orig := []string{"/usr/local/sbin", "/usr/sbin", "/sbin"}
	t.Cleanup(func() {
		sbinSearchPaths = orig
	})
	sbinSearchPaths = []string{dir}

	if !commandExists("sshd") {
		t.Fatal("expected commandExists to find sshd via sbin fallback path")
	}
}

func TestParseUFWActive(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want bool
	}{
		{"active", "Status: active\n", true},
		{"inactive", "Status: inactive\n", false},
		{"verbose active", "Status: active\nLogging: on (low)\n", true},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseUFWActive(tt.out); got != tt.want {
				t.Errorf("parseUFWActive(%q) = %v, want %v", tt.out, got, tt.want)
			}
		})
	}
}
