//go:build linux

package storage

import (
	"errors"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// Linux directory timestamps can repeat within one kernel tick. Watch the
// anchored inode before reading its revision so a same-tick edit cannot reuse
// a retained listing. A queue overflow or lost watch is also a change.
type inventoryChanges struct {
	fd      int
	changed bool
}

func watchInventoryChanges(f *os.File) (*inventoryChanges, error) {
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		return nil, err
	}
	path := "/proc/self/fd/" + strconv.FormatUint(uint64(f.Fd()), 10)
	_, err = unix.InotifyAddWatch(fd, path, unix.IN_ONLYDIR|unix.IN_CREATE|unix.IN_DELETE|unix.IN_MOVED_FROM|unix.IN_MOVED_TO|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF|unix.IN_ATTRIB)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	return &inventoryChanges{fd: fd}, nil
}

func (w *inventoryChanges) Changed() bool {
	if w == nil {
		return false
	}
	if w.changed {
		return true
	}
	var events [4096]byte
	for {
		n, err := unix.Read(w.fd, events[:])
		if errors.Is(err, unix.EINTR) {
			continue
		}
		w.changed = n > 0 || (err != nil && !errors.Is(err, unix.EAGAIN))
		return w.changed
	}
}

func (w *inventoryChanges) Close() {
	if w != nil && w.fd >= 0 {
		unix.Close(w.fd)
		w.fd = -1
		w.changed = true
	}
}
