package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/app"
	"github.com/frankwei98/sys-bootstrap/internal/i18n"
	"github.com/frankwei98/sys-bootstrap/internal/modules"
	"github.com/frankwei98/sys-bootstrap/internal/settings"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

func TestNormalizeSSHRunnerErrorHandlesOnlyPurePendingAsSuccess(t *testing.T) {
	if err := normalizeSSHRunnerError(context.Background(), types.ErrSSHPendingConfirmation); err != nil {
		t.Fatalf("pure pending error = %v, want nil", err)
	}
}

func TestIsRecoverableRunnerFailure(t *testing.T) {
	if !isRecoverableRunnerFailure(fmt.Errorf("%w: zellij", app.ErrModulesFailed)) {
		t.Fatal("ErrModulesFailed should be treated as recoverable by RunCmd")
	}
	if isRecoverableRunnerFailure(context.Canceled) {
		t.Fatal("context cancellation must remain fatal")
	}
}

func TestNormalizeSSHRunnerErrorDoesNotSwallowTerminalErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want error
	}{
		{
			name: "cancellation joined with pending",
			err:  errors.Join(types.ErrSSHPendingConfirmation, context.Canceled),
			want: context.Canceled,
		},
		{
			name: "deadline joined with pending",
			err:  errors.Join(types.ErrSSHPendingConfirmation, context.DeadlineExceeded),
			want: context.DeadlineExceeded,
		},
		{
			name: "EOF joined with pending",
			err:  errors.Join(types.ErrSSHPendingConfirmation, io.EOF),
			want: io.EOF,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := normalizeSSHRunnerError(context.Background(), tt.err)
			if err == nil {
				t.Fatalf("normalizeSSHRunnerError returned nil, want %v", tt.want)
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("normalizeSSHRunnerError error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNormalizeSSHRunnerErrorCallerCancellationWinsPending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := normalizeSSHRunnerError(ctx, types.ErrSSHPendingConfirmation)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("normalizeSSHRunnerError error = %v, want context.Canceled", err)
	}
}

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
	f, err := parseUninstallFlags(nil)
	if err != nil {
		t.Fatalf("parseUninstallFlags: %v", err)
	}
	if f.DryRun || f.All || f.Yes {
		t.Errorf("expected all false, got dryRun=%v all=%v yes=%v", f.DryRun, f.All, f.Yes)
	}
}

func TestParseUninstallFlags_All(t *testing.T) {
	f, err := parseUninstallFlags([]string{"--all", "--yes"})
	if err != nil {
		t.Fatalf("parseUninstallFlags: %v", err)
	}
	if !f.All || !f.Yes {
		t.Errorf("expected all=true yes=true, got all=%v yes=%v", f.All, f.Yes)
	}
}

func TestParseUninstallFlags_DryRun(t *testing.T) {
	f, err := parseUninstallFlags([]string{"--dry-run"})
	if err != nil {
		t.Fatalf("parseUninstallFlags: %v", err)
	}
	if !f.DryRun {
		t.Error("expected dryRun=true")
	}
	if f.All || f.Yes {
		t.Error("expected all=false yes=false")
	}
}

func TestParseUninstallFlags_Mixed(t *testing.T) {
	f, err := parseUninstallFlags([]string{"--all", "--dry-run", "--yes"})
	if err != nil {
		t.Fatalf("parseUninstallFlags: %v", err)
	}
	if !f.DryRun || !f.All || !f.Yes {
		t.Errorf("expected all true, got dryRun=%v all=%v yes=%v", f.DryRun, f.All, f.Yes)
	}
}

func TestParseUninstallFlags_RejectsUnknownFlag(t *testing.T) {
	_, err := parseUninstallFlags([]string{"--dryrun", "--all", "--yes"})
	if err == nil {
		t.Fatal("expected misspelled --dryrun to be rejected")
	}
	if !strings.Contains(err.Error(), "--dryrun") {
		t.Fatalf("error %q does not name the rejected flag", err)
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

// --- resolveRunMode tests ---

func TestResolveRunMode_EnvUser(t *testing.T) {
	os.Setenv("SYS_BOOTSTRAP_RUN_MODE", "user")
	defer os.Unsetenv("SYS_BOOTSTRAP_RUN_MODE")

	mode, err := resolveRunMode(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != RunModeUser {
		t.Errorf("mode = %q, want user", mode)
	}
}

func TestResolveRunMode_EnvFull(t *testing.T) {
	os.Setenv("SYS_BOOTSTRAP_RUN_MODE", "full")
	defer os.Unsetenv("SYS_BOOTSTRAP_RUN_MODE")

	mode, err := resolveRunMode(false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != RunModeFull {
		t.Errorf("mode = %q, want full", mode)
	}
}

func TestResolveRunMode_EnvInvalid(t *testing.T) {
	os.Setenv("SYS_BOOTSTRAP_RUN_MODE", "invalid")
	defer os.Unsetenv("SYS_BOOTSTRAP_RUN_MODE")

	_, err := resolveRunMode(false)
	if err == nil {
		t.Error("expected error for invalid SYS_BOOTSTRAP_RUN_MODE")
	}
}

func TestDoctorCmdPrintsConclusion(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("doctor OS detection is exercised on Linux integration coverage")
	}
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	t.Cleanup(func() {
		os.Stdout = oldStdout
		r.Close()
		w.Close()
	})

	os.Stdout = w
	_, _ = DoctorCmd()
	w.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("ReadFrom failed: %v", err)
	}
	if !strings.Contains(buf.String(), "Conclusion:") {
		t.Fatalf("doctor output missing conclusion line:\n%s", buf.String())
	}
}

func TestResolveRunMode_NonInteractiveWithoutEnvFails(t *testing.T) {
	os.Unsetenv("SYS_BOOTSTRAP_RUN_MODE")

	_, err := resolveRunMode(false)
	if err == nil {
		t.Fatal("expected error when no run mode env is set and terminal is non-interactive")
	}
}

func TestIsInteractiveTerminalFalseForPipes(t *testing.T) {
	oldIn, oldOut, oldErr := os.Stdin, os.Stdout, os.Stderr
	inR, _, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdin failed: %v", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdout failed: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stderr failed: %v", err)
	}
	t.Cleanup(func() {
		os.Stdin, os.Stdout, os.Stderr = oldIn, oldOut, oldErr
		inR.Close()
		outR.Close()
		outW.Close()
		errR.Close()
		errW.Close()
	})

	os.Stdin = inR
	os.Stdout = outW
	os.Stderr = errW

	if isInteractiveTerminal() {
		t.Fatal("expected piped stdio to be treated as non-interactive")
	}
}

func TestResolveRunMode_NonInteractive_NoEnv(t *testing.T) {
	os.Unsetenv("SYS_BOOTSTRAP_RUN_MODE")

	_, err := resolveRunMode(false)
	if err == nil {
		t.Error("expected error when non-interactive and no env var")
	}
}

func TestRunCmdNonInteractiveFailsFastEvenWithRunModeEnv(t *testing.T) {
	t.Setenv("SYS_BOOTSTRAP_RUN_MODE", "user")

	oldStdin := os.Stdin
	stdin, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe stdin failed: %v", err)
	}
	if err := stdinWriter.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	t.Cleanup(func() {
		os.Stdin = oldStdin
		stdin.Close()
	})
	os.Stdin = stdin

	err = RunCmd(context.Background(), modules.NewRegistry())
	if err == nil {
		t.Fatal("expected run to reject non-interactive execution")
	}
	if !strings.Contains(err.Error(), "run requires an interactive terminal") {
		t.Fatalf("error = %q, want interactive terminal guidance", err)
	}
	if !strings.Contains(err.Error(), "SYS_BOOTSTRAP_RUN_MODE only preselects the mode") {
		t.Fatalf("error = %q, want run-mode environment variable semantics", err)
	}
}

func TestModuleDefaultConfigFail2banDoesNotSetSSHPort(t *testing.T) {
	cfg := moduleDefaultConfig("fail2ban", nil)
	if cfg.SSHPort != 0 {
		t.Fatalf("SSHPort = %d, want 0 for standalone fail2ban module defaults", cfg.SSHPort)
	}
	if cfg.Fail2banMaxRetry != 5 || cfg.Fail2banFindTime != "10m" || cfg.Fail2banBanTime != "1h" {
		t.Fatalf("unexpected fail2ban defaults: %#v", cfg)
	}
}

func TestModuleDefaultConfigDockerSetsTargetUser(t *testing.T) {
	cfg := moduleDefaultConfig("docker", nil)
	if cfg.DockerUser == "" {
		t.Fatal("DockerUser should default to a target username")
	}
	if cfg.SSHPort != 0 {
		t.Fatalf("SSHPort = %d, want 0 for standalone docker module defaults", cfg.SSHPort)
	}
}

func TestModuleDefaultConfigTimezonePreservesInteractiveDetection(t *testing.T) {
	cfg := moduleDefaultConfig("timezone", nil)
	if cfg.Timezone != "" {
		t.Fatalf("Timezone = %q, want empty so interactive form can default to detected current timezone", cfg.Timezone)
	}
}

func TestPreviewPlanConfigDoesNotSetSSHPort(t *testing.T) {
	cfg := previewPlanConfig(nil)
	if cfg.SSHPort != 0 {
		t.Fatalf("SSHPort = %d, want 0 for plan preview defaults", cfg.SSHPort)
	}
	if cfg.Timezone != "Etc/UTC" {
		t.Fatalf("Timezone = %q, want Etc/UTC", cfg.Timezone)
	}
}
