//go:build windows

package storage

import (
	"context"
	"encoding/binary"
	"golang.org/x/sys/windows"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"sync"
)

type windowsInventoryWatcher struct {
	handles []windows.Handle
	changes chan InventoryChange
	done    chan struct{}
	once    sync.Once
}

func newInventoryWatcher(r WatchRequest) (inventoryWatcher, error) {
	w := &windowsInventoryWatcher{changes: make(chan InventoryChange, 256), done: make(chan struct{})}
	for _, root := range r.Roots {
		path, err := windows.UTF16PtrFromString(root.Root)
		if err != nil {
			continue
		}
		h, err := windows.CreateFile(path, windows.FILE_LIST_DIRECTORY, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err != nil {
			continue
		}
		w.handles = append(w.handles, h)
		supervise.Go("storage.inventory-watch", func() { w.read(h, root) })
	}
	if len(w.handles) == 0 {
		return nil, ErrWatchUnavailable
	}
	return w, nil
}
func (w *windowsInventoryWatcher) send(change InventoryChange) {
	select {
	case w.changes <- change:
	case <-w.done:
	default:
		// Dropped detail becomes one root scan, rather than lost absence/additions.
		change.RelativePath = "."
		change.Overflow = true
		select {
		case w.changes <- change:
		case <-w.done:
		}
	}
}
func (w *windowsInventoryWatcher) read(h windows.Handle, root WatchRoot) {
	var buf [32 << 10]byte
	for {
		var size uint32
		err := windows.ReadDirectoryChanges(h, &buf[0], uint32(len(buf)), true, windows.FILE_NOTIFY_CHANGE_FILE_NAME|windows.FILE_NOTIFY_CHANGE_DIR_NAME|windows.FILE_NOTIFY_CHANGE_SIZE|windows.FILE_NOTIFY_CHANGE_LAST_WRITE|windows.FILE_NOTIFY_CHANGE_ATTRIBUTES, &size, nil, 0)
		if err != nil {
			w.send(InventoryChange{SourceID: root.SourceID, RelativePath: ".", Overflow: true})
			return
		}
		if size == 0 {
			w.send(InventoryChange{SourceID: root.SourceID, RelativePath: ".", Overflow: true})
			continue
		}
		for at := uint32(0); at+12 <= size; {
			next := binary.LittleEndian.Uint32(buf[at:])
			length := binary.LittleEndian.Uint32(buf[at+8:])
			if length%2 != 0 || at+12+length > size {
				w.send(InventoryChange{SourceID: root.SourceID, RelativePath: ".", Overflow: true})
				break
			}
			words := make([]uint16, length/2)
			for i := range words {
				words[i] = binary.LittleEndian.Uint16(buf[int(at)+12+i*2:])
			}
			relative := windows.UTF16ToString(words)
			if filepath.IsLocal(relative) {
				w.send(InventoryChange{SourceID: root.SourceID, RelativePath: relative})
			}
			if next == 0 {
				break
			}
			if next < 12 || at+next >= size {
				w.send(InventoryChange{SourceID: root.SourceID, RelativePath: ".", Overflow: true})
				break
			}
			at += next
		}
	}
}
func (w *windowsInventoryWatcher) Next(ctx context.Context) ([]InventoryChange, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case change := <-w.changes:
		return []InventoryChange{change}, nil
	}
}
func (w *windowsInventoryWatcher) Close() error {
	w.once.Do(func() {
		close(w.done)
		for _, h := range w.handles {
			windows.CancelIoEx(h, nil)
			windows.CloseHandle(h)
		}
	})
	return nil
}
