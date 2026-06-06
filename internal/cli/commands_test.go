package cli

import (
	"os"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/i18n"
	"github.com/frankwei98/sys-bootstrap/internal/settings"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

func init() {
	i18n.SetLang(i18n.LangEN)
}

// --- resolveAptMirror tests ---

func TestResolveAptMirror_EnvCernet(t *testing.T) {
	os.Setenv("SYS_BOOTSTRAP_APT_MIRROR", "cernet")
	defer os.Unsetenv("SYS_BOOTSTRAP_APT_MIRROR")

	cfg := &types.Config{}
	st := settings.Settings{}
	resolved, saveNeeded := resolveAptMirror(cfg, st, false)
	if !resolved {
		t.Error("expected resolved=true")
	}
	if saveNeeded {
		t.Error("expected saveNeeded=false")
	}
	if cfg.AptMirror != "cernet" {
		t.Errorf("cfg.AptMirror = %q, want cernet", cfg.AptMirror)
	}
}

func TestResolveAptMirror_EnvDefault(t *testing.T) {
	os.Setenv("SYS_BOOTSTRAP_APT_MIRROR", "default")
	defer os.Unsetenv("SYS_BOOTSTRAP_APT_MIRROR")

	cfg := &types.Config{}
	st := settings.Settings{AptMirror: "cernet"} // env should override
	resolved, _ := resolveAptMirror(cfg, st, false)
	if !resolved {
		t.Error("expected resolved=true")
	}
	if cfg.AptMirror != "" {
		t.Errorf("cfg.AptMirror = %q, want empty (default)", cfg.AptMirror)
	}
}

func TestResolveAptMirror_EnvUnknown(t *testing.T) {
	os.Setenv("SYS_BOOTSTRAP_APT_MIRROR", "invalid")
	defer os.Unsetenv("SYS_BOOTSTRAP_APT_MIRROR")

	cfg := &types.Config{}
	st := settings.Settings{AptMirror: "cernet"} // should fall through to settings
	resolved, _ := resolveAptMirror(cfg, st, false)
	if !resolved {
		t.Error("expected resolved=true from settings")
	}
	if cfg.AptMirror != "cernet" {
		t.Errorf("cfg.AptMirror = %q, want cernet (from settings)", cfg.AptMirror)
	}
}

func TestResolveAptMirror_SettingsCernet(t *testing.T) {
	os.Unsetenv("SYS_BOOTSTRAP_APT_MIRROR")

	cfg := &types.Config{}
	st := settings.Settings{AptMirror: "cernet"}
	resolved, saveNeeded := resolveAptMirror(cfg, st, false)
	if !resolved {
		t.Error("expected resolved=true")
	}
	if saveNeeded {
		t.Error("expected saveNeeded=false")
	}
	if cfg.AptMirror != "cernet" {
		t.Errorf("cfg.AptMirror = %q, want cernet", cfg.AptMirror)
	}
}

func TestResolveAptMirror_SettingsDefault(t *testing.T) {
	os.Unsetenv("SYS_BOOTSTRAP_APT_MIRROR")

	cfg := &types.Config{}
	st := settings.Settings{AptMirror: "default"}
	resolved, saveNeeded := resolveAptMirror(cfg, st, false)
	if !resolved {
		t.Error("expected resolved=true")
	}
	if saveNeeded {
		t.Error("expected saveNeeded=false")
	}
	if cfg.AptMirror != "" {
		t.Errorf("cfg.AptMirror = %q, want empty (default means no mirror)", cfg.AptMirror)
	}
}

func TestResolveAptMirror_Unset_Interactive(t *testing.T) {
	os.Unsetenv("SYS_BOOTSTRAP_APT_MIRROR")

	cfg := &types.Config{}
	st := settings.Settings{} // empty
	resolved, saveNeeded := resolveAptMirror(cfg, st, true)
	if resolved {
		t.Error("expected resolved=false (needs prompt)")
	}
	if !saveNeeded {
		t.Error("expected saveNeeded=true (should save after prompt)")
	}
}

func TestResolveAptMirror_Unset_NonInteractive(t *testing.T) {
	os.Unsetenv("SYS_BOOTSTRAP_APT_MIRROR")

	cfg := &types.Config{}
	st := settings.Settings{} // empty
	resolved, saveNeeded := resolveAptMirror(cfg, st, false)
	if resolved {
		t.Error("expected resolved=false")
	}
	if saveNeeded {
		t.Error("expected saveNeeded=false (non-interactive, can't prompt)")
	}
}

func TestResolveAptMirror_EnvOverridesSettings(t *testing.T) {
	os.Setenv("SYS_BOOTSTRAP_APT_MIRROR", "default")
	defer os.Unsetenv("SYS_BOOTSTRAP_APT_MIRROR")

	cfg := &types.Config{}
	st := settings.Settings{AptMirror: "cernet"} // settings says cernet
	resolved, _ := resolveAptMirror(cfg, st, false)
	if !resolved {
		t.Error("expected resolved=true")
	}
	if cfg.AptMirror != "" {
		t.Errorf("cfg.AptMirror = %q, want empty (env default overrides settings cernet)", cfg.AptMirror)
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
	f := uninstallFlags{DryRun: true}
	err := validateUninstallFlags(f, false)
	if err != nil {
		t.Errorf("expected no error for --dry-run non-interactive, got: %v", err)
	}
}

func TestValidateUninstallFlags_Interactive_AllWithoutYes(t *testing.T) {
	f := uninstallFlags{All: true, Yes: false}
	err := validateUninstallFlags(f, true)
	if err != nil {
		t.Errorf("expected no error for --all without --yes in interactive mode, got: %v", err)
	}
}

func TestValidateUninstallFlags_Interactive_NoFlags(t *testing.T) {
	f := uninstallFlags{}
	err := validateUninstallFlags(f, true)
	if err != nil {
		t.Errorf("expected no error for no flags in interactive mode, got: %v", err)
	}
}
