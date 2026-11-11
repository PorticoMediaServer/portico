//go:build darwin

package scan

import (
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// watcher hears of changes in folders through the kernel (kqueue), one open folder for each
// folder watched. It says which library changed, not what: the scan finds that out. The
// kernel tells of a folder's entries changing, not of a file being written, so a folder that
// changed is followed until it settles (see settling).
type watcher struct {
	kq    int
	limit int // the most folders that may be held open

	mu      sync.Mutex
	folders map[int]watchedFolder // by the folder's open descriptor
	open    map[string]int        // the descriptor of each folder watched
	settle  settling
}

type watchedFolder struct{ library, path string }

// newWatcher returns nil where folders cannot be watched. Each watched folder costs an open
// file, so the process's limit is raised as far as it may be and most of it is used.
func newWatcher() *watcher {
	kq, err := unix.Kqueue()
	if err != nil {
		return nil
	}
	limit := 256
	var files unix.Rlimit
	if unix.Getrlimit(unix.RLIMIT_NOFILE, &files) == nil {
		if want := min(files.Max, 16384); files.Cur < want {
			raised := unix.Rlimit{Cur: want, Max: files.Max}
			if unix.Setrlimit(unix.RLIMIT_NOFILE, &raised) == nil {
				files.Cur = want
			}
		}
		limit = int(files.Cur)
	}
	// A quarter is left for everything else the server opens.
	return &watcher{kq: kq, limit: limit * 3 / 4, folders: map[int]watchedFolder{}, open: map[string]int{}}
}

func (w *watcher) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for fd := range w.folders {
		unix.Close(fd)
	}
	unix.Close(w.kq)
}

// add watches one folder for a library. ok is false when the folder cannot be watched: most
// often the limit on open files is reached, and the library is then scanned on its interval.
// A folder that is gone is not a failure. fresh says the folder was not watched before.
func (w *watcher) add(library, folder string) (ok, fresh bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, watched := w.open[folder]; watched {
		return true, false
	}
	if len(w.folders) >= w.limit {
		return false, false
	}
	fd, err := unix.Open(folder, unix.O_EVTONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR), false
	}
	event := unix.Kevent_t{}
	unix.SetKevent(&event, fd, unix.EVFILT_VNODE, unix.EV_ADD|unix.EV_CLEAR)
	event.Fflags = unix.NOTE_WRITE | unix.NOTE_EXTEND | unix.NOTE_DELETE | unix.NOTE_RENAME | unix.NOTE_REVOKE
	if _, err := unix.Kevent(w.kq, []unix.Kevent_t{event}, nil, nil); err != nil {
		unix.Close(fd)
		return false, false
	}
	w.folders[fd], w.open[folder] = watchedFolder{library, folder}, fd
	return true, true
}

// run calls changed with the library of every change until ctx ends.
func (w *watcher) run(ctx context.Context, changed func(library string)) {
	events := make([]unix.Kevent_t, 64)
	wait := unix.NsecToTimespec(int64(500 * time.Millisecond))
	for ctx.Err() == nil {
		n, err := unix.Kevent(w.kq, nil, events, &wait)
		if err != nil && !errors.Is(err, unix.EINTR) {
			return
		}
		now := time.Now()
		told := map[string]bool{}
		w.mu.Lock()
		for _, event := range events[:max(n, 0)] {
			fd := int(event.Ident)
			folder, known := w.folders[fd]
			if !known {
				continue
			}
			told[folder.library] = true
			if event.Fflags&(unix.NOTE_DELETE|unix.NOTE_RENAME|unix.NOTE_REVOKE) != 0 {
				// The folder is gone or moved: its watch goes, and the scan this starts
				// watches whatever is there now.
				unix.Close(fd)
				delete(w.folders, fd)
				delete(w.open, folder.path)
				continue
			}
			w.settle.touch(folder.path, folder.library, now)
		}
		w.settle.poll(now, func(library string) { told[library] = true })
		w.mu.Unlock()
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
	switch unix.ByteSliceToString(fs.Fstypename[:]) {
	case "nfs", "smbfs", "afpfs", "webdav", "ftp", "osxfuse", "macfuse", "fusefs":
		return true
	}
	return false
}

// watchesSubfolders: kqueue watches one folder at a time.
const watchesSubfolders = false
