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
	registry *modules.Registry
	sys      *system.Context
	log      *logging.Logger
}

// NewRunner creates a new module runner.
func NewRunner(registry *modules.Registry, sys *system.Context, log *logging.Logger) *Runner {
	return &Runner{
		registry: registry,
		sys:      sys,
		log:      log,
	}
}

// Run executes the given modules in dependency order.
func (r *Runner) Run(ctx context.Context, cfg *types.Config, ids []string) error {
	ordered, err := r.registry.ResolveOrder(ids)
	if err != nil {
		return fmt.Errorf("dependency resolution failed: %w", err)
	}

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
		if ShouldSkipSatisfiedForModule(m.ID(), cfg, check) {
			r.log.Successf(i18n.T("runner_skipping"), m.Name())
			if check.Message != "" {
				r.log.Warn(check.Message)
			}
			continue
		}

		if err := m.Run(ctx, r.sys, cfg, r.log); err != nil {
			r.log.Errorf(i18n.T("runner_failed"), m.Name(), err)
			return fmt.Errorf("module %s failed: %w", m.Name(), err)
		}

		r.log.Successf(i18n.T("runner_completed"), m.Name())
	}

	r.log.SetModule("")
	return nil
}

func ShouldSkipSatisfiedForModule(moduleID string, cfg *types.Config, check modules.CheckResult) bool {
	if !check.Satisfied {
		return false
	}

	if moduleID == "ssh_keygen" {
		return false
	}

	return true
}
