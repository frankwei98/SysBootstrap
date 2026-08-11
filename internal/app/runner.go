package app

import (
	"context"
	"errors"
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

// RunResult records non-fatal module failures from one run. Callers can use it
// to avoid launching a module whose prerequisite only produced a warning.
type RunResult struct {
	failedModules map[string]bool
}

// ModuleFailed reports whether a module failed or was skipped because one of
// its required modules failed during this run.
func (r *RunResult) ModuleFailed(moduleID string) bool {
	return r != nil && r.failedModules[moduleID]
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
	_, err := r.RunWithResult(ctx, cfg, ids)
	return err
}

// RunWithResult executes the given modules in dependency order and returns
// non-fatal failures alongside any fatal error.
func (r *Runner) RunWithResult(ctx context.Context, cfg *types.Config, ids []string) (*RunResult, error) {
	result := &RunResult{failedModules: make(map[string]bool)}
	ordered, err := r.registry.ResolveOrder(ids)
	if err != nil {
		return result, fmt.Errorf("dependency resolution failed: %w", err)
	}

	var pendingSSH bool
	for _, id := range ordered {
		if err := ctx.Err(); err != nil {
			return result, withSSHPending(err, pendingSSH)
		}

		m, err := r.registry.Get(id)
		if err != nil {
			return result, withSSHPending(err, pendingSSH)
		}

		if m.RequiresRoot() && !r.sys.IsRoot {
			return result, withSSHPending(fmt.Errorf(i18n.T("runner_module_needs_root"), m.Name()), pendingSSH)
		}

		r.log.SetModule(m.Name())
		if failedDeps := failedDependencies(m, result.failedModules); len(failedDeps) > 0 {
			r.log.Warnf(i18n.T("runner_skipping_failed_dependencies"), m.Name(), failedDeps)
			result.failedModules[m.ID()] = true
			continue
		}
		r.log.Infof(i18n.T("runner_starting"), m.Name())

		check := m.Check(ctx, r.sys, cfg)
		steps, planErr := m.Plan(ctx, r.sys, cfg)
		if planErr != nil {
			if err := ctx.Err(); err != nil {
				return result, withSSHPending(err, pendingSSH)
			}
			return result, withSSHPending(fmt.Errorf("module %s plan failed: %w", m.Name(), planErr), pendingSSH)
		}
		if contractErr := moduleStateContractError(m.Name(), check, steps); contractErr != nil {
			return result, contractErr
		}
		if check.Satisfied {
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
			if ctxErr := ctx.Err(); ctxErr != nil {
				return result, withSSHPending(ctxErr, pendingSSH)
			}
			if !ShouldWarnOnModuleFailure(m.ID(), ctx, err) {
				r.log.Errorf(i18n.T("runner_failed"), m.Name(), err)
				return result, withSSHPending(fmt.Errorf("module %s failed: %w", m.Name(), err), pendingSSH)
			}
			result.failedModules[m.ID()] = true
			r.log.Warnf(i18n.T("runner_failed_continue"), m.Name(), err)
			continue
		}

		r.log.Successf(i18n.T("runner_completed"), m.Name())
	}

	r.log.SetModule("")
	return result, withSSHPending(nil, pendingSSH)
}

func withSSHPending(err error, pending bool) error {
	if !pending {
		return err
	}
	if err == nil {
		return types.ErrSSHPendingConfirmation
	}
	return errors.Join(err, types.ErrSSHPendingConfirmation)
}

func failedDependencies(m modules.Module, failedModules map[string]bool) []string {
	var failed []string
	for _, dependency := range m.Dependencies() {
		if failedModules[dependency] {
			failed = append(failed, dependency)
		}
	}
	return failed
}

// ShouldWarnOnModuleFailure reports whether an installation failure may be
// downgraded to a warning. Security and configuration modules remain fatal;
// cancellation is always fatal so the caller's stop request is honored.
func ShouldWarnOnModuleFailure(moduleID string, ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	switch moduleID {
	case "zellij", "node", "ai", "docker", "fail2ban":
		return true
	default:
		return false
	}
}
