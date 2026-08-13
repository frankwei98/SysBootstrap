//go:build !linux

package system

import "golang.org/x/sys/unix"

func openRemovalDirAt(parentFD int, name string) (int, error) {
	return unix.Openat(parentFD, name, secureRemoveOpenFlags, 0)
}
