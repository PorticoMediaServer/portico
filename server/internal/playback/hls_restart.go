package playback

import (
	"context"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
)

// restart discards every converted segment and moves the producer to the grid
// slot covering position. A rung change is exactly this: the output that exists
// was cut for a different target, so it is not this session's output any more.
// A plain seek does not come through here — it keeps its converted segments and
// only moves the producer, which is what makes seeking back instant.
func (h *HLS) restart(ctx context.Context, id string, positionSeconds float64) {
	// The plan may have changed between copying and converting the picture, and
	// with it the timeline. The cut list in memory belongs to the old plan; the
	// published playlist is rewritten by the next producer.
	h.mu.Lock()
	delete(h.timelines, id)
	h.mu.Unlock()
	index := 0
	if positionSeconds > 0 {
		index = int(positionSeconds) / HLSSegmentSeconds
	}
	if timeline := h.sessionTimeline(ctx, id); timeline != nil {
		index = timeline.indexAt(positionSeconds)
	}
	if index < 0 || index >= HLSMaxSegments {
		return
	}
	h.mu.Lock()
	waits := []<-chan struct{}{}
	for key, cancel := range h.active {
		if producerSession(key) == id {
			cancel()
			if w := h.windows[key]; w != nil {
				waits = append(waits, w.done)
			}
		}
	}
	h.mu.Unlock()
	for _, done := range waits {
		<-done
	}
	if err := h.waitOutputMove(ctx, id); err != nil {
		return
	}
	h.mu.Lock()
	if h.moving[id] != nil || h.cleaning[id] {
		h.mu.Unlock()
		return
	}
	if h.cleaning == nil {
		h.cleaning = map[string]bool{}
	}
	h.cleaning[id] = true
	dir := h.outputDirLocked(id)
	h.mu.Unlock()
	cleared := false
	clear := func() {
		if cleared {
			return
		}
		cleared = true
		h.mu.Lock()
		delete(h.cleaning, id)
		h.mu.Unlock()
	}
	defer clear()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return
	}
	for _, entry := range entries {
		if _, ok := hlsSegmentIndex(entry.Name()); ok || entry.Name() == "generation" || strings.HasPrefix(entry.Name(), "audio-") || strings.HasPrefix(entry.Name(), "init") || strings.HasSuffix(entry.Name(), ".m3u8") {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	h.mu.Lock()
	delete(h.reclaimed, id)
	h.mu.Unlock()
	write, done := persist(ctx)
	defer done()
	_, _ = dbwork.ExecWrite(write, h.db, dbwork.ClassEstablishedPlayback, `DELETE FROM playback_hls_demand WHERE session_id=?`, id)
	clear()
	_ = h.start(ctx, id, index)
}
