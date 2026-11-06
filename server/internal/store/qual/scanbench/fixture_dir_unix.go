//go:build linux || darwin

package scanbench

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"time"
)

type fixtureDir struct{ file *os.File }

func createFixtureRoot(path string) (*fixtureDir, error) {
	parent, err := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(parent)
	name := filepath.Base(path)
	if err := unix.Mkdirat(parent, name, 0700); err != nil {
		return nil, err
	}
	return openFixtureDir(parent, name)
}
func openFixtureDir(parent int, name string) (*fixtureDir, error) {
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return &fixtureDir{os.NewFile(uintptr(fd), name)}, nil
}
func (d *fixtureDir) Close() error { return d.file.Close() }
func (d *fixtureDir) bucket(name string) (*fixtureDir, error) {
	if err := unix.Mkdirat(int(d.file.Fd()), name, 0700); err != nil {
		return nil, err
	}
	return openFixtureDir(int(d.file.Fd()), name)
}
func (d *fixtureDir) create(name string) (*os.File, error) {
	fd, err := unix.Openat(int(d.file.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}
func stampFixtureFile(f *os.File, stamp time.Time) error {
	// Operate on the opened file, never resolve its pathname again.
	tv := unix.NsecToTimeval(stamp.UnixNano())
	return unix.Futimes(int(f.Fd()), []unix.Timeval{tv, tv})
}
