//go:build darwin || linux

package linearbuffer

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Buffer files are server-owned; the decoder has only the loopback PUT route.
// A crashed server therefore cannot leave a decoder writing these files. An
// advisory owner descriptor distinguishes orphans from another active server.
func cacheLock(path string, nonblocking bool) (*os.File, error) {
	file, e := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	info, e := file.Stat()
	if e != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, ErrMedia
	}
	mode := syscall.LOCK_EX
	if nonblocking {
		mode |= syscall.LOCK_NB
	}
	if e = syscall.Flock(int(file.Fd()), mode); e != nil {
		file.Close()
		return nil, e
	}
	return file, nil
}
func createOwnedDirectory(parent string) (string, *os.File, error) {
	registry, e := cacheLock(filepath.Join(parent, ".linear-maintenance"), false)
	if e != nil {
		return "", nil, e
	}
	defer registry.Close()
	dir, e := os.MkdirTemp(parent, "linear-")
	if e != nil {
		return "", nil, e
	}
	owner, e := cacheLock(filepath.Join(dir, ".owner"), true)
	if e != nil {
		os.RemoveAll(dir)
		return "", nil, e
	}
	return dir, owner, nil
}

// Sweep removes only directories whose producer/server ownership is gone. The
// shared registry serializes creation, and per-buffer locks protect live peers.
func Sweep(parent string) error {
	registry, e := cacheLock(filepath.Join(parent, ".linear-maintenance"), false)
	if e != nil {
		return e
	}
	defer registry.Close()
	root, e := os.Open(parent)
	if e != nil {
		return e
	}
	defer root.Close()
	for {
		entries, readError := root.ReadDir(128)
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "linear-") {
				continue
			}
			if _, e := entry.Info(); e != nil {
				continue
			}
			dir := filepath.Join(parent, entry.Name())
			owner, e := cacheLock(filepath.Join(dir, ".owner"), true)
			if e != nil {
				continue
			}
			e = os.RemoveAll(dir)
			owner.Close()
			if e != nil {
				return e
			}
		}
		if errors.Is(readError, io.EOF) {
			return nil
		}
		if readError != nil {
			return readError
		}
	}
}
