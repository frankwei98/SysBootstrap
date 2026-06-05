package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/FrankWiZe/sys-bootstrap/internal/logging"
	"github.com/FrankWiZe/sys-bootstrap/internal/modules"
	"github.com/FrankWiZe/sys-bootstrap/internal/system"
	"github.com/FrankWiZe/sys-bootstrap/internal/types"
)

// stubModule satisfies modules.Module for plan testing.
type stubModule struct {
	id        string
	name      string
	deps      []string
	satisfied bool
	checkMsg  string
	steps     []types.Step
}

func (m *stubModule) ID() string             { return m.id }
func (m *stubModule) Name() string           { return m.name }
func (m *stubModule) Description() string    { return m.name }
func (m *stubModule) DefaultEnabled() bool   { return false }
func (m *stubModule) RequiresRoot() bool     { return false }
func (m *stubModule) Dependencies() []string { return m.deps }
func (m *stubModule) Check(_ context.Context, _ *system.Context) modules.CheckResult {
	return modules.CheckResult{Satisfied: m.satisfied, Message: m.checkMsg}
}
func (m *stubModule) Plan(_ context.Context, _ *system.Context, _ *types.Config) ([]types.Step, error) {
	return m.steps, nil
}
func (m *stubModule) Run(_ context.Context, _ *system.Context, _ *types.Config, _ *logging.Logger) error {
	return nil
}

func TestPlanJSONStructure(t *testing.T) {
	r := modules.NewRegistry()
	r.Register(&stubModule{
		id: "base", name: "Base Environment",
		satisfied: true, checkMsg: "all installed",
		steps: []types.Step{{Module: "base", Title: "apt update", Detail: "update packages"}},
	})
	r.Register(&stubModule{
		id: "node", name: "Node.js Environment", deps: []string{},
		satisfied: false, checkMsg: "node missing",
		steps: []types.Step{{Module: "node", Title: "Install nvm", Detail: "v0.40.4"}},
	})
	r.Register(&stubModule{
		id: "ai", name: "AI CLI Tools", deps: []string{"node"},
		satisfied: false, checkMsg: "AI tools not yet installed",
		steps: []types.Step{{Module: "ai", Title: "Install Claude Code", Detail: "via pnpm", Risk: "medium"}},
	})

	ctx := context.Background()
	sys := &system.Context{}
	cfg := &types.Config{}

	plan, err := GeneratePlan(ctx, sys, cfg, r, []string{"base", "ai"})
	if err != nil {
		t.Fatalf("GeneratePlan failed: %v", err)
	}

	// Verify top-level structure
	if plan.Summary == "" {
		t.Error("expected non-empty summary")
	}

	if len(plan.Modules) != 3 {
		t.Fatalf("expected 3 modules (base, node, ai), got %d", len(plan.Modules))
	}

	// Verify module order: base → node → ai
	expectedOrder := []string{"base", "node", "ai"}
	for i, id := range expectedOrder {
		if plan.Modules[i].ID != id {
			t.Errorf("module[%d].ID = %q, want %q", i, plan.Modules[i].ID, id)
		}
	}

	// Verify base is satisfied
	if plan.Modules[0].Status != "satisfied" {
		t.Errorf("base status = %q, want satisfied", plan.Modules[0].Status)
	}

	// Verify node and ai are pending
	for _, mp := range plan.Modules[1:] {
		if mp.Status != "pending" {
			t.Errorf("%s status = %q, want pending", mp.ID, mp.Status)
		}
	}

	// Verify steps exist
	if len(plan.Modules[2].Steps) != 1 {
		t.Errorf("ai steps count = %d, want 1", len(plan.Modules[2].Steps))
	}
	if plan.Modules[2].Steps[0].Risk != "medium" {
		t.Errorf("ai step risk = %q, want medium", plan.Modules[2].Steps[0].Risk)
	}

	// Verify JSON round-trip contains expected fields
	jsonStr, err := FormatPlanJSON(plan)
	if err != nil {
		t.Fatalf("FormatPlanJSON failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("JSON parse failed: %v", err)
	}

	if _, ok := parsed["modules"]; !ok {
		t.Error("JSON missing 'modules' field")
	}
	if _, ok := parsed["summary"]; !ok {
		t.Error("JSON missing 'summary' field")
	}

	// Verify module entries have required fields
	modulesArr := parsed["modules"].([]interface{})
	firstMod := modulesArr[0].(map[string]interface{})
	for _, field := range []string{"id", "name", "steps", "status"} {
		if _, ok := firstMod[field]; !ok {
			t.Errorf("module JSON missing %q field", field)
		}
	}
}

func TestPlanTextFormat(t *testing.T) {
	r := modules.NewRegistry()
	r.Register(&stubModule{
		id: "base", name: "Base", satisfied: true,
		steps: []types.Step{{Module: "base", Title: "Step 1"}},
	})

	ctx := context.Background()
	plan, err := GeneratePlan(ctx, &system.Context{}, &types.Config{}, r, []string{"base"})
	if err != nil {
		t.Fatalf("GeneratePlan failed: %v", err)
	}

	text := FormatPlanText(plan)
	if text == "" {
		t.Error("expected non-empty plan text")
	}
}
