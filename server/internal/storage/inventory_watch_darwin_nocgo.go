//go:build darwin && !cgo

package storage

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
)

// Builds without the macOS frameworks use kqueue folder hints and the same
// periodic inventory fallback. Release macOS builds use recursive FSEvents.
type darwinInventoryWatcher struct {
	fd    int
	files []*os.File
	paths map[int]InventoryChange
}

func newInventoryWatcher(r WatchRequest) (inventoryWatcher, error) {
	fd, err := unix.Kqueue()
	if err != nil {
		return nil, err
	}
	w := &darwinInventoryWatcher{fd: fd, paths: map[int]InventoryChange{}}
	for _, root := range r.Roots {
		for _, relative := range root.Directories {
			path, err := watchDirectory(root, relative)
			if err != nil {
				continue
			}
			f, err := os.Open(path)
			if err != nil {
				continue
			}
			event := unix.Kevent_t{Ident: uint64(f.Fd()), Filter: unix.EVFILT_VNODE, Flags: unix.EV_ADD | unix.EV_CLEAR, Fflags: unix.NOTE_WRITE | unix.NOTE_DELETE | unix.NOTE_RENAME | unix.NOTE_ATTRIB}
			if _, err = unix.Kevent(fd, []unix.Kevent_t{event}, nil, nil); err != nil {
				f.Close()
				continue
			}
			w.files = append(w.files, f)
			w.paths[int(f.Fd())] = InventoryChange{SourceID: root.SourceID, RelativePath: relative}
		}
	}
	if len(w.files) == 0 {
		w.Close()
		return nil, ErrWatchUnavailable
	}
	return w, nil
}
func (w *darwinInventoryWatcher) Next(ctx context.Context) ([]InventoryChange, error) {
	for ctx.Err() == nil {
		events := make([]unix.Kevent_t, 128)
		timeout := unix.Timespec{Nsec: 500000000}
		n, err := unix.Kevent(w.fd, nil, events, &timeout)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return nil, err
		}
		if n == 0 {
			continue
		}
		out := []InventoryChange{}
		for _, event := range events[:n] {
			if change, ok := w.paths[int(event.Ident)]; ok {
				out = append(out, change)
			}
		}
		return out, nil
	}
	return nil, ctx.Err()
}
func (w *darwinInventoryWatcher) Close() error {
	for _, f := range w.files {
		f.Close()
	}
	return unix.Close(w.fd)
}
