package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// The group scans the maintained counters replaced, kept here as the
// reference: a container's availability over every member's file links, and
// a show's episode-borne facet values over every episode.
const referenceAvailabilitySQL = `SELECT entity_id,CASE WHEN NOT EXISTS(SELECT 1 FROM catalog_browse_memberships bm JOIN catalog_asset_links a ON a.entity_id=bm.item_id AND a.available=1 WHERE bm.entity_id=e.entity_id) THEN 'unavailable'
 WHEN EXISTS(SELECT 1 FROM catalog_browse_memberships bm JOIN catalog_asset_links a ON a.entity_id=bm.item_id AND a.available=0 WHERE bm.entity_id=e.entity_id) THEN 'partial' ELSE 'available' END FROM catalog_browse_rows e ORDER BY entity_id`

const referenceShowFacetsSQL = `SELECT s.entity_id,v.field,v.value FROM catalog_shows s CROSS JOIN (
 SELECT 'resolution' field,CASE WHEN ast.height>=2000 THEN '4k' WHEN ast.height>=1000 THEN '1080p' WHEN ast.height>=700 THEN '720p' ELSE 'sd' END value,ep.show_id show
  FROM catalog_episodes ep JOIN catalog_asset_links ia ON ia.entity_id=ep.entity_id JOIN catalog_assets ast ON ast.id=ia.asset_id
 UNION SELECT 'collection',CAST(cm.collection_id AS TEXT),ep.show_id FROM catalog_episodes ep JOIN catalog_collection_members cm ON cm.item_id=ep.entity_id
 UNION SELECT f.field,ca.source_value,ep.show_id FROM catalog_episodes ep JOIN catalog_item_attribute_edges ca ON ca.item_id=ep.entity_id
  JOIN catalog_attribute_terms t ON t.id=ca.term_id JOIN catalog_attribute_fields f ON f.id=t.field_id WHERE ca.source_value<>''
 UNION SELECT 'collection',CAST(cm.collection_id AS TEXT),cm.item_id FROM catalog_collection_members cm
 UNION SELECT 'year',CAST(year AS TEXT),entity_id FROM catalog_browse_rows WHERE kind=2 AND year BETWEEN 1800 AND 2199
 UNION SELECT 'decade',CAST(((year/10)*10) AS TEXT),entity_id FROM catalog_browse_rows WHERE kind=2 AND year BETWEEN 1800 AND 2199
 ) v ON v.show=s.entity_id WHERE EXISTS(SELECT 1 FROM catalog_browse_rows r WHERE r.entity_id=s.entity_id) ORDER BY 1,2,3`

func groupRows(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out []string
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i, v := range values {
			if b, ok := v.([]byte); ok {
				values[i] = string(b)
			}
		}
		out = append(out, fmt.Sprint(values...))
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(out, "\n")
}

func checkGroupCounters(t *testing.T, db *sql.DB, step string) {
	t.Helper()
	if want, got := groupRows(t, db, referenceAvailabilitySQL), groupRows(t, db, `SELECT entity_id,availability_state FROM catalog_browse_rows ORDER BY entity_id`); want != got {
		t.Fatalf("%s: availability differs from a member scan\nwant\n%s\ngot\n%s", step, want, got)
	}
	// The counters equal a recount of every container's member links.
	if diff := cc1Scalar(t, db, `SELECT count(*) FROM (
	 SELECT m.entity_id,sum(l.available=1),sum(l.available=0) FROM catalog_browse_memberships m JOIN catalog_asset_links l ON l.entity_id=m.item_id GROUP BY m.entity_id
	 EXCEPT SELECT entity_id,available,unavailable FROM catalog_browse_member_links WHERE available>0 OR unavailable>0)`); diff != 0 {
		t.Fatalf("%s: %d member-link counters disagree with a recount", step, diff)
	}
	if extra := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_member_links c WHERE (available<>0 OR unavailable<>0) AND NOT EXISTS(SELECT 1 FROM catalog_browse_memberships m JOIN catalog_asset_links l ON l.entity_id=m.item_id WHERE m.entity_id=c.entity_id)`); extra != 0 {
		t.Fatalf("%s: %d counters without member links", step, extra)
	}
	shows := `SELECT entity_id,field,value FROM catalog_facet_members WHERE entity_id IN(SELECT entity_id FROM catalog_shows) AND field IN('resolution','collection','year','decade','studio','network','tag') ORDER BY 1,2,3`
	if want, got := groupRows(t, db, referenceShowFacetsSQL), groupRows(t, db, shows); want != got {
		t.Fatalf("%s: show facets differ from an episode scan\nwant\n%s\ngot\n%s", step, want, got)
	}
	if err := CheckFacetCounts(context.Background(), db); err != nil {
		t.Fatalf("%s: %v", step, err)
	}
}

// A container's availability and a show's facets come from counters kept per
// member change; they must equal a scan of the members after every kind of
// change: files appearing, going and changing, members moving and leaving.
func TestGroupCountersMatchMemberScans(t *testing.T) {
	db := cc1OpenDB(t, "group-counters.sqlite")
	lib := cc1Library(t, db, "tv", "TV", "tv", "/tv")
	show := cc1Entity(t, db, Entity{Library: lib, Kind: Show, Key: "show:a", Title: "A", Year: 2001}, map[string]any{"local_key": "a"})
	season := cc1Entity(t, db, Entity{Library: lib, Kind: Season, Parent: show, Key: "season:a", Title: "Season 1"}, map[string]any{"show_id": show, "number": 1})
	other := cc1Entity(t, db, Entity{Library: lib, Kind: Show, Key: "show:b", Title: "B", Year: 1995}, map[string]any{"local_key": "b"})
	otherSeason := cc1Entity(t, db, Entity{Library: lib, Kind: Season, Parent: other, Key: "season:b", Title: "Season 1"}, map[string]any{"show_id": other, "number": 1})
	collection := cc1Entity(t, db, Entity{Library: lib, Kind: Collection, Key: CollectionKey("set"), Title: "Set"}, nil)
	episodes := make([]int64, 12)
	assets := make([]int64, 12)
	for i := range episodes {
		episodes[i] = cc1Entity(t, db, Entity{Library: lib, Kind: Episode, Parent: season, Key: fmt.Sprint("ep:", i), Title: fmt.Sprint("Episode ", i), Added: "2026-01-01T00:00:00.000Z"},
			map[string]any{"show_id": show, "season_id": season, "numbering": "seasonal", "number": i + 1})
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			asset, _, err := UpsertAssetTx(ctx, tx, Asset{Path: fmt.Sprintf("/tv/a/%d.mkv", i), Size: 100, Height: []int{480, 720, 1080, 2160}[i%4], Duration: 1200})
			if err != nil {
				return err
			}
			assets[i] = asset
			if err = LinkAssetTx(ctx, tx, episodes[i], asset, Link{}); err != nil {
				return err
			}
			if i%3 == 0 {
				if err = SetAttributesTx(ctx, tx, episodes[i], "studio", []string{fmt.Sprint("Studio ", i%2)}); err != nil {
					return err
				}
			}
			if i == 5 {
				return SetCollectionMemberTx(ctx, tx, collection, episodes[i], "a", true)
			}
			return nil
		})
	}
	cc1Drain(t, db)
	checkGroupCounters(t, db, "built")

	// A file goes missing and comes back; another changes resolution.
	cc1Exec(t, db, `UPDATE catalog_asset_links SET available=0 WHERE entity_id=?`, episodes[1])
	touchAll := func(ctx context.Context, tx *sql.Tx) error {
		return TouchTx(ctx, tx, DomainBrowseRows, append([]int64{show, season, other, otherSeason, collection}, episodes...)...)
	}
	cc1Write(t, db, touchAll)
	cc1Drain(t, db)
	checkGroupCounters(t, db, "file unavailable")
	cc1Exec(t, db, `UPDATE catalog_asset_links SET available=1 WHERE entity_id=?`, episodes[1])
	cc1Exec(t, db, `UPDATE catalog_assets SET height=2160 WHERE id=?`, assets[2])
	cc1Write(t, db, touchAll)
	cc1Drain(t, db)
	checkGroupCounters(t, db, "file back, resolution changed")

	// A file is unlinked, a second version added, attributes replaced.
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := UnlinkAssetTx(ctx, tx, episodes[3], assets[3]); err != nil {
			return err
		}
		if err := LinkAssetTx(ctx, tx, episodes[4], assets[3], Link{}); err != nil {
			return err
		}
		return SetAttributesTx(ctx, tx, episodes[0], "studio", []string{"Studio Z"})
	})
	cc1Drain(t, db)
	checkGroupCounters(t, db, "links and attributes changed")

	// An episode moves to another show; another is deleted.
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_entities SET parent_id=? WHERE id=?`, otherSeason, episodes[6]); err != nil {
			return err
		}
		if err := SetFactsTx(ctx, tx, episodes[6], map[string]any{"show_id": other, "season_id": otherSeason}); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM catalog_entities WHERE id=?`, episodes[9])
		return err
	})
	cc1Drain(t, db)
	checkGroupCounters(t, db, "moved and deleted")
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_show_facet_episodes WHERE episode_id=?`, episodes[9]); got != 0 {
		t.Fatalf("deleted episode left %d facet sources", got)
	}
}

// cc1DrainTrickleSeed builds only the initial fixture. Current bounded facet
// publication takes 2,310 worker admissions for 300 episodes (3.59 s with
// Go 1.26.10); the old 500-step helper stopped before the measured edit began.
// Use the canonical 10k fixture-drain bound here. The production raw-eight,
// byte and write budgets, and the post-edit 40-unit/seek assertions below,
// remain unchanged.
func cc1DrainTrickleSeed(t *testing.T, db *sql.DB) {
	t.Helper()
	worker := NewWorker(db)
	for step := 0; step < 10000; step++ {
		n, err := worker.Step(context.Background(), 500)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("catalogue fixture seed did not drain within 10000 bounded steps")
}

// One episode's new details (a provider filling them in one title at a time)
// must cost the same in a show of 30 episodes as in one of thousands: no
// statement walks the show's episodes or members, and the container's
// availability and facets still answer from counters.
func TestEpisodeTrickleCostIndependentOfShowSize(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	sizes := []int{30, 300}
	if os.Getenv("PORTICO_PERFORMANCE_TIER") != "" {
		sizes = []int{30, 5000}
	}
	var costs []time.Duration
	for _, n := range sizes {
		db := cc1OpenDB(t, fmt.Sprint("trickle-", n, ".sqlite"))
		lib := cc1Library(t, db, "tv", "TV", "tv", "/tv")
		show := cc1Entity(t, db, Entity{Library: lib, Kind: Show, Key: "show:a", Title: "A"}, map[string]any{"local_key": "a"})
		season := cc1Entity(t, db, Entity{Library: lib, Kind: Season, Parent: show, Key: "season:a", Title: "Season 1"}, map[string]any{"show_id": show, "number": 1})
		var last int64
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			for i := 0; i < n; i++ {
				id, _, err := UpsertEntityTx(ctx, tx, Entity{Library: lib, Kind: Episode, Parent: season, Key: fmt.Sprint("ep:", i), Title: fmt.Sprint("Episode ", i)})
				if err != nil {
					return err
				}
				if err = SetFactsTx(ctx, tx, id, map[string]any{"show_id": show, "season_id": season, "numbering": "seasonal", "number": i + 1}); err != nil {
					return err
				}
				asset, _, err := UpsertAssetTx(ctx, tx, Asset{Path: fmt.Sprintf("/tv/a/%d.mkv", i), Size: 100, Height: 1080, Duration: 1200})
				if err != nil {
					return err
				}
				if err = LinkAssetTx(ctx, tx, id, asset, Link{}); err != nil {
					return err
				}
				last = id
			}
			return nil
		})
		cc1DrainTrickleSeed(t, db)
		// The show's details arrive, then one episode's.
		ctx, trace := dbwork.TraceQueryPlans(context.Background())
		start := time.Now()
		cc1Write(t, db, func(_ context.Context, tx *sql.Tx) error {
			if err := SetFactsTx(ctx, tx, show, map[string]any{"overview": "A show.", "studio": "Studio"}); err != nil {
				return err
			}
			if err := SetAttributesTx(ctx, tx, last, "studio", []string{"Studio"}); err != nil {
				return err
			}
			return SetFactsTx(ctx, tx, last, map[string]any{"number": n + 1})
		})
		w := NewWorker(db)
		units := 0
		for {
			done, err := w.Step(ctx, BatchLimit)
			if err != nil {
				t.Fatal(err)
			}
			if done == 0 {
				break
			}
			units += done
		}
		costs = append(costs, time.Since(start))
		statements := trace()
		for _, q := range statements {
			flat := strings.Join(strings.Fields(q), " ")
			if strings.Contains(flat, "ep.show_id=?1") || strings.Contains(flat, "catalog_browse_memberships bm JOIN catalog_asset_links") {
				t.Fatalf("show of %d: a trickle update walked the show: %.200s", n, flat)
			}
		}
		// The show's details re-derive the show, not its episodes.
		if units > 40 {
			t.Fatalf("show of %d: one show and one episode change drained %d units", n, units)
		}
		if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_facet_members WHERE entity_id=? AND field='studio' AND value='Studio'`, show); got != 1 {
			t.Fatalf("show of %d: episode studio did not reach the show's facets", n)
		}
		t.Logf("show of %d episodes: trickle drained %d units, %d statements, %s", n, units, len(statements), costs[len(costs)-1])
	}
}

// A file going missing reaches its title's and its show's availability through
// derivation alone (no other change to either), and only titles whose
// availability actually changed are derived again.
func TestFileAvailabilityReachesTitleAndShow(t *testing.T) {
	db := cc1OpenDB(t, "availability-follow.sqlite")
	lib := cc1Library(t, db, "tv", "TV", "tv", "/tv")
	show := cc1Entity(t, db, Entity{Library: lib, Kind: Show, Key: "show:a", Title: "A"}, map[string]any{"local_key": "a"})
	season := cc1Entity(t, db, Entity{Library: lib, Kind: Season, Parent: show, Key: "season:a", Title: "Season 1"}, map[string]any{"show_id": show, "number": 1})
	var first, firstAsset, secondAsset int64
	for i := 0; i < 3; i++ {
		ep := cc1Entity(t, db, Entity{Library: lib, Kind: Episode, Parent: season, Key: fmt.Sprint("ep:", i), Title: fmt.Sprint("Episode ", i)},
			map[string]any{"show_id": show, "season_id": season, "numbering": "seasonal", "number": i + 1})
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			for v := 0; v < 2 && (i == 0 || v == 0); v++ {
				asset, _, err := UpsertAssetTx(ctx, tx, Asset{Path: fmt.Sprintf("/tv/a/%d-%d.mkv", i, v), Size: 100, Height: 1080, Duration: 1200})
				if err != nil {
					return err
				}
				if err = LinkAssetTx(ctx, tx, ep, asset, Link{}); err != nil {
					return err
				}
				if i == 0 && v == 0 {
					first, firstAsset = ep, asset
				} else if i == 0 {
					secondAsset = asset
				}
			}
			return nil
		})
	}
	cc1Drain(t, db)
	state := func(id int64) string {
		var s string
		if err := db.QueryRow(`SELECT availability_state FROM catalog_browse_rows WHERE entity_id=?`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if state(first) != "available" || state(show) != "available" {
		t.Fatalf("built: %s/%s", state(first), state(show))
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return SetAssetAvailableTx(ctx, tx, firstAsset, false) })
	cc1Drain(t, db)
	if state(first) != "partial" || state(show) != "partial" {
		t.Fatalf("one version missing: title %s, show %s", state(first), state(show))
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return SetAssetAvailableTx(ctx, tx, secondAsset, false) })
	cc1Drain(t, db)
	if state(first) != "unavailable" || state(show) != "partial" || cc1Scalar(t, db, `SELECT available FROM catalog_browse_rows WHERE entity_id=?`, first) != 0 {
		t.Fatalf("both versions missing: title %s, show %s", state(first), state(show))
	}
	checkGroupCounters(t, db, "files missing")
	// Settled availability queues nothing more.
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return TouchTx(ctx, tx, DomainAvailability, first) })
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		_, _, _, err := NewWorker(db).drainDomain(ctx, tx, DomainAvailability, 0, 8, false)
		return err
	})
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain IN(18,25)`); n != 0 {
		t.Fatalf("unchanged availability queued %d rows", n)
	}
}

// An artist's overview or tags arriving must not send each of its songs back
// to matching; a change to its identity still does.
func TestArtistDetailsLeaveSongMatchingAlone(t *testing.T) {
	db := cc1OpenDB(t, "artist-details.sqlite")
	lib := cc1Library(t, db, "music", "Music", "music", "/music")
	artist := cc1Entity(t, db, Entity{Library: lib, Kind: Artist, Key: ArtistKey("x"), Title: "X"}, map[string]any{"local_key": "x"})
	album := cc1Entity(t, db, Entity{Library: lib, Kind: Album, Parent: artist, Key: AlbumKey("y"), Title: "Y"}, map[string]any{"local_key": "y", "artist_id": artist})
	for i := 0; i < 3; i++ {
		song := cc1Entity(t, db, Entity{Library: lib, Kind: Track, Parent: album, Key: fmt.Sprint("song:", i), Title: fmt.Sprint("Song ", i)}, map[string]any{"album_id": album, "track_number": i + 1})
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return SetSongArtistsTx(ctx, tx, song, []int64{artist}) })
	}
	jobs := func() int { return cc1Scalar(t, db, `SELECT COALESCE(sum(revision),0) FROM mb_jobs WHERE kind='song'`) }
	before := jobs()
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetFactsTx(ctx, tx, artist, map[string]any{"overview": "An artist.", "tags": "rock"})
	})
	if after := jobs(); after != before {
		t.Fatalf("artist overview re-queued song matching: %d → %d", before, after)
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetFactsTx(ctx, tx, artist, map[string]any{"local_key": "x2"})
	})
	if after := jobs(); after == before {
		t.Fatal("artist identity change did not reach song matching")
	}
}
