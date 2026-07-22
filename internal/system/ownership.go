package system

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
)

// ChownToInvokingUser gives files created by a sudo-launched process back to
// the non-root user who invoked it. Direct root runs intentionally retain root
// ownership. Callers should use this only for user-scoped state they created.
func ChownToInvokingUser(paths ...string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	username := os.Getenv("SUDO_USER")
	if username == "" || username == "root" {
		return nil
	}
	u, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("cannot resolve invoking user %q: %w", username, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return fmt.Errorf("invalid UID for invoking user %q: %w", username, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return fmt.Errorf("invalid GID for invoking user %q: %w", username, err)
	}
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := os.Chown(path, uid, gid); err != nil {
			return fmt.Errorf("cannot set ownership of %s to %s: %w", path, username, err)
		}
	}
	return nil
}
