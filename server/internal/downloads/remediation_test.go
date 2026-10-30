package downloads

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
	"testing"
	"time"
)

func TestOfflineProgressHonorsPausedHistory(t *testing.T) {
	h := newHarness(t)
	if _, err := operations.New(h.db).ApplyPreferences(context.Background(), h.viewer, func(context.Context, *sql.Tx, string) error { return nil }, operations.PreferenceChange{Scope: operations.ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: "pause", Values: operations.PreferencePatch{"privacy.pauseWatchHistory": true}}); err != nil {
		t.Fatal(err)
	}
	got, err := h.service.SyncProgress(context.Background(), h.viewer, "paused-history", []ProgressEntry{{PlayID: "play-paused", ItemID: h.item, PositionSeconds: 42, Ended: true, ObservedAt: h.now.Add(-time.Minute).Format(time.RFC3339)}})
	if err != nil || len(got.Entries) != 1 || got.Entries[0].Outcome != ProgressHistoryPaused || got.Applied != 0 {
		t.Fatal(got, err)
	}
	// Nothing of the viewer's own is written while they have paused it...
	for _, table := range []string{"progress", "progress_activity", "personal_items", "personal_history", "personal_watched_intents", "download_progress_marks"} {
		var n int
		if err = h.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	// ...and the owner's record of plays is kept, as it is for online playback.
	var plays int
	if err = h.db.QueryRow(`SELECT count(*) FROM play_history`).Scan(&plays); err != nil || plays != 1 {
		t.Fatal("play_history", plays, err)
	}
}

func TestEveryDownloadRungHasMatchingRecipeAndEstimate(t *testing.T) {
	for _, rung := range playback.QualityLadder {
		recipe, err := preparedmedia.ProfileByID(preparedProfile("movie", rung))
		if err != nil || recipe.Height != rung.Height {
			t.Fatal(rung, recipe, err)
		}
		got, _ := estimateBytes(rung.ID, "", source{Kind: "movie", Duration: 100})
		want := int64(float64((recipe.VideoKbps+recipe.AudioKbps)*1000) / 8 * 100 * 1.03)
		if got != want {
			t.Fatal(rung.ID, got, want)
		}
	}
}

// Shared-count tests explicitly opt in. The actual default stays private.
func shareOfflineFixture(t *testing.T, h *harness) {
	t.Helper()
	if _, err := operations.New(h.db).ApplyPreferences(context.Background(), h.viewer, func(context.Context, *sql.Tx, string) error { return nil }, operations.PreferenceChange{Scope: operations.ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: "share-offline-fixture", Values: operations.PreferencePatch{"privacy.showActivityToMembers": true}}); err != nil {
		t.Fatal(err)
	}
}

func TestOfflinePrivacyDefaultRemainsUnshared(t *testing.T) {
	h := newHarness(t)
	if _, err := h.service.SyncProgress(context.Background(), h.viewer, "private-offline", []ProgressEntry{{PlayID: "private-play", ItemID: h.item, PositionSeconds: 60, Ended: true, ObservedAt: h.now.Add(-time.Minute).Format(time.RFC3339)}}); err != nil {
		t.Fatal(err)
	}
	var shared, personal int
	if err := h.db.QueryRow(`SELECT count(*) FROM play_counts`).Scan(&shared); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(`SELECT count(*) FROM personal_play_counts`).Scan(&personal); err != nil {
		t.Fatal(err)
	}
	if shared != 0 || personal != 1 {
		t.Fatalf("shared=%d personal=%d", shared, personal)
	}
}
