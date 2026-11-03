//go:build darwin

package storage

import (
	"strings"

	"golang.org/x/sys/unix"
)

// mountIsLocal answers from getfsstat(MNT_NOWAIT), which reads the kernel's
// cached mount list without contacting any filesystem (localUnder).
func mountIsLocal(path string) bool {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil || n <= 0 {
		return false
	}
	list := make([]unix.Statfs_t, n)
	if n, err = unix.Getfsstat(list, unix.MNT_NOWAIT); err != nil {
		return false
	}
	mounts := make([]mountEntry, 0, n)
	for _, m := range list[:n] {
		kind := unix.ByteSliceToString(m.Fstypename[:])
		mounts = append(mounts, mountEntry{point: unix.ByteSliceToString(m.Mntonname[:]), remote: darwinRemoteFilesystem(kind)})
	}
	return localUnder(path, mounts)
}

func darwinRemoteFilesystem(kind string) bool {
	switch kind {
	case "nfs", "smbfs", "afpfs", "webdav", "ftp", "cifs":
		return true
	}
	return strings.Contains(kind, "fuse")
}
