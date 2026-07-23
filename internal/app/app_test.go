package app

import "testing"

func TestNewRegistryIncludesExtendedModules(t *testing.T) {
	r := NewRegistry()
	for _, id := range []string{"zellij", "docker", "timezone", "fail2ban"} {
		if _, err := r.Get(id); err != nil {
			t.Fatalf("registry missing %q: %v", id, err)
		}
	}
}
