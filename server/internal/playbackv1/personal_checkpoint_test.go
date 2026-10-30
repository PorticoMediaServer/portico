package playbackv1

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Real P2-19 applies, real catalogue evidence, sixteen distinct media/items.
// This test is intentionally named separately for the lead's exclusive timing
// slot. The control removes ONLY the tested guard in this disposable fixture.
func TestA3RealSixteenCheckpointBatchEpochAndCost(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newObservationFixture(t)
	personal := catalog.New(f.catalog.DB)
	f.service.Playback.PersonalProgress = personal.AcceptPlaybackProgressEvidence
	var library int64
	if err := f.catalog.DB.QueryRow(`SELECT library_id FROM catalog_entities WHERE id=?`, f.items[0].ID).Scan(&library); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for i := len(f.items); i < observationBatch; i++ {
		item := f.catalog.Movie(library, filepath.Join(root, fmt.Sprintf("checkpoint-%02d.mp4", i)), fmt.Sprintf("Checkpoint %02d", i), 2026)
		f.catalog.Write(func(_ context.Context, tx *sql.Tx) error {
			return assets.PersistStreams(tx, item.Token, 1000, 1, []assets.Stream{
				{Index: 0, Type: "video", Codec: "h264", Detail: assets.StreamDetail{Profile: "High", Level: 41, BitDepth: 8, Width: 1920, Height: 1080, FrameRate: 24, DynamicRange: assets.RangeSDR}},
				{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Language: "eng", Default: true},
			})
		})
		f.items = append(f.items, item)
	}
	f.catalog.Drain()
	ids := []string{f.session.ID}
	for i := 1; i < observationBatch; i++ {
		q, err := f.service.CreateQueue(context.Background(), f.caller, fmt.Sprintf("a3-checkpoint-queue-%02d", i), CreateQueueRequest{Segments: []SegmentInput{{Source: Selector{Items: &ItemList{IDs: []string{f.items[i].Public}}}}}, StartPlayback: &StartPlayback{State: "playing"}})
		if err != nil || q.Session == nil {
			t.Fatal(q, err)
		}
		ids = append(ids, q.Session.ID)
	}
	epochs := make([]string, len(ids))
	generations := make([]int, len(ids))
	for i, id := range ids {
		if err := f.catalog.DB.QueryRow(`SELECT generation FROM playback_v1_sessions WHERE id=?`, id).Scan(&generations[i]); err != nil {
			t.Fatal(err)
		}
		grant, err := f.service.ClaimObserver(context.Background(), f.caller, id, ObserverClaim{Generation: generations[i]})
		if err != nil {
			t.Fatal(err)
		}
		epochs[i] = grant.Epoch
	}
	t.Cleanup(func() { _ = f.service.CloseObservations(context.Background()) })
	sample := func(seq int64) {
		t.Helper()
		f.clock.add(time.Second)
		for i, id := range ids {
			_, err := f.service.Observe(context.Background(), f.caller, id, Observation{Epoch: epochs[i], Seq: seq, Generation: generations[i], State: "playing", PositionMs: 400000 + seq*1000, MonotonicMs: seq * 1000, PlayClockMs: seq * 1000, Rate: 1})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	sample(1)
	if err := f.service.FlushObservations(context.Background()); err != nil {
		t.Fatal(err)
	}
	var media, personalRows int
	if err := f.catalog.DB.QueryRow(`SELECT count(DISTINCT media_session_id) FROM playback_v1_sessions WHERE ended_ms=0`).Scan(&media); err != nil {
		t.Fatal(err)
	}
	if err := f.catalog.DB.QueryRow(`SELECT count(*) FROM personal_items WHERE last_played_at!=''`).Scan(&personalRows); err != nil {
		t.Fatal(err)
	}
	if media != 16 || personalRows != 16 {
		t.Fatalf("fixture did not exercise sixteen real personal/media records: %d/%d", media, personalRows)
	}
	var trigger string
	if err := f.catalog.DB.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='trigger' AND name='selection_predicate_progress_update'`).Scan(&trigger); err != nil {
		t.Fatal(err)
	}
	type measurement struct {
		cost    dbwork.WriteCost
		elapsed time.Duration
	}
	run := func(seq int64) measurement {
		t.Helper()
		sample(seq)
		o := f.service.observations()
		batch := make([]*observer, 0, 16)
		o.mu.Lock()
		for _, id := range ids {
			e := o.entries[id]
			if e == nil || e.busy || !e.dirty {
				o.mu.Unlock()
				t.Fatal("fixture did not retain full routine batch")
			}
			e.busy = true
			batch = append(batch, e)
		}
		o.mu.Unlock()
		var before, after int64
		if err := f.catalog.DB.QueryRow(`SELECT revision FROM catalog_selection_predicate_epoch WHERE singleton=1`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		dbwork.ResetPlans()
		tickets := establishedPlaybackTickets()
		start := time.Now()
		if err := f.service.applyObservationBatch(context.Background(), o, batch); err != nil {
			t.Fatal(err)
		}
		elapsed := time.Since(start)
		cost := dbwork.Plans().Largest
		if establishedPlaybackTickets()-tickets != 1 {
			t.Fatal("sixteen checkpoint batch was not one writer transaction")
		}
		if err := f.catalog.DB.QueryRow(`SELECT revision FROM catalog_selection_predicate_epoch WHERE singleton=1`).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after != before {
			t.Fatalf("routine sixteen-session batch changed whole selection epoch %d->%d", before, after)
		}
		return measurement{cost: cost, elapsed: elapsed}
	}
	with := run(2)
	f.catalog.Exec(`DROP TRIGGER selection_predicate_progress_update`)
	without := run(3)
	f.catalog.Exec(trigger)
	t.Logf("real16 guarded=%s rows=%d pages=%d VM=%d; no-guard=%s rows=%d pages=%d VM=%d; added VM=%d", with.elapsed, with.cost.Rows, with.cost.Pages, with.cost.VMOps, without.elapsed, without.cost.Rows, without.cost.Pages, without.cost.VMOps, with.cost.VMOps-without.cost.VMOps)
	if with.cost.Rows != without.cost.Rows {
		t.Fatal("routine guard added writes", with.cost, without.cost)
	}
	if overhead := with.cost.VMOps - without.cost.VMOps; overhead > 512+without.cost.VMOps/20 {
		t.Fatal("routine guard VM overhead is material", with.cost, without.cost)
	}
	// An explicit Stop still records the true terminal time after checkpoints
	// stopped authoring recency. The most recently stopped item sorts first.
	beforeStop := f.clock.now()
	f.clock.add(time.Minute)
	if err := f.service.Stop(context.Background(), f.caller, ids[0], nil); err != nil {
		t.Fatal(err)
	}
	profile := identity.PersonalKey(f.caller.Principal.Viewer)
	var last string
	if err := f.catalog.DB.QueryRow(`SELECT last_played_at FROM personal_items WHERE profile_id=? AND item_id=?`, profile, f.items[0].ID).Scan(&last); err != nil {
		t.Fatal(err)
	}
	if at, err := time.Parse(time.RFC3339Nano, last); err != nil || !at.After(beforeStop) || !at.Equal(f.clock.now().UTC().Truncate(time.Millisecond)) {
		t.Fatalf("Stop lost true terminal recency: %s (%v)", last, err)
	}
	// Last-played order is the existing maintained personal projection. Publish
	// its ordinary queued work before comparing the real Browse ordering.
	f.catalog.Drain()
	page, err := personal.BrowseEntities(catalog.Viewer{Profile: profile, Fence: "a3-stop", Libraries: []string{"carry-library"}}, catalog.BrowseRequest{Library: "carry-library", Pivot: "movies", Sort: []catalog.BrowseSortSelection{{Field: "lastPlayed", Direction: "desc"}}, Limit: 1})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ID != f.items[0].Public {
		t.Fatal("Recently played order after Stop", page.Entries, err)
	}
}
