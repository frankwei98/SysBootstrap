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
	Modules []ModulePlan `json:"modules"`
	Summary string       `json:"summary"`
}

// ModulePlan describes what a single module will do.
type ModulePlan struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Steps   []types.Step `json:"steps"`
	Status  string       `json:"status"`
	Warning string       `json:"warning,omitempty"`
}

// GeneratePlan creates a plan for the given module IDs.
func GeneratePlan(ctx context.Context, sys *system.Context, cfg *types.Config, registry *modules.Registry, ids []string) (*PlanResult, error) {
	ordered, err := registry.ResolveOrder(ids)
	if err != nil {
		return nil, err
	}

	plan := &PlanResult{}
	for _, id := range ordered {
		m, err := registry.Get(id)
		if err != nil {
			return nil, err
		}

		mp := ModulePlan{
			ID:   m.ID(),
			Name: m.Name(),
		}

		check := m.Check(ctx, sys)
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

		plan.Modules = append(plan.Modules, mp)
	}

	pending := 0
	for _, mp := range plan.Modules {
		if mp.Status == "pending" {
			pending++
		}
	}
	plan.Summary = fmt.Sprintf("%d module(s) to execute, %d already satisfied", pending, len(plan.Modules)-pending)

	return plan, nil
}

// FormatPlanText formats a plan as human-readable text.
// Uses i18n for display strings; status field values remain English for JSON compatibility.
func FormatPlanText(plan *PlanResult) string {
	var b strings.Builder
	b.WriteString(i18n.T("plan_title") + "\n")
	b.WriteString("==============\n\n")

	for _, mp := range plan.Modules {
		statusIcon := "●"
		switch mp.Status {
		case "satisfied":
			statusIcon = "✓"
		case "error":
			statusIcon = "✗"
		}

		fmt.Fprintf(&b, "%s %s — %s\n", statusIcon, mp.Name, mp.Status)
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
