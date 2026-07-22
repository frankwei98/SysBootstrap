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

// conditionalDeps returns extra edges that apply only when both modules are
// in the selected set. The map key is the dependent; values are dependencies
// that must appear before the dependent when both are present.
func (r *Registry) conditionalDeps() map[string][]string {
	return map[string][]string{
		"ssh": {"user"},
	}
}

// ResolveOrder returns the given module IDs in dependency-correct order,
// automatically including dependencies and applying conditional ordering. It
// uses a DFS topological sort rather than depending on registration order.
func (r *Registry) ResolveOrder(ids []string) ([]string, error) {
	needed := make(map[string]bool)
	var include func(string) error
	include = func(id string) error {
		if needed[id] {
			return nil
		}
		m, ok := r.modules[id]
		if !ok {
			return fmt.Errorf("unknown module: %s", id)
		}
		needed[id] = true
		for _, dep := range m.Dependencies() {
			if err := include(dep); err != nil {
				return err
			}
		}
		return nil
	}
	for _, id := range ids {
		if err := include(id); err != nil {
			return nil, err
		}
	}

	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int, len(needed))
	result := make([]string, 0, len(needed))
	conditional := r.conditionalDeps()
	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case visiting:
			return fmt.Errorf("module dependency cycle detected at %s", id)
		case visited:
			return nil
		}
		m, ok := r.modules[id]
		if !ok {
			return fmt.Errorf("unknown module: %s", id)
		}
		state[id] = visiting
		deps := append([]string{}, m.Dependencies()...)
		for _, dep := range conditional[id] {
			if needed[dep] {
				deps = append(deps, dep)
			}
		}
		for _, dep := range deps {
			if !needed[dep] {
				continue
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[id] = visited
		result = append(result, id)
		return nil
	}

	// Iterating registration order keeps independent modules stable while the
	// DFS guarantees every dependency edge is honored.
	for _, id := range r.order {
		if needed[id] {
			if err := visit(id); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}
