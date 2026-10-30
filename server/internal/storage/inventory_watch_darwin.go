//go:build darwin && cgo

package storage

/*
#cgo LDFLAGS: -framework CoreServices -framework CoreFoundation
#include <stdint.h>
#include <stdlib.h>
void *portico_watch_start(char **roots, int count, uintptr_t token);
void portico_watch_stop(void *watch);
*/
import "C"

import (
	"context"
	"path/filepath"
	"runtime/cgo"
	"unsafe"
)

type darwinInventoryWatcher struct {
	stream   unsafe.Pointer
	handle   cgo.Handle
	roots    []WatchRoot
	changes  chan InventoryChange
	overflow chan struct{}
}

func newInventoryWatcher(r WatchRequest) (inventoryWatcher, error) {
	w := &darwinInventoryWatcher{roots: r.Roots, changes: make(chan InventoryChange, 256), overflow: make(chan struct{}, 1)}
	w.handle = cgo.NewHandle(w)
	roots := make([]*C.char, len(r.Roots))
	for i, root := range r.Roots {
		roots[i] = C.CString(root.Root)
		defer C.free(unsafe.Pointer(roots[i]))
	}
	w.stream = C.portico_watch_start((**C.char)(unsafe.Pointer(&roots[0])), C.int(len(roots)), C.uintptr_t(w.handle))
	if w.stream == nil {
		w.handle.Delete()
		return nil, ErrWatchUnavailable
	}
	return w, nil
}

//export portico_watch_event
func portico_watch_event(token C.uintptr_t, path *C.char, flags C.uint32_t) {
	w := cgo.Handle(token).Value().(*darwinInventoryWatcher)
	// Kernel/user drops, wrapped history or a lost root require full observation.
	if uint32(flags)&(0x1|0x2|0x4|0x8|0x20) != 0 {
		select {
		case w.overflow <- struct{}{}:
		default:
		}
		return
	}
	full := C.GoString(path)
	for _, root := range w.roots {
		relative, err := filepath.Rel(root.Root, full)
		if err != nil || !filepath.IsLocal(relative) {
			continue
		}
		// Retain the named path so a removed file can be verified separately
		// from an ambiguous directory/overflow hint.
		change := InventoryChange{SourceID: root.SourceID, RelativePath: relative}
		select {
		case w.changes <- change:
		default:
			select {
			case w.overflow <- struct{}{}:
			default:
			}
		}
	}
}
func (w *darwinInventoryWatcher) Next(ctx context.Context) ([]InventoryChange, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case change := <-w.changes:
		return []InventoryChange{change}, nil
	case <-w.overflow:
		out := []InventoryChange{}
		for _, root := range w.roots {
			out = append(out, InventoryChange{SourceID: root.SourceID, RelativePath: ".", Overflow: true})
		}
		return out, nil
	}
}
func (w *darwinInventoryWatcher) Close() error {
	C.portico_watch_stop(w.stream)
	w.handle.Delete()
	return nil
}
