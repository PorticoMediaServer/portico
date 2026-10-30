package downloads

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"testing"
	"time"
)

func TestOfflineProgressClampsToKnownDuration(t *testing.T) {
	h := newHarness(t)
	// The fixture film is 60 seconds long. A position past its end is the end,
	// and the end of a film is a finished play: watched, with nothing to resume.
	entries := []ProgressEntry{{PlayID: "play-bounded", ItemID: h.item, PositionSeconds: 600, ObservedAt: h.now.Format(time.RFC3339)}}
	out, err := h.service.SyncProgress(context.Background(), h.viewer, "bounded-progress", entries)
	if err != nil {
		t.Fatal(err)
	}
	if out.Applied != 1 || out.Entries[0].PositionSeconds != 0 || !out.Entries[0].Watched {
		t.Fatalf("%+v", out)
	}
	var stored, history int64
	var unit int
	if err = h.db.QueryRow(`SELECT position,unit FROM progress WHERE profile_id=? AND item_id=?`, identity.PersonalKey(h.viewer.Viewer), h.itemID).Scan(&stored, &unit); err != nil || stored != 0 || unit != 0 {
		t.Fatalf("stored %v %v", stored, err)
	}
	if err = h.db.QueryRow(`SELECT position_ms FROM play_history`).Scan(&history); err != nil || history != 60_000 {
		t.Fatalf("the play was recorded at %v, not at the film's end: %v", history, err)
	}
	replay, err := h.service.SyncProgress(context.Background(), h.viewer, "bounded-progress", entries)
	if err != nil || replay.Entries[0].PositionSeconds != 0 {
		t.Fatalf("replay %+v %v", replay, err)
	}
}
