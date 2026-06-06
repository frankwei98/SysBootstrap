package modules

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/frankwei98/sys-bootstrap/internal/system"
	"github.com/frankwei98/sys-bootstrap/internal/types"
)

func TestValidatePublicKey(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		valid bool
	}{
		{"ed25519", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGJjYWFhYmJiY2NjZGRkZWVlZWZmZmdoaGhoaWlpampq", true},
		{"rsa", "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC...", true},
		{"ecdsa", "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTY...", true},
		{"sk", "sk-ssh-ed25519@openssh.com AAAA...", true},
		{"empty", "", false},
		{"random text", "hello world", false},
		{"no prefix", "AAAAAC3NzaC1lZDI1NTE5AAAA...", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidatePublicKey(tt.key)
			if got != tt.valid {
				t.Errorf("ValidatePublicKey(%q) = %v, want %v", tt.key, got, tt.valid)
			}
		})
	}
}

func TestSSHPlanNoUFW(t *testing.T) {
	m := NewSSHModule()
	sys := &system.Context{HasUFW: false}
	cfg := &types.Config{SSHPort: 22122}

	steps, err := m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	for _, s := range steps {
		if s.Title == "Allow SSH port in UFW" || s.Title == "UFW firewall warning" {
			t.Errorf("unexpected UFW step when UFW not present: %s", s.Title)
		}
	}
}

func TestSSHPlanInstallsOpenSSHServerWhenMissing(t *testing.T) {
	m := NewSSHModule()
	sys := &system.Context{HasSSHD: false, HasSSHDService: false}
	cfg := &types.Config{SSHPort: 22122}

	steps, err := m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if len(steps) == 0 || steps[0].Title != "Install OpenSSH server" {
		t.Fatalf("first step = %#v, want Install OpenSSH server", steps)
	}
}

func TestSSHCheckMissingConfig(t *testing.T) {
	orig := sshConfigPath
	sshConfigPath = filepath.Join(t.TempDir(), "missing_sshd_config")
	t.Cleanup(func() {
		sshConfigPath = orig
	})

	m := NewSSHModule()
	result := m.Check(context.Background(), &system.Context{HasSSHD: true})
	if result.Satisfied {
		t.Fatal("expected missing sshd_config to be unsatisfied")
	}
	if result.Message != "sshd_config not found" {
		t.Fatalf("message = %q, want sshd_config not found", result.Message)
	}
}

func TestSSHPlanUFWActiveAllow(t *testing.T) {
	m := NewSSHModule()
	sys := &system.Context{HasUFW: true, UFWActive: true}
	cfg := &types.Config{SSHPort: 22122, SSHAllowUFW: true}

	steps, err := m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	found := false
	for _, s := range steps {
		if s.Title == "Allow SSH port in UFW" {
			found = true
			if s.Detail != "ufw allow 22122/tcp" {
				t.Errorf("UFW step detail = %q, want %q", s.Detail, "ufw allow 22122/tcp")
			}
		}
	}
	if !found {
		t.Error("expected 'Allow SSH port in UFW' step not found")
	}
}

func TestSSHPlanUFWActiveDeny(t *testing.T) {
	m := NewSSHModule()
	sys := &system.Context{HasUFW: true, UFWActive: true}
	cfg := &types.Config{SSHPort: 22122, SSHAllowUFW: false}

	steps, err := m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	foundWarning := false
	foundAllow := false
	for _, s := range steps {
		if s.Title == "UFW firewall warning" {
			foundWarning = true
			if s.Risk != "manual-step" {
				t.Errorf("UFW warning risk = %q, want manual-step", s.Risk)
			}
		}
		if s.Title == "Allow SSH port in UFW" {
			foundAllow = true
		}
	}
	if !foundWarning {
		t.Error("expected 'UFW firewall warning' step not found")
	}
	if foundAllow {
		t.Error("unexpected 'Allow SSH port in UFW' step when SSHAllowUFW=false")
	}
}

func TestSSHPlanUFWInactive(t *testing.T) {
	m := NewSSHModule()
	sys := &system.Context{HasUFW: true, UFWActive: false}
	cfg := &types.Config{SSHPort: 22122, SSHAllowUFW: true}

	steps, err := m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	for _, s := range steps {
		if s.Title == "Allow SSH port in UFW" || s.Title == "UFW firewall warning" {
			t.Errorf("unexpected UFW step when UFW inactive: %s", s.Title)
		}
	}
}

func TestSSHPlanDefaultPort(t *testing.T) {
	m := NewSSHModule()
	sys := &system.Context{HasSSHD: true, HasSSHDService: true}
	cfg := &types.Config{SSHPort: 0} // zero means default

	steps, err := m.Plan(context.Background(), sys, cfg)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	if steps[0].Detail != "Set port to 22122" {
		t.Errorf("default port detail = %q, want %q", steps[0].Detail, "Set port to 22122")
	}
}
