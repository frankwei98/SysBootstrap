package modules

import (
	"context"
	"testing"

	"github.com/FrankWiZe/sys-bootstrap/internal/logging"
	"github.com/FrankWiZe/sys-bootstrap/internal/system"
	"github.com/FrankWiZe/sys-bootstrap/internal/types"
)

type mockModule struct {
	id        string
	deps      []string
	defaultOn bool
	needsRoot bool
}

func (m *mockModule) ID() string             { return m.id }
func (m *mockModule) Name() string           { return m.id }
func (m *mockModule) Description() string    { return m.id }
func (m *mockModule) DefaultEnabled() bool   { return m.defaultOn }
func (m *mockModule) RequiresRoot() bool     { return m.needsRoot }
func (m *mockModule) Dependencies() []string { return m.deps }
func (m *mockModule) Check(ctx context.Context, sys *system.Context) CheckResult {
	return CheckResult{Satisfied: false}
}
func (m *mockModule) Plan(ctx context.Context, sys *system.Context, cfg *types.Config) ([]types.Step, error) {
	return nil, nil
}
func (m *mockModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	return nil
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	m := &mockModule{id: "test"}
	r.Register(m)

	got, err := r.Get("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID() != "test" {
		t.Errorf("expected ID 'test', got %q", got.ID())
	}
}

func TestRegistryGetUnknown(t *testing.T) {
	r := NewRegistry()
	_, err := r.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown module")
	}
}

func TestRegistryAll(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockModule{id: "a"})
	r.Register(&mockModule{id: "b"})
	r.Register(&mockModule{id: "c"})

	all := r.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 modules, got %d", len(all))
	}
	if all[0].ID() != "a" || all[1].ID() != "b" || all[2].ID() != "c" {
		t.Errorf("unexpected order: %v", moduleIDs(all))
	}
}

func TestRegistryIDs(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockModule{id: "x"})
	r.Register(&mockModule{id: "y"})

	ids := r.IDs()
	if len(ids) != 2 || ids[0] != "x" || ids[1] != "y" {
		t.Errorf("unexpected IDs: %v", ids)
	}
}

func TestRegistryResolveOrder(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockModule{id: "base"})
	r.Register(&mockModule{id: "node"})
	r.Register(&mockModule{id: "ai", deps: []string{"node"}})

	ordered, err := r.ResolveOrder([]string{"ai"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ordered) != 2 {
		t.Fatalf("expected 2 modules, got %d: %v", len(ordered), ordered)
	}
	if ordered[0] != "node" || ordered[1] != "ai" {
		t.Errorf("unexpected order: %v", ordered)
	}
}

func TestRegistryResolveOrderUnknown(t *testing.T) {
	r := NewRegistry()
	_, err := r.ResolveOrder([]string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown module")
	}
}

func TestRegistryDuplicateRegister(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockModule{id: "test"})
	r.Register(&mockModule{id: "test"})

	ids := r.IDs()
	if len(ids) != 1 {
		t.Errorf("expected 1 module, got %d: %v", len(ids), ids)
	}
}

func moduleIDs(mods []Module) []string {
	ids := make([]string, len(mods))
	for i, m := range mods {
		ids[i] = m.ID()
	}
	return ids
}
