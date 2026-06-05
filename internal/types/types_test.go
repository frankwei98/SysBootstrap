package types

import (
	"encoding/json"
	"testing"
)

func TestConfigDefaults(t *testing.T) {
	cfg := Config{}
	if cfg.SSHPort != 0 {
		t.Errorf("default SSHPort should be 0, got %d", cfg.SSHPort)
	}
	if cfg.SSHAddKey {
		t.Error("default SSHAddKey should be false")
	}
}

func TestStepJSON(t *testing.T) {
	step := Step{
		Module: "base",
		Title:  "Install packages",
		Detail: "apt-get install",
		Status: "pending",
		Risk:   "low",
	}

	data, err := json.Marshal(step)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}

	var got Step
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal error: %v", err)
	}

	if got.Module != step.Module || got.Title != step.Title || got.Risk != step.Risk {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, step)
	}
}
