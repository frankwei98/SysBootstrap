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
// automatically including dependencies and applying conditional ordering.
func (r *Registry) ResolveOrder(ids []string) ([]string, error) {
	needed := make(map[string]bool)
	for _, id := range ids {
		needed[id] = true
	}

	// Expand static dependencies
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
	result := make([]string, 0, len(needed))
	for _, id := range r.order {
		if needed[id] {
			result = append(result, id)
		}
	}

	// Apply conditional ordering: reorder so deps come before dependents
	// only when both are present in the result.
	result = applyConditionalOrdering(result, r.conditionalDeps())

	return result, nil
}

// applyConditionalOrdering reorders the given slice so that for each
// conditional edge dependent→dep, dep appears before dependent if both are
// present. This is a stable bubble to satisfy the edge.
func applyConditionalOrdering(ids []string, deps map[string][]string) []string {
	result := make([]string, len(ids))
	copy(result, ids)

	for dependent, depIDs := range deps {
		depIdx := indexOf(dependent, result)
		if depIdx < 0 {
			continue
		}
		for _, dep := range depIDs {
			otherIdx := indexOf(dep, result)
			if otherIdx < 0 {
				continue
			}
			if depIdx < otherIdx {
				// dependent appears before dep — move dep earlier
				result = moveBefore(result, otherIdx, depIdx)
				depIdx = indexOf(dependent, result)
			}
		}
	}
	return result
}

func indexOf(id string, ids []string) int {
	for i, v := range ids {
		if v == id {
			return i
		}
	}
	return -1
}

func moveBefore(ids []string, from, before int) []string {
	if from <= before {
		return ids
	}
	v := ids[from]
	result := append(ids[:from], ids[from+1:]...)
	result = append(result[:before], append([]string{v}, result[before:]...)...)
	return result
}
