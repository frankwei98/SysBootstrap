package app

import (
	"context"
	"errors"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/modules"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

type runnerTestModule struct {
	id        string
	satisfied bool
	runCalled bool
	steps     []types.Step
}

func (m *runnerTestModule) ID() string             { return m.id }
func (m *runnerTestModule) Name() string           { return m.id }
func (m *runnerTestModule) Description() string    { return m.id }
func (m *runnerTestModule) DefaultEnabled() bool   { return false }
func (m *runnerTestModule) RequiresRoot() bool     { return false }
func (m *runnerTestModule) Dependencies() []string { return nil }
func (m *runnerTestModule) Plan(context.Context, *system.Context, *types.Config) ([]types.Step, error) {
	return m.steps, nil
}
func (m *runnerTestModule) Check(context.Context, *system.Context) modules.CheckResult {
	return modules.CheckResult{Satisfied: m.satisfied, Message: "already exists"}
}
func (m *runnerTestModule) Run(context.Context, *system.Context, *types.Config, *logging.Logger) error {
	m.runCalled = true
	return nil
}

func TestShouldSkipSatisfiedForModule(t *testing.T) {
	check := modules.CheckResult{Satisfied: true}

	if !ShouldSkipSatisfiedForModule("base", &types.Config{}, check) {
		t.Fatal("expected satisfied base module to be skipped")
	}
	if ShouldSkipSatisfiedForModule("ssh_keygen", &types.Config{KeygenOverwrite: true}, check) {
		t.Fatal("expected ssh_keygen overwrite to force execution")
	}
	if ShouldSkipSatisfiedForModule("ssh_keygen", &types.Config{KeygenOverwrite: false}, check) {
		t.Fatal("expected ssh_keygen to always defer skip/overwrite behavior to Run()")
	}
	if !ShouldSkipSatisfiedForModule("timezone", &types.Config{Timezone: "Asia/Shanghai"}, check) {
		t.Fatal("expected satisfied timezone module state to be skipped before config-sensitive plan adjustment")
	}
}

type pendingModule struct {
	runnerTestModule
	returnPending bool
}

func (m *pendingModule) Run(ctx context.Context, sys *system.Context, cfg *types.Config, log *logging.Logger) error {
	m.runCalled = true
	if m.returnPending {
		return types.ErrSSHPendingConfirmation
	}
	return nil
}

func TestRunnerSSHPendingConfirmationContinues(t *testing.T) {
	registry := modules.NewRegistry()
	base := &pendingModule{runnerTestModule: runnerTestModule{id: "base", satisfied: false}, returnPending: false}
	sshMod := &pendingModule{
		runnerTestModule: runnerTestModule{
			id:        "ssh",
			satisfied: false,
			steps:     []types.Step{{Module: "ssh", Title: "Prepare SSH hardening"}},
		},
		returnPending: true,
	}
	registry.Register(base)
	registry.Register(sshMod)

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New() failed: %v", err)
	}
	defer log.Close()

	runner := NewRunner(registry, &system.Context{}, log)
	cfg := &types.Config{SSHPort: 22122}

	// ssh returns pending but base should still run first
	if err := runner.Run(context.Background(), cfg, []string{"base", "ssh"}); !errors.Is(err, types.ErrSSHPendingConfirmation) {
		t.Fatalf("Run() should preserve pending sentinel, got: %v", err)
	}
	if !base.runCalled {
		t.Fatal("expected base module Run() to be called")
	}
	if !sshMod.runCalled {
		t.Fatal("expected ssh module Run() to be called")
	}
}

func TestRunnerDoesNotSkipSSHKeygenOverwrite(t *testing.T) {
	registry := modules.NewRegistry()
	module := &runnerTestModule{id: "ssh_keygen", satisfied: true}
	registry.Register(module)

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New() failed: %v", err)
	}
	defer log.Close()

	runner := NewRunner(registry, &system.Context{}, log)
	cfg := &types.Config{KeygenOverwrite: true}

	if err := runner.Run(context.Background(), cfg, []string{"ssh_keygen"}); err != nil {
		t.Fatalf("Run() failed: %v", err)
	}
	if !module.runCalled {
		t.Fatal("expected ssh_keygen Run() to be called when overwrite is true")
	}
}

func TestRunnerExecutesConfigSensitiveModuleWhenPlanHasSteps(t *testing.T) {
	registry := modules.NewRegistry()
	module := &runnerTestModule{
		id:        "timezone",
		satisfied: true,
		steps:     []types.Step{{Module: "timezone", Title: "Set system timezone", Detail: "Asia/Shanghai"}},
	}
	registry.Register(module)

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New() failed: %v", err)
	}
	defer log.Close()

	runner := NewRunner(registry, &system.Context{}, log)
	cfg := &types.Config{Timezone: "Asia/Shanghai"}

	if err := runner.Run(context.Background(), cfg, []string{"timezone"}); err != nil {
		t.Fatalf("Run() failed: %v", err)
	}
	if !module.runCalled {
		t.Fatal("expected config-sensitive module to run when plan still has steps")
	}
}

func TestRunnerSkipsConfigSensitiveSSHWhenNoStepsRemain(t *testing.T) {
	registry := modules.NewRegistry()
	module := &runnerTestModule{
		id:        "ssh",
		satisfied: false,
		steps:     nil,
	}
	registry.Register(module)

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New() failed: %v", err)
	}
	defer log.Close()

	runner := NewRunner(registry, &system.Context{}, log)
	if err := runner.Run(context.Background(), &types.Config{}, []string{"ssh"}); err != nil {
		t.Fatalf("Run() failed: %v", err)
	}
	if module.runCalled {
		t.Fatal("expected satisfied ssh module to be skipped when no config-sensitive steps remain")
	}
}
