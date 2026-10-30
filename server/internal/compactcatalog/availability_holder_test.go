package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// availabilityParity checks every link and item against the approved A2 view.
// The reducer must not be a second opinion: the view is the answer.
func availabilityParity(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`SELECT l.entity_id,l.asset_id,l.available,
 COALESCE((SELECT av.available FROM inventory_item_availability av
  WHERE av.item_id=l.entity_id AND av.asset_id=(SELECT a.token FROM catalog_assets a WHERE a.id=l.asset_id)),0)
 FROM catalog_asset_links l ORDER BY l.entity_id,l.asset_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	links := 0
	for rows.Next() {
		var item, asset int64
		var got, want bool
		if err := rows.Scan(&item, &asset, &got, &want); err != nil {
			t.Fatal(err)
		}
		links++
		if got != want {
			t.Errorf("link item %d asset %d derived %v, view %v", item, asset, got, want)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if links == 0 {
		t.Fatal("parity check saw no links")
	}
	rows, err = db.Query(`SELECT e.id,
 COALESCE((SELECT v.available FROM inventory_item_availability v WHERE v.item_id=e.id AND v.available=1 LIMIT 1),0),
 COALESCE((SELECT ia.available FROM catalog_item_availability ia WHERE ia.entity_id=e.id),-1),
 COALESCE((SELECT ia.retired FROM catalog_item_availability ia WHERE ia.entity_id=e.id),-1),
 EXISTS(SELECT 1 FROM dvr_catalog_retirements d WHERE d.item_id=e.id)
 FROM catalog_entities e ORDER BY e.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var want, available, retired, dvr bool
		if err := rows.Scan(&id, &want, &available, &retired, &dvr); err != nil {
			t.Fatal(err)
		}
		if available != want {
			t.Errorf("item %d availability %v, view %v", id, available, want)
		}
		if retired != dvr {
			t.Errorf("item %d retirement %v, dvr %v", id, retired, dvr)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func availabilityToken(t *testing.T, db *sql.DB, asset int64) string {
	t.Helper()
	var token string
	if err := db.QueryRow(`SELECT token FROM catalog_assets WHERE id=?`, asset).Scan(&token); err != nil {
		t.Fatal(err)
	}
	return token
}

// availabilityTurn runs one direct pass turn, outside the Worker's scheduling,
// and acknowledges exactly the revision the turn read.
func availabilityTurn(t *testing.T, db *sql.DB, limit int) (done bool, visits int) {
	t.Helper()
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var k Key
		err := tx.QueryRowContext(ctx, `SELECT entity_id,revision FROM catalog_dirty WHERE domain=? ORDER BY entity_id LIMIT 1`, DomainAvailability).Scan(&k.ID, &k.Revision)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		done, visits, err = deriveAvailabilityKey(ctx, tx, k, limit)
		if err != nil {
			return err
		}
		if done {
			_, err = tx.ExecContext(ctx, `DELETE FROM catalog_dirty WHERE domain=? AND entity_id=? AND revision=?`, DomainAvailability, k.ID, k.Revision)
		}
		return err
	})
	return done, visits
}

func availabilityHolderRow(t *testing.T, db *sql.DB, item int64) (revision, phase, afterAsset int64, ok bool) {
	t.Helper()
	err := db.QueryRow(`SELECT revision,phase,after_asset FROM catalog_availability_work WHERE entity_id=?`, item).Scan(&revision, &phase, &afterAsset)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return revision, phase, afterAsset, true
}

// TestAvailabilityHolderFollowsViewOverRawEvidence exercises every branch of
// the approved view: inventory evidence, unsupported and cross-library
// objects, root incarnation, DVR retirement, folder fallback including
// Windows roots, and transient offline. Offline must neither queue the item
// nor change a link.
func TestAvailabilityHolderFollowsViewOverRawEvidence(t *testing.T) {
	db := cc1OpenDB(t, "availability-parity.sqlite")
	lib := cc1Library(t, db, "avail", "Avail", "movie", "/avail")
	cc1Library(t, db, "other", "Other", "movie", "/other")
	cc1Exec(t, db, `INSERT INTO library_sources(id,library_id,configured_root,root) VALUES('avail-windows','avail','D:\Films\','D:\Films\')`)
	item := facetTestEntity(t, db, lib, "avail:item")
	inventoried := sourceTestAsset(t, db, []int64{item}, "/avail/one.mkv")
	unsupported := sourceTestAsset(t, db, []int64{item}, "/avail/two.mkv")
	crossLibrary := sourceTestAsset(t, db, []int64{item}, "/avail/three.mkv")
	windows := sourceTestAsset(t, db, []int64{item}, `D:\Films\four.mkv`)
	cc1Drain(t, db)
	availabilityParity(t, db)

	cc1Exec(t, db, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
 SELECT 'obj-own',s.id,?,s.incarnation,'one.mkv','r1','{}',1,1 FROM library_sources s WHERE s.id='avail'`, availabilityToken(t, db, inventoried))
	cc1Exec(t, db, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,unsupported_reason)
 SELECT 'obj-unsupported',s.id,?,s.incarnation,'two.mkv','r1','{}',1,1,'unsupported_container' FROM library_sources s WHERE s.id='avail'`, availabilityToken(t, db, unsupported))
	cc1Exec(t, db, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
 SELECT 'obj-other',s.id,?,s.incarnation,'three.mkv','r1','{}',1,1 FROM library_sources s WHERE s.id='other'`, availabilityToken(t, db, crossLibrary))
	cc1Drain(t, db)
	availabilityParity(t, db)

	var available int
	if err := db.QueryRow(`SELECT available FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, item, inventoried).Scan(&available); err != nil {
		t.Fatal(err)
	}
	if available != 1 {
		t.Fatal("own-library inventory evidence did not make the file available")
	}
	if err := db.QueryRow(`SELECT available FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, item, unsupported).Scan(&available); err != nil {
		t.Fatal(err)
	}
	if available != 0 {
		t.Fatal("unsupported inventory evidence did not reject the file")
	}
	if err := db.QueryRow(`SELECT available FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, item, crossLibrary).Scan(&available); err != nil {
		t.Fatal(err)
	}
	if available != 1 {
		t.Fatal("another library's inventory object changed this library's fallback")
	}
	if err := db.QueryRow(`SELECT available FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, item, windows).Scan(&available); err != nil {
		t.Fatal(err)
	}
	if available != 1 {
		t.Fatal("a Windows-rooted file was not found under its folder")
	}

	// Transient offline is not evidence: no queue, no per-item write.
	before := fmt.Sprintf("%v", availabilitySnapshot(t, db))
	cc1Exec(t, db, `UPDATE library_sources SET health='offline' WHERE id='avail'`)
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=20`); n != 0 {
		t.Fatalf("source offline queued %d domain20 keys", n)
	}
	cc1Drain(t, db)
	if after := fmt.Sprintf("%v", availabilitySnapshot(t, db)); after != before {
		t.Fatalf("source offline changed file evidence: %s -> %s", before, after)
	}
	availabilityParity(t, db)

	// Explicit root_changed is a real change; offline around it still is not.
	cc1Exec(t, db, `UPDATE library_sources SET health='root_changed' WHERE id='avail-windows'`)
	cc1Drain(t, db)
	availabilityParity(t, db)
	if err := db.QueryRow(`SELECT available FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, item, windows).Scan(&available); err != nil {
		t.Fatal(err)
	}
	if available != 0 {
		t.Fatal("root_changed did not withdraw the Windows-rooted file")
	}
	cc1Exec(t, db, `UPDATE library_sources SET health='ready' WHERE id='avail-windows'`)
	cc1Exec(t, db, `UPDATE library_sources SET health='offline' WHERE id='avail'`)
	cc1Drain(t, db)
	availabilityParity(t, db)

	// A root incarnation change no longer matches the inventoried object.
	cc1Exec(t, db, `UPDATE inventory_objects SET root_incarnation='stale' WHERE id='obj-own'`)
	cc1Drain(t, db)
	availabilityParity(t, db)
	if err := db.QueryRow(`SELECT available FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, item, inventoried).Scan(&available); err != nil {
		t.Fatal(err)
	}
	if available != 0 {
		t.Fatalf("inventory evidence outlived its root incarnation: %v", available)
	}

	// DVR retirement precedes every file answer.
	cc1Exec(t, db, `INSERT INTO dvr_catalog_retirements(item_id,requested_ms) VALUES(?,1)`, item)
	cc1Drain(t, db)
	availabilityParity(t, db)
	if err := db.QueryRow(`SELECT count(*) FROM catalog_asset_links WHERE entity_id=? AND available=1`, item).Scan(&available); err != nil {
		t.Fatal(err)
	}
	if available != 0 {
		t.Fatal("a retired recording kept an available file")
	}
	cc1Exec(t, db, `DELETE FROM dvr_catalog_retirements WHERE item_id=?`, item)
	cc1Exec(t, db, `UPDATE inventory_objects SET unsupported_reason='' WHERE id='obj-unsupported'`)
	cc1Exec(t, db, `UPDATE inventory_objects SET root_incarnation=(SELECT incarnation FROM library_sources WHERE id='avail') WHERE id='obj-own'`)
	cc1Drain(t, db)
	availabilityParity(t, db)

	// Removing the source withdraws its evidence without a per-item scan.
	cc1Exec(t, db, `UPDATE library_sources SET enabled=0,health='removing' WHERE id='avail'`)
	cc1Drain(t, db)
	availabilityParity(t, db)
}

// availabilitySnapshot is the file evidence an offline transition must leave
// untouched.
func availabilitySnapshot(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT 'link:'||entity_id||':'||asset_id||':'||available FROM catalog_asset_links
 UNION ALL SELECT 'item:'||entity_id||':'||available||':'||retired FROM catalog_item_availability ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestAvailabilityHolderBoundsResumeAndReopen keeps the raw cursor in bounds,
// makes progress across turns, and survives a close and reopen.
func TestAvailabilityHolderBoundsResumeAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "availability-bounds.sqlite")
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	lib := cc1Library(t, db, "bounds", "Bounds", "movie", "/bounds")
	item := facetTestEntity(t, db, lib, "bounds:item")
	const links = 9
	for i := 0; i < links; i++ {
		sourceTestAsset(t, db, []int64{item}, fmt.Sprintf("/bounds/file-%02d.mkv", i))
	}
	cc1Drain(t, db)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return TouchTx(ctx, tx, DomainAvailability, item)
	})
	limit := 3
	turns, maxVisits := 0, 0
	for ; turns < 200; turns++ {
		if turns == 2 {
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if db, err = persistence.Open(path); err != nil {
				t.Fatal(err)
			}
		}
		done, visits := availabilityTurn(t, db, limit)
		if visits > limit {
			t.Fatalf("turn %d read %d raw rows, limit %d", turns, visits, limit)
		}
		maxVisits = max(maxVisits, visits)
		if done {
			break
		}
	}
	if turns >= 200 || turns == 0 {
		t.Fatalf("bounded pass did not finish in %d turns", turns)
	}
	if maxVisits == 0 {
		t.Fatal("pass reported no raw reads")
	}
	// Nine links, one link row and at least one source row each, is more than
	// one turn at three raw rows.
	if turns < 3 {
		t.Fatalf("nine links finished in %d turns, expected bounded progress", turns)
	}
	if _, _, _, ok := availabilityHolderRow(t, db, item); ok {
		t.Fatal("finished pass kept its cursor")
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=20`); n != 0 {
		t.Fatalf("finished pass left %d queued keys", n)
	}
	availabilityParity(t, db)
	db.Close()
}

// TestAvailabilityHolderRollbackKeepsCursorAndDirty forces a failure inside
// the pass's transaction: the cursor, the link write and the dirty revision
// must all roll back.
func TestAvailabilityHolderRollbackKeepsCursorAndDirty(t *testing.T) {
	db := cc1OpenDB(t, "availability-rollback.sqlite")
	lib := cc1Library(t, db, "rollback", "Rollback", "movie", "/rollback")
	item := facetTestEntity(t, db, lib, "rollback:item")
	asset := sourceTestAsset(t, db, []int64{item}, "/rollback/one.mkv")
	cc1Drain(t, db)
	cc1Exec(t, db, `UPDATE catalog_asset_links SET available=0 WHERE entity_id=?`, item)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return TouchTx(ctx, tx, DomainAvailability, item)
	})
	cc1Exec(t, db, `CREATE TRIGGER availability_rollback_probe BEFORE UPDATE OF available ON catalog_asset_links
 BEGIN SELECT RAISE(ABORT,'availability rollback probe'); END`)
	var key Key
	if err := db.QueryRow(`SELECT entity_id,revision FROM catalog_dirty WHERE domain=20`).Scan(&key.ID, &key.Revision); err != nil {
		t.Fatal(err)
	}
	err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		_, _, err := deriveAvailabilityKey(context.Background(), tx, key, 16)
		return err
	})
	if err == nil {
		t.Fatal("forced link update failure did not abort the pass")
	}
	if _, _, _, ok := availabilityHolderRow(t, db, item); ok {
		t.Fatal("a rolled-back pass kept a cursor row")
	}
	if n := cc1Scalar(t, db, `SELECT revision FROM catalog_dirty WHERE domain=20 AND entity_id=?`, item); n != int(key.Revision) {
		t.Fatalf("rollback changed the dirty revision: %d, want %d", n, key.Revision)
	}
	if n := cc1Scalar(t, db, `SELECT available FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, item, asset); n != 0 {
		t.Fatalf("rolled-back link write survived: %d", n)
	}
	cc1Exec(t, db, `DROP TRIGGER availability_rollback_probe`)
	cc1Drain(t, db)
	availabilityParity(t, db)
	if n := cc1Scalar(t, db, `SELECT available FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, item, asset); n != 1 {
		t.Fatalf("retried pass did not publish the file: %d", n)
	}
}

// TestAvailabilityHolderChurnRerunsFromNewHead keeps touching the same item
// while a pass walks it: the pass keeps its raw cursor, reaches its end,
// leaves the newer dirty revision queued, and a fresh pass from the head is
// the only coalesced rerun. Continuous churn still makes bounded progress and
// never piles up dirty rows or cursors.
func TestAvailabilityHolderChurnRerunsFromNewHead(t *testing.T) {
	db := cc1OpenDB(t, "availability-churn.sqlite")
	lib := cc1Library(t, db, "churn", "Churn", "movie", "/churn")
	item := facetTestEntity(t, db, lib, "churn:item")
	for i := 0; i < 4; i++ {
		sourceTestAsset(t, db, []int64{item}, fmt.Sprintf("/churn/file-%d.mkv", i))
	}
	cc1Drain(t, db)
	w := NewWorker(db)
	touch := func() {
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			return TouchTx(ctx, tx, DomainAvailability, item)
		})
	}
	step := func(limit int) {
		if _, err := w.Step(context.Background(), limit); err != nil {
			t.Fatal(err)
		}
	}
	for round := 0; round < 3; round++ {
		touch()
		var revision int64
		if err := db.QueryRow(`SELECT revision FROM catalog_dirty WHERE domain=20 AND entity_id=?`, item).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		// Start the pass, then touch the same item twice while it walks. A
		// turn may serve the step workers first, so wait for the holder.
		for i := 0; i < 20; i++ {
			if _, _, _, ok := availabilityHolderRow(t, db, item); ok {
				break
			}
			step(1)
		}
		captured, _, after, ok := availabilityHolderRow(t, db, item)
		if !ok {
			t.Fatalf("round %d: first turn kept no holder", round)
		}
		if captured > revision {
			t.Fatalf("round %d: holder captured a revision newer than the queue", round)
		}
		for i := 0; i < 2; i++ {
			touch()
			step(1 + i)
			if got, _, _, still := availabilityHolderRow(t, db, item); still && got != captured {
				t.Fatalf("round %d: churn reset the pass cursor from revision %d to %d", round, captured, got)
			}
			if _, _, next, still := availabilityHolderRow(t, db, item); still && next < after {
				t.Fatalf("round %d: churn moved the pass back from asset %d to %d", round, after, next)
			}
		}
		// Finish this pass: the cursor must disappear at EOF, the newer dirty
		// revision must stay queued, and no second dirty row may appear.
		for i := 0; i < 200; i++ {
			if _, _, _, ok := availabilityHolderRow(t, db, item); !ok {
				break
			}
			step(2)
		}
		if _, _, _, ok := availabilityHolderRow(t, db, item); ok {
			t.Fatalf("round %d: churned pass did not reach its end", round)
		}
		if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=20 AND entity_id=?`, item); n != 1 {
			t.Fatalf("round %d: churn left %d dirty rows, want one coalesced rerun", round, n)
		}
	}
	// Stop churning: the queued rerun publishes from the new head.
	for i := 0; i < 200; i++ {
		if cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=20`) == 0 {
			break
		}
		step(8)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=20`); n != 0 {
		t.Fatalf("quiet queue did not finish: %d keys", n)
	}
	if _, _, _, ok := availabilityHolderRow(t, db, item); ok {
		t.Fatal("finished rerun kept its cursor")
	}
	availabilityParity(t, db)
}

// TestAvailabilityHolderChurnDoesNotPublishMixedPass starts a pass, flips real
// evidence behind its cursor, and lets it reach EOF. The pass must not rewrite
// the item aggregate it never walked: the queued rerun publishes alone.
func TestAvailabilityHolderChurnDoesNotPublishMixedPass(t *testing.T) {
	db := cc1OpenDB(t, "availability-mixed.sqlite")
	lib := cc1Library(t, db, "mixed", "Mixed", "movie", "/mixed")
	item := facetTestEntity(t, db, lib, "mixed:item")
	sourceTestAsset(t, db, []int64{item}, "/mixed/one.mkv")
	cc1Drain(t, db)
	if n := cc1Scalar(t, db, `SELECT available FROM catalog_item_availability WHERE entity_id=?`, item); n != 1 {
		t.Fatalf("fixture item availability = %d", n)
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return TouchTx(ctx, tx, DomainAvailability, item)
	})
	w := NewWorker(db)
	step := func(limit int) {
		if _, err := w.Step(context.Background(), limit); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		if _, _, _, ok := availabilityHolderRow(t, db, item); ok {
			break
		}
		step(1)
	}
	if _, _, _, ok := availabilityHolderRow(t, db, item); !ok {
		t.Fatal("pass did not start")
	}
	// A real source change while the pass is between its link row and its
	// folder evidence. It queues a rerun revision.
	cc1Exec(t, db, `UPDATE library_sources SET health='removing' WHERE id='mixed'`)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return TouchTx(ctx, tx, DomainAvailability, item)
	})
	for i := 0; i < 200; i++ {
		if _, _, _, ok := availabilityHolderRow(t, db, item); !ok {
			break
		}
		step(2)
	}
	if _, _, _, ok := availabilityHolderRow(t, db, item); ok {
		t.Fatal("churned pass did not reach its end")
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=20 AND entity_id=?`, item); n != 1 {
		t.Fatalf("churned pass left %d queued revisions", n)
	}
	if n := cc1Scalar(t, db, `SELECT available FROM catalog_item_availability WHERE entity_id=?`, item); n != 1 {
		t.Fatal("a mixed pass published the item aggregate it never walked")
	}
	for i := 0; i < 200; i++ {
		if cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=20`) == 0 {
			break
		}
		step(4)
	}
	if n := cc1Scalar(t, db, `SELECT available FROM catalog_item_availability WHERE entity_id=?`, item); n != 0 {
		t.Fatalf("rerun did not publish the changed evidence: %d", n)
	}
	availabilityParity(t, db)
}

// TestAvailabilityHolderFenceEndsAtCapturedFrontier appends a link while the
// pass walks. The pass must end at the link frontier captured at its start
// (leaving the tail's flag untouched), keep the tail's newer dirty revision,
// and correct the tail in exactly one rerun from the new head.
func TestAvailabilityHolderFenceEndsAtCapturedFrontier(t *testing.T) {
	db := cc1OpenDB(t, "availability-fence.sqlite")
	lib := cc1Library(t, db, "fence", "Fence", "movie", "/fence")
	item := facetTestEntity(t, db, lib, "fence:item")
	for i := 0; i < 6; i++ {
		sourceTestAsset(t, db, []int64{item}, fmt.Sprintf("/fence/base-%d.mkv", i))
	}
	cc1Drain(t, db)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return TouchTx(ctx, tx, DomainAvailability, item)
	})
	w := NewWorker(db)
	step := func(limit int) {
		if _, err := w.Step(context.Background(), limit); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ {
		if _, _, _, ok := availabilityHolderRow(t, db, item); ok {
			break
		}
		step(1)
	}
	var fence sql.NullInt64
	if err := db.QueryRow(`SELECT fence_asset FROM catalog_availability_work WHERE entity_id=?`, item).Scan(&fence); err != nil {
		t.Fatal(err)
	}
	if !fence.Valid {
		t.Fatal("pass did not capture its link frontier")
	}
	// Append a seventh link during the walk. Its inventory evidence makes the
	// correct flag 0, while the link enters at 1, so a frontier pass that read
	// the tail would be visible.
	tail := sourceTestAsset(t, db, []int64{item}, "/fence/tail.mkv")
	if tail <= fence.Int64 {
		t.Fatalf("tail asset %d is not beyond the captured frontier %d", tail, fence.Int64)
	}
	cc1Exec(t, db, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,unsupported_reason)
 SELECT 'tail-object',s.id,?,s.incarnation,'tail.mkv','r1','{}',1,1,'unsupported_container' FROM library_sources s WHERE s.id='fence'`, availabilityToken(t, db, tail))
	var tailRevision int64
	if err := db.QueryRow(`SELECT revision FROM catalog_dirty WHERE domain=20 AND entity_id=?`, item).Scan(&tailRevision); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if _, _, _, ok := availabilityHolderRow(t, db, item); !ok {
			break
		}
		step(2)
	}
	if _, _, _, ok := availabilityHolderRow(t, db, item); ok {
		t.Fatal("fenced pass did not reach the captured frontier")
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=20 AND entity_id=?`, item); n != 1 {
		t.Fatalf("frontier pass left %d queued revisions, want the tail's one", n)
	}
	if n := cc1Scalar(t, db, `SELECT revision FROM catalog_dirty WHERE domain=20 AND entity_id=?`, item); int64(n) < tailRevision {
		t.Fatalf("frontier pass lost the tail revision: %d, want at least %d", n, tailRevision)
	}
	if n := cc1Scalar(t, db, `SELECT available FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, item, tail); n != 1 {
		t.Fatalf("frontier pass walked the appended tail (flag %d)", n)
	}
	// Exactly one rerun from the new head: its captured frontier includes the
	// tail and it carries the tail's dirty revision forward.
	for i := 0; i < 20; i++ {
		if _, _, _, ok := availabilityHolderRow(t, db, item); ok {
			break
		}
		step(1)
	}
	var rerunFence sql.NullInt64
	if err := db.QueryRow(`SELECT fence_asset FROM catalog_availability_work WHERE entity_id=?`, item).Scan(&rerunFence); err != nil {
		t.Fatal(err)
	}
	if !rerunFence.Valid || rerunFence.Int64 < tail {
		t.Fatalf("rerun frontier %v does not include the appended tail %d", rerunFence, tail)
	}
	for i := 0; i < 200; i++ {
		if cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=20`) == 0 {
			break
		}
		step(4)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=20 AND entity_id=?`, item); n != 0 {
		t.Fatalf("tail rerun left %d queued revisions", n)
	}
	if _, _, _, ok := availabilityHolderRow(t, db, item); ok {
		t.Fatal("tail rerun kept its cursor")
	}
	if n := cc1Scalar(t, db, `SELECT available FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, item, tail); n != 0 {
		t.Fatalf("tail rerun did not publish the tail evidence: %d", n)
	}
	availabilityParity(t, db)
}

// TestAvailabilityHolderCursorHandlesZeroNegativeAndEmptyIDs gives the walk
// the keys a positive-only cursor would skip or repeat: asset IDs 0 and -1,
// an empty inventory object ID and an empty library source ID.
func TestAvailabilityHolderCursorHandlesZeroNegativeAndEmptyIDs(t *testing.T) {
	db := cc1OpenDB(t, "availability-ids.sqlite")
	lib := cc1Library(t, db, "ids", "Ids", "movie", "/ids")
	item := facetTestEntity(t, db, lib, "ids:item")
	positive := sourceTestAsset(t, db, []int64{item}, "/ids/positive.mkv")
	cc1Exec(t, db, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,available)
 VALUES(0,'id-zero','/ids/zero.mkv',1,1,'mkv','h264','aac',1,1,60,1),
       (-1,'id-negative','/ids/negative.mkv',1,1,'mkv','h264','aac',1,1,60,1)`)
	cc1Exec(t, db, `INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,available,start_seconds,end_seconds,edition)
 VALUES(?,0,1,1,0,NULL,''),(?,-1,2,1,0,NULL,'')`, item, item)
	// An empty source ID sorts before every other source in this library.
	cc1Exec(t, db, `INSERT INTO library_sources(id,library_id,configured_root,root) VALUES('','ids','/ids-empty','/ids-empty')`)
	// An empty object ID is the first raw object of the positive asset, and a
	// later object qualifies, so the cursor must advance past the empty key.
	cc1Exec(t, db, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)
 SELECT '',s.id,?,s.incarnation,'positive.mkv','r0','{}',1,1,'missing' FROM library_sources s WHERE s.id='ids'`, availabilityToken(t, db, positive))
	cc1Exec(t, db, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)
 SELECT 'z-object',s.id,?,s.incarnation,'positive-copy.mkv','r1','{}',1,1,'available' FROM library_sources s WHERE s.id='ids'`, availabilityToken(t, db, positive))
	cc1Drain(t, db)
	availabilityParity(t, db)
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_asset_links WHERE entity_id=? AND available=1`, item); n != 3 {
		t.Fatalf("%d of three edge-keyed links are available", n)
	}
	if _, _, _, ok := availabilityHolderRow(t, db, item); ok {
		t.Fatal("edge-keyed pass kept its cursor")
	}
}

// TestAvailabilityHolderWideScalarStaysBounded gives one turn a source root
// and a link path wider than the ordinary scalar budget. A single wide scalar
// is the legal O(B) exception, but the turn must stop after it and the pass
// must still finish and match the view.
func TestAvailabilityHolderWideScalarStaysBounded(t *testing.T) {
	db := cc1OpenDB(t, "availability-wide.sqlite")
	lib := cc1Library(t, db, "wide", "Wide", "movie", "/wide")
	wideRoot := "/wide/" + strings.Repeat("r", facetScalarBudget+64)
	cc1Exec(t, db, `INSERT INTO library_sources(id,library_id,configured_root,root) VALUES('wide-big','wide',?,?)`, wideRoot, wideRoot)
	item := facetTestEntity(t, db, lib, "wide:item")
	sourceTestAsset(t, db, []int64{item}, "/wide/short.mkv")
	sourceTestAsset(t, db, []int64{item}, "/wide/"+strings.Repeat("w", facetScalarBudget+64)+".mkv")
	cc1Drain(t, db)
	availabilityParity(t, db)
	if _, _, _, ok := availabilityHolderRow(t, db, item); ok {
		t.Fatal("wide-scalar pass kept its cursor")
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=20`); n != 0 {
		t.Fatalf("wide-scalar pass left %d queued keys", n)
	}
}

// An empty captured frontier stays empty even if evidence is appended later.
func TestAvailabilityHolderNullFrontierDoesNotRecapture(t *testing.T) {
	db := cc1OpenDB(t, "availability-empty-frontier.sqlite")
	lib := cc1Library(t, db, "empty", "Empty", "movie", "/empty")
	item := facetTestEntity(t, db, lib, "empty:item")
	var holder *availabilityHolder
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := TouchTx(ctx, tx, DomainAvailability, item); err != nil {
			return err
		}
		var revision int64
		if err := tx.QueryRowContext(ctx, `SELECT revision FROM catalog_dirty WHERE domain=20 AND entity_id=?`, item).Scan(&revision); err != nil {
			return err
		}
		var err error
		holder, err = availabilityHolderTx(ctx, tx, Key{ID: item, Revision: revision})
		return err
	})
	if holder.fenceAsset.Valid {
		t.Fatal("empty frontier is not NULL")
	}
	sourceTestAsset(t, db, []int64{item}, "/empty/appended.mkv")
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		visits, err := holder.advance(ctx, tx, 8)
		if err != nil {
			return err
		}
		if visits != 0 || !holder.walked {
			t.Fatalf("empty captured pass read %d rows; ended=%v", visits, holder.walked)
		}
		return nil
	})
}

// TestAvailabilityHolderLargeLinkMeasurement is opt-in: it reports the turns,
// wall time and writer share of a scaled file-mutation holder. PORTICO_P2_63_LARGE_LINKS sets the
// link count (default 100000).
func TestAvailabilityHolderLargeLinkMeasurement(t *testing.T) {
	value := os.Getenv("PORTICO_P2_63_LARGE_LINKS")
	if value == "" {
		t.Skip("set PORTICO_P2_63_LARGE_LINKS to measure a large-link availability pass")
	}
	links, err := strconv.Atoi(value)
	if err != nil || links < 1 {
		t.Fatalf("PORTICO_P2_63_LARGE_LINKS=%q is not a link count", value)
	}
	db := cc1OpenDB(t, "availability-large.sqlite")
	lib := cc1Library(t, db, "large", "Large", "movie", "/large")
	item := facetTestEntity(t, db, lib, "large:item")
	// Seed actual missing-file evidence and deliberately stale advertised flags.
	// Other derivations are outside this isolated holder measurement.
	cc1Drain(t, db)
	cc1Exec(t, db, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<?)
 INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,available)
 SELECT printf('large-%08d',i),printf('/large/file-%08d.mkv',i),1,1,'mkv','h264','aac',1,1,60,0 FROM n`, links)
	cc1Exec(t, db, `INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,available)
 SELECT ?,id,id,1 FROM catalog_assets WHERE token LIKE 'large-%'`, item)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return TouchTx(ctx, tx, DomainAvailability, item) })
	before := dbwork.WriteGate().Stats().ByClass["maintenance"].HeldMilli
	started := time.Now()
	turns := 0
	var maxUser time.Duration
	for turns < links*4+1000 {
		// Same domain20 handler and maintenance budget; raw32 is below the worker's
		// existing64 cap. Each turn yields a writer slot to an interactive point write.
		done, _ := availabilityTurn(t, db, 32)
		turns++
		began := time.Now()
		if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
			_, err := tx.Exec(`UPDATE admin_revision SET revision=revision+1 WHERE id=1`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		maxUser = max(maxUser, time.Since(began))
		if done {
			break
		}
	}
	wall := time.Since(started)
	held := dbwork.WriteGate().Stats().ByClass["maintenance"].HeldMilli - before
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=20`); n != 0 {
		t.Fatalf("large holder did not finish in %d turns", turns)
	}
	availabilityParity(t, db)
	t.Logf("large-link holder: %d changed links, %d turns, wall %s, maintenance writer %dms (%.1f%%), %d interactive writer slots, max user %s", links, turns, wall, held, 100*float64(held)/float64(wall.Milliseconds()), turns, maxUser)
}

func TestAvailabilityHolderChangedFlagsStayInsideMaintenancePlan(t *testing.T) {
	db := cc1OpenDB(t, "availability-plan.sqlite")
	lib := cc1Library(t, db, "plan", "Plan", "movie", "/plan")
	item := facetTestEntity(t, db, lib, "plan:item")
	cc1Drain(t, db)
	cc1Exec(t, db, `WITH RECURSIVE n(i) AS(SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<128)
 INSERT INTO catalog_assets(token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,available)
 SELECT printf('plan-%d',i),printf('/plan/file-%d.mkv',i),1,1,'mkv','h264','aac',1,1,60,0 FROM n`)
	cc1Exec(t, db, `INSERT INTO catalog_asset_links(entity_id,asset_id,part_index,available) SELECT ?,id,id,1 FROM catalog_assets WHERE token LIKE 'plan-%'`, item)
	dbwork.ResetPlans()
	for turn := 0; turn < 100; turn++ {
		done, _ := availabilityTurn(t, db, 64)
		if done {
			break
		}
		if turn == 99 {
			t.Fatal("changed flags did not finish")
		}
	}
	stats := dbwork.Plans()
	plan := dbwork.DefaultPlan(dbwork.ClassMaintenance)
	if stats.Measured == 0 || stats.Largest.Rows > plan.Rows || stats.Largest.Pages > plan.Pages || stats.Largest.VMOps > plan.VMOps {
		t.Fatalf("availability flag turn escaped existing maintenance plan: %+v", stats)
	}
	availabilityParity(t, db)
	t.Logf("availability changed flags max rows/pages/VM=%d/%d/%d", stats.Largest.Rows, stats.Largest.Pages, stats.Largest.VMOps)
}
