//go:build linux

package storage

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"golang.org/x/sys/unix"
	"path/filepath"
)

type linuxInventoryWatcher struct {
	fd          int
	directories map[int][]InventoryChange
	roots       []WatchRoot
}

func newInventoryWatcher(r WatchRequest) (inventoryWatcher, error) {
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		return nil, err
	}
	w := &linuxInventoryWatcher{fd: fd, directories: map[int][]InventoryChange{}, roots: r.Roots}
	for _, root := range r.Roots {
		for _, relative := range root.Directories {
			path, err := watchDirectory(root, relative)
			if err != nil {
				continue
			}
			wd, err := unix.InotifyAddWatch(fd, path, unix.IN_ONLYDIR|unix.IN_CREATE|unix.IN_DELETE|unix.IN_MOVED_FROM|unix.IN_MOVED_TO|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF|unix.IN_CLOSE_WRITE|unix.IN_MODIFY|unix.IN_ATTRIB)
			if err != nil {
				continue
			}
			// One inode may be shared by several admitted library sources. Each
			// source must receive the hint; the request bounds the combined list.
			w.directories[wd] = append(w.directories[wd], InventoryChange{SourceID: root.SourceID, RelativePath: relative})
		}
	}
	if len(w.directories) == 0 {
		w.Close()
		return nil, ErrWatchUnavailable
	}
	return w, nil
}
func (w *linuxInventoryWatcher) Close() error { return unix.Close(w.fd) }
func (w *linuxInventoryWatcher) Next(ctx context.Context) ([]InventoryChange, error) {
	poll := []unix.PollFd{{Fd: int32(w.fd), Events: unix.POLLIN}}
	for ctx.Err() == nil {
		_, err := unix.Poll(poll, 500)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if poll[0].Revents == 0 {
			continue
		}
		var buf [64 << 10]byte
		n, err := unix.Read(w.fd, buf[:])
		if errors.Is(err, unix.EAGAIN) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out := []InventoryChange{}
		seen := map[InventoryChange]bool{}
		for at := 0; at+unix.SizeofInotifyEvent <= n; {
			wd := int(int32(binary.NativeEndian.Uint32(buf[at:])))
			mask := binary.NativeEndian.Uint32(buf[at+4:])
			size := int(binary.NativeEndian.Uint32(buf[at+12:]))
			nameStart := at + unix.SizeofInotifyEvent
			at += unix.SizeofInotifyEvent + size
			if at > n {
				return nil, ErrInventoryChanged
			}
			if mask&unix.IN_Q_OVERFLOW != 0 {
				for _, root := range w.roots {
					out = append(out, InventoryChange{SourceID: root.SourceID, RelativePath: ".", Overflow: true})
				}
				return out, nil
			}
			if changes, ok := w.directories[wd]; ok {
				name := string(bytes.TrimRight(buf[nameStart:at], "\x00"))
				for _, change := range changes {
					if name != "" && filepath.Base(name) == name && filepath.IsLocal(name) {
						change.RelativePath = filepath.Join(change.RelativePath, name)
					}
					if !seen[change] {
						out = append(out, change)
						seen[change] = true
					}
				}
			}
			if mask&unix.IN_IGNORED != 0 {
				delete(w.directories, wd)
			}
		}
		return out, nil
	}
	return nil, ctx.Err()
}
