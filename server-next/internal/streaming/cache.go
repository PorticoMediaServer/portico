package streaming

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// CachedConversions measures, or removes, abandoned disk output. Every stream
// still owned by a viewer, its producers and its readers are left alone.
func (s *Streams) CachedConversions(ctx context.Context, empty bool) (int64, error) {
	h := s.hls
	entries, err := os.ReadDir(h.root)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var bytes int64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return bytes, err
		}
		id := entry.Name()
		if !entry.IsDir() || !generatedDirectoryID.MatchString(id) {
			continue // live buffers and files are not abandoned VOD output
		}
		if info, err := entry.Info(); err != nil || time.Since(info.ModTime()) < 2*time.Minute {
			continue // a producer may be creating its folder now
		}
		h.mu.Lock()
		busy := h.streams[id] != nil || h.sessionActiveLocked(id) || h.cleaning[id] || h.moving[id] != nil || h.outputBusy[id] > 0
		h.mu.Unlock()
		if busy {
			continue
		}
		n, _, err := h.measureOutput(filepath.Join(h.root, id))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return bytes, err
		}
		if empty {
			if err := h.remove(id); err != nil {
				if errors.Is(err, ErrSegmentPreparing) {
					continue
				}
				return bytes, err
			}
		}
		bytes += n
	}
	return bytes, nil
}
