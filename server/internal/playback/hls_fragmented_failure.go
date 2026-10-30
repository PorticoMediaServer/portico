package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"strconv"
	"strings"
	"syscall"
)

func (h *HLS) publicationFailed(id string, owned *hlsWindow, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if w := h.windows[id]; w == owned {
		w.publicationError = err
	}
}

// A changed initialization retires the entire presentation. No old grant can
// observe a differently shaped playlist; the controller publishes a new generation.
func (h *HLS) fragmentedFailure(ctx context.Context, id string, plan *DeliveryPlan, position float64, cause error) {
	if errors.Is(cause, syscall.ENOSPC) && h.retryMemoryOutput(id) {
		return
	}
	if !errors.Is(cause, errMP4InitChanged) || plan == nil {
		h.failedWith(ctx, id, FailureConverter, "fragmented output could not be published")
		return
	}
	owner, _ := ctx.Value(occurrenceOwnerKey{}).(*occurrenceOwner)
	if owner == nil || owner.id != id {
		return
	}
	release, err := childprocess.AcquirePublication(ctx)
	if err != nil {
		return
	}
	defer release()
	expected := owner.run.generation + 1
	write, done := persist(ctx)
	defer done()
	next, e := h.downgradeCopyPlan(write, id, plan, true)
	if e != nil {
		h.failedWith(ctx, id, FailureConverter, "fragmented output could not be converted")
		return
	}
	h.mu.Lock()
	delete(h.timelines, id)
	window := h.windows[id]
	waits := []<-chan struct{}{}
	for key, cancel := range h.active {
		if producerSession(key) == id && key != id {
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
	dir := h.outputDir(id)
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "segment-") || strings.HasPrefix(entry.Name(), "audio-") || strings.HasPrefix(entry.Name(), "init") || strings.HasSuffix(entry.Name(), ".m3u8") {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	next.TextRenditions = nil
	if e = h.loadTextRenditions(write, id, next); e == nil {
		e = publishRenditionManifests(dir, next, hlsTimelineManifest(next.Duration))
	}
	if e == nil {
		e = writeNamedManifest(dir, "generation", []byte(strconv.Itoa(expected)))
	}
	if e != nil {
		h.failedWith(ctx, id, FailureConverter, "replacement manifest could not be published")
		return
	}
	supervise.Go("playback.hls.fragmented-fallback", func() {
		if window != nil {
			select {
			case <-window.done:
			case <-h.ctx.Done():
				return
			}
		}
		_ = h.startGeneration(h.ctx, id, int(position)/HLSSegmentSeconds, expected)
	})
}
