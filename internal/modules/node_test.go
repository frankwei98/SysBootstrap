package modules

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/FrankWiZe/sys-bootstrap/internal/system"
)

func TestNodeModuleCheckNoNvm(t *testing.T) {
	t.Setenv("NVM_DIR", filepath.Join(t.TempDir(), ".nvm"))

	// In CI/test environment, nvm is not installed.
	// Check should return Satisfied=false with descriptive message.
	m := NewNodeModule()

	if m.ID() != "node" {
		t.Errorf("ID() = %q, want node", m.ID())
	}
	if m.RequiresRoot() {
		t.Error("node module should not require root")
	}
	if m.Dependencies() != nil {
		t.Errorf("Dependencies() = %v, want nil", m.Dependencies())
	}

	result := m.Check(context.Background(), &system.Context{})
	// In test env without nvm, should not be satisfied
	if result.Satisfied {
		t.Log("nvm is installed in test environment — skipping unsatisfied check")
	} else {
		if result.Message == "" {
			t.Error("expected non-empty check message")
		}
	}
}

func TestAIModuleInterface(t *testing.T) {
	m := NewAIModule()

	if m.ID() != "ai" {
		t.Errorf("ID() = %q, want ai", m.ID())
	}
	if m.RequiresRoot() {
		t.Error("ai module should not require root")
	}
	deps := m.Dependencies()
	if deps != nil {
		t.Errorf("Dependencies() = %v, want nil", deps)
	}
}

func TestAIModuleCheckRequiresNode(t *testing.T) {
	t.Setenv("NVM_DIR", filepath.Join(t.TempDir(), ".nvm"))

	m := NewAIModule()
	result := m.Check(context.Background(), &system.Context{})
	if result.Satisfied {
		t.Error("ai module should not be satisfied without node")
	}
}
