package app

import (
	"github.com/frankwei98/sys-bootstrap/internal/modules"
)

// NewRegistry creates a registry with all Phase 1 modules.
func NewRegistry() *modules.Registry {
	r := modules.NewRegistry()
	r.Register(modules.NewBaseModule())
	r.Register(modules.NewSSHModule())
	r.Register(modules.NewNodeModule())
	r.Register(modules.NewAIModule())
	r.Register(modules.NewUserModule())
	r.Register(modules.NewSSHKeygenModule())
	return r
}
