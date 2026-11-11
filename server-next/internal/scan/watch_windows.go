//go:build windows

package scan

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
)

// watcher hears of changes in folders through Windows (ReadDirectoryChangesW), which watches
// a folder with everything under it: one watch for each top folder, however many folders are
// inside. It says which library changed, not what: the scan finds that out.
type watcher struct {
	mu      sync.Mutex
	tops    map[string]windows.Handle // the folders watched, each with everything under it
	changes chan string
}

// What counts as a change: a file or folder made, removed or renamed, or a file's last-written
// time moving, which it does when writing to it ends.
const watchMask = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME | windows.FILE_NOTIFY_CHANGE_LAST_WRITE

func newWatcher() *watcher {
	return &watcher{tops: map[string]windows.Handle{}, changes: make(chan string, 256)}
}

func (w *watcher) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, handle := range w.tops {
		windows.CancelIoEx(handle, nil)
		windows.CloseHandle(handle)
	}
	w.tops = map[string]windows.Handle{}
}

// add watches one folder for a library. A folder inside one already watched needs nothing,
// which is why a library's folders are given top first. ok is false when the folder cannot
// be watched; a folder that is gone is not a failure. fresh says a watch was started.
func (w *watcher) add(library, folder string) (ok, fresh bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for top := range w.tops {
		if insideOrSame(folder, top) {
			return true, false
		}
	}
	name, err := windows.UTF16PtrFromString(folder)
	if err != nil {
		return false, false
	}
	handle, err := windows.CreateFile(name, windows.FILE_LIST_DIRECTORY, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND), false
	}
	w.tops[folder] = handle
	go w.follow(library, folder, handle)
	return true, true
}

// follow reports the library each time Windows says something under the folder changed, until
// the watch is closed or the folder goes away.
func (w *watcher) follow(library, folder string, handle windows.Handle) {
	buf := make([]byte, 64<<10)
	for {
		var n uint32
		// Each call waits for the next changes. Which entries changed is not read: also a
		// call that comes back with nothing (more changed than the buffer holds) is a change.
		if err := windows.ReadDirectoryChanges(handle, &buf[0], uint32(len(buf)), true, watchMask, &n, nil, 0); err != nil {
			w.mu.Lock()
			if w.tops[folder] == handle {
				windows.CloseHandle(handle)
				delete(w.tops, folder)
			}
			w.mu.Unlock()
			return
		}
		select {
		case w.changes <- library:
		default: // a report of this library is already waiting to be read
		}
	}
}

// run calls changed with the library of every change until ctx ends.
func (w *watcher) run(ctx context.Context, changed func(library string)) {
	for {
		select {
		case <-ctx.Done():
			return
		case library := <-w.changes:
			changed(library)
		}
	}
}

// insideOrSame reports whether a folder is another folder or inside it. Windows paths are
// compared without regard to case.
func insideOrSame(folder, top string) bool {
	rel, err := filepath.Rel(strings.ToLower(top), strings.ToLower(folder))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// networkShare reports whether a folder is on a network share, where Windows does not tell
// of every change made from other machines.
func networkShare(folder string) bool {
	if strings.HasPrefix(folder, `\\`) {
		return true
	}
	root, err := windows.UTF16PtrFromString(filepath.VolumeName(folder) + `\`)
	return err == nil && windows.GetDriveType(root) == windows.DRIVE_REMOTE
}

// watchesSubfolders: ReadDirectoryChangesW watches a folder with everything under it.
const watchesSubfolders = true
