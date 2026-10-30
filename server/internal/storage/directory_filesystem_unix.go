//go:build darwin || linux

package storage

import (
	"fmt"
	"os"
	"syscall"
)

// Directory identity includes the backing filesystem, so another volume's
// root inode cannot masquerade as the admitted root after a remount.
func directoryFilesystemIdentity(info os.FileInfo, path, object string) (string, error) {
	if !info.IsDir() {
		return object, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	current, err := f.Stat()
	if err != nil || !os.SameFile(info, current) {
		return "", ErrObservedSourceLost
	}
	return directoryFilesystemIdentityFromFile(f, object)
}

func directoryFilesystemIdentityFromFile(f *os.File, object string) (string, error) {
	var fs, zero syscall.Statfs_t
	if err := syscall.Fstatfs(int(f.Fd()), &fs); err != nil {
		return "", err
	}
	if fs.Fsid == zero.Fsid {
		return object, nil
	}
	return object + fmt.Sprintf(":fsid:%x", fs.Fsid), nil
}
