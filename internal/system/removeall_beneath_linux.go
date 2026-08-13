//go:build linux

package system

import "golang.org/x/sys/unix"

func openRemovalDirAt(parentFD int, name string) (int, error) {
	return unix.Openat2(parentFD, name, &unix.OpenHow{
		Flags: uint64(secureRemoveOpenFlags),
		Resolve: unix.RESOLVE_BENEATH |
			unix.RESOLVE_NO_SYMLINKS |
			unix.RESOLVE_NO_XDEV,
	})
}
