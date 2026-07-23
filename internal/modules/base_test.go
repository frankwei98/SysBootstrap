package modules

import (
	"context"
	"strings"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

func TestSummarizeBasePackages(t *testing.T) {
	status := map[string]bool{}
	for _, pkg := range basePackages {
		status[pkg] = false
	}
	status["git"] = true
	status["zsh"] = true
	status["curl"] = false

	installed, missing := summarizeBasePackages(map[string]bool{
		"sudo":                status["sudo"],
		"zsh":                 status["zsh"],
		"gnupg":               status["gnupg"],
		"apt-transport-https": status["apt-transport-https"],
		"git":                 status["git"],
		"curl":                status["curl"],
		"wget":                status["wget"],
		"unzip":               status["unzip"],
		"tree":                status["tree"],
		"neovim":              status["neovim"],
	})

	if len(installed) != 2 {
		t.Fatalf("installed len = %d, want 2", len(installed))
	}
	if len(missing) != len(basePackages)-2 {
		t.Fatalf("missing len = %d, want %d", len(missing), len(basePackages)-2)
	}
	foundCurl := false
	for _, pkg := range missing {
		if pkg == "curl" {
			foundCurl = true
			break
		}
	}
	if !foundCurl {
		t.Fatalf("missing = %v, want curl to be present", missing)
	}
}

func TestBuildBaseCheckMessage(t *testing.T) {
	msg := buildBaseCheckMessage([]string{"git", "zsh"}, []string{"curl"})
	if !strings.Contains(msg, "Installed packages: git, zsh") {
		t.Fatalf("missing installed section: %s", msg)
	}
	if !strings.Contains(msg, "Missing packages: curl") {
		t.Fatalf("missing package section: %s", msg)
	}
	if strings.Contains(msg, "zellij") {
		t.Fatalf("base check must not include zellij: %s", msg)
	}
}

func TestBuildBasePackageSteps(t *testing.T) {
	steps := buildBasePackageSteps([]string{"curl", "wget"})
	if len(steps) != 2 {
		t.Fatalf("steps len = %d, want 2", len(steps))
	}
	if steps[0].Title != "Install base packages" {
		t.Fatalf("first title = %q", steps[0].Title)
	}
	if !strings.Contains(steps[0].Detail, "curl") || !strings.Contains(steps[1].Detail, "wget") {
		t.Fatalf("unexpected details: %#v", steps)
	}
}

func TestBasePlanIncludesMissingPackagesOnly(t *testing.T) {
	m := NewBaseModule()
	t.Setenv("PATH", "")
	steps, err := m.Plan(context.Background(), &system.Context{}, &types.Config{})
	if err != nil {
		t.Fatalf("Plan() failed: %v", err)
	}
	foundBaseInstall := false
	for _, step := range steps {
		if step.Title == "Install base packages" {
			foundBaseInstall = true
		}
		if step.Title == "Install zellij" {
			t.Fatal("base plan must not include zellij; it is an independent module")
		}
	}
	if !foundBaseInstall {
		t.Fatal("expected plan to include base package installation step")
	}
}

func TestBaseCheckDoesNotMentionZellij(t *testing.T) {
	result := NewBaseModule().Check(context.Background(), &system.Context{})
	if strings.Contains(result.Message, "zellij") {
		t.Fatalf("base check should not include zellij state, got: %q", result.Message)
	}
}
