//go:build !windows

package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func isMount(path string) (bool, error) {
	current, err := os.Lstat(path)
	if err != nil {
		return false, err
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return false, err
	}
	if !current.IsDir() || current.Mode()&os.ModeSymlink != 0 {
		return false, os.ErrInvalid
	}
	left, lok := current.Sys().(*syscall.Stat_t)
	right, rok := parent.Sys().(*syscall.Stat_t)
	if !lok || !rok {
		return false, os.ErrInvalid
	}
	return left.Dev != right.Dev, nil
}

func DirectoryIdentity(path string) (string, error) {
	info, e := os.Lstat(path)
	if e != nil {
		return "", e
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", os.ErrInvalid
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", os.ErrInvalid
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino), nil
}
