package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/modules"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

// testModule implements modules.Module for testing.
type testModule struct {
	id        string
	deps      []string
	defaultOn bool
	needsRoot bool
}

type configCapturingModule struct {
	testModule
	checkCfg  *types.Config
	satisfied bool
}

func (m *configCapturingModule) Check(_ context.Context, _ *system.Context, cfg *types.Config) modules.CheckResult {
	m.checkCfg = cfg
	return modules.CheckResult{Satisfied: m.satisfied}
}

func (m *testModule) ID() string             { return m.id }
func (m *testModule) Name() string           { return m.id }
func (m *testModule) Description() string    { return m.id }
func (m *testModule) DefaultEnabled() bool   { return m.defaultOn }
func (m *testModule) RequiresRoot() bool     { return m.needsRoot }
func (m *testModule) Dependencies() []string { return m.deps }
func (m *testModule) Check(context.Context, *system.Context, *types.Config) modules.CheckResult {
	return modules.CheckResult{Satisfied: false}
}
func (m *testModule) Plan(context.Context, *system.Context, *types.Config) ([]types.Step, error) {
	return nil, nil
}
func (m *testModule) Run(context.Context, *system.Context, *types.Config, *logging.Logger) error {
	return nil
}

// newTestRegistry creates a registry matching the real module layout.
func newTestRegistry() *modules.Registry {
	r := modules.NewRegistry()
	r.Register(&testModule{id: "base", defaultOn: true, needsRoot: true})
	r.Register(&testModule{id: "zellij", defaultOn: true, needsRoot: true, deps: []string{"base"}})
	r.Register(&testModule{id: "ssh", needsRoot: true})
	r.Register(&testModule{id: "node"})
	r.Register(&testModule{id: "ai", deps: []string{"node"}})
	r.Register(&testModule{id: "user", needsRoot: true})
	r.Register(&testModule{id: "ssh_keygen"})
	r.Register(&testModule{id: "docker", needsRoot: true, deps: []string{"base"}})
	r.Register(&testModule{id: "timezone", needsRoot: true})
	r.Register(&testModule{id: "fail2ban", needsRoot: true})
	return r
}

func TestMissingDependenciesUseTargetExecutionConfig(t *testing.T) {
	registry := modules.NewRegistry()
	dependency := &configCapturingModule{testModule: testModule{id: "base"}}
	target := &testModule{id: "docker", deps: []string{"base"}}
	registry.Register(dependency)
	registry.Register(target)
	cfg := &types.Config{AptMirror: "cernet", DockerUser: "alice"}

	missing := missingDependenciesForModule(context.Background(), registry, target, &system.Context{}, cfg)
	if len(missing) != 1 || missing[0] != "base" {
		t.Fatalf("missing = %v, want [base]", missing)
	}
	if dependency.checkCfg != cfg {
		t.Fatal("dependency Check did not receive the target execution config")
	}
}

// --- buildModuleList tests ---

func contains(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func TestBuildModuleList_UserMode_NoBase(t *testing.T) {
	r := newTestRegistry()
	ordered, err := buildModuleList(r, RunModeUser, []string{"node", "ssh_keygen"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if contains(ordered, "base") {
		t.Errorf("user mode should not inject base, got: %v", ordered)
	}
	if !contains(ordered, "node") {
		t.Errorf("expected node in ordered list, got: %v", ordered)
	}
	if !contains(ordered, "ssh_keygen") {
		t.Errorf("expected ssh_keygen in ordered list, got: %v", ordered)
	}
}

func TestBuildModuleList_FullMode_HasBase(t *testing.T) {
	r := newTestRegistry()
	ordered, err := buildModuleList(r, RunModeFull, []string{"node", "ssh_keygen"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(ordered, "base") {
		t.Errorf("full mode should inject base, got: %v", ordered)
	}
	if !contains(ordered, "zellij") {
		t.Errorf("full mode should inject zellij, got: %v", ordered)
	}
	// base should be first (registered first)
	if ordered[0] != "base" {
		t.Errorf("expected base first, got %q in %v", ordered[0], ordered)
	}
}

func TestBuildModuleList_AIAutoAddsNode(t *testing.T) {
	for _, mode := range []RunMode{RunModeUser, RunModeFull} {
		r := newTestRegistry()
		ordered, err := buildModuleList(r, mode, []string{"ai"})
		if err != nil {
			t.Fatalf("mode=%s: unexpected error: %v", mode, err)
		}
		if !contains(ordered, "node") {
			t.Errorf("mode=%s: selecting ai should auto-add node, got: %v", mode, ordered)
		}
		if !contains(ordered, "ai") {
			t.Errorf("mode=%s: ai should be in ordered list, got: %v", mode, ordered)
		}
		// node must come before ai
		nodeIdx, aiIdx := -1, -1
		for i, id := range ordered {
			if id == "node" {
				nodeIdx = i
			}
			if id == "ai" {
				aiIdx = i
			}
		}
		if nodeIdx >= aiIdx {
			t.Errorf("mode=%s: node (idx %d) must come before ai (idx %d)", mode, nodeIdx, aiIdx)
		}
	}
}

func TestBuildModuleList_AINotDuplicatedWhenNodeSelected(t *testing.T) {
	r := newTestRegistry()
	ordered, err := buildModuleList(r, RunModeUser, []string{"node", "ai"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	nodeCount := 0
	for _, id := range ordered {
		if id == "node" {
			nodeCount++
		}
	}
	if nodeCount != 1 {
		t.Errorf("node should appear exactly once, got %d times in %v", nodeCount, ordered)
	}
}

func TestBuildModuleList_FullMode_BaseNotDuplicatedIfSelected(t *testing.T) {
	r := newTestRegistry()
	ordered, err := buildModuleList(r, RunModeFull, []string{"base", "node"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	baseCount := 0
	for _, id := range ordered {
		if id == "base" {
			baseCount++
		}
	}
	if baseCount != 1 {
		t.Errorf("base should appear exactly once, got %d times in %v", baseCount, ordered)
	}
}

// --- checkFullModeRoot tests ---

func TestCheckFullModeRoot_RootUser_NoError(t *testing.T) {
	r := newTestRegistry()
	ordered := []string{"base", "node", "ai"}
	err := checkFullModeRoot(r, ordered, true)
	if err != nil {
		t.Errorf("root user should pass, got: %v", err)
	}
}

func TestCheckFullModeRoot_NonRoot_WithRootModules_Error(t *testing.T) {
	r := newTestRegistry()
	ordered := []string{"base", "node", "ai"}
	err := checkFullModeRoot(r, ordered, false)
	if err == nil {
		t.Fatal("non-root with base should return error")
	}
	if !strings.Contains(err.Error(), "base") {
		t.Errorf("error should mention 'base', got: %v", err)
	}
}

func TestCheckFullModeRoot_NonRoot_NoRootModules_OK(t *testing.T) {
	r := newTestRegistry()
	ordered := []string{"node", "ai", "ssh_keygen"}
	err := checkFullModeRoot(r, ordered, false)
	if err != nil {
		t.Errorf("non-root with only user-level modules should pass, got: %v", err)
	}
}

func TestCheckFullModeRoot_NonRoot_SSHModule_Error(t *testing.T) {
	r := newTestRegistry()
	ordered := []string{"ssh"}
	err := checkFullModeRoot(r, ordered, false)
	if err == nil {
		t.Fatal("non-root with ssh module should return error")
	}
	if !strings.Contains(err.Error(), "ssh") {
		t.Errorf("error should mention 'ssh', got: %v", err)
	}
}

func TestCheckRootRequirementsForMode_UserMode_SkipsFullModeCheck(t *testing.T) {
	r := newTestRegistry()
	if err := checkRootRequirementsForMode(RunModeUser, r, []string{"base", "node"}, false); err != nil {
		t.Fatalf("user mode should skip full-mode root check, got: %v", err)
	}
}

func TestCheckRootRequirementsForMode_FullMode_UsesFullModeCheck(t *testing.T) {
	r := newTestRegistry()
	err := checkRootRequirementsForMode(RunModeFull, r, []string{"base", "node"}, false)
	if err == nil {
		t.Fatal("full mode with base should return error")
	}
	if !strings.Contains(err.Error(), "base") {
		t.Errorf("error should mention 'base', got: %v", err)
	}
}
