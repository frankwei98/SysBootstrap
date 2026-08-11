package app

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/logging"
	"github.com/frankwei98/sys-bootstrap/internal/modules"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

type runnerTestModule struct {
	id        string
	deps      []string
	satisfied bool
	runCalled bool
	runErr    error
	steps     []types.Step
}

func (m *runnerTestModule) ID() string             { return m.id }
func (m *runnerTestModule) Name() string           { return m.id }
func (m *runnerTestModule) Description() string    { return m.id }
func (m *runnerTestModule) DefaultEnabled() bool   { return false }
func (m *runnerTestModule) RequiresRoot() bool     { return false }
func (m *runnerTestModule) Dependencies() []string { return m.deps }
func (m *runnerTestModule) Plan(context.Context, *system.Context, *types.Config) ([]types.Step, error) {
	return m.steps, nil
}
func (m *runnerTestModule) Check(context.Context, *system.Context, *types.Config) modules.CheckResult {
	return modules.CheckResult{Satisfied: m.satisfied, Message: "already exists"}
}
func (m *runnerTestModule) Run(context.Context, *system.Context, *types.Config, *logging.Logger) error {
	m.runCalled = true
	return m.runErr
}

type capturedRunnerLog struct {
	t        *testing.T
	original *os.File
	reader   *os.File
	writer   *os.File
	log      *logging.Logger
	closed   bool
}

func newCapturedRunnerLog(t *testing.T) *capturedRunnerLog {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() failed: %v", err)
	}

	capture := &capturedRunnerLog{
		t:        t,
		original: os.Stdout,
		reader:   reader,
		writer:   writer,
	}
	os.Stdout = writer
	log, err := logging.New(false)
	if err != nil {
		os.Stdout = capture.original
		_ = reader.Close()
		_ = writer.Close()
		t.Fatalf("logging.New() failed: %v", err)
	}
	capture.log = log
	t.Cleanup(capture.close)
	return capture
}

func (c *capturedRunnerLog) close() {
	if c.closed {
		return
	}
	c.closed = true
	c.log.Close()
	_ = c.writer.Close()
	os.Stdout = c.original
}

func (c *capturedRunnerLog) Output() string {
	c.t.Helper()
	c.close()
	output, err := io.ReadAll(c.reader)
	if err != nil {
		c.t.Fatalf("reading captured output failed: %v", err)
	}
	if err := c.reader.Close(); err != nil {
		c.t.Fatalf("closing captured output failed: %v", err)
	}
	return string(output)
}

func TestShouldWarnOnModuleFailure(t *testing.T) {
	installErr := errors.New("installation failed")
	for _, moduleID := range []string{"zellij", "node", "ai", "docker", "fail2ban"} {
		if !ShouldWarnOnModuleFailure(moduleID, context.Background(), installErr) {
			t.Fatalf("%s installation failure should be reported as a warning", moduleID)
		}
	}
	for _, moduleID := range []string{"base", "ssh", "user", "ssh_keygen", "timezone"} {
		if ShouldWarnOnModuleFailure(moduleID, context.Background(), installErr) {
			t.Fatalf("%s failure must remain fatal", moduleID)
		}
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if ShouldWarnOnModuleFailure("zellij", cancelled, installErr) {
		t.Fatal("cancellation must remain fatal for optional installation modules")
	}
	if ShouldWarnOnModuleFailure("zellij", context.Background(), context.Canceled) {
		t.Fatal("wrapped cancellation must remain fatal for optional installation modules")
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

func TestRunnerSSHPendingConfirmationStopsFollowingModules(t *testing.T) {
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
	following := &runnerTestModule{id: "node"}
	registry.Register(base)
	registry.Register(sshMod)
	registry.Register(following)

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New() failed: %v", err)
	}
	defer log.Close()

	runner := NewRunner(registry, &system.Context{}, log)
	cfg := &types.Config{SSHPort: 22122}

	if err := runner.Run(context.Background(), cfg, []string{"base", "ssh", "node"}); !errors.Is(err, types.ErrSSHPendingConfirmation) {
		t.Fatalf("Run() should preserve pending sentinel, got: %v", err)
	}
	if !base.runCalled {
		t.Fatal("expected base module Run() to be called")
	}
	if !sshMod.runCalled {
		t.Fatal("expected ssh module Run() to be called")
	}
	if following.runCalled {
		t.Fatal("module after pending SSH must not run")
	}
}

func TestRunnerDoesNotCombinePendingSSHWithLaterFailure(t *testing.T) {
	registry := modules.NewRegistry()
	sshMod := &pendingModule{
		runnerTestModule: runnerTestModule{
			id:    "ssh",
			steps: []types.Step{{Module: "ssh", Title: "Prepare SSH hardening"}},
		},
		returnPending: true,
	}
	fatalErr := errors.New("apt failed")
	base := &runnerTestModule{id: "base", runErr: fatalErr}
	registry.Register(sshMod)
	registry.Register(base)

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New() failed: %v", err)
	}
	defer log.Close()

	runner := NewRunner(registry, &system.Context{}, log)
	err = runner.Run(context.Background(), &types.Config{SSHPort: 22122}, []string{"ssh", "base"})
	if !errors.Is(err, types.ErrSSHPendingConfirmation) {
		t.Fatalf("Run() error = %v, want SSH pending sentinel", err)
	}
	if errors.Is(err, fatalErr) {
		t.Fatalf("Run() error = %v, must not include an unexecuted later failure", err)
	}
	if base.runCalled {
		t.Fatal("later fatal module must not run after SSH becomes pending")
	}
}

func TestRunnerWarnsAndContinuesAfterNonBaseModuleFailure(t *testing.T) {
	registry := modules.NewRegistry()
	base := &runnerTestModule{id: "base", satisfied: true}
	zellij := &runnerTestModule{id: "zellij", deps: []string{"base"}, runErr: errors.New("checksum mismatch")}
	following := &runnerTestModule{id: "node"}
	registry.Register(base)
	registry.Register(zellij)
	registry.Register(following)

	capture := newCapturedRunnerLog(t)
	runner := NewRunner(registry, &system.Context{}, capture.log)
	if err := runner.Run(context.Background(), &types.Config{}, []string{"zellij", "node"}); err != nil {
		t.Fatalf("Run() should continue after non-base module failure, got: %v", err)
	}
	if !following.runCalled {
		t.Fatal("expected independent module to run after zellij failure")
	}

	output := capture.Output()
	if !strings.Contains(output, "[WARN]") || !strings.Contains(output, "checksum mismatch") {
		t.Fatalf("expected warning for failed optional module, got: %q", output)
	}
}

func TestRunnerStopsAfterBaseModuleFailure(t *testing.T) {
	registry := modules.NewRegistry()
	base := &runnerTestModule{id: "base", runErr: errors.New("apt failed")}
	following := &runnerTestModule{id: "zellij"}
	registry.Register(base)
	registry.Register(following)

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New() failed: %v", err)
	}
	defer log.Close()

	runner := NewRunner(registry, &system.Context{}, log)
	if err := runner.Run(context.Background(), &types.Config{}, []string{"base", "zellij"}); err == nil {
		t.Fatal("Run() should fail when base module fails")
	}
	if following.runCalled {
		t.Fatal("expected runner to stop after base module failure")
	}
}

func TestRunnerSkipsModuleWhoseDependencyFailed(t *testing.T) {
	registry := modules.NewRegistry()
	node := &runnerTestModule{id: "node", runErr: errors.New("Node download failed")}
	ai := &runnerTestModule{id: "ai", deps: []string{"node"}}
	registry.Register(node)
	registry.Register(ai)

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New() failed: %v", err)
	}
	defer log.Close()

	runner := NewRunner(registry, &system.Context{}, log)
	result, err := runner.RunWithResult(context.Background(), &types.Config{}, []string{"ai"})
	if err != nil {
		t.Fatalf("Run() should continue after failed optional dependency, got: %v", err)
	}
	if !result.ModuleFailed("node") {
		t.Fatal("expected result to report the failed node dependency")
	}
	if ai.runCalled {
		t.Fatal("expected dependent module to be skipped after its dependency failed")
	}
}

func TestRunnerStopsOnCancellationDuringOptionalModule(t *testing.T) {
	registry := modules.NewRegistry()
	base := &runnerTestModule{id: "base", satisfied: true}
	zellij := &runnerTestModule{id: "zellij", deps: []string{"base"}, runErr: errors.New("download interrupted")}
	following := &runnerTestModule{id: "node"}
	registry.Register(base)
	registry.Register(zellij)
	registry.Register(following)

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New() failed: %v", err)
	}
	defer log.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := NewRunner(registry, &system.Context{}, log)
	err = runner.Run(ctx, &types.Config{}, []string{"zellij", "node"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context cancellation", err)
	}
	if following.runCalled {
		t.Fatal("expected runner to stop after cancellation")
	}
}

func TestRunnerStopsOnNonInstallationModuleFailure(t *testing.T) {
	registry := modules.NewRegistry()
	ssh := &runnerTestModule{
		id:     "ssh",
		runErr: errors.New("no replacement access path"),
		steps:  []types.Step{{Module: "ssh", Title: "Apply SSH hardening"}},
	}
	following := &runnerTestModule{id: "zellij"}
	registry.Register(ssh)
	registry.Register(following)

	log, err := logging.New(true)
	if err != nil {
		t.Fatalf("logging.New() failed: %v", err)
	}
	defer log.Close()

	runner := NewRunner(registry, &system.Context{}, log)
	if err := runner.Run(context.Background(), &types.Config{}, []string{"ssh", "zellij"}); err == nil {
		t.Fatal("Run() should preserve fatal SSH safety errors")
	}
	if following.runCalled {
		t.Fatal("expected runner to stop after SSH safety failure")
	}
}

func TestRunnerExecutesSSHKeygenOverwriteWhenCheckIsUnsatisfied(t *testing.T) {
	registry := modules.NewRegistry()
	module := &runnerTestModule{id: "ssh_keygen", satisfied: false}
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

func TestRunnerRejectsSatisfiedCheckWithPlanSteps(t *testing.T) {
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

	err = runner.Run(context.Background(), cfg, []string{"timezone"})
	if err == nil || !strings.Contains(err.Error(), "reported satisfied but planned 1 action") {
		t.Fatalf("Run() error = %v, want module contract error", err)
	}
	if module.runCalled {
		t.Fatal("module must not run after a Check/Plan contract error")
	}
}

func TestRunnerTrustsUnsatisfiedCheckEvenWhenPlanHasNoSteps(t *testing.T) {
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
	if !module.runCalled {
		t.Fatal("expected authoritative unsatisfied check to run the module")
	}
}
