package cli

import (
	"fmt"
	"strings"

	"github.com/frankwei98/sys-bootstrap/internal/i18n"
	"github.com/frankwei98/sys-bootstrap/internal/modules"
)

// buildModuleList takes the user-selected module IDs and a run mode, then
// returns the final ordered module list:
//   - Ensures node is present when ai is selected.
//   - Injects all default-enabled modules in full mode.
//   - Resolves dependency order via the registry.
//
// This is a pure function (no UI, no system checks) extracted for testability.
func buildModuleList(registry *modules.Registry, mode RunMode, selected []string) ([]string, error) {
	// Auto-add node when ai is selected without it
	hasAI := false
	hasNode := false
	for _, id := range selected {
		if id == "ai" {
			hasAI = true
		}
		if id == "node" {
			hasNode = true
		}
	}
	if hasAI && !hasNode {
		selected = append([]string{"node"}, selected...)
	}

	// Inject default-enabled modules only in full mode.
	if mode == RunModeFull {
		var defaults []string
		for _, m := range registry.All() {
			if m.DefaultEnabled() {
				defaults = append(defaults, m.ID())
			}
		}
		selected = append(defaults, selected...)
	}

	return registry.ResolveOrder(selected)
}

// checkFullModeRoot verifies that no root-required module is present when
// running full mode as non-root. Returns an error listing the offending
// modules, or nil if the check passes.
func checkFullModeRoot(registry *modules.Registry, ordered []string, isRoot bool) error {
	if isRoot {
		return nil
	}
	var rootMods []string
	for _, id := range ordered {
		m, err := registry.Get(id)
		if err != nil {
			continue
		}
		if m.RequiresRoot() {
			rootMods = append(rootMods, m.Name())
		}
	}
	if len(rootMods) > 0 {
		return fmt.Errorf(i18n.T("run_full_needs_root"), strings.Join(rootMods, ", "))
	}
	return nil
}

func checkRootRequirementsForMode(mode RunMode, registry *modules.Registry, ordered []string, isRoot bool) error {
	if mode != RunModeFull {
		return nil
	}
	return checkFullModeRoot(registry, ordered, isRoot)
}
