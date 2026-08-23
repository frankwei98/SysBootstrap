package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/frankwei98/sys-bootstrap/internal/i18n"
	"github.com/frankwei98/sys-bootstrap/internal/modules"
	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

// PlanResult is the output of plan generation.
type PlanResult struct {
	SupportTier string       `json:"support_tier,omitempty"`
	Checks      []PlanCheck  `json:"checks,omitempty"`
	Modules     []ModulePlan `json:"modules"`
	Summary     string       `json:"summary"`
	Counts      PlanCounts   `json:"counts"`
}

// ErrPlanNotExecutable marks a plan containing fatal environment checks or
// module planning errors. The plan may still be rendered for diagnostics.
var ErrPlanNotExecutable = errors.New("plan is not executable")

// Err reports whether a rendered plan is safe to execute.
func (p *PlanResult) Err() error {
	if p == nil {
		return fmt.Errorf("%w: no plan result", ErrPlanNotExecutable)
	}
	var reasons []string
	if p.SupportTier == string(system.SupportTierUnsupported) {
		reasons = append(reasons, "unsupported operating system")
	}
	for _, check := range p.Checks {
		if check.Status == "fatal" || check.Status == "error" {
			reasons = append(reasons, fmt.Sprintf("%s check is %s", check.Name, check.Status))
		}
	}
	for _, module := range p.Modules {
		if module.Status == "error" {
			reasons = append(reasons, fmt.Sprintf("module %s has a planning error", module.ID))
		}
	}
	if len(reasons) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrPlanNotExecutable, strings.Join(reasons, "; "))
}

type PlanCounts struct {
	Pending       int `json:"pending"`
	Satisfied     int `json:"satisfied"`
	NotConfigured int `json:"not_configured"`
	Error         int `json:"error"`
}

// ModulePlan describes what a single module will do.
type ModulePlan struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Dependencies []string     `json:"dependencies,omitempty"`
	Steps        []types.Step `json:"steps"`
	Status       string       `json:"status"`
	CheckMessage string       `json:"check_message,omitempty"`
	Warning      string       `json:"warning,omitempty"`
}

// PlanCheck captures a high-level environment check that affects plan output.
type PlanCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// GeneratePlan creates a plan for the given module IDs.
func GeneratePlan(ctx context.Context, sys *system.Context, cfg *types.Config, registry *modules.Registry, ids []string) (*PlanResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ordered, err := registry.ResolveOrder(ids)
	if err != nil {
		return nil, err
	}

	plan := &PlanResult{}
	plan.SupportTier = string(sys.SupportTier())
	plan.Checks = buildPlanChecks(sys)
	for _, id := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m, err := registry.Get(id)
		if err != nil {
			return nil, err
		}

		mp := ModulePlan{
			ID:           m.ID(),
			Name:         m.Name(),
			Dependencies: append([]string{}, m.Dependencies()...),
		}

		moduleCfg := cfg
		if m.ID() == "ssh" {
			moduleCfg = cloneConfig(cfg)
			if moduleCfg.SSHPort == 0 {
				moduleCfg.SSHPort = modules.DefaultSSHPort
			}
		}

		check := m.Check(ctx, sys, moduleCfg)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		mp.CheckMessage = strings.TrimSpace(check.Message)
		if check.Satisfied {
			mp.Status = "satisfied"
			mp.Warning = joinCheckWarnings("", check.Warnings)
		} else {
			mp.Status = "pending"
			mp.Warning = joinCheckWarnings(check.Message, check.Warnings)
		}

		steps, err := m.Plan(ctx, sys, moduleCfg)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			mp.Status = "error"
			mp.Warning = err.Error()
		} else {
			mp.Steps = steps
			if contractErr := moduleStateContractError(m.Name(), check, steps); contractErr != nil {
				mp.Status = "error"
				mp.Warning = contractErr.Error()
			}
		}

		if m.ID() == "user" && cfg.NewUsername == "" && len(mp.Steps) == 0 {
			mp.Status = "not_configured"
			mp.CheckMessage = "No username configured yet"
			mp.Warning = ""
		}

		plan.Modules = append(plan.Modules, mp)
	}

	pending := 0
	satisfied := 0
	notConfigured := 0
	errorCount := 0
	for _, mp := range plan.Modules {
		switch mp.Status {
		case "pending":
			pending++
		case "satisfied":
			satisfied++
		case "not_configured":
			notConfigured++
		case "error":
			errorCount++
		}
	}
	plan.Counts = PlanCounts{
		Pending:       pending,
		Satisfied:     satisfied,
		NotConfigured: notConfigured,
		Error:         errorCount,
	}
	if notConfigured > 0 {
		plan.Summary = fmt.Sprintf("%d module(s) to execute, %d already satisfied, %d awaiting input", pending, satisfied, notConfigured)
		if errorCount > 0 {
			plan.Summary += fmt.Sprintf(", %d plan error(s)", errorCount)
		}
	} else {
		plan.Summary = fmt.Sprintf("%d module(s) to execute, %d already satisfied", pending, satisfied)
		if errorCount > 0 {
			plan.Summary += fmt.Sprintf(", %d plan error(s)", errorCount)
		}
	}

	return plan, nil
}

func cloneConfig(cfg *types.Config) *types.Config {
	if cfg == nil {
		return &types.Config{}
	}
	copy := *cfg
	return &copy
}

func moduleStateContractError(moduleName string, check modules.CheckResult, steps []types.Step) error {
	if !check.Satisfied || len(steps) == 0 {
		return nil
	}
	noun := "actions"
	if len(steps) == 1 {
		noun = "action"
	}
	return fmt.Errorf("module %s reported satisfied but planned %d %s", moduleName, len(steps), noun)
}

func joinCheckWarnings(message string, warnings []string) string {
	parts := make([]string, 0, len(warnings)+1)
	if message = strings.TrimSpace(message); message != "" {
		parts = append(parts, message)
	}
	for _, warning := range warnings {
		if warning = strings.TrimSpace(warning); warning != "" {
			parts = append(parts, warning)
		}
	}
	return strings.Join(parts, "; ")
}

// FormatPlanText formats a plan as human-readable text.
// Uses i18n for display strings; status field values remain English for JSON compatibility.
func FormatPlanText(plan *PlanResult) string {
	var b strings.Builder
	b.WriteString(i18n.T("plan_title") + "\n")
	b.WriteString("==============\n\n")

	if plan.SupportTier != "" {
		fmt.Fprintf(&b, "%s: %s\n", i18n.T("plan_support_tier"), plan.SupportTier)
	}
	if len(plan.Checks) > 0 {
		b.WriteString(i18n.T("plan_checks") + ":\n")
		for _, c := range plan.Checks {
			icon := "✓"
			switch c.Status {
			case "warning":
				icon = "⚠"
			case "fatal", "error":
				icon = "✗"
			}
			fmt.Fprintf(&b, "  %s %s: %s", icon, c.Name, c.Status)
			if c.Detail != "" {
				fmt.Fprintf(&b, " (%s)", c.Detail)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Overview: %d pending, %d satisfied", plan.Counts.Pending, plan.Counts.Satisfied)
	if plan.Counts.NotConfigured > 0 {
		fmt.Fprintf(&b, ", %d awaiting input", plan.Counts.NotConfigured)
	}
	if plan.Counts.Error > 0 {
		fmt.Fprintf(&b, ", %d errors", plan.Counts.Error)
	}
	b.WriteString("\n\n")

	for _, mp := range plan.Modules {
		statusIcon := "●"
		switch mp.Status {
		case "satisfied":
			statusIcon = "✓"
		case "not_configured":
			statusIcon = "○"
		case "error":
			statusIcon = "✗"
		}

		fmt.Fprintf(&b, "%s %s — %s\n", statusIcon, mp.Name, mp.Status)
		if len(mp.Dependencies) > 0 {
			fmt.Fprintf(&b, "    %s %s\n", i18n.T("plan_dependencies"), strings.Join(mp.Dependencies, ", "))
		}
		if mp.CheckMessage != "" {
			fmt.Fprintf(&b, "    %s %s\n", i18n.T("plan_check_result"), mp.CheckMessage)
		}
		if mp.Status == "not_configured" {
			fmt.Fprintf(&b, "    %s\n", i18n.T("plan_awaiting_interactive_input"))
		} else if len(mp.Steps) > 0 {
			for _, step := range mp.Steps {
				riskTag := ""
				if step.Risk != "" {
					riskTag = " [" + step.Risk + "]"
				}
				fmt.Fprintf(&b, "    • %s%s\n", step.Title, riskTag)
				if step.Detail != "" {
					fmt.Fprintf(&b, "      %s\n", step.Detail)
				}
			}
		} else if mp.Status == "satisfied" {
			fmt.Fprintf(&b, "    %s\n", i18n.T("plan_no_actions_required"))
		}
		if mp.Warning != "" && mp.Warning != mp.CheckMessage {
			fmt.Fprintf(&b, "    ⚠ %s\n", mp.Warning)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "%s\n", plan.Summary)
	return b.String()
}

// FormatPlanJSON formats a plan as JSON.
func FormatPlanJSON(plan *PlanResult) (string, error) {
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func buildPlanChecks(sys *system.Context) []PlanCheck {
	if sys == nil {
		return nil
	}

	status := func(ok bool) string {
		if ok {
			return "ok"
		}
		return "warning"
	}
	osTier := sys.SupportTier()
	osStatus := "ok"
	if osTier == system.SupportTierUnsupported {
		osStatus = "fatal"
	}

	checks := []PlanCheck{
		{Name: "os", Status: osStatus, Detail: fmt.Sprintf("%s: %s %s", osTier, sys.OSID, sys.OSVersion)},
		{Name: "arch", Status: fatalStatus(system.IsSupportedArchitecture(sys.Arch)), Detail: sys.Arch},
		{Name: "apt-get", Status: fatalStatus(sys.HasApt), Detail: boolDetail(sys.HasApt, "available", "missing")},
		{Name: "network", Status: fatalStatus(sys.HasNetwork), Detail: boolDetail(sys.HasNetwork, "dns ok", "dns failed")},
	}

	if sys.HasSystemd || sys.HasSSHDService {
		checks = append(checks, PlanCheck{
			Name:   "ssh service",
			Status: status(sys.HasSSHDService),
			Detail: boolDetail(sys.HasSSHDService, "available", "not found"),
		})
	}
	if sys.HasUFW {
		check := PlanCheck{Name: "ufw", Status: "ok", Detail: boolDetail(sys.UFWActive, "active", "inactive")}
		if !sys.UFWStatusKnown {
			check.Status = "warning"
			check.Detail = "status unknown; run as root for an accurate firewall plan"
		}
		checks = append(checks, check)
	}

	return checks
}

func fatalStatus(ok bool) string {
	if ok {
		return "ok"
	}
	return "fatal"
}

func boolDetail(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}
