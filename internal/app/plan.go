package app

import (
	"context"
	"encoding/json"
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
	ordered, err := registry.ResolveOrder(ids)
	if err != nil {
		return nil, err
	}

	plan := &PlanResult{}
	plan.SupportTier = string(sys.SupportTier())
	plan.Checks = buildPlanChecks(sys)
	for _, id := range ordered {
		m, err := registry.Get(id)
		if err != nil {
			return nil, err
		}

		mp := ModulePlan{
			ID:           m.ID(),
			Name:         m.Name(),
			Dependencies: append([]string{}, m.Dependencies()...),
		}

		check := m.Check(ctx, sys)
		mp.CheckMessage = check.Message
		if check.Satisfied {
			mp.Status = "satisfied"
			mp.Warning = strings.Join(check.Warnings, "; ")
		} else {
			mp.Status = "pending"
			mp.Warning = check.Message
		}

		steps, err := m.Plan(ctx, sys, cfg)
		if err != nil {
			mp.Status = "error"
			mp.Warning = err.Error()
		} else {
			mp.Steps = steps
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
	for _, mp := range plan.Modules {
		switch mp.Status {
		case "pending":
			pending++
		case "satisfied":
			satisfied++
		case "not_configured":
			notConfigured++
		}
	}
	if notConfigured > 0 {
		plan.Summary = fmt.Sprintf("%d module(s) to execute, %d already satisfied, %d awaiting input", pending, satisfied, notConfigured)
	} else {
		plan.Summary = fmt.Sprintf("%d module(s) to execute, %d already satisfied", pending, satisfied)
	}

	return plan, nil
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
			fmt.Fprintf(&b, "  - %s: %s", c.Name, c.Status)
			if c.Detail != "" {
				fmt.Fprintf(&b, " (%s)", c.Detail)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

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
		if mp.Status == "satisfied" {
			if len(mp.Steps) > 0 {
				fmt.Fprintf(&b, "    %s\n", "No actions required")
			}
		} else {
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
		}
		if mp.Warning != "" {
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

	checks := []PlanCheck{
		{Name: "os", Status: string(sys.SupportTier()), Detail: fmt.Sprintf("%s %s", sys.OSID, sys.OSVersion)},
		{Name: "arch", Status: "ok", Detail: sys.Arch},
		{Name: "apt-get", Status: status(sys.HasApt), Detail: boolDetail(sys.HasApt, "available", "missing")},
		{Name: "network", Status: status(sys.HasNetwork), Detail: boolDetail(sys.HasNetwork, "dns ok", "dns failed")},
	}

	if sys.HasSystemd || sys.HasSSHDService {
		checks = append(checks, PlanCheck{
			Name:   "ssh service",
			Status: status(sys.HasSSHDService),
			Detail: boolDetail(sys.HasSSHDService, "available", "not found"),
		})
	}

	return checks
}

func boolDetail(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}
