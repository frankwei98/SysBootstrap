package app

import (
	"runtime"
	"testing"
)

func TestGetVersion(t *testing.T) {
	v := GetVersion()

	if v.Version == "" {
		t.Error("Version should not be empty")
	}
	if v.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", v.GoVersion, runtime.Version())
	}
	if v.OS != runtime.GOOS {
		t.Errorf("OS = %q, want %q", v.OS, runtime.GOOS)
	}
	if v.Arch != runtime.GOARCH {
		t.Errorf("Arch = %q, want %q", v.Arch, runtime.GOARCH)
	}
}
