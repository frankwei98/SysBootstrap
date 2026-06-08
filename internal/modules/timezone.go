package modules

import (
	"context"
	"fmt"
	"strings"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

const defaultTimezone = "Etc/UTC"

type TimezoneModule struct{}

func NewTimezoneModule() *TimezoneModule { return &TimezoneModule{} }

func (m *TimezoneModule) ID() string             { return "timezone" }
func (m *TimezoneModule) Name() string           { return "Timezone Configuration" }
func (m *TimezoneModule) Description() string    { return "Configure system timezone" }
func (m *TimezoneModule) DefaultEnabled() bool   { return false }
func (m *TimezoneModule) RequiresRoot() bool     { return true }
func (m *TimezoneModule) Dependencies() []string { return nil }

func (m *TimezoneModule) Check(ctx context.Context, sys *system.Context) CheckResult {
	current, ok := currentTimezone()
	if !system.CommandExists("timedatectl") {
		return CheckResult{Satisfied: false, Message: "timedatectl not available"}
	}
	if ok && current != "" {
		return CheckResult{Satisfied: current == defaultTimezone, Message: fmt.Sprintf("current timezone: %s", current)}
	}
	return CheckResult{Satisfied: false, Message: "timezone not detected"}
}

func (m *TimezoneModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	target := cfg.Timezone
	if target == "" {
		target = defaultTimezone
	}
	current, ok := currentTimezone()
	if ok && current == target {
		return nil, nil
	}
	return []types.Step{
		{Module: "timezone", Title: "Set system timezone", Detail: target},
	}, nil
}

func (m *TimezoneModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	if !system.CommandExists("timedatectl") {
		return fmt.Errorf("timedatectl is not available on this system")
	}
	target := cfg.Timezone
	if target == "" {
		target = defaultTimezone
	}
	current, ok := currentTimezone()
	if ok && current == target {
		log.Infof("Timezone already set to %s, skipping", target)
		return nil
	}
	log.Infof("Setting timezone to %s...", target)
	if res, err := system.Run("timedatectl", "set-timezone", target); err != nil || res.ExitCode != 0 {
		return system.FormatCommandError("failed to set timezone", res, err)
	}
	log.Successf("Timezone set to %s", target)
	return nil
}

func currentTimezone() (string, bool) {
	if !system.CommandExists("timedatectl") {
		return "", false
	}
	res, err := system.Run("timedatectl", "show", "--property=Timezone", "--value")
	if err != nil || res.ExitCode != 0 {
		return "", false
	}
	tz := strings.TrimSpace(res.Stdout)
	return tz, tz != ""
}
