//go:build linux

package scan

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"

	"golang.org/x/sys/unix"
)

// watcher hears of changes in folders through the kernel (inotify), which costs nothing
// while nothing changes. It says which library changed, not what: the scan finds that out.
type watcher struct {
	fd int

	mu        sync.Mutex
	libraries map[int32]string // by watch
}

// What counts as a change: a file or folder made, removed, moved in or out, or written and
// closed. Writing alone does not count, so a download in progress is not a scan a second.
const watchMask = unix.IN_ONLYDIR | unix.IN_CREATE | unix.IN_DELETE | unix.IN_MOVED_FROM | unix.IN_MOVED_TO | unix.IN_CLOSE_WRITE |
	unix.IN_DELETE_SELF | unix.IN_MOVE_SELF

// newWatcher returns nil where folders cannot be watched.
func newWatcher() *watcher {
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		return nil
	}
	return &watcher{fd: fd, libraries: map[int32]string{}}
}

func (w *watcher) close() { unix.Close(w.fd) }

// add watches one folder for a library. ok is false when the folder cannot be watched: most
// often the kernel's limit on watches is reached. A folder that is gone is not a failure.
// fresh says the folder was not watched before, so changes in it until now went unheard.
func (w *watcher) add(library, folder string) (ok, fresh bool) {
	wd, err := unix.InotifyAddWatch(w.fd, folder, watchMask)
	if err != nil {
		return errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR), false
	}
	w.mu.Lock()
	_, had := w.libraries[int32(wd)]
	w.libraries[int32(wd)] = library
	w.mu.Unlock()
	return true, !had
}

// run calls changed with the library of every change until ctx ends. When the kernel says it
// lost changes, every watched library is reported.
func (w *watcher) run(ctx context.Context, changed func(library string)) {
	poll := []unix.PollFd{{Fd: int32(w.fd), Events: unix.POLLIN}}
	buf := make([]byte, 64<<10)
	for ctx.Err() == nil {
		if n, err := unix.Poll(poll, 500); err != nil && !errors.Is(err, unix.EINTR) {
			return
		} else if n <= 0 || poll[0].Revents == 0 {
			continue
		}
		n, err := unix.Read(w.fd, buf)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
				continue
			}
			return
		}
		told := map[string]bool{}
		for at := 0; at+unix.SizeofInotifyEvent <= n; {
			wd := int32(binary.NativeEndian.Uint32(buf[at:]))
			mask := binary.NativeEndian.Uint32(buf[at+4:])
			at += unix.SizeofInotifyEvent + int(binary.NativeEndian.Uint32(buf[at+12:]))
			w.mu.Lock()
			library, known := w.libraries[wd]
			if mask&unix.IN_IGNORED != 0 {
				delete(w.libraries, wd) // the folder is gone, and its watch with it
			}
			if mask&unix.IN_Q_OVERFLOW != 0 {
				for _, each := range w.libraries {
					told[each] = true
				}
			}
			w.mu.Unlock()
			if known && mask&unix.IN_IGNORED == 0 {
				told[library] = true
			}
		}
		for library := range told {
			changed(library)
		}
	}
}

// networkShare reports whether a folder is on a file system that does not tell of changes
// made from other machines: a network share, or a mount another program provides.
func networkShare(folder string) bool {
	var fs unix.Statfs_t
	if unix.Statfs(folder, &fs) != nil {
		return false
	}
	switch int64(fs.Type) {
	case unix.NFS_SUPER_MAGIC, unix.SMB_SUPER_MAGIC, unix.SMB2_SUPER_MAGIC, unix.CIFS_SUPER_MAGIC, unix.FUSE_SUPER_MAGIC, unix.AFS_SUPER_MAGIC,
		unix.CODA_SUPER_MAGIC, unix.NCP_SUPER_MAGIC, unix.V9FS_MAGIC, unix.CEPH_SUPER_MAGIC:
		return true
	}
	return false
}
