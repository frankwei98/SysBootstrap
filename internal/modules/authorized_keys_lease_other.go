//go:build !linux

package modules

import "os"

// sys-bootstrap provisions Linux hosts. Other platforms use a no-op lease so
// unit tests and read-only development tooling remain usable.
func acquireAuthorizedKeysWriteLease(_ *os.File) error { return nil }
func releaseAuthorizedKeysWriteLease(_ *os.File) error { return nil }
