package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// browseCounterCheckExact is the independent recount oracle: item counters
// equal the raw link rows; container counters equal the applied member-metric
// contributions and the membership rows; no counter is negative.
func browseCounterCheckExact(t *testing.T, db *sql.DB, step string) {
	t.Helper()
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_item_metrics WHERE available_links<0 OR unavailable_links<0`); n != 0 {
		t.Fatalf("%s: %d item counters are negative", step, n)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_item_metrics i
 WHERE i.available_links<>(SELECT count(*) FROM catalog_asset_links l WHERE l.entity_id=i.item_id AND l.available=1)
    OR i.unavailable_links<>(SELECT count(*) FROM catalog_asset_links l WHERE l.entity_id=i.item_id AND l.available=0)`); n != 0 {
		t.Fatalf("%s: %d item link counters disagree with the raw links", step, n)
	}
	browseCounterCheckContainers(t, db, step)
}

func browseCounterCheckContainers(t *testing.T, db *sql.DB, step string) {
	t.Helper()
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_member_links WHERE available<0 OR unavailable<0 OR members<0`); n != 0 {
		t.Fatalf("%s: %d container counters are negative", step, n)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_member_links c
 WHERE c.available<>COALESCE((SELECT sum(m.available_links) FROM catalog_browse_member_metrics m WHERE m.entity_id=c.entity_id),0)
    OR c.unavailable<>COALESCE((SELECT sum(m.unavailable_links) FROM catalog_browse_member_metrics m WHERE m.entity_id=c.entity_id),0)
    OR c.members<>(SELECT count(*) FROM catalog_browse_memberships b WHERE b.entity_id=c.entity_id)`); n != 0 {
		t.Fatalf("%s: %d container counters disagree with applied metrics and memberships", step, n)
	}
}

func browseCounterDrain(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := Drain(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

// browseCounterFixture builds shows and episodes, links one asset per episode
// and drains the ordinary derivations, so item metrics, memberships, applied
// member metrics and container counters all exist.
func browseCounterFixture(t *testing.T, db *sql.DB, items, shows int) (itemIDs, showIDs, assetIDs []int64) {
	t.Helper()
	lib := cc1Library(t, db, "counter", "Counter", "tv", "/counter")
	showIDs = make([]int64, shows)
	for i := range showIDs {
		showIDs[i] = cc1Entity(t, db, Entity{Library: lib, Kind: Show, Key: fmt.Sprintf("show:%d", i), Title: fmt.Sprintf("Show %d", i)}, map[string]any{"local_key": fmt.Sprintf("%d", i)})
	}
	itemIDs = make([]int64, items)
	assetIDs = make([]int64, items)
	for i := range itemIDs {
		show := showIDs[i%shows]
		itemIDs[i] = cc1Entity(t, db, Entity{Library: lib, Kind: Episode, Parent: show, Key: fmt.Sprintf("ep:%d", i), Title: fmt.Sprintf("Episode %d", i), Added: "2026-01-01T00:00:00.000Z"},
			map[string]any{"show_id": show, "number": i + 1, "numbering": "seasonal"})
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			asset, _, err := UpsertAssetTx(ctx, tx, Asset{Path: fmt.Sprintf("/counter/%d.mkv", i), Size: 100, Height: 1080, Duration: 1200})
			if err != nil {
				return err
			}
			assetIDs[i] = asset
			return LinkAssetTx(ctx, tx, itemIDs[i], asset, Link{})
		})
	}
	browseCounterDrain(t, db)
	return itemIDs, showIDs, assetIDs
}

func browseCounterRandomLink(t *testing.T, db *sql.DB, rng *rand.Rand) (item, asset int64, ok bool) {
	t.Helper()
	n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_asset_links`)
	if n == 0 {
		return 0, 0, false
	}
	if err := db.QueryRow(`SELECT entity_id,asset_id FROM catalog_asset_links ORDER BY entity_id,asset_id LIMIT 1 OFFSET ?`, rng.Intn(n)).Scan(&item, &asset); err != nil {
		t.Fatal(err)
	}
	return item, asset, true
}

func browseCounterRandomMembership(t *testing.T, db *sql.DB, rng *rand.Rand) (container, item int64, source int, ok bool) {
	t.Helper()
	n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_memberships`)
	if n == 0 {
		return 0, 0, 0, false
	}
	if err := db.QueryRow(`SELECT entity_id,item_id,source FROM catalog_browse_memberships ORDER BY entity_id,item_id,source LIMIT 1 OFFSET ?`, rng.Intn(n)).Scan(&container, &item, &source); err != nil {
		t.Fatal(err)
	}
	return container, item, source, true
}

func browseCounterRandomMetric(t *testing.T, db *sql.DB, rng *rand.Rand) (container, item int64, source int, ok bool) {
	t.Helper()
	n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_member_metrics`)
	if n == 0 {
		return 0, 0, 0, false
	}
	if err := db.QueryRow(`SELECT entity_id,item_id,source FROM catalog_browse_member_metrics ORDER BY entity_id,item_id,source LIMIT 1 OFFSET ?`, rng.Intn(n)).Scan(&container, &item, &source); err != nil {
		t.Fatal(err)
	}
	return container, item, source, true
}

// browseCounterApply runs one random mutation in its own write transaction.
// Some moves are legitimately rejected by auxiliary uniqueness (for example a
// link move that collides with a playback-origin association): the transaction
// rolls back, so the counters must still match the unchanged catalogue. Any
// error other than a constraint rejection fails the test.
func browseCounterApply(t *testing.T, db *sql.DB, fn func(context.Context, *sql.Tx) error) bool {
	t.Helper()
	err := dbwork.WithWriteTxContext(context.Background(), db, dbwork.ClassMaintenance, fn)
	if err == nil {
		return true
	}
	if !strings.Contains(err.Error(), "constraint failed") && !strings.Contains(err.Error(), "UNIQUE constraint") {
		t.Fatalf("random mutation: %v", err)
	}
	browseCounterCheckExact(t, db, "rejected mutation")
	return false
}

// browseCounterRandomOp applies one link, membership or metric insert, update
// or delete, or an entity move, directly to the maintained tables so the
// triggers are the only thing keeping the counters honest. A move that an
// auxiliary table legitimately rejects is rolled back and checked as a no-op.
func browseCounterRandomOp(t *testing.T, db *sql.DB, rng *rand.Rand, items, shows, assets []int64) string {
	t.Helper()
	switch rng.Intn(11) {
	case 0:
		item, asset := items[rng.Intn(len(items))], assets[rng.Intn(len(assets))]
		available := rng.Intn(2)
		browseCounterApply(t, db, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO catalog_asset_links(entity_id,asset_id,available) VALUES(?,?,?) ON CONFLICT(entity_id,asset_id) DO NOTHING`, item, asset, available)
			return err
		})
		return "link insert"
	case 1:
		item, asset, ok := browseCounterRandomLink(t, db, rng)
		if !ok {
			return "link update (none)"
		}
		browseCounterApply(t, db, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE catalog_asset_links SET available=1-available WHERE entity_id=? AND asset_id=?`, item, asset)
			return err
		})
		return "link update"
	case 2:
		item, asset, ok := browseCounterRandomLink(t, db, rng)
		if !ok {
			return "link delete (none)"
		}
		browseCounterApply(t, db, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, item, asset)
			return err
		})
		return "link delete"
	case 3:
		item, asset, ok := browseCounterRandomLink(t, db, rng)
		if !ok {
			return "link move (none)"
		}
		target := items[rng.Intn(len(items))]
		browseCounterApply(t, db, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE OR IGNORE catalog_asset_links SET entity_id=? WHERE entity_id=? AND asset_id=?`, target, item, asset)
			return err
		})
		return "link move"
	case 4:
		container, item, source := shows[rng.Intn(len(shows))], items[rng.Intn(len(items))], rng.Intn(9)
		browseCounterApply(t, db, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,?) ON CONFLICT(entity_id,item_id,source) DO NOTHING`, container, item, source)
			return err
		})
		return "membership insert"
	case 5:
		container, item, source, ok := browseCounterRandomMembership(t, db, rng)
		if !ok {
			return "membership move (none)"
		}
		target := shows[rng.Intn(len(shows))]
		browseCounterApply(t, db, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE OR IGNORE catalog_browse_memberships SET entity_id=? WHERE entity_id=? AND item_id=? AND source=?`, target, container, item, source)
			return err
		})
		return "membership move"
	case 6:
		container, item, source, ok := browseCounterRandomMembership(t, db, rng)
		if !ok {
			return "membership source update (none)"
		}
		next := rng.Intn(9)
		browseCounterApply(t, db, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE OR IGNORE catalog_browse_memberships SET source=? WHERE entity_id=? AND item_id=? AND source=?`, next, container, item, source)
			return err
		})
		return "membership source update"
	case 7:
		container, item, source, ok := browseCounterRandomMembership(t, db, rng)
		if !ok {
			return "membership delete (none)"
		}
		browseCounterApply(t, db, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM catalog_browse_memberships WHERE entity_id=? AND item_id=? AND source=?`, container, item, source)
			return err
		})
		return "membership delete"
	case 8:
		container, item, source, ok := browseCounterRandomMembership(t, db, rng)
		if !ok {
			return "metric insert (none)"
		}
		available, unavailable := rng.Intn(3), rng.Intn(3)
		browseCounterApply(t, db, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO catalog_browse_member_metrics(entity_id,item_id,source,available_links,unavailable_links) VALUES(?,?,?,?,?) ON CONFLICT(entity_id,item_id,source) DO NOTHING`, container, item, source, available, unavailable)
			return err
		})
		return "metric insert"
	case 9:
		container, item, source, ok := browseCounterRandomMetric(t, db, rng)
		if !ok {
			return "metric update (none)"
		}
		available, unavailable := rng.Intn(3), rng.Intn(3)
		browseCounterApply(t, db, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE catalog_browse_member_metrics SET available_links=?,unavailable_links=? WHERE entity_id=? AND item_id=? AND source=?`, available, unavailable, container, item, source)
			return err
		})
		return "metric update"
	default:
		container, item, source, ok := browseCounterRandomMetric(t, db, rng)
		if !ok {
			return "metric delete (none)"
		}
		browseCounterApply(t, db, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM catalog_browse_member_metrics WHERE entity_id=? AND item_id=? AND source=?`, container, item, source)
			return err
		})
		return "metric delete"
	}
}

// TestBrowseCounterTriggersMatchRecounts drives link, membership and metric
// inserts, updates and deletes, including entity moves, and requires the
// counters to equal an independent recount after every transaction and again
// after the ordinary worker drains.
func TestBrowseCounterTriggersMatchRecounts(t *testing.T) {
	db := cc1OpenDB(t, "browse-counter-property.sqlite")
	items, shows, assets := browseCounterFixture(t, db, 6, 2)
	browseCounterCheckExact(t, db, "built")
	// Exercise non-zero applied contributions before the random mutations.
	cc1Exec(t, db, `UPDATE catalog_browse_member_metrics SET available_links=available_links+1,unavailable_links=unavailable_links+1`)
	browseCounterCheckExact(t, db, "contributions")

	rng := rand.New(rand.NewSource(20261028))
	for i := 0; i < 150; i++ {
		op := browseCounterRandomOp(t, db, rng, items, shows, assets)
		browseCounterCheckExact(t, db, fmt.Sprintf("step %d (%s)", i, op))
	}
	// Random metric mutations exercise the delta arithmetic independently.
	// Re-run the real producer from raw links for every holder afterwards and
	// require the final container totals to match the raw graph as well.
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return TouchTx(ctx, tx, DomainItemMetrics, items...) })
	browseCounterDrain(t, db)
	browseCounterCheckExact(t, db, "drained")
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_member_links c WHERE c.available<>COALESCE((SELECT sum(l.available=1) FROM catalog_browse_memberships m JOIN catalog_asset_links l ON l.entity_id=m.item_id WHERE m.entity_id=c.entity_id),0) OR c.unavailable<>COALESCE((SELECT sum(l.available=0) FROM catalog_browse_memberships m JOIN catalog_asset_links l ON l.entity_id=m.item_id WHERE m.entity_id=c.entity_id),0)`); n != 0 {
		t.Fatalf("%d converged container counters disagree with the raw member/link graph", n)
	}
}

// TestBrowseCounterSampleRepairsInjectedDrift samples a healthy catalogue
// without writing, then injects item and container drift, checks that the
// sample queues exactly the affected holders, and drives both repairs through
// the worker to an exact recount.
func TestBrowseCounterSampleRepairsInjectedDrift(t *testing.T) {
	db := cc1OpenDB(t, "browse-counter-sample.sqlite")
	items, shows, _ := browseCounterFixture(t, db, 6, 2)
	cc1Exec(t, db, `UPDATE catalog_browse_member_metrics SET available_links=1,unavailable_links=1`)
	browseCounterCheckExact(t, db, "built")
	healthyRepairs := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_counter_repairs`)
	healthyDirty35 := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=?`, DomainContainerRefresh)
	healthyDirty25 := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=?`, DomainItemMetrics)

	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return SampleBrowseCounterIntegrityTx(ctx, tx) })
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_counter_repairs`); got != healthyRepairs {
		t.Fatalf("healthy sample created %d repairs", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=?`, DomainContainerRefresh); got != healthyDirty35 {
		t.Fatalf("healthy sample queued %d containers", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=?`, DomainItemMetrics); got != healthyDirty25 {
		t.Fatalf("healthy sample queued %d items", got)
	}

	driftedItem := items[0]
	driftedContainer := shows[0]
	cc1Exec(t, db, `UPDATE catalog_browse_item_metrics SET available_links=available_links+3,unavailable_links=unavailable_links+2 WHERE item_id=?`, driftedItem)
	cc1Exec(t, db, `UPDATE catalog_browse_member_links SET available=available+2,unavailable=unavailable+1,members=members+1 WHERE entity_id=?`, driftedContainer)
	corrupted := cc1Scalar(t, db, `SELECT available_links FROM catalog_browse_item_metrics WHERE item_id=?`, driftedItem)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return SampleBrowseCounterIntegrityTx(ctx, tx) })

	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_counter_repairs WHERE entity_id=?`, driftedContainer); got != 1 {
		t.Fatalf("drifted container was not queued for repair")
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_counter_repairs`); got != 1 {
		t.Fatalf("sample queued %d repairs, want only the drifted container", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=? AND entity_id=?`, DomainItemMetrics, driftedItem); got != 1 {
		t.Fatalf("drifted item was not queued for Domain25")
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=? AND entity_id=?`, DomainContainerRefresh, driftedContainer); got != 1 {
		t.Fatalf("drifted container was not queued for Domain35")
	}
	// The sample only queues: it does not recount a holder in place.
	if got := cc1Scalar(t, db, `SELECT available_links FROM catalog_browse_item_metrics WHERE item_id=?`, driftedItem); got != corrupted {
		t.Fatal("sample rewrote the item counter")
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_member_links c
 WHERE c.entity_id=? AND c.available<>(SELECT COALESCE(sum(m.available_links),0) FROM catalog_browse_member_metrics m WHERE m.entity_id=c.entity_id)`, driftedContainer); got != 1 {
		t.Fatal("sample rewrote the container counter")
	}

	// The wired worker repairs the container through Domain35 and republishes
	// the item's link totals through Domain25; both converge to the recount.
	browseCounterDrain(t, db)
	browseCounterCheckExact(t, db, "converged")
}

// TestBrowseCounterSampleDetectsLargeHolderDrift checks the lower-bound path:
// a holder with more raw rows than one probe reads is flagged when its counter
// is negative or below the observed prefix.
func TestBrowseCounterSampleDetectsLargeHolderDrift(t *testing.T) {
	db := cc1OpenDB(t, "browse-counter-large.sqlite")
	lib := cc1Library(t, db, "large", "Large", "tv", "/large")
	show := cc1Entity(t, db, Entity{Library: lib, Kind: Show, Key: "show:large", Title: "Large"}, map[string]any{"local_key": "large"})
	items := make([]int64, 5)
	for i := range items {
		items[i] = cc1Entity(t, db, Entity{Library: lib, Kind: Episode, Parent: show, Key: fmt.Sprintf("ep:%d", i), Title: fmt.Sprintf("Episode %d", i)}, map[string]any{"show_id": show, "number": i + 1, "numbering": "seasonal"})
	}
	// The first item has twelve links: more than one nine-row probe.
	for i := 0; i < 12; i++ {
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			asset, _, err := UpsertAssetTx(ctx, tx, Asset{Path: fmt.Sprintf("/large/%d.mkv", i), Size: 100, Duration: 60})
			if err != nil {
				return err
			}
			return LinkAssetTx(ctx, tx, items[0], asset, Link{})
		})
	}
	for i := 1; i < len(items); i++ {
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO catalog_browse_item_metrics(item_id) VALUES(?) ON CONFLICT(item_id) DO NOTHING`, items[i])
			return err
		})
	}
	// The show has ten applied metric rows and ten memberships.
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		for i := range items {
			for source := 0; source < 2; source++ {
				if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,?)`, show, items[i], source); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_browse_member_metrics(entity_id,item_id,source,available_links,unavailable_links) VALUES(?,?,?,1,1)`, show, items[i], source); err != nil {
					return err
				}
			}
		}
		return nil
	})
	browseCounterCheckExact(t, db, "large built")

	// Counters below the observed prefix (and memberships below ten) are drift.
	cc1Exec(t, db, `UPDATE catalog_browse_item_metrics SET available_links=0,unavailable_links=0 WHERE item_id=?`, items[0])
	cc1Exec(t, db, `UPDATE catalog_browse_member_links SET available=0,unavailable=0,members=0 WHERE entity_id=?`, show)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return SampleBrowseCounterIntegrityTx(ctx, tx) })
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=? AND entity_id=?`, DomainItemMetrics, items[0]); got != 1 {
		t.Fatalf("large item lower-bound drift was not queued")
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_counter_repairs WHERE entity_id=?`, show); got != 1 {
		t.Fatalf("large container lower-bound drift was not queued")
	}
}

// TestBrowseCounterRepairBoundedPages verifies one repair call applies at most
// eight raw rows, resets on a revision change and replaces the counters only
// when both phases finish.
func TestBrowseCounterRepairBoundedPages(t *testing.T) {
	db := cc1OpenDB(t, "browse-counter-pages.sqlite")
	_, shows, _ := browseCounterFixture(t, db, 10, 1)
	show := shows[0]
	cc1Exec(t, db, `UPDATE catalog_browse_member_metrics SET available_links=1,unavailable_links=1 WHERE entity_id=?`, show)
	cc1Exec(t, db, `UPDATE catalog_browse_member_links SET available=available+5 WHERE entity_id=?`, show)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_browse_counter_repairs(entity_id) VALUES(?) ON CONFLICT(entity_id) DO NOTHING`, show); err != nil {
			return err
		}
		return TouchTx(ctx, tx, DomainContainerRefresh, show)
	})
	var revision int64
	if err := db.QueryRow(`SELECT revision FROM catalog_dirty WHERE domain=? AND entity_id=?`, DomainContainerRefresh, show).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	repairPage := func() (pending bool, statements int64) {
		ctx, cost := dbwork.Measure(context.Background())
		err := dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
			var err error
			pending, err = RepairBrowseCountersTx(ctx, tx, Key{ID: show, Revision: revision}, 100)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return pending, cost().Statements
	}
	state := func() (phase int, afterItem int64, afterSource int, members int64) {
		if err := db.QueryRow(`SELECT phase,after_item,after_source,members FROM catalog_browse_counter_repairs WHERE entity_id=?`, show).Scan(&phase, &afterItem, &afterSource, &members); err != nil {
			t.Fatal(err)
		}
		return
	}
	countMetrics := func(afterItem int64, afterSource int) int {
		return cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_member_metrics WHERE entity_id=? AND (item_id,source)<=(?,?)`, show, afterItem, afterSource)
	}
	countMembers := func(afterItem int64, afterSource int) int {
		return cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_memberships WHERE entity_id=? AND (item_id,source)<=(?,?)`, show, afterItem, afterSource)
	}

	for _, want := range []struct {
		phase   int
		metrics int
		members int
		pending bool
	}{
		{0, 8, 0, true},
		{1, 0, 0, true}, // the remaining two metrics completed the phase
		{1, 0, 8, true},
	} {
		pending, statements := repairPage()
		phase, afterItem, afterSource, members := state()
		if pending != want.pending || phase != want.phase || members != int64(want.members) {
			t.Fatalf("repair page: pending=%v phase=%d metrics=%d members=%d, want pending=%v phase=%d metrics=%d members=%d",
				pending, phase, countMetrics(afterItem, afterSource), members, want.pending, want.phase, want.metrics, want.members)
		}
		if want.phase == 0 && countMetrics(afterItem, afterSource) != want.metrics {
			t.Fatalf("repair read %d metric rows, want %d", countMetrics(afterItem, afterSource), want.metrics)
		}
		if want.phase == 1 && countMembers(afterItem, afterSource) != want.members {
			t.Fatalf("repair read %d memberships, want %d", countMembers(afterItem, afterSource), want.members)
		}
		if statements > 8 {
			t.Fatalf("one repair page ran %d statements", statements)
		}
	}
	pending, _ := repairPage()
	if pending {
		t.Fatal("repair still pending after both phases")
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_counter_repairs WHERE entity_id=?`, show); got != 0 {
		t.Fatalf("finished repair row remains")
	}
	browseCounterCheckExact(t, db, "repaired pages")

	// A revision change restarts the cursor from the first page.
	cc1Exec(t, db, `UPDATE catalog_browse_member_links SET available=available+1 WHERE entity_id=?`, show)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_browse_counter_repairs(entity_id,phase,after_item,after_source,members) VALUES(?,1,7,1,3) ON CONFLICT(entity_id) DO UPDATE SET phase=1,after_item=7,after_source=1,members=3`, show); err != nil {
			return err
		}
		return TouchTx(ctx, tx, DomainContainerRefresh, show)
	})
	var next int64
	if err := db.QueryRow(`SELECT revision FROM catalog_dirty WHERE domain=? AND entity_id=?`, DomainContainerRefresh, show).Scan(&next); err != nil {
		t.Fatal(err)
	}
	var restarted bool
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		restarted, err = RepairBrowseCountersTx(ctx, tx, Key{ID: show, Revision: next}, 8)
		return err
	})
	phase, afterItem, _, members := state()
	if !restarted || phase != 0 || afterItem == 7 || members != 0 {
		t.Fatalf("revision change did not restart the repair: pending=%v phase=%d afterItem=%d members=%d", restarted, phase, afterItem, members)
	}
}

// TestBrowseCounterRepairDropsDeletedContainer covers the cleanup point: a
// repair whose container no longer exists is removed without a counter write.
func TestBrowseCounterRepairDropsDeletedContainer(t *testing.T) {
	db := cc1OpenDB(t, "browse-counter-deleted.sqlite")
	cc1Exec(t, db, `INSERT INTO catalog_browse_counter_repairs(entity_id,phase,after_item,members) VALUES(999999,1,4,2)`)
	var pending bool
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		pending, err = RepairBrowseCountersTx(ctx, tx, Key{ID: 999999, Revision: 3}, 8)
		return err
	})
	if pending {
		t.Fatal("repair for a deleted container stayed pending")
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_counter_repairs WHERE entity_id=999999`); got != 0 {
		t.Fatalf("repair row for a deleted container remains")
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_member_links WHERE entity_id=999999`); got != 0 {
		t.Fatalf("repair wrote counters for a deleted container")
	}
	// The startup sample also drops a stale repair whose dirty key is gone.
	cc1Exec(t, db, `INSERT INTO catalog_browse_counter_repairs(entity_id,phase) VALUES(999998,1)`)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return SampleBrowseCounterIntegrityTx(ctx, tx) })
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_counter_repairs WHERE entity_id=999998`); got != 0 {
		t.Fatalf("sample left a repair for a deleted container")
	}
}

// TestBrowseCounterSampleIsBounded checks that one startup sample repairs at
// most eight items and eight containers, whatever the catalogue holds.
func TestBrowseCounterSampleIsBounded(t *testing.T) {
	db := cc1OpenDB(t, "browse-counter-bounds.sqlite")
	_, shows, _ := browseCounterFixture(t, db, 9, 9)
	beforeItems := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=?`, DomainItemMetrics)
	beforeRepairs := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_counter_repairs`)
	cc1Exec(t, db, `UPDATE catalog_browse_item_metrics SET available_links=available_links+2`)
	cc1Exec(t, db, `UPDATE catalog_browse_member_links SET available=available+2,members=members+1`)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return SampleBrowseCounterIntegrityTx(ctx, tx) })
	items := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=?`, DomainItemMetrics) - beforeItems
	repairs := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_counter_repairs`) - beforeRepairs
	if items == 0 || items > browseCounterSample {
		t.Fatalf("sample queued %d items, want 1..%d", items, browseCounterSample)
	}
	if repairs == 0 || repairs > browseCounterSample {
		t.Fatalf("sample queued %d repairs, want 1..%d", repairs, browseCounterSample)
	}
	if len(shows) != 9 {
		t.Fatalf("fixture made %d shows", len(shows))
	}
}
