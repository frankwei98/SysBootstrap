package app

import (
	"context"
	"encoding/json"
	"errors"
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
	warnings  []string
	steps     []types.Step
}

func (m *stubModule) ID() string             { return m.id }
func (m *stubModule) Name() string           { return m.name }
func (m *stubModule) Description() string    { return m.name }
func (m *stubModule) DefaultEnabled() bool   { return false }
func (m *stubModule) RequiresRoot() bool     { return false }
func (m *stubModule) Dependencies() []string { return m.deps }
func (m *stubModule) Check(_ context.Context, _ *system.Context, _ *types.Config) modules.CheckResult {
	return modules.CheckResult{Satisfied: m.satisfied, Message: m.checkMsg, Warnings: m.warnings}
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
		satisfied: false, checkMsg: "base packages missing",
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

	// Verify base is pending because its plan still contains an action.
	if plan.Modules[0].Status != "pending" {
		t.Errorf("base status = %q, want pending", plan.Modules[0].Status)
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
	if _, ok := parsed["counts"]; !ok {
		t.Error("JSON missing 'counts' field")
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

func TestGeneratePlan_PropagatesCancellation(t *testing.T) {
	registry := modules.NewRegistry()
	registry.Register(&stubModule{id: "base", name: "Base", steps: []types.Step{{Module: "base", Title: "work"}}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := GeneratePlan(ctx, &system.Context{}, &types.Config{}, registry, []string{"base"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GeneratePlan error = %v, want context.Canceled", err)
	}
}

func TestPlanTextFormat(t *testing.T) {
	i18n.SetLang(i18n.LangEN)

	r := modules.NewRegistry()
	r.Register(&stubModule{
		id: "base", name: "Base", satisfied: true,
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
	if !strings.Contains(text, "Overview:") {
		t.Errorf("plan text missing overview line:\n%s", text)
	}
	if !strings.Contains(text, "✓ os:") {
		t.Errorf("plan text should show check status icons:\n%s", text)
	}
	if !strings.Contains(text, "No actions required") {
		t.Errorf("plan text should mark satisfied modules as requiring no actions:\n%s", text)
	}
}

func TestPlanTextLocalizesEmptyModuleGuidance(t *testing.T) {
	i18n.SetLang(i18n.LangZhCN)
	t.Cleanup(func() { i18n.SetLang(i18n.LangEN) })

	plan := &PlanResult{
		Modules: []ModulePlan{
			{Name: "Base", Status: "satisfied"},
			{Name: "Create User", Status: "not_configured"},
		},
		Counts: PlanCounts{Satisfied: 1, NotConfigured: 1},
	}

	text := FormatPlanText(plan)
	for _, want := range []string{"无需执行任何操作", "等待交互输入"} {
		if !strings.Contains(text, want) {
			t.Errorf("Chinese plan text missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"No actions required", "Awaiting interactive input"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("Chinese plan text contains untranslated guidance %q:\n%s", unwanted, text)
		}
	}
}

func TestPlanReportsUnsatisfiedCheckWithPlannedActionsAsPending(t *testing.T) {
	i18n.SetLang(i18n.LangEN)

	r := modules.NewRegistry()
	r.Register(&stubModule{
		id:        "base",
		name:      "Base Environment",
		satisfied: false,
		checkMsg:  "CERNET mirror pending",
		steps: []types.Step{{
			Module: "base",
			Title:  "Switch APT mirror to CERNET",
		}},
	})

	plan, err := GeneratePlan(context.Background(), &system.Context{}, &types.Config{}, r, []string{"base"})
	if err != nil {
		t.Fatalf("GeneratePlan failed: %v", err)
	}

	if plan.Modules[0].Status != "pending" {
		t.Fatalf("base status = %q, want pending when plan has actions", plan.Modules[0].Status)
	}
	if plan.Counts.Pending != 1 || plan.Counts.Satisfied != 0 {
		t.Fatalf("plan counts = %+v, want 1 pending and 0 satisfied", plan.Counts)
	}
	if !strings.Contains(plan.Summary, "1 module(s) to execute, 0 already satisfied") {
		t.Fatalf("summary = %q, want pending action reflected", plan.Summary)
	}

	text := FormatPlanText(plan)
	if !strings.Contains(text, "Switch APT mirror to CERNET") {
		t.Fatalf("plan text hides planned action:\n%s", text)
	}
	if strings.Contains(text, "No actions required") {
		t.Fatalf("plan text says no actions are required despite planned action:\n%s", text)
	}
}

func TestPlanChecksReportUnknownUFWState(t *testing.T) {
	checks := buildPlanChecks(&system.Context{HasUFW: true, UFWStatusKnown: false})
	for _, check := range checks {
		if check.Name != "ufw" {
			continue
		}
		if check.Status != "warning" || !strings.Contains(check.Detail, "unknown") {
			t.Fatalf("UFW check = %+v, want warning with unknown detail", check)
		}
		return
	}
	t.Fatal("plan checks omitted installed UFW with unknown status")
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
	if !strings.Contains(text, "Awaiting interactive input") {
		t.Fatalf("plan text missing awaiting input helper:\n%s", text)
	}
}

func TestPlanMarksConfiguredUserModuleSatisfiedWhenNoStepsRemain(t *testing.T) {
	i18n.SetLang(i18n.LangEN)

	r := modules.NewRegistry()
	r.Register(&stubModule{
		id:        "user",
		name:      "Create User",
		satisfied: true,
		checkMsg:  "user alice exists and matches requested configuration",
	})

	plan, err := GeneratePlan(context.Background(), &system.Context{}, &types.Config{
		NewUsername:          "alice",
		UserShell:            "bash",
		UserAddSudo:          false,
		UserPasswordlessSudo: false,
	}, r, []string{"user"})
	if err != nil {
		t.Fatalf("GeneratePlan failed: %v", err)
	}

	if len(plan.Modules) != 1 {
		t.Fatalf("module count = %d, want 1", len(plan.Modules))
	}
	if plan.Modules[0].Status != "satisfied" {
		t.Fatalf("user status = %q, want satisfied", plan.Modules[0].Status)
	}
	if !strings.Contains(plan.Modules[0].CheckMessage, "user alice exists") {
		t.Fatalf("unexpected user check message: %q", plan.Modules[0].CheckMessage)
	}
}

func TestPlanPreservesWarningsForPendingModule(t *testing.T) {
	r := modules.NewRegistry()
	r.Register(&stubModule{
		id:        "example",
		name:      "Example",
		satisfied: false,
		checkMsg:  "configuration pending",
		warnings:  []string{"first warning", "second warning"},
	})

	plan, err := GeneratePlan(context.Background(), &system.Context{}, &types.Config{}, r, []string{"example"})
	if err != nil {
		t.Fatalf("GeneratePlan failed: %v", err)
	}
	warning := plan.Modules[0].Warning
	for _, want := range []string{"configuration pending", "first warning", "second warning"} {
		if !strings.Contains(warning, want) {
			t.Fatalf("warning = %q, want %q", warning, want)
		}
	}
}

func TestPlanReportsSatisfiedCheckWithStepsAsContractError(t *testing.T) {
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
	if plan.Modules[0].Status != "error" {
		t.Fatalf("timezone status = %q, want module contract error", plan.Modules[0].Status)
	}
	if !strings.Contains(plan.Modules[0].Warning, "reported satisfied but planned 1 action") {
		t.Fatalf("timezone warning = %q, want module contract guidance", plan.Modules[0].Warning)
	}
}

func TestPlanPreservesAuthoritativeUnsatisfiedCheckWhenNoStepsRemain(t *testing.T) {
	i18n.SetLang(i18n.LangEN)

	r := modules.NewRegistry()
	r.Register(&stubModule{
		id:        "ssh",
		name:      "SSH Hardening",
		satisfied: false,
		checkMsg:  "port 22122. service ready",
		steps:     nil,
	})
	r.Register(&stubModule{
		id:        "docker",
		name:      "Docker Environment",
		satisfied: false,
		checkMsg:  "docker state partially satisfied",
		steps:     nil,
	})

	plan, err := GeneratePlan(context.Background(), &system.Context{}, &types.Config{}, r, []string{"ssh", "docker"})
	if err != nil {
		t.Fatalf("GeneratePlan failed: %v", err)
	}
	if len(plan.Modules) != 2 {
		t.Fatalf("module count = %d, want 2", len(plan.Modules))
	}
	if plan.Modules[0].Status != "pending" {
		t.Fatalf("ssh status = %q, want pending from the module's authoritative check", plan.Modules[0].Status)
	}
	if plan.Modules[0].Warning != plan.Modules[0].CheckMessage {
		t.Fatalf("ssh warning = %q, want authoritative check message", plan.Modules[0].Warning)
	}
	if plan.Modules[1].Status != "pending" {
		t.Fatalf("docker status = %q, want pending from the module's authoritative check", plan.Modules[1].Status)
	}
	if plan.Modules[1].Warning != plan.Modules[1].CheckMessage {
		t.Fatalf("docker warning = %q, want authoritative check message", plan.Modules[1].Warning)
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

	text := FormatPlanText(plan)
	if strings.Count(text, "fail2ban missing. service disabled. sshd jail missing") != 1 {
		t.Fatalf("plan text should not repeat identical check and warning messages:\n%s", text)
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
