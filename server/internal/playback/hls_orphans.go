package playback

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Each root has a resumable bounded iterator. Paths from the database are never
// followed; live/cleaning/moving sessions retain ownership of their output.
func (h *HLS) sweepOrphanDirectories(ctx context.Context) {
	for _, root := range []string{h.root, h.memoryRoot} {
		if root == "" {
			continue
		}
		for _, entry := range h.nextRootOrphanEntries(ctx, root) {
			if ctx.Err() != nil {
				return
			}
			id := entry.Name()
			info, err := entry.Info()
			if err != nil || time.Since(info.ModTime()) < 2*time.Minute {
				continue
			}
			if strings.HasPrefix(id, ".evicted-") || strings.HasPrefix(id, ".spill-") {
				h.mu.Lock()
				busy := false
				for moving := range h.moving {
					if strings.HasPrefix(id, ".spill-"+moving+"-") || strings.HasPrefix(id, ".evicted-"+moving+"-") {
						busy = true
						break
					}
				}
				h.mu.Unlock()
				if !busy {
					_ = os.RemoveAll(filepath.Join(root, id))
				}
				continue
			}
			placementMarker := root == h.root && strings.HasPrefix(id, diskPlacementPrefix) && info.Mode().IsRegular() && info.Size() == 0
			if placementMarker {
				id = strings.TrimPrefix(id, diskPlacementPrefix)
			}
			if h.db == nil || (!entry.IsDir() && !placementMarker) || !generatedDirectoryID.MatchString(id) {
				continue
			}
			var exists bool
			if err = h.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_sessions WHERE id=?) OR EXISTS(SELECT 1 FROM playback_artifacts WHERE session_id=?) OR EXISTS(SELECT 1 FROM playback_hls_reservations WHERE session_id=?)`, id, id, id).Scan(&exists); err != nil || exists {
				continue
			}
			h.mu.Lock()
			if h.sessionActiveLocked(id) || h.cleaning[id] || h.moving[id] != nil || h.outputBusy[id] > 0 {
				h.mu.Unlock()
				continue
			}
			if h.cleaning == nil {
				h.cleaning = map[string]bool{}
			}
			h.cleaning[id] = true
			h.mu.Unlock()
			retired := filepath.Join(root, ".evicted-"+id+"-"+time.Now().Format("150405.000000000"))
			if placementMarker {
				err = os.Remove(filepath.Join(root, entry.Name()))
			} else {
				err = h.renameOutput(filepath.Join(root, id), retired)
			}
			h.mu.Lock()
			delete(h.cleaning, id)
			if err == nil && placementMarker {
				delete(h.diskOnly, id)
			}
			if err == nil && root == h.memoryRoot {
				delete(h.inMemory, id)
				delete(h.memoryFull, id)
				delete(h.usage, id)
			}
			h.mu.Unlock()
			if err == nil {
				_ = os.RemoveAll(retired)
			}
		}
	}
}

func (h *HLS) nextOrphanEntries(ctx context.Context) []os.DirEntry {
	return h.nextRootOrphanEntries(ctx, h.root)
}
func (h *HLS) nextRootOrphanEntries(ctx context.Context, root string) []os.DirEntry {
	h.orphanMu.Lock()
	defer h.orphanMu.Unlock()
	if ctx.Err() != nil {
		return nil
	}
	cursor := &h.orphanDir
	if root == h.memoryRoot {
		cursor = &h.orphanMemoryDir
	}
	if *cursor == nil {
		var err error
		*cursor, err = os.Open(root)
		if err != nil {
			return nil
		}
	}
	entries, err := (*cursor).ReadDir(128)
	if err != nil {
		_ = (*cursor).Close()
		*cursor = nil
		if err != io.EOF {
			return nil
		}
	}
	return entries
}
func (h *HLS) closeOrphanDirectory() {
	h.orphanMu.Lock()
	defer h.orphanMu.Unlock()
	for _, cursor := range []**os.File{&h.orphanDir, &h.orphanMemoryDir} {
		if *cursor != nil {
			_ = (*cursor).Close()
			*cursor = nil
		}
	}
}
