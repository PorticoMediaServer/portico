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
	// partial holds the sources inotify covers only in part: a folder list the
	// server truncated, or a folder whose watch could not be added (the
	// kernel's per-user watch limit is the usual reason).
	partial map[string]bool
	watches int
}

const linuxWatchMask = unix.IN_ONLYDIR | unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_CLOSE_WRITE | unix.IN_MODIFY | unix.IN_ATTRIB

func newInventoryWatcher(r WatchRequest) (inventoryWatcher, error) {
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		return nil, err
	}
	w := &linuxInventoryWatcher{fd: fd, directories: map[int][]InventoryChange{}, roots: r.Roots, partial: map[string]bool{}}
	for _, root := range r.Roots {
		if root.Truncated {
			w.partial[root.SourceID] = true
		}
		for _, relative := range root.Directories {
			if !w.watch(root, relative) {
				w.partial[root.SourceID] = true
			}
		}
	}
	if len(w.directories) == 0 {
		w.Close()
		return nil, ErrWatchUnavailable
	}
	return w, nil
}

// watch adds one folder of root. A folder that is already gone is not a gap in
// coverage; one that could not be watched is.
func (w *linuxInventoryWatcher) watch(root WatchRoot, relative string) bool {
	path, err := watchDirectory(root, relative)
	if err != nil {
		return errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR)
	}
	if w.watches >= MaxInventoryWatches {
		return false
	}
	wd, err := unix.InotifyAddWatch(w.fd, path, linuxWatchMask)
	if err != nil {
		return errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR)
	}
	change := InventoryChange{SourceID: root.SourceID, RelativePath: relative}
	for _, existing := range w.directories[wd] {
		if existing.SourceID == change.SourceID && existing.RelativePath == change.RelativePath {
			return true
		}
	}
	// One inode may be shared by several admitted library sources. Each
	// source must receive the hint; the request bounds the combined list.
	if len(w.directories[wd]) == 0 {
		w.watches++
	}
	w.directories[wd] = append(w.directories[wd], change)
	return true
}

func (w *linuxInventoryWatcher) unwatched() []string {
	out := make([]string, 0, len(w.partial))
	for _, root := range w.roots {
		if w.partial[root.SourceID] {
			out = append(out, root.SourceID)
		}
	}
	return out
}

func (w *linuxInventoryWatcher) root(source string) (WatchRoot, bool) {
	for _, root := range w.roots {
		if root.SourceID == source {
			return root, true
		}
	}
	return WatchRoot{}, false
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
		seen := map[string]bool{}
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
				named := name != "" && filepath.Base(name) == name && filepath.IsLocal(name)
				// inotify is not recursive: a folder created (or moved in)
				// under a watched one is watched from now on, so files added to
				// it later are seen without a rescan.
				created := named && mask&unix.IN_ISDIR != 0 && mask&(unix.IN_CREATE|unix.IN_MOVED_TO) != 0
				for _, change := range changes {
					if named {
						change.RelativePath = filepath.Join(change.RelativePath, name)
					}
					if created {
						if root, ok := w.root(change.SourceID); ok && !w.watch(root, change.RelativePath) && !w.partial[change.SourceID] {
							w.partial[change.SourceID] = true
							out = append(out, InventoryChange{SourceID: change.SourceID, Unwatched: []string{change.SourceID}})
						}
					}
					if !seen[change.SourceID+"\x00"+change.RelativePath] {
						out = append(out, change)
						seen[change.SourceID+"\x00"+change.RelativePath] = true
					}
				}
			}
			if mask&unix.IN_IGNORED != 0 {
				if len(w.directories[wd]) > 0 {
					w.watches--
				}
				delete(w.directories, wd)
			}
		}
		return out, nil
	}
	return nil, ctx.Err()
}
