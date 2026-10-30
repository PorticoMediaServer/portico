//go:build darwin || linux

package storage

import (
	"fmt"
	"os"
	"syscall"
)

// Called only by a storage child: combines actual filesystem identity with the
// mounted root object. Runtime child incarnation is separately owner-fenced.
func physicalMountIdentity(path string) (string, error) {
	mounted, err := isMount(path)
	if err != nil || !mounted {
		return "", ErrObservedSourceLost
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.IsDir() {
		return "", ErrObservedSourceLost
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ErrObservedSourceUnsupported
	}
	var fs syscall.Statfs_t
	if err = syscall.Fstatfs(int(file.Fd()), &fs); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%v", st.Dev, st.Ino, fs.Fsid), nil
}

func sameMountFilesystem(file *os.File, path string) error {
	mount, err := os.Open(path)
	if err != nil {
		return err
	}
	defer mount.Close()
	var selected, actual syscall.Statfs_t
	if err = syscall.Fstatfs(int(file.Fd()), &selected); err != nil {
		return err
	}
	if err = syscall.Fstatfs(int(mount.Fd()), &actual); err != nil {
		return err
	}
	if selected.Fsid != actual.Fsid {
		return ErrObservedSourceLost
	}
	return nil
}
