package cli

import (
	"os"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/types"
)

func TestApplyAptMirrorEnv_Set(t *testing.T) {
	os.Setenv("SYS_BOOTSTRAP_APT_MIRROR", "cernet")
	defer os.Unsetenv("SYS_BOOTSTRAP_APT_MIRROR")

	cfg := &types.Config{}
	ok := applyAptMirrorEnv(cfg)
	if !ok {
		t.Error("expected true when env is set")
	}
	if cfg.AptMirror != "cernet" {
		t.Errorf("cfg.AptMirror = %q, want %q", cfg.AptMirror, "cernet")
	}
}

func TestApplyAptMirrorEnv_Unset(t *testing.T) {
	os.Unsetenv("SYS_BOOTSTRAP_APT_MIRROR")

	cfg := &types.Config{}
	ok := applyAptMirrorEnv(cfg)
	if ok {
		t.Error("expected false when env is unset")
	}
	if cfg.AptMirror != "" {
		t.Errorf("cfg.AptMirror = %q, want empty", cfg.AptMirror)
	}
}

func TestApplyAptMirrorEnv_UnknownValue(t *testing.T) {
	os.Setenv("SYS_BOOTSTRAP_APT_MIRROR", "invalid-mirror")
	defer os.Unsetenv("SYS_BOOTSTRAP_APT_MIRROR")

	cfg := &types.Config{}
	ok := applyAptMirrorEnv(cfg)
	if ok {
		t.Error("expected false for unknown mirror value")
	}
	if cfg.AptMirror != "" {
		t.Errorf("cfg.AptMirror = %q, want empty for unknown value", cfg.AptMirror)
	}
}
