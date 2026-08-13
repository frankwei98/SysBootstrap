//go:build linux

package modules

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func acquireAuthorizedKeysWriteLease(f *os.File) error {
	if _, err := unix.FcntlInt(f.Fd(), unix.F_SETLEASE, unix.F_WRLCK); err != nil {
		return fmt.Errorf("set write lease: %w", err)
	}
	return nil
}

func releaseAuthorizedKeysWriteLease(f *os.File) error {
	if _, err := unix.FcntlInt(f.Fd(), unix.F_SETLEASE, unix.F_UNLCK); err != nil {
		return fmt.Errorf("release write lease: %w", err)
	}
	return nil
}
