package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Focused tests for the bounded domain-25 item metric walk. They drive
// deriveItemMetricsBounded directly, one committed call at a time, so the
// job row, the raw-row budget and the multi-call fanout are observable. The
// caller's refreshEntityMetrics/touchContainers tail is exercised by the
// ordinary drain tests.

type p264Fixture struct {
	db    *sql.DB
	album int64
	track int64
}

func p264NewFixture(t *testing.T) p264Fixture {
	t.Helper()
	db := cc1OpenDB(t, "browse-metric-work.sqlite")
	library := cc1Library(t, db, "audio", "Audio", "music", "/music")
	album := cc1Entity(t, db, Entity{Library: library, Kind: Album, Key: "album:a", Title: "Album"}, nil)
	track := cc1Entity(t, db, Entity{Library: library, Kind: Track, Key: "track:a", Title: "Track"}, nil)
	f := p264Fixture{db: db, album: album, track: track}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		for _, id := range []int64{album, track} {
			if err := DeriveBrowseRowTx(ctx, tx, id); err != nil {
				return err
			}
		}
		return nil
	})
	return f
}

func (f p264Fixture) addEdge(t *testing.T, entity int64, source int) {
	t.Helper()
	cc1Exec(t, f.db, `INSERT OR IGNORE INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,?)`, entity, f.track, source)
}

func (f p264Fixture) addAsset(t *testing.T, path string, duration float64, size, height int64, available bool) int64 {
	t.Helper()
	var asset int64
	cc1Write(t, f.db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		asset, err = CreateAssetTx(ctx, tx, NewAssetToken(), Asset{
			Path: path, Size: size, ModifiedNS: 1, Container: "mkv",
			VideoCodec: "h264", AudioCodec: "aac", Width: 1920,
			Height: int(height), Duration: duration,
		}, available)
		return err
	})
	cc1Write(t, f.db, func(ctx context.Context, tx *sql.Tx) error {
		return LinkAssetTx(ctx, tx, f.track, asset, Link{})
	})
	return asset
}

func p264Touch(t *testing.T, db *sql.DB, item int64) int64 {
	t.Helper()
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return TouchTx(ctx, tx, DomainItemMetrics, item)
	})
	var revision int64
	if err := db.QueryRow(`SELECT revision FROM catalog_dirty WHERE domain=? AND entity_id=?`, DomainItemMetrics, item).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

func p264Run(t *testing.T, db *sql.DB, item, revision int64, limit, maxCalls int) map[int64]bool {
	t.Helper()
	touched := map[int64]bool{}
	for call := 0; call < maxCalls; call++ {
		done := false
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			done, err = deriveItemMetricsBounded(ctx, tx, Key{ID: item, Revision: revision}, limit, touched)
			return err
		})
		if done {
			return touched
		}
	}
	t.Fatalf("item %d did not finish in %d calls", item, maxCalls)
	return nil
}

type p264JobState struct {
	phase                  int
	available, unavailable int64
	afterAsset             int64
	afterProvider          string
	afterEntity            int64
	afterSource            int
	exists                 bool
}

func p264Job(t *testing.T, db *sql.DB, item int64) p264JobState {
	t.Helper()
	var state p264JobState
	err := db.QueryRow(`SELECT phase,link_available,link_unavailable,after_asset,after_provider,after_entity,after_source
 FROM catalog_item_metric_jobs WHERE item_id=?`, item).
		Scan(&state.phase, &state.available, &state.unavailable, &state.afterAsset, &state.afterProvider, &state.afterEntity, &state.afterSource)
	if errors.Is(err, sql.ErrNoRows) {
		return state
	}
	if err != nil {
		t.Fatal(err)
	}
	state.exists = true
	return state
}

type p264ItemMetricState struct {
	available, unavailable int64
	duration, rating       sql.NullFloat64
	added                  sql.NullString
}

func p264ItemMetric(t *testing.T, db *sql.DB, item int64) p264ItemMetricState {
	t.Helper()
	var state p264ItemMetricState
	if err := db.QueryRow(`SELECT available_links,unavailable_links,duration,rating,added
 FROM catalog_browse_item_metrics WHERE item_id=?`, item).
		Scan(&state.available, &state.unavailable, &state.duration, &state.rating, &state.added); err != nil {
		t.Fatal(err)
	}
	return state
}

type p264MemberMetricState struct {
	duration, rating       sql.NullFloat64
	added                  sql.NullString
	air                    string
	critic                 float64
	size, resolution       int64
	available, unavailable int64
}

func p264MemberMetric(t *testing.T, db *sql.DB, entity, item int64, source int) p264MemberMetricState {
	t.Helper()
	var state p264MemberMetricState
	if err := db.QueryRow(`SELECT duration,rating,added,air_key,critic,file_size,resolution,available_links,unavailable_links
 FROM catalog_browse_member_metrics WHERE entity_id=? AND item_id=? AND source=?`, entity, item, source).
		Scan(&state.duration, &state.rating, &state.added, &state.air, &state.critic,
			&state.size, &state.resolution, &state.available, &state.unavailable); err != nil {
		t.Fatal(err)
	}
	return state
}

func p264WantFloat(t *testing.T, label string, got sql.NullFloat64, want float64) {
	t.Helper()
	if !got.Valid || got.Float64 != want {
		t.Fatalf("%s duration = %#v, want %v", label, got, want)
	}
}

func p264WantNullFloat(t *testing.T, label string, got sql.NullFloat64) {
	t.Helper()
	if got.Valid {
		t.Fatalf("%s duration = %v, want NULL", label, got.Float64)
	}
}

func p264WantString(t *testing.T, label string, got sql.NullString, want string) {
	t.Helper()
	if !got.Valid || got.String != want {
		t.Fatalf("%s = %#v, want %q", label, got, want)
	}
}

func TestDeriveItemMetricsBoundedPublishesScalarsAndRepairsCounters(t *testing.T) {
	f := p264NewFixture(t)
	f.addEdge(t, f.track, 0)
	f.addEdge(t, f.album, 3)
	f.addAsset(t, "/media/a", -10, 100, 720, true)
	f.addAsset(t, "/media/b", 5, -7, 1080, false)
	f.addAsset(t, "/media/c", 3, 50, 480, true)
	cc1Exec(t, f.db, `INSERT INTO catalog_item_details(entity_id,release_date) VALUES(?,?)
 ON CONFLICT(entity_id) DO UPDATE SET release_date=excluded.release_date`, f.track, "2020-01-02")
	for _, rating := range []struct {
		provider string
		value    float64
	}{
		{"imdb", 7.5}, {"tmdb", 8}, {"metacritic", 60}, {"rottentomatoes", 90}, {"zzz", 1},
	} {
		cc1Exec(t, f.db, `INSERT INTO metadata_ratings(item_id,provider,value,scale,votes,source_url,observed_at)
 VALUES(?,?,?,10,0,'','')`, f.track, rating.provider, rating.value)
	}
	cc1Exec(t, f.db, `UPDATE catalog_browse_rows SET available=1,recent=1,added_text='2024-05-06T07:08:09Z' WHERE entity_id=?`, f.track)
	// Inject item-counter drift: the phase-2 publication must replace it with
	// the raw link flags.
	cc1Exec(t, f.db, `INSERT INTO catalog_browse_item_metrics(item_id,available_links,unavailable_links,duration,rating,added)
 VALUES(?,99,99,-1,-1,NULL)
 ON CONFLICT(item_id) DO UPDATE SET available_links=99,unavailable_links=99,duration=-1,rating=-1,added=NULL`, f.track)
	revision := p264Touch(t, f.db, f.track)
	touched := p264Run(t, f.db, f.track, revision, 8, 20)

	item := p264ItemMetric(t, f.db, f.track)
	if item.available != 2 || item.unavailable != 1 {
		t.Fatalf("item counters = %d/%d, want 2/1", item.available, item.unavailable)
	}
	p264WantFloat(t, "item", item.duration, 5)
	p264WantFloat(t, "item rating", item.rating, 8)
	p264WantString(t, "item added", item.added, "2024-05-06T07:08:09Z")

	for _, edge := range []struct {
		entity int64
		source int
	}{{f.track, 0}, {f.album, 3}} {
		got := p264MemberMetric(t, f.db, edge.entity, f.track, edge.source)
		p264WantFloat(t, fmt.Sprintf("member %d", edge.entity), got.duration, 5)
		p264WantFloat(t, fmt.Sprintf("member %d rating", edge.entity), got.rating, 8)
		p264WantString(t, fmt.Sprintf("member %d added", edge.entity), got.added, "2024-05-06T07:08:09Z")
		if got.air != "2020-01-02T00:00:00.000Z" {
			t.Fatalf("member %d air = %q", edge.entity, got.air)
		}
		if got.critic != 90 || got.size != 100 || got.resolution != 1080 {
			t.Fatalf("member %d critic/size/resolution = %v/%d/%d", edge.entity, got.critic, got.size, got.resolution)
		}
		if got.available != 2 || got.unavailable != 1 {
			t.Fatalf("member %d counters = %d/%d", edge.entity, got.available, got.unavailable)
		}
	}
	if job := p264Job(t, f.db, f.track); job.exists {
		t.Fatal("finished job row was not removed")
	}
	if !touched[f.track] || !touched[f.album] {
		t.Fatalf("touched = %v, want self and album", touched)
	}
}

func TestDeriveItemMetricsBoundedPreservesNegativeMaximaAndNulls(t *testing.T) {
	f := p264NewFixture(t)
	f.addEdge(t, f.track, 0)
	f.addEdge(t, f.album, 3)
	f.addAsset(t, "/media/a", -10, -100, -1080, true)
	f.addAsset(t, "/media/b", -5, -50, -720, false)
	cc1Exec(t, f.db, `INSERT INTO metadata_ratings(item_id,provider,value,scale,votes,source_url,observed_at)
 VALUES(?,'imdb',-3.5,10,0,'','')`, f.track)
	cc1Exec(t, f.db, `INSERT INTO metadata_ratings(item_id,provider,value,scale,votes,source_url,observed_at)
 VALUES(?,'metacritic',-5,10,0,'','')`, f.track)
	cc1Exec(t, f.db, `UPDATE catalog_browse_rows SET available=0,recent=0,added_text=NULL WHERE entity_id=?`, f.track)
	revision := p264Touch(t, f.db, f.track)
	p264Run(t, f.db, f.track, revision, 8, 20)

	item := p264ItemMetric(t, f.db, f.track)
	p264WantFloat(t, "item", item.duration, -5)
	p264WantFloat(t, "item rating", item.rating, -3.5)
	if item.added.Valid {
		t.Fatalf("item added = %q, want NULL", item.added.String)
	}
	if item.available != 1 || item.unavailable != 1 {
		t.Fatalf("item counters = %d/%d", item.available, item.unavailable)
	}
	got := p264MemberMetric(t, f.db, f.album, f.track, 3)
	p264WantFloat(t, "member", got.duration, -5)
	p264WantFloat(t, "member rating", got.rating, -3.5)
	if got.added.Valid {
		t.Fatalf("member added = %q, want NULL", got.added.String)
	}
	if got.air != "" || got.critic != -5 || got.size != -50 || got.resolution != -720 {
		t.Fatalf("member air/critic/size/resolution = %q/%v/%d/%d", got.air, got.critic, got.size, got.resolution)
	}
	if got.available != 1 || got.unavailable != 1 {
		t.Fatalf("member counters = %d/%d", got.available, got.unavailable)
	}
}

func TestDeriveItemMetricsBoundedReadsAirWithoutLinks(t *testing.T) {
	f := p264NewFixture(t)
	f.addEdge(t, f.track, 0)
	cc1Exec(t, f.db, `INSERT INTO catalog_item_details(entity_id,release_date) VALUES(?,?)
 ON CONFLICT(entity_id) DO UPDATE SET release_date=excluded.release_date`, f.track, "1999-12-31")
	cc1Exec(t, f.db, `UPDATE catalog_browse_rows SET available=0,recent=0,added_text=NULL WHERE entity_id=?`, f.track)
	// A stale item counter must be repaired to zero even when the item has no
	// links: the sample repair compares these counters with the raw rows.
	cc1Exec(t, f.db, `INSERT INTO catalog_browse_item_metrics(item_id,available_links,unavailable_links)
 VALUES(?,7,7)
 ON CONFLICT(item_id) DO UPDATE SET available_links=7,unavailable_links=7`, f.track)
	revision := p264Touch(t, f.db, f.track)
	p264Run(t, f.db, f.track, revision, 8, 20)

	item := p264ItemMetric(t, f.db, f.track)
	p264WantNullFloat(t, "item", item.duration)
	p264WantNullFloat(t, "item rating", item.rating)
	if item.added.Valid {
		t.Fatalf("item added = %q, want NULL", item.added.String)
	}
	if item.available != 0 || item.unavailable != 0 {
		t.Fatalf("item counters = %d/%d", item.available, item.unavailable)
	}
	got := p264MemberMetric(t, f.db, f.track, f.track, 0)
	if got.air != "1999-12-31T00:00:00.000Z" {
		t.Fatalf("member air = %q, want release date even with zero links", got.air)
	}
	p264WantNullFloat(t, "member", got.duration)
	p264WantNullFloat(t, "member rating", got.rating)
	if got.critic != 0 || got.size != 0 || got.resolution != 0 {
		t.Fatalf("member critic/size/resolution = %v/%d/%d", got.critic, got.size, got.resolution)
	}
}

func TestDeriveItemMetricsBoundedPagesRawInputsIncludingRejectedProviders(t *testing.T) {
	f := p264NewFixture(t)
	f.addEdge(t, f.track, 0)
	f.addEdge(t, f.album, 3)
	var assets []int64
	for i := 1; i <= 10; i++ {
		assets = append(assets, f.addAsset(t, fmt.Sprintf("/media/%02d", i),
			float64(i), int64(i*10), int64(i*100), i != 4))
	}
	for _, rating := range []struct {
		provider string
		value    float64
	}{
		{"aaa", 1}, {"abc", 2}, {"audible", 3}, {"imdb", 7},
		{"metacritic", 60}, {"rottentomatoes", 90}, {"tmdb", 8}, {"zzz", 100},
	} {
		cc1Exec(t, f.db, `INSERT INTO metadata_ratings(item_id,provider,value,scale,votes,source_url,observed_at)
 VALUES(?,?,?,10,0,'','')`, f.track, rating.provider, rating.value)
	}
	revision := p264Touch(t, f.db, f.track)

	done := false
	cc1Write(t, f.db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		done, err = deriveItemMetricsBounded(ctx, tx, Key{ID: f.track, Revision: revision}, 2, map[int64]bool{})
		return err
	})
	if done {
		t.Fatal("finished after two raw rows")
	}
	job := p264Job(t, f.db, f.track)
	if job.phase != 0 || job.afterAsset != assets[1] {
		t.Fatalf("first page = phase %d after asset %d, want phase 0 after %d", job.phase, job.afterAsset, assets[1])
	}
	if job.available+job.unavailable != 2 {
		t.Fatalf("first page captured %d raw links, want 2", job.available+job.unavailable)
	}

	// Walk to the provider phase. The two leading providers are rejected
	// spellings, so seeing after_provider at the second one proves the raw
	// scan charged them instead of filtering them out.
	for call := 0; call < 20; call++ {
		cc1Write(t, f.db, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			done, err = deriveItemMetricsBounded(ctx, tx, Key{ID: f.track, Revision: revision}, 2, map[int64]bool{})
			return err
		})
		if done {
			t.Fatal("finished before the provider phase was observed")
		}
		job = p264Job(t, f.db, f.track)
		if job.phase == 1 {
			break
		}
	}
	if job.phase != 1 {
		t.Fatalf("job stayed in phase %d", job.phase)
	}
	if job.available != 9 || job.unavailable != 1 {
		t.Fatalf("phase 1 lost the raw link capture: %d/%d", job.available, job.unavailable)
	}
	if job.afterProvider != "abc" {
		t.Fatalf("after provider = %q, want the second rejected spelling", job.afterProvider)
	}

	p264Run(t, f.db, f.track, revision, 2, 40)
	item := p264ItemMetric(t, f.db, f.track)
	if item.duration.Float64 != 10 || item.rating.Float64 != 8 || item.available != 9 || item.unavailable != 1 {
		t.Fatalf("item = duration %v rating %v counters %d/%d", item.duration, item.rating, item.available, item.unavailable)
	}
	got := p264MemberMetric(t, f.db, f.album, f.track, 3)
	if got.critic != 90 || got.size != 100 || got.resolution != 1000 {
		t.Fatalf("member critic/size/resolution = %v/%d/%d", got.critic, got.size, got.resolution)
	}
}

func TestDeriveItemMetricsBoundedRevisionDriftResetsTheWalk(t *testing.T) {
	f := p264NewFixture(t)
	f.addEdge(t, f.track, 0)
	f.addEdge(t, f.album, 3)
	var assets []int64
	for i := 1; i <= 6; i++ {
		assets = append(assets, f.addAsset(t, fmt.Sprintf("/media/%02d", i),
			float64(i), int64(i*10), int64(i*100), true))
	}
	cc1Exec(t, f.db, `INSERT INTO catalog_browse_item_metrics(item_id,available_links,unavailable_links,duration,rating,added)
 VALUES(?,99,99,-1,-1,NULL)
 ON CONFLICT(item_id) DO UPDATE SET available_links=99,unavailable_links=99`, f.track)
	firstRevision := p264Touch(t, f.db, f.track)
	cc1Write(t, f.db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := deriveItemMetricsBounded(ctx, tx, Key{ID: f.track, Revision: firstRevision}, 2, map[int64]bool{})
		return err
	})
	job := p264Job(t, f.db, f.track)
	if job.afterAsset != assets[1] || job.available+job.unavailable != 2 {
		t.Fatalf("initial page = asset %d, %d rows", job.afterAsset, job.available+job.unavailable)
	}

	// Change the underlying facts and queue the item again: the old cursor and
	// counters must not survive.
	cc1Exec(t, f.db, `UPDATE catalog_asset_links SET available=0 WHERE entity_id=? AND asset_id=?`, f.track, assets[0])
	f.addAsset(t, "/media/new", 20, 999, 2160, true)
	secondRevision := p264Touch(t, f.db, f.track)
	if secondRevision <= firstRevision {
		t.Fatalf("revision %d did not move past %d", secondRevision, firstRevision)
	}
	cc1Write(t, f.db, func(ctx context.Context, tx *sql.Tx) error {
		_, err := deriveItemMetricsBounded(ctx, tx, Key{ID: f.track, Revision: secondRevision}, 2, map[int64]bool{})
		return err
	})
	job = p264Job(t, f.db, f.track)
	if job.afterAsset != assets[1] {
		t.Fatalf("revision drift kept cursor at asset %d, want reset to %d", job.afterAsset, assets[1])
	}
	if job.available != 1 || job.unavailable != 1 {
		t.Fatalf("reset page counters = %d/%d, want the first two links", job.available, job.unavailable)
	}

	p264Run(t, f.db, f.track, secondRevision, 2, 40)
	var available, unavailable int64
	if err := f.db.QueryRow(`SELECT sum(available=1),sum(available=0) FROM catalog_asset_links WHERE entity_id=?`, f.track).
		Scan(&available, &unavailable); err != nil {
		t.Fatal(err)
	}
	item := p264ItemMetric(t, f.db, f.track)
	if item.available != available || item.unavailable != unavailable {
		t.Fatalf("repaired counters = %d/%d, raw = %d/%d", item.available, item.unavailable, available, unavailable)
	}
	if !item.duration.Valid || item.duration.Float64 != 20 {
		t.Fatalf("duration after drift = %#v, want 20", item.duration)
	}
}

func TestDeriveItemMetricsBoundedStopsOnOversizedRawRow(t *testing.T) {
	f := p264NewFixture(t)
	f.addEdge(t, f.track, 0)
	f.addAsset(t, "/media/a", 1, 10, 100, true)
	huge := strings.Repeat("z", metricRawBudget)
	cc1Exec(t, f.db, `INSERT INTO metadata_ratings(item_id,provider,value,scale,votes,source_url,observed_at)
 VALUES(?,'imdb',5,10,0,'','')`, f.track)
	cc1Exec(t, f.db, `INSERT INTO metadata_ratings(item_id,provider,value,scale,votes,source_url,observed_at)
 VALUES(?,?,1,10,0,'','')`, f.track, huge)
	revision := p264Touch(t, f.db, f.track)

	done := false
	cc1Write(t, f.db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		done, err = deriveItemMetricsBounded(ctx, tx, Key{ID: f.track, Revision: revision}, 8, map[int64]bool{})
		return err
	})
	if done {
		t.Fatal("oversized raw row did not end the quantum")
	}
	job := p264Job(t, f.db, f.track)
	if job.phase != 1 || job.afterProvider != huge {
		t.Fatalf("job = phase %d after provider %d bytes, want the oversized row alone", job.phase, len(job.afterProvider))
	}
	p264Run(t, f.db, f.track, revision, 8, 20)
	if item := p264ItemMetric(t, f.db, f.track); !item.rating.Valid || item.rating.Float64 != 5 {
		t.Fatalf("rating after oversized row = %#v, want 5", item.rating)
	}
}

func TestDeriveItemMetricsBoundedRemovesMissingMembershipMetric(t *testing.T) {
	f := p264NewFixture(t)
	f.addEdge(t, f.track, 0)
	f.addEdge(t, f.album, 3)
	f.addAsset(t, "/media/a", 1, 10, 100, true)
	const staleEntity = int64(999999)
	cc1Exec(t, f.db, `INSERT INTO catalog_browse_member_metrics(entity_id,item_id,source,duration,rating,added,
 air_key,critic,file_size,resolution,available_links,unavailable_links)
 VALUES(?,?,4,1,2,NULL,'',0,0,0,0,0)`, staleEntity, f.track)
	revision := p264Touch(t, f.db, f.track)
	p264Run(t, f.db, f.track, revision, 8, 20)
	if got := cc1Scalar(t, f.db, `SELECT count(*) FROM catalog_browse_member_metrics WHERE item_id=? AND entity_id=?`, f.track, staleEntity); got != 0 {
		t.Fatalf("stale member metric row survived: %d", got)
	}
	p264MemberMetric(t, f.db, f.album, f.track, 3)
}

func TestDeriveItemMetricsBoundedDropsDeletedItem(t *testing.T) {
	f := p264NewFixture(t)
	f.addEdge(t, f.track, 0)
	f.addEdge(t, f.album, 3)
	f.addAsset(t, "/media/a", 1, 10, 100, true)
	revision := p264Touch(t, f.db, f.track)
	p264Run(t, f.db, f.track, revision, 8, 20)
	cc1Write(t, f.db, func(ctx context.Context, tx *sql.Tx) error {
		return DeleteEntityTx(ctx, tx, f.track)
	})
	const staleEntity = int64(999998)
	cc1Exec(t, f.db, `INSERT INTO catalog_browse_member_metrics(entity_id,item_id,source,duration,rating,added,
 air_key,critic,file_size,resolution,available_links,unavailable_links)
 VALUES(?,?,4,1,2,NULL,'',0,0,0,0,0)`, staleEntity, f.track)
	var deletedRevision int64
	if err := f.db.QueryRow(`SELECT revision FROM catalog_dirty WHERE domain=? AND entity_id=?`, DomainItemMetrics, f.track).
		Scan(&deletedRevision); err != nil {
		t.Fatal(err)
	}
	p264Run(t, f.db, f.track, deletedRevision, 8, 10)
	if got := cc1Scalar(t, f.db, `SELECT count(*) FROM catalog_browse_item_metrics WHERE item_id=?`, f.track); got != 0 {
		t.Fatalf("deleted item metrics survived: %d", got)
	}
	if got := cc1Scalar(t, f.db, `SELECT count(*) FROM catalog_browse_member_metrics WHERE item_id=?`, f.track); got != 0 {
		t.Fatalf("deleted item member metrics survived: %d", got)
	}
	if job := p264Job(t, f.db, f.track); job.exists {
		t.Fatal("deleted item job row survived")
	}
}
