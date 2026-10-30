package downloads

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

func TestOfflineProgressReplayUsesCurrentVisibility(t *testing.T) {
	h := newHarness(t)
	c := catalogtest.New(t, h.db)
	other := c.Movie(c.Handle("lib"), "/visible.mp4", "Visible film", 2020)
	c.Drain()
	entries := []ProgressEntry{
		{PlayID: "hidden-play", ItemID: h.item, PositionSeconds: 15, ObservedAt: h.now.Add(-time.Hour).Format(time.RFC3339)},
		// The catalog fixture is a 90-minute film. Cross its play-count
		// threshold so both initial plays really exist before testing replay.
		{PlayID: "visible-play", ItemID: other.Public, PositionSeconds: 600, ObservedAt: h.now.Add(-time.Hour).Format(time.RFC3339)},
	}
	first, err := h.service.SyncProgress(context.Background(), h.viewer, "visibility-replay", entries)
	if err != nil || first.Applied != 2 || first.Entries[0].PositionSeconds == 0 {
		t.Fatalf("initial progress receipt: %+v %v", first, err)
	}
	var plays int
	if err := h.db.QueryRow(`SELECT count(*) FROM play_history`).Scan(&plays); err != nil || plays != 2 {
		t.Fatalf("initial plays were not recorded: %d %v", plays, err)
	}
	scope := "downloads-progress:" + operations.ViewerKey(h.viewer)
	var stored string
	if err := h.db.QueryRow(`SELECT result FROM console_receipts WHERE scope=? AND key='visibility-replay'`, scope).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	c.Attributes(h.itemID, "label", "Blocked")
	c.Exec(`INSERT INTO profile_restrictions(profile_id,blocked_labels) VALUES('profile','["blocked"]')`)
	replay, err := h.service.SyncProgress(context.Background(), h.viewer, "visibility-replay", entries)
	if err != nil || replay.Applied != 1 || replay.Conflicts != 1 || len(replay.Entries) != 2 {
		t.Fatalf("restricted replay: %+v %v", replay, err)
	}
	hidden := replay.Entries[0]
	if hidden.PlayID != entries[0].PlayID || hidden.ItemID != h.item || hidden.Outcome != ProgressMissing || hidden.PositionSeconds != 0 || hidden.Watched || hidden.ObservedAt != "" {
		t.Fatalf("replay disclosed hidden progress: %+v", hidden)
	}
	if replay.Entries[1] != first.Entries[1] {
		t.Fatalf("visible progress changed: %+v", replay.Entries[1])
	}
	var retained string
	if err := h.db.QueryRow(`SELECT result FROM console_receipts WHERE scope=? AND key='visibility-replay'`, scope).Scan(&retained); err != nil || retained != stored {
		t.Fatalf("redaction rewrote receipt: %v", err)
	}
	if err := h.db.QueryRow(`SELECT count(*) FROM play_history`).Scan(&plays); err != nil || plays != 2 {
		t.Fatalf("replay reapplied plays: %d %v", plays, err)
	}
	c.Exec(`UPDATE profile_restrictions SET blocked_labels='[]',revision=revision+1 WHERE profile_id='profile'`)
	restored, err := h.service.SyncProgress(context.Background(), h.viewer, "visibility-replay", entries)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(restored)
	if err != nil || string(raw) != stored {
		t.Fatalf("visible replay lost original receipt: %+v %v", restored, err)
	}
	h.service.SetLibraryAccess(func(context.Context, *sql.Tx, identity.Principal, string) error { return identity.ErrNotVisible })
	denied, err := h.service.SyncProgress(context.Background(), h.viewer, "visibility-replay", entries)
	if err != nil || denied.Applied != 0 || denied.Conflicts != 2 || denied.Entries[0].Outcome != ProgressMissing || denied.Entries[1].Outcome != ProgressMissing {
		t.Fatalf("library revocation replay: %+v %v", denied, err)
	}
	broken := errors.New("visibility storage failure")
	h.service.SetLibraryAccess(func(context.Context, *sql.Tx, identity.Principal, string) error { return broken })
	failed, err := h.service.SyncProgress(context.Background(), h.viewer, "visibility-replay", entries)
	if !errors.Is(err, broken) || len(failed.Entries) != 0 {
		t.Fatalf("authorization error returned progress: %+v %v", failed, err)
	}
}
