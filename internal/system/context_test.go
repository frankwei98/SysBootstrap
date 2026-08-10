package system

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetectUFWStatusTreatsCommandFailureAsUnknown(t *testing.T) {
	original := ufwStatusOutputFn
	ufwStatusOutputFn = func() ([]byte, error) {
		return nil, errors.New("permission denied")
	}
	t.Cleanup(func() { ufwStatusOutputFn = original })

	active, known := detectUFWStatus()
	if known || active {
		t.Fatalf("detectUFWStatus() = active %v, known %v; want unknown", active, known)
	}
}

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

func TestSupportTier(t *testing.T) {
	tests := []struct {
		name string
		ctx  Context
		want SupportTier
	}{
		{
			name: "primary debian",
			ctx:  Context{OSID: "debian", OSVersionMajor: 12, HasApt: true},
			want: SupportTierPrimary,
		},
		{
			name: "apt compatible",
			ctx:  Context{OSID: "linuxmint", OSVersionMajor: 22, HasApt: true},
			want: SupportTierAptCompatible,
		},
		{
			name: "unsupported",
			ctx:  Context{OSID: "arch", HasApt: false},
			want: SupportTierUnsupported,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ctx.SupportTier(); got != tt.want {
				t.Fatalf("SupportTier() = %q, want %q", got, tt.want)
			}
		})
	}
}
