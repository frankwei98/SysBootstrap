package modules

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

func TestTimezoneModuleInterface(t *testing.T) {
	m := NewTimezoneModule()
	if m.ID() != "timezone" {
		t.Fatalf("ID() = %q, want timezone", m.ID())
	}
	if !m.RequiresRoot() {
		t.Fatal("timezone module should require root")
	}
}

func TestTimezonePlanDefaultsToUTC(t *testing.T) {
	m := NewTimezoneModule()
	steps, err := m.Plan(context.Background(), &system.Context{}, &types.Config{})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(steps) > 1 {
		t.Fatalf("steps len = %d, want 0 or 1 depending on current timezone", len(steps))
	}
	if len(steps) == 1 && steps[0].Detail != "Etc/UTC" {
		t.Fatalf("timezone detail = %q, want Etc/UTC", steps[0].Detail)
	}
}

func TestTimezonePlanNoStepsWhenAlreadyTarget(t *testing.T) {
	if !system.CommandExists("timedatectl") {
		t.Skip("timedatectl not available")
	}
	current, ok := currentTimezone()
	if !ok || current == "" {
		t.Skip("current timezone not available")
	}

	m := NewTimezoneModule()
	steps, err := m.Plan(context.Background(), &system.Context{}, &types.Config{Timezone: current})
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	if len(steps) != 0 {
		t.Fatalf("steps len = %d, want 0 when target equals current timezone", len(steps))
	}
}

func TestTimezoneRunSetsRequestedTimezone(t *testing.T) {
	origPath := os.Getenv("PATH")
	t.Cleanup(func() {
		_ = os.Setenv("PATH", origPath)
	})

	tempBin := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "timezone-run.log")
	writeFakeCommand(t, tempBin, "timedatectl", `#!/bin/sh
echo "timedatectl $*" >> "$SYSBOOTSTRAP_TEST_LOG"
case "$1" in
  show)
    echo "Etc/UTC"
    exit 0
    ;;
  set-timezone)
    exit 0
    ;;
esac
exit 0
`)
	t.Setenv("SYSBOOTSTRAP_TEST_LOG", logFile)
	t.Setenv("PATH", tempBin+":"+origPath)

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New failed: %v", err)
	}
	defer log.Close()

	m := NewTimezoneModule()
	cfg := &types.Config{Timezone: "Asia/Shanghai"}
	if err := m.Run(context.Background(), &system.Context{}, cfg, log); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log file failed: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "timedatectl set-timezone Asia/Shanghai") {
		t.Fatalf("timezone run log missing set-timezone command:\n%s", text)
	}
}
