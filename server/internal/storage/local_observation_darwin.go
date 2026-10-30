//go:build darwin

package storage

import (
	"fmt"
	"os"
	"syscall"
)

func observedObjectBinding(info os.FileInfo, path string) (string, string, error) {
	object, token, err := nativeObjectBinding(info)
	if err != nil {
		return "", "", err
	}
	object, err = directoryFilesystemIdentity(info, path, object)
	return object, token, err
}

func nativeObjectBinding(info os.FileInfo) (string, string, error) {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", "", ErrObservedSourceUnsupported
	}
	return fmt.Sprintf("dev:%d:ino:%d", s.Dev, s.Ino), localChangeToken(s.Ino, info.Size(), info.ModTime().UnixNano()), nil
}

func checkLocalRoot(string) error { return nil }
