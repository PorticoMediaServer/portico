package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A paced Worker sizes each domain's batch from its recent cost per unit:
// an unknown domain starts with one unit, cheap units double a batch up to
// BatchMax, expensive ones shrink it towards the floor, units that change many
// rows are held to the write plan, and an unpaced Worker keeps the caller's
// fixed limit.
func TestBatchesAreSizedToTheHoldTarget(t *testing.T) {
	w := NewWorker(nil)
	if got := w.batchFor(DomainCategories, 500); got != 500 {
		t.Fatalf("unpaced batch %d, want the caller's limit", got)
	}
	w.PaceToHold(20 * time.Millisecond)
	if got := w.batchFor(DomainCategories, BatchMax); got != 1 {
		t.Fatalf("first paced batch %d, want one unit", got)
	}
	// Cheap units (0.1 ms a unit) grow the batch by doubling up to BatchMax.
	for _, want := range []int{16, 32, 64} {
		w.observeBatch(DomainCategories, want/2, time.Duration(want/2)*100*time.Microsecond, int64(want/2))
		if got := w.batchFor(DomainCategories, BatchMax); got != want {
			t.Fatalf("cheap units: batch %d, want %d", got, want)
		}
	}
	if got := w.batchFor(DomainCategories, 16); got != 16 {
		t.Fatalf("batch %d exceeds the caller's limit", got)
	}
	for i := 0; i < 8; i++ {
		w.observeBatch(DomainBrowseRows, 2, 2*50*time.Millisecond, 2) // 50 ms a unit
	}
	if got := w.batchFor(DomainBrowseRows, BatchMax); got != batchFloor {
		t.Fatalf("expensive units: batch %d, want the floor %d", got, batchFloor)
	}
	for i := 0; i < 8; i++ {
		w.observeBatch(DomainBrowseRows, 10, 10*time.Millisecond, 10) // 1 ms a unit
	}
	if got := w.batchFor(DomainBrowseRows, BatchMax); got < 16 || got > 24 {
		t.Fatalf("1 ms units: batch %d, want about 20", got)
	}
	if got := w.batchFor(DomainCategories, BatchMax); got != BatchMax {
		t.Fatal("one domain's cost changed another's batch")
	}
	// Cheap units that each change many rows are bounded by the write plan's
	// 160 rows less the bookkeeping reserve: 40 rows fits three, 400 fits one.
	for i := 0; i < 8; i++ {
		w.observeBatch(DomainCategories, 8, 8*100*time.Microsecond, 8*40)
	}
	if got := w.batchFor(DomainCategories, BatchMax); got != 3 {
		t.Fatalf("40-row units: batch %d, want 3", got)
	}
	// One spike shrinks the next batch at once.
	w.observeBatch(DomainCategories, 1, 100*time.Microsecond, 400)
	if got := w.batchFor(DomainCategories, BatchMax); got != 1 {
		t.Fatalf("400-row units: batch %d, want 1", got)
	}
}

// Batch size changes when work commits, never what it derives: the same
// catalogue drained in paced batches and in single units holds the same
// derived rows.
func TestPacedBatchesDeriveTheSameCatalogue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	build := func(paced bool) *sql.DB {
		db := cc1OpenDB(t, fmt.Sprint("paced-", paced, ".sqlite"))
		tv := cc1Library(t, db, "tv", "TV", "tv", "/tv")
		music := cc1Library(t, db, "music", "Music", "music", "/music")
		show := cc1Entity(t, db, Entity{Library: tv, Kind: Show, Key: "show:a", Title: "A", Year: 2001}, map[string]any{"local_key": "a"})
		season := cc1Entity(t, db, Entity{Library: tv, Kind: Season, Parent: show, Key: "season:a", Title: "Season 1"}, map[string]any{"show_id": show, "number": 1})
		artist := cc1Entity(t, db, Entity{Library: music, Kind: Artist, Key: ArtistKey("x"), Title: "X"}, map[string]any{"local_key": "x"})
		album := cc1Entity(t, db, Entity{Library: music, Kind: Album, Parent: artist, Key: AlbumKey("y"), Title: "Y"}, map[string]any{"local_key": "y", "artist_id": artist})
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			for i := 0; i < 90; i++ {
				ep, _, err := UpsertEntityTx(ctx, tx, Entity{Library: tv, Kind: Episode, Parent: season, Key: fmt.Sprint("ep:", i), Title: fmt.Sprint("Episode ", i), Added: fmt.Sprintf("2026-01-%02dT00:00:00.000Z", 1+i%28)})
				if err != nil {
					return err
				}
				if err = SetFactsTx(ctx, tx, ep, map[string]any{"show_id": show, "season_id": season, "numbering": "seasonal", "number": i + 1}); err != nil {
					return err
				}
				song, _, err := UpsertEntityTx(ctx, tx, Entity{Library: music, Kind: Track, Parent: album, Key: fmt.Sprint("song:", i), Title: fmt.Sprint("Song ", i), Added: "2026-02-01T00:00:00.000Z"})
				if err != nil {
					return err
				}
				if err = SetFactsTx(ctx, tx, song, map[string]any{"album_id": album, "track_number": i + 1}); err != nil {
					return err
				}
				for _, item := range []int64{ep, song} {
					asset, _, err := UpsertAssetTx(ctx, tx, Asset{Path: fmt.Sprintf("/f/%d-%d", item, i), Size: int64(100 + i), Height: 720 + i, Duration: float64(60 + i)})
					if err != nil {
						return err
					}
					if err = LinkAssetTx(ctx, tx, item, asset, Link{}); err != nil {
						return err
					}
				}
			}
			return nil
		})
		w := NewWorker(db)
		limit := 1
		if paced {
			w.PaceToHold(BatchHoldTarget)
			limit = BatchMax
		}
		for i := 0; ; i++ {
			n, err := w.Step(context.Background(), limit)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				break
			}
			if i > 100000 {
				t.Fatal("worker did not drain")
			}
		}
		return db
	}
	single, paced := build(false), build(true)
	for _, q := range []string{
		`SELECT entity_id,library_id,kind,title,sort_key,year,added_text,available,recent_text,duration_max,episode_count,file_size,resolution_key,availability_state FROM catalog_browse_rows ORDER BY entity_id`,
		`SELECT entity_id,item_id,source FROM catalog_browse_memberships ORDER BY 1,2,3`,
		`SELECT entity_id,field,value FROM catalog_facet_members ORDER BY 1,2,3`,
		`SELECT library_id,kind,container,axis,value,total FROM catalog_browse_buckets ORDER BY 1,2,3,4,5`,
		`SELECT entity_id,search FROM catalog_search_documents ORDER BY 1`,
		// Block boundaries depend on arrival order; what they count does not.
		`SELECT library_id,kind,ordering,sum(total) FROM catalog_browse_blocks GROUP BY 1,2,3 ORDER BY 1,2,3`,
	} {
		a, b := groupRows(t, single, q), groupRows(t, paced, q)
		if a != b {
			t.Fatalf("paced batches derived differently for %.60s", strings.Fields(q)[1])
		}
	}
	for _, db := range []*sql.DB{single, paced} {
		if err := CheckBrowseBlocks(context.Background(), db); err != nil {
			t.Fatal(err)
		}
	}
}
