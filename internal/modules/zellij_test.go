package modules

import "testing"

func TestZellijModuleMetadata(t *testing.T) {
	m := NewZellijModule()
	if m.ID() != "zellij" {
		t.Errorf("ID() = %q, want zellij", m.ID())
	}
	if !m.DefaultEnabled() {
		t.Error("zellij should remain enabled by default in full mode")
	}
	if !m.RequiresRoot() {
		t.Error("zellij should require root to install to /usr/local/bin")
	}
	if deps := m.Dependencies(); len(deps) != 1 || deps[0] != "base" {
		t.Errorf("Dependencies() = %v, want [base]", deps)
	}
}
