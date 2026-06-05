package modules

import (
	"fmt"
)

// Registry holds all available modules.
type Registry struct {
	modules map[string]Module
	order   []string
}

// NewRegistry creates an empty module registry.
func NewRegistry() *Registry {
	return &Registry{
		modules: make(map[string]Module),
	}
}

// Register adds a module to the registry.
func (r *Registry) Register(m Module) {
	id := m.ID()
	if _, exists := r.modules[id]; !exists {
		r.order = append(r.order, id)
	}
	r.modules[id] = m
}

// Get returns a module by ID.
func (r *Registry) Get(id string) (Module, error) {
	m, ok := r.modules[id]
	if !ok {
		return nil, fmt.Errorf("unknown module: %s", id)
	}
	return m, nil
}

// All returns all modules in registration order.
func (r *Registry) All() []Module {
	result := make([]Module, 0, len(r.order))
	for _, id := range r.order {
		result = append(result, r.modules[id])
	}
	return result
}

// IDs returns all module IDs in registration order.
func (r *Registry) IDs() []string {
	return append([]string{}, r.order...)
}

// ResolveOrder returns the given module IDs in dependency-correct order,
// automatically including dependencies.
func (r *Registry) ResolveOrder(ids []string) ([]string, error) {
	needed := make(map[string]bool)
	for _, id := range ids {
		needed[id] = true
	}

	// Expand dependencies
	changed := true
	for changed {
		changed = false
		for id := range needed {
			m, ok := r.modules[id]
			if !ok {
				return nil, fmt.Errorf("unknown module: %s", id)
			}
			for _, dep := range m.Dependencies() {
				if !needed[dep] {
					needed[dep] = true
					changed = true
				}
			}
		}
	}

	// Return in registration order, filtered to needed
	var result []string
	for _, id := range r.order {
		if needed[id] {
			result = append(result, id)
		}
	}
	return result, nil
}
