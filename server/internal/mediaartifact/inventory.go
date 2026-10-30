package mediaartifact

import (
	"context"
	"io"
	"os"
	"time"
)

// Inventory is a bounded, restartable enumeration of closed physical objects.
// It supplies no publication or deletion authority. Callers must serialize a
// reference check with publication and call Remove (which honors reader leases).
// Names created during one pass may appear on the next pass; no full-directory
// allocation is required. This is domain-neutral and does not inspect content.
type Inventory struct{ directory *os.File }
type InventoryObject struct {
	Object   Object
	Modified time.Time
}

func (s *Store) Inventory() (*Inventory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrState
	}
	f, e := os.Open(s.objects)
	if e != nil {
		return nil, e
	}
	return &Inventory{directory: f}, nil
}
func (i *Inventory) Next(ctx context.Context, limit int) ([]InventoryObject, error) {
	if i.directory == nil || limit < 1 || limit > 256 {
		return nil, ErrState
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	entries, e := i.directory.ReadDir(limit)
	if e != nil && e != io.EOF {
		return nil, e
	}
	out := make([]InventoryObject, 0, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		object := Object{Digest: entry.Name(), Size: info.Size()}
		if validObject(object) {
			out = append(out, InventoryObject{object, info.ModTime()})
		}
	}
	return out, e
}
func (i *Inventory) Close() error {
	if i.directory == nil {
		return nil
	}
	e := i.directory.Close()
	i.directory = nil
	return e
}
