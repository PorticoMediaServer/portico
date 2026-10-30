//go:build linux || darwin

package recordingmedia

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
)

// layoutDirectory pins every ancestor separately. O_NOFOLLOW applies to each
// component, rather than checking a pathname and resolving it again for IO.
type layoutDirectory struct{ file *os.File }

func openLayoutDirectory(path string) (*layoutDirectory, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, mediaartifact.ErrPrivate
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	d := &layoutDirectory{os.NewFile(uintptr(fd), "/")}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, err := d.child(part, false)
		d.Close()
		if err != nil {
			return nil, err
		}
		d = next
	}
	return d, nil
}
func (d *layoutDirectory) Close() error { return d.file.Close() }
func (d *layoutDirectory) child(name string, create bool) (*layoutDirectory, error) {
	if !layoutComponent(name) {
		return nil, mediaartifact.ErrPrivate
	}
	if create {
		err := unix.Mkdirat(int(d.file.Fd()), name, 0700)
		if err != nil && !errors.Is(err, unix.EEXIST) {
			return nil, err
		}
		// Sync retries as well: an earlier mkdir may not have reached disk.
		if err := d.file.Sync(); err != nil {
			return nil, err
		}
	}
	fd, err := unix.Openat(int(d.file.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return &layoutDirectory{os.NewFile(uintptr(fd), name)}, nil
}
func (d *layoutDirectory) parent(relative string, create bool) (*layoutDirectory, string, error) {
	if !layoutRelative(relative) {
		return nil, "", mediaartifact.ErrPrivate
	}
	parts := strings.Split(relative, "/")
	// Duplicate through "." so the caller always owns the returned descriptor.
	fd, err := unix.Openat(int(d.file.Fd()), ".", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, "", err
	}
	p := &layoutDirectory{os.NewFile(uintptr(fd), ".")}
	for _, part := range parts[:len(parts)-1] {
		next, err := p.child(part, create)
		p.Close()
		if err != nil {
			return nil, "", err
		}
		p = next
	}
	return p, parts[len(parts)-1], nil
}
func (d *layoutDirectory) open(name string) (*os.File, error) {
	if !layoutComponent(name) {
		return nil, mediaartifact.ErrPrivate
	}
	// NONBLOCK prevents an unexpected FIFO from blocking before fstat rejects it.
	fd, err := unix.Openat(int(d.file.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}
func (d *layoutDirectory) link(source *layoutDirectory, old, name string) error {
	if !layoutComponent(old) || !layoutComponent(name) {
		return mediaartifact.ErrPrivate
	}
	return unix.Linkat(int(source.file.Fd()), old, int(d.file.Fd()), name, 0)
}
func (d *layoutDirectory) remove(name string) error {
	if !layoutComponent(name) {
		return mediaartifact.ErrPrivate
	}
	if err := unix.Unlinkat(int(d.file.Fd()), name, 0); err != nil {
		return err
	}
	return d.file.Sync()
}

func layoutPhysicalIdentity(info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return strconv.FormatUint(uint64(stat.Dev), 10) + ":" + strconv.FormatUint(uint64(stat.Ino), 10)
}
