package cli

import (
	"os"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/i18n"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

func init() {
	i18n.SetLang(i18n.LangEN)
}

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

// --- Uninstall flag parsing and validation tests ---

func TestParseUninstallFlags_Empty(t *testing.T) {
	f := parseUninstallFlags(nil)
	if f.DryRun || f.All || f.Yes {
		t.Errorf("expected all false, got dryRun=%v all=%v yes=%v", f.DryRun, f.All, f.Yes)
	}
}

func TestParseUninstallFlags_All(t *testing.T) {
	f := parseUninstallFlags([]string{"--all", "--yes"})
	if !f.All || !f.Yes {
		t.Errorf("expected all=true yes=true, got all=%v yes=%v", f.All, f.Yes)
	}
}

func TestParseUninstallFlags_DryRun(t *testing.T) {
	f := parseUninstallFlags([]string{"--dry-run"})
	if !f.DryRun {
		t.Error("expected dryRun=true")
	}
	if f.All || f.Yes {
		t.Error("expected all=false yes=false")
	}
}

func TestParseUninstallFlags_Mixed(t *testing.T) {
	f := parseUninstallFlags([]string{"--all", "--dry-run", "--yes"})
	if !f.DryRun || !f.All || !f.Yes {
		t.Errorf("expected all true, got dryRun=%v all=%v yes=%v", f.DryRun, f.All, f.Yes)
	}
}

func TestValidateUninstallFlags_NonInteractive_AllWithoutYes(t *testing.T) {
	f := uninstallFlags{All: true, Yes: false}
	err := validateUninstallFlags(f, false)
	if err == nil {
		t.Error("expected error: --all without --yes in non-interactive mode")
	}
}

func TestValidateUninstallFlags_NonInteractive_AllWithYes(t *testing.T) {
	f := uninstallFlags{All: true, Yes: true}
	err := validateUninstallFlags(f, false)
	if err != nil {
		t.Errorf("expected no error for --all --yes non-interactive, got: %v", err)
	}
}

func TestValidateUninstallFlags_NonInteractive_NoFlags(t *testing.T) {
	f := uninstallFlags{}
	err := validateUninstallFlags(f, false)
	if err == nil {
		t.Error("expected error: no flags in non-interactive mode")
	}
}

func TestValidateUninstallFlags_NonInteractive_DryRun(t *testing.T) {
	// --dry-run is exempt from --all --yes requirement
	f := uninstallFlags{DryRun: true}
	err := validateUninstallFlags(f, false)
	if err != nil {
		t.Errorf("expected no error for --dry-run non-interactive, got: %v", err)
	}
}

func TestValidateUninstallFlags_Interactive_AllWithoutYes(t *testing.T) {
	// Interactive mode: --all without --yes is allowed (will ask confirmation)
	f := uninstallFlags{All: true, Yes: false}
	err := validateUninstallFlags(f, true)
	if err != nil {
		t.Errorf("expected no error for --all without --yes in interactive mode, got: %v", err)
	}
}

func TestValidateUninstallFlags_Interactive_NoFlags(t *testing.T) {
	// Interactive mode: no flags is allowed (will show multi-select)
	f := uninstallFlags{}
	err := validateUninstallFlags(f, true)
	if err != nil {
		t.Errorf("expected no error for no flags in interactive mode, got: %v", err)
	}
}
