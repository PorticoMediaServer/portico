//go:build !windows

package stateownership

import (
	"os"
	"syscall"
)

// SingleLinkedFileIdentity returns the native identity of a regular file with
// exactly one name. It creates no ownership, descriptor or execution authority.
// A database with hardlink aliases cannot use directory-local state ownership.
func SingleLinkedFileIdentity(info os.FileInfo) (device, inode uint64, err error) {
	if info == nil || !info.Mode().IsRegular() {
		return 0, 0, ErrInvalid
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, ErrUnsupported
	}
	if stat.Nlink != 1 {
		return 0, 0, ErrChanged
	}
	return uint64(stat.Dev), uint64(stat.Ino), nil
}
