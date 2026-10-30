//go:build linux || darwin

package storage

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// ErrRootedUnsupported is declared for platforms that cannot pin by descriptor.
var ErrRootedUnsupported = errors.New("rooted directory browsing is unavailable on this platform")

// RootedDir is one directory pinned by descriptor, opened from the filesystem
// root one component at a time with O_NOFOLLOW. Every later lookup is relative
// to that descriptor, so neither a symlink nor a directory replaced under the
// walk can move it outside the tree the caller opened. It exists for paths the
// server must browse without following: a mounted remote's folders.
type RootedDir struct{ file *os.File }

// OpenRootedDir pins an absolute directory, refusing a symlink at any
// component of the path.
func OpenRootedDir(path string) (*RootedDir, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 8192 {
		return nil, ErrRemoteConfig
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	current := &RootedDir{os.NewFile(uintptr(fd), "/")}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, err := current.Child(part)
		current.Close()
		if err != nil {
			return nil, err
		}
		current = next
	}
	return current, nil
}

// Child pins one subdirectory of this directory, refusing a symlink or a
// non-directory at that name.
func (d *RootedDir) Child(name string) (*RootedDir, error) {
	if d == nil || d.file == nil || name == "" || name == "." || name == ".." || strings.ContainsRune(name, '/') || strings.ContainsRune(name, 0) {
		return nil, ErrRemoteConfig
	}
	fd, err := unix.Openat(int(d.file.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return &RootedDir{os.NewFile(uintptr(fd), name)}, nil
}

func (d *RootedDir) Close() error {
	if d == nil || d.file == nil {
		return nil
	}
	return d.file.Close()
}

// Names reads at most limit names and probes once more, so a huge folder costs
// bounded work and says truthfully whether it holds more.
func (d *RootedDir) Names(limit int) ([]string, bool, error) {
	if d == nil || d.file == nil || limit < 1 {
		return nil, false, ErrRemoteConfig
	}
	fd, err := unix.Openat(int(d.file.Fd()), ".", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, false, err
	}
	directory := os.NewFile(uintptr(fd), ".")
	defer directory.Close()
	out := make([]string, 0, 64)
	for len(out) < limit {
		batch := 256
		if remaining := limit - len(out); remaining < batch {
			batch = remaining
		}
		names, err := directory.Readdirnames(batch)
		out = append(out, names...)
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return out, false, nil
		}
		return nil, false, err
	}
	probe, err := directory.Readdirnames(1)
	if len(probe) > 0 {
		return out, true, nil
	}
	if err == nil || errors.Is(err, io.EOF) {
		return out, false, nil
	}
	return nil, false, err
}

// IsDirectoryEntry reports whether one name in this directory is a real
// directory, opening it with O_NOFOLLOW: a symlink or a file is not.
func (d *RootedDir) IsDirectoryEntry(name string) bool {
	if d == nil || d.file == nil || name == "" || name == "." || name == ".." || strings.ContainsRune(name, '/') || strings.ContainsRune(name, 0) {
		return false
	}
	fd, err := unix.Openat(int(d.file.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.ENOENT) {
			return false
		}
		return false
	}
	_ = unix.Close(fd)
	return true
}
