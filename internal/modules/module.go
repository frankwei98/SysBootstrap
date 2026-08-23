package modules

import (
	"context"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

// Module is the interface all provisioning modules must implement.
type Module interface {
	ID() string
	Name() string
	Description() string
	DefaultEnabled() bool
	RequiresRoot() bool
	Dependencies() []string
	Check(ctx context.Context, sys *system.Context, cfg *types.Config) CheckResult
	Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error)
	Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error
}

// CheckResult describes the current state of a module's prerequisites.
type CheckResult struct {
	Satisfied             bool
	DependenciesSatisfied bool
	Message               string
	Warnings              []string
}

// ReadyForDependencies reports whether this module can be used as a
// prerequisite even when it still has recurring work to perform.
func (r CheckResult) ReadyForDependencies() bool {
	return r.Satisfied || r.DependenciesSatisfied
}
