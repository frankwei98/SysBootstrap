package app

import (
	"context"
	"fmt"

	"github.com/frankwei98/sys-bootstrap/internal/i18n"
	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/modules"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

// Runner executes modules in order.
type Runner struct {
	registry      *modules.Registry
	sys           *system.Context
	log           *logging.Logger
	sshCheckpoint types.CheckpointFunc
}

// NewRunner creates a new module runner.
func NewRunner(registry *modules.Registry, sys *system.Context, log *logging.Logger) *Runner {
	return &Runner{
		registry: registry,
		sys:      sys,
		log:      log,
	}
}

// SetSSHCheckpoint sets the checkpoint function to use for the SSH module's
// two-phase confirmation flow. When set, the SSH module pauses after prepare
// and waits for the operator to test the new login before finalizing.
func (r *Runner) SetSSHCheckpoint(f types.CheckpointFunc) {
	r.sshCheckpoint = f
}

// Run executes the given modules in dependency order.
func (r *Runner) Run(ctx context.Context, cfg *types.Config, ids []string) error {
	ordered, err := r.registry.ResolveOrder(ids)
	if err != nil {
		return fmt.Errorf("dependency resolution failed: %w", err)
	}

	var pendingSSH bool
	for _, id := range ordered {
		m, err := r.registry.Get(id)
		if err != nil {
			return err
		}

		if m.RequiresRoot() && !r.sys.IsRoot {
			return fmt.Errorf(i18n.T("runner_module_needs_root"), m.Name())
		}

		r.log.SetModule(m.Name())
		r.log.Infof(i18n.T("runner_starting"), m.Name())

		check := m.Check(ctx, r.sys)
		steps, planErr := m.Plan(ctx, r.sys, cfg)
		if planErr != nil {
			return fmt.Errorf("module %s plan failed: %w", m.Name(), planErr)
		}
		if m.ID() == "user" {
			if userCheck, err := modules.DescribeUserCheckForConfig(cfg); err == nil {
				check = userCheck
			}
		}
		check = ApplyConfigSensitiveModuleState(m.ID(), check, steps)
		if ShouldSkipSatisfiedForModule(m.ID(), cfg, check) {
			r.log.Successf(i18n.T("runner_skipping"), m.Name())
			if check.Message != "" {
				r.log.Info(check.Message)
			}
			continue
		}

		// Inject checkpoint for SSH two-phase flow
		if sm, ok := m.(*modules.SSHModule); ok && r.sshCheckpoint != nil {
			sm.SetCheckpoint(r.sshCheckpoint)
		}

		err = m.Run(ctx, r.sys, cfg, r.log)
		if err == types.ErrSSHPendingConfirmation {
			r.log.Warnf("SSH %s: hardening prepared but pending operator confirmation.", m.Name())
			r.log.Warn("Test the new login from another terminal, then run the tool again to finalize.")
			pendingSSH = true
			continue
		}
		if err != nil {
			r.log.Errorf(i18n.T("runner_failed"), m.Name(), err)
			return fmt.Errorf("module %s failed: %w", m.Name(), err)
		}

		r.log.Successf(i18n.T("runner_completed"), m.Name())
	}

	r.log.SetModule("")
	if pendingSSH {
		return types.ErrSSHPendingConfirmation
	}
	return nil
}

func ApplyConfigSensitiveModuleState(moduleID string, check modules.CheckResult, steps []types.Step) modules.CheckResult {
	if !isConfigSensitivePlanModule(moduleID) {
		return check
	}
	if len(steps) > 0 {
		check.Satisfied = false
	} else {
		check.Satisfied = true
	}
	return check
}

func ShouldSkipSatisfiedForModule(moduleID string, cfg *types.Config, check modules.CheckResult) bool {
	if !check.Satisfied {
		return false
	}

	switch moduleID {
	case "ssh_keygen":
		return false
	case "base":
		if cfg.AptMirror != "" {
			return false
		}
	}

	return true
}
