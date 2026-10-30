package playback

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/atomicfile"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// A descriptor's size and mtime do not identify its remote version. Retain the
// cut list only with this session's artifacts and pinned remote evidence. An
// evicted, missing, or stale copy is re-probed when the producer opens its window.
type windowCopyTimeline struct {
	Evidence string        `json:"evidence"`
	Timeline *copyTimeline `json:"timeline"`
}

func (h *HLS) windowTimelineEvidence(ctx context.Context, id string) (string, error) {
	var evidence string
	err := dbwork.QueryRow(ctx, h.db, `SELECT evidence FROM subtitle_remote_sessions WHERE session_id=?`, id).Scan(&evidence)
	if err == nil && evidence == "" {
		err = errCopyTimeline
	}
	return evidence, err
}

func (h *HLS) loadWindowTimeline(id, evidence string) *copyTimeline {
	file, err := os.Open(filepath.Join(h.root, id, "copy-timeline.json"))
	if err != nil {
		return nil
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return nil
	}
	var saved windowCopyTimeline
	if json.Unmarshal(raw, &saved) != nil || saved.Evidence != evidence || saved.Timeline.count() == 0 {
		return nil
	}
	return saved.Timeline
}

func (h *HLS) storeWindowTimeline(id, evidence string, t *copyTimeline) error {
	raw, err := json.Marshal(windowCopyTimeline{evidence, t})
	if err != nil {
		return err
	}
	if len(raw) > 1<<20 {
		return errCopyTimeline
	}
	return atomicfile.Write(filepath.Join(h.root, id, "copy-timeline.json"), raw, 0600)
}
