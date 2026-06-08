package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/i18n"
	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/modules"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
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
		steps: []types.Step{{Module: "ai", Title: "Install Claude Code", Detail: "@anthropic-ai/claude-code — pnpm when available, otherwise npm", Risk: "medium"}},
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
	if plan.SupportTier == "" {
		t.Error("expected support tier to be populated")
	}
	if len(plan.Checks) == 0 {
		t.Error("expected environment checks to be populated")
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
	if plan.Modules[2].Dependencies == nil || len(plan.Modules[2].Dependencies) != 1 || plan.Modules[2].Dependencies[0] != "node" {
		t.Errorf("ai dependencies = %v, want [node]", plan.Modules[2].Dependencies)
	}
	if plan.Modules[2].CheckMessage == "" {
		t.Error("expected ai check message to be populated")
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
	if _, ok := parsed["support_tier"]; !ok {
		t.Error("JSON missing 'support_tier' field")
	}
	if _, ok := parsed["checks"]; !ok {
		t.Error("JSON missing 'checks' field")
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
	i18n.SetLang(i18n.LangEN)

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
	if !containsAll(text, []string{"Environment checks", "Support tier"}) {
		t.Errorf("plan text missing new sections:\n%s", text)
	}
	if !strings.Contains(text, "No actions required") {
		t.Errorf("plan text should mark satisfied modules as requiring no actions:\n%s", text)
	}
}

func TestPlanMarksUserModuleNotConfiguredWithoutUsername(t *testing.T) {
	i18n.SetLang(i18n.LangEN)

	r := modules.NewRegistry()
	r.Register(modules.NewUserModule())

	plan, err := GeneratePlan(context.Background(), &system.Context{}, &types.Config{}, r, []string{"user"})
	if err != nil {
		t.Fatalf("GeneratePlan failed: %v", err)
	}

	if len(plan.Modules) != 1 {
		t.Fatalf("module count = %d, want 1", len(plan.Modules))
	}
	if plan.Modules[0].Status != "not_configured" {
		t.Fatalf("user status = %q, want not_configured", plan.Modules[0].Status)
	}
	if plan.Modules[0].CheckMessage != "No username configured yet" {
		t.Fatalf("user check message = %q", plan.Modules[0].CheckMessage)
	}
	if !strings.Contains(plan.Summary, "awaiting input") {
		t.Fatalf("summary = %q, want awaiting input", plan.Summary)
	}

	text := FormatPlanText(plan)
	if !strings.Contains(text, "Create User — not_configured") {
		t.Fatalf("plan text missing not_configured status:\n%s", text)
	}
}

func TestPlanUsesStepsForConfigSensitiveModules(t *testing.T) {
	i18n.SetLang(i18n.LangEN)

	r := modules.NewRegistry()
	r.Register(&stubModule{
		id:        "timezone",
		name:      "Timezone Configuration",
		satisfied: true,
		checkMsg:  "current timezone: Etc/UTC",
		steps:     []types.Step{{Module: "timezone", Title: "Set system timezone", Detail: "Asia/Shanghai"}},
	})

	plan, err := GeneratePlan(context.Background(), &system.Context{}, &types.Config{Timezone: "Asia/Shanghai"}, r, []string{"timezone"})
	if err != nil {
		t.Fatalf("GeneratePlan failed: %v", err)
	}
	if len(plan.Modules) != 1 {
		t.Fatalf("module count = %d, want 1", len(plan.Modules))
	}
	if plan.Modules[0].Status != "pending" {
		t.Fatalf("timezone status = %q, want pending when plan still has actions", plan.Modules[0].Status)
	}
}

func TestPlanMarksConfigSensitiveModuleSatisfiedWhenNoStepsRemain(t *testing.T) {
	i18n.SetLang(i18n.LangEN)

	r := modules.NewRegistry()
	r.Register(&stubModule{
		id:        "docker",
		name:      "Docker Environment",
		satisfied: false,
		checkMsg:  "docker state partially satisfied",
		steps:     nil,
	})

	plan, err := GeneratePlan(context.Background(), &system.Context{}, &types.Config{}, r, []string{"docker"})
	if err != nil {
		t.Fatalf("GeneratePlan failed: %v", err)
	}
	if len(plan.Modules) != 1 {
		t.Fatalf("module count = %d, want 1", len(plan.Modules))
	}
	if plan.Modules[0].Status != "satisfied" {
		t.Fatalf("docker status = %q, want satisfied when no config-sensitive steps remain", plan.Modules[0].Status)
	}
	if plan.Modules[0].Warning != "" {
		t.Fatalf("docker warning = %q, want empty", plan.Modules[0].Warning)
	}
}

func TestPlanPreservesSpecificCheckMessageForUnsatisfiedConfigSensitiveModule(t *testing.T) {
	i18n.SetLang(i18n.LangEN)

	r := modules.NewRegistry()
	r.Register(&stubModule{
		id:        "fail2ban",
		name:      "Fail2ban Protection",
		satisfied: false,
		checkMsg:  "fail2ban missing. service disabled. sshd jail missing",
		steps:     []types.Step{{Module: "fail2ban", Title: "Install fail2ban"}},
	})

	plan, err := GeneratePlan(context.Background(), &system.Context{}, &types.Config{}, r, []string{"fail2ban"})
	if err != nil {
		t.Fatalf("GeneratePlan failed: %v", err)
	}
	if len(plan.Modules) != 1 {
		t.Fatalf("module count = %d, want 1", len(plan.Modules))
	}
	if plan.Modules[0].CheckMessage != "fail2ban missing. service disabled. sshd jail missing" {
		t.Fatalf("check message = %q", plan.Modules[0].CheckMessage)
	}
	if plan.Modules[0].Warning != plan.Modules[0].CheckMessage {
		t.Fatalf("warning = %q, want same as check message", plan.Modules[0].Warning)
	}
}

func TestGeneratePlanDoesNotMutateSharedConfig(t *testing.T) {
	i18n.SetLang(i18n.LangEN)

	r := modules.NewRegistry()
	r.Register(&stubModule{
		id:        "ssh",
		name:      "SSH Hardening",
		satisfied: false,
		checkMsg:  "SSH configuration not yet applied",
		steps:     []types.Step{{Module: "ssh", Title: "Configure SSH port", Detail: "Set port to 22122"}},
	})

	cfg := &types.Config{}
	_, err := GeneratePlan(context.Background(), &system.Context{}, cfg, r, []string{"ssh"})
	if err != nil {
		t.Fatalf("GeneratePlan failed: %v", err)
	}
	if cfg.SSHPort != 0 {
		t.Fatalf("GeneratePlan mutated cfg.SSHPort to %d, want 0", cfg.SSHPort)
	}
}

func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
