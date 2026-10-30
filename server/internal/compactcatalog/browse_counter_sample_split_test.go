package compactcatalog

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func browseCounterQueue(t *testing.T, db *sql.DB, query string, args ...any) []int64 {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func browseCounterQueuedItems(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	return browseCounterQueue(t, db, `SELECT entity_id FROM catalog_dirty WHERE domain=? ORDER BY entity_id`, DomainItemMetrics)
}

func browseCounterQueuedRepairs(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	return browseCounterQueue(t, db, `SELECT entity_id FROM catalog_browse_counter_repairs ORDER BY entity_id`)
}

func browseCounterQueuedContainers(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	return browseCounterQueue(t, db, `SELECT entity_id FROM catalog_dirty WHERE domain=? ORDER BY entity_id`, DomainContainerRefresh)
}

func browseCounterDriftedOnly(t *testing.T, queued, drifted []int64) {
	t.Helper()
	for _, id := range queued {
		if !slices.Contains(drifted, id) {
			t.Fatalf("sample queued healthy holder %d from %v", id, drifted)
		}
	}
}

// TestBrowseCounterSampleSplitsHoldersPerWriter checks the process-level sample
// against the transactional sampler it replaced. Both are given the same seed,
// so they must pick and queue exactly the same holders: every drifted item, the
// drifted containers among the sampled ones, the healthy item left alone, and a
// stale repair for a deleted container dropped. The sampler must spend one
// short read snapshot plus one maintenance writer per sampled holder rather
// than checking all of them inside the last writer.
func TestBrowseCounterSampleSplitsHoldersPerWriter(t *testing.T) {
	db := cc1OpenDB(t, "browse-counter-sample-split.sqlite")
	items, shows, _ := browseCounterFixture(t, db, browseCounterSample, browseCounterSample)
	if len(items) != browseCounterSample || len(shows) != browseCounterSample {
		t.Fatalf("fixture made %d items and %d shows, want %d of each", len(items), len(shows), browseCounterSample)
	}
	// Drift every item but one, and all but one show. The item metrics table has
	// exactly one row per item, so its whole sibling is one of the sampled
	// holders; the member-links stream samples only eight of its rows, so a
	// drifted container is only queued when that key was sampled.
	for i := 0; i < browseCounterSample-1; i++ {
		cc1Exec(t, db, `UPDATE catalog_browse_item_metrics SET available_links=available_links+2 WHERE item_id=?`, items[i])
		cc1Exec(t, db, `UPDATE catalog_browse_member_links SET available=available+2,members=members+1 WHERE entity_id=?`, shows[i])
	}
	driftedItems := items[:browseCounterSample-1]
	driftedShows := shows[:browseCounterSample-1]
	healthyItem := items[browseCounterSample-1]
	healthyShow := shows[browseCounterSample-1]
	const staleRepair = 999998
	cc1Exec(t, db, `INSERT INTO catalog_browse_counter_repairs(entity_id,phase) VALUES(?,1)`, staleRepair)

	seed := int64(41)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return sampleBrowseCounterIntegrityTxAt(ctx, tx, seed)
	})
	wantItems := browseCounterQueuedItems(t, db)
	wantRepairs := browseCounterQueuedRepairs(t, db)
	wantContainers := browseCounterQueuedContainers(t, db)
	if len(wantItems) != len(driftedItems) || !slices.Equal(wantItems, driftedItems) {
		t.Fatalf("transactional sample queued items %v, want %v", wantItems, driftedItems)
	}
	if !slices.Equal(wantRepairs, wantContainers) {
		t.Fatalf("transactional sample queued repairs %v and containers %v differently", wantRepairs, wantContainers)
	}
	browseCounterDriftedOnly(t, wantRepairs, driftedShows)
	if slices.Contains(wantRepairs, healthyShow) {
		t.Fatal("transactional sample queued a healthy container")
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_counter_repairs WHERE entity_id=?`, staleRepair); got != 0 {
		t.Fatal("transactional sample left a repair for a deleted container")
	}

	// Reset the queue and replay the same drift through the split sampler.
	cc1Exec(t, db, `DELETE FROM catalog_browse_counter_repairs`)
	cc1Exec(t, db, `DELETE FROM catalog_dirty WHERE domain IN (?,?)`, DomainItemMetrics, DomainContainerRefresh)
	cc1Exec(t, db, `INSERT INTO catalog_browse_counter_repairs(entity_id,phase) VALUES(?,1)`, staleRepair)
	holders := min(browseCounterSample, cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_item_metrics`)) +
		min(browseCounterSample, cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_member_links`)) +
		min(browseCounterSample, cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_counter_repairs`))

	ctx, cost := dbwork.Measure(context.Background())
	if err := sampleBrowseCounterIntegrityAt(ctx, db, seed); err != nil {
		t.Fatal(err)
	}
	gotItems := browseCounterQueuedItems(t, db)
	gotRepairs := browseCounterQueuedRepairs(t, db)
	gotContainers := browseCounterQueuedContainers(t, db)
	if !slices.Equal(gotItems, wantItems) {
		t.Fatalf("split sample queued items %v, want %v", gotItems, wantItems)
	}
	if !slices.Equal(gotRepairs, wantRepairs) {
		t.Fatalf("split sample queued repairs %v, want %v", gotRepairs, wantRepairs)
	}
	if !slices.Equal(gotContainers, wantContainers) {
		t.Fatalf("split sample queued containers %v, want %v", gotContainers, wantContainers)
	}
	if slices.Contains(gotItems, healthyItem) || slices.Contains(gotRepairs, healthyShow) {
		t.Fatal("split sample queued a healthy sibling")
	}
	browseCounterDriftedOnly(t, gotRepairs, driftedShows)
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_counter_repairs WHERE entity_id=?`, staleRepair); got != 0 {
		t.Fatal("split sample left a repair for a deleted container")
	}

	// One read snapshot, then one writer for each sampled item, container and
	// repair key: no holder is checked in another holder's writer.
	if got, want := cost().Transactions, int64(holders+1); got != want {
		t.Fatalf("split sample opened %d transactions, want %d (one read snapshot and one writer per holder)", got, want)
	}
}
