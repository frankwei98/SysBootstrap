package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/frankwei98/sys-bootstrap/internal/i18n"
	"github.com/frankwei98/sys-bootstrap/internal/system"
)

// userLevelModules lists module IDs that install into the current user's home directory.
// This is the single source of truth for the user-level module set.
var userLevelModules = map[string]bool{
	"node":       true,
	"ai":         true,
	"ssh_keygen": true,
}

// IsUserLevelModule reports whether the given module ID is a user-level module
// (does not require root). This is the single source of truth.
func IsUserLevelModule(id string) bool {
	return userLevelModules[id]
}

// UserLevelModuleSet returns a copy of the user-level module ID set.
// Callers should not modify the returned map; use IsUserLevelModule for checks.
func UserLevelModuleSet() map[string]bool {
	cp := make(map[string]bool, len(userLevelModules))
	for k, v := range userLevelModules {
		cp[k] = v
	}
	return cp
}

// CheckRootUserInstall warns when root is running user-level modules.
// Returns nil if no warning needed or user confirmed. Returns error if
// the user declined or if non-interactive mode can't confirm.
func CheckRootUserInstall(sys *system.Context, moduleIDs []string, interactive bool) error {
	if !sys.IsRoot {
		return nil // not root, no warning needed
	}

	// Filter to only user-level modules in the selection
	var userMods []string
	for _, id := range moduleIDs {
		if IsUserLevelModule(id) {
			userMods = append(userMods, id)
		}
	}
	if len(userMods) == 0 {
		return nil // no user-level modules selected
	}

	if sys.InvokingUser != nil {
		return nil // root via sudo: user-level modules target the invoking user
	}

	modList := strings.Join(userMods, ", ")

	// Check for explicit override
	if os.Getenv("SYS_BOOTSTRAP_CONFIRM_ROOT_USER_INSTALL") == "1" {
		return nil
	}

	// Non-interactive: can't ask, must fail
	if !interactive {
		return fmt.Errorf("%s", i18n.T("root_install_non_interactive"))
	}

	// Build the appropriate message
	var message string
	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser != "" && sudoUser != "root" {
		message = i18n.Tf("root_install_sudo_root", sudoUser, sudoUser, modList)
	} else {
		message = i18n.Tf("root_install_direct_root", modList)
	}

	// Show confirmation
	var confirm bool
	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title(i18n.T("root_install_title")).
				Description(message),
			huh.NewConfirm().
				Title(i18n.T("root_install_confirm")).
				Value(&confirm),
		),
	).Run(); err != nil {
		return err
	}
	if !confirm {
		return fmt.Errorf("%s", i18n.T("runner_cancelled"))
	}

	return nil
}
