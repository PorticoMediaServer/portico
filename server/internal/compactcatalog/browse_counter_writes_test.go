package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// The six catalog_browse_member_links triggers as source HEAD had them. The
// reference database below keeps these and drops the new metric-link triggers,
// so the same statement can be compared with the B triggers without the new
// metric contribution path. The original link triggers write one container row
// per membership; the new link triggers write one item row.
var browseCounterOriginalTriggers = []string{
	`CREATE TRIGGER catalog_browse_member_links_join AFTER INSERT ON catalog_browse_memberships BEGIN
 INSERT INTO catalog_browse_member_links(entity_id,available,unavailable,members)
  SELECT NEW.entity_id,COALESCE(sum(l.available=1),0),COALESCE(sum(l.available=0),0),1 FROM catalog_asset_links l WHERE l.entity_id=NEW.item_id
  ON CONFLICT(entity_id) DO UPDATE SET available=available+excluded.available,unavailable=unavailable+excluded.unavailable,members=members+1;
END`,
	`CREATE TRIGGER catalog_browse_member_links_leave AFTER DELETE ON catalog_browse_memberships BEGIN
 UPDATE catalog_browse_member_links SET available=available-(SELECT count(*) FROM catalog_asset_links l WHERE l.entity_id=OLD.item_id AND l.available=1),
  unavailable=unavailable-(SELECT count(*) FROM catalog_asset_links l WHERE l.entity_id=OLD.item_id AND l.available=0),members=members-1 WHERE entity_id=OLD.entity_id;
END`,
	`CREATE TRIGGER catalog_browse_member_links_move AFTER UPDATE OF entity_id,item_id ON catalog_browse_memberships WHEN OLD.entity_id IS NOT NEW.entity_id OR OLD.item_id IS NOT NEW.item_id BEGIN
 UPDATE catalog_browse_member_links SET available=available-(SELECT count(*) FROM catalog_asset_links l WHERE l.entity_id=OLD.item_id AND l.available=1),
  unavailable=unavailable-(SELECT count(*) FROM catalog_asset_links l WHERE l.entity_id=OLD.item_id AND l.available=0),members=members-1 WHERE entity_id=OLD.entity_id;
 INSERT INTO catalog_browse_member_links(entity_id,available,unavailable,members)
  SELECT NEW.entity_id,COALESCE(sum(l.available=1),0),COALESCE(sum(l.available=0),0),1 FROM catalog_asset_links l WHERE l.entity_id=NEW.item_id
  ON CONFLICT(entity_id) DO UPDATE SET available=available+excluded.available,unavailable=unavailable+excluded.unavailable,members=members+1;
END`,
	`CREATE TRIGGER catalog_browse_member_links_link_insert AFTER INSERT ON catalog_asset_links BEGIN
 INSERT INTO catalog_browse_member_links(entity_id,available,unavailable)
  SELECT m.entity_id,count(*)*(NEW.available=1),count(*)*(NEW.available=0) FROM catalog_browse_memberships m INDEXED BY catalog_browse_membership_item WHERE m.item_id=NEW.entity_id GROUP BY m.entity_id
  ON CONFLICT(entity_id) DO UPDATE SET available=available+excluded.available,unavailable=unavailable+excluded.unavailable;
END`,
	`CREATE TRIGGER catalog_browse_member_links_link_delete AFTER DELETE ON catalog_asset_links BEGIN
 UPDATE catalog_browse_member_links SET
  available=available-(OLD.available=1)*(SELECT count(*) FROM catalog_browse_memberships m INDEXED BY catalog_browse_membership_item WHERE m.item_id=OLD.entity_id AND m.entity_id=catalog_browse_member_links.entity_id),
  unavailable=unavailable-(OLD.available=0)*(SELECT count(*) FROM catalog_browse_memberships m INDEXED BY catalog_browse_membership_item WHERE m.item_id=OLD.entity_id AND m.entity_id=catalog_browse_member_links.entity_id)
  WHERE entity_id IN(SELECT entity_id FROM catalog_browse_memberships INDEXED BY catalog_browse_membership_item WHERE item_id=OLD.entity_id);
END`,
	`CREATE TRIGGER catalog_browse_member_links_link_update AFTER UPDATE OF entity_id,available ON catalog_asset_links WHEN OLD.entity_id IS NOT NEW.entity_id OR OLD.available IS NOT NEW.available BEGIN
 UPDATE catalog_browse_member_links SET
  available=available-(OLD.available=1)*(SELECT count(*) FROM catalog_browse_memberships m INDEXED BY catalog_browse_membership_item WHERE m.item_id=OLD.entity_id AND m.entity_id=catalog_browse_member_links.entity_id),
  unavailable=unavailable-(OLD.available=0)*(SELECT count(*) FROM catalog_browse_memberships m INDEXED BY catalog_browse_membership_item WHERE m.item_id=OLD.entity_id AND m.entity_id=catalog_browse_member_links.entity_id)
  WHERE entity_id IN(SELECT entity_id FROM catalog_browse_memberships INDEXED BY catalog_browse_membership_item WHERE item_id=OLD.entity_id);
 INSERT INTO catalog_browse_member_links(entity_id,available,unavailable)
  SELECT m.entity_id,count(*)*(NEW.available=1),count(*)*(NEW.available=0) FROM catalog_browse_memberships m INDEXED BY catalog_browse_membership_item WHERE m.item_id=NEW.entity_id GROUP BY m.entity_id
  ON CONFLICT(entity_id) DO UPDATE SET available=available+excluded.available,unavailable=unavailable+excluded.unavailable;
END`,
}

// browseCounterReferenceDB is a fresh install with source HEAD's six
// member_links triggers and without the new metric-link triggers. Everything
// else, including the B tables, is the current schema.
func browseCounterReferenceDB(t *testing.T, name string) *sql.DB {
	t.Helper()
	db := cc1OpenDB(t, name)
	for _, trigger := range []string{
		"catalog_browse_member_links_join",
		"catalog_browse_member_links_leave",
		"catalog_browse_member_links_move",
		"catalog_browse_member_links_link_insert",
		"catalog_browse_member_links_link_delete",
		"catalog_browse_member_links_link_update",
		"catalog_browse_metric_links_insert",
		"catalog_browse_metric_links_delete",
		"catalog_browse_metric_links_update",
	} {
		cc1Exec(t, db, "DROP TRIGGER IF EXISTS "+trigger)
	}
	for _, ddl := range browseCounterOriginalTriggers {
		cc1Exec(t, db, ddl)
	}
	return db
}

type browseCounterWriteFixture struct {
	item, other int64
	containers  []int64
	fresh       []int64
	spare, move int64
}

func browseCounterWriteSeed(t *testing.T, db *sql.DB, memberships int) browseCounterWriteFixture {
	t.Helper()
	lib := cc1Library(t, db, "writes", "Writes", "tv", "/writes")
	f := browseCounterWriteFixture{containers: make([]int64, memberships), fresh: make([]int64, 4)}
	for i := range f.containers {
		f.containers[i] = cc1Entity(t, db, Entity{Library: lib, Kind: Show, Key: fmt.Sprintf("show:%d", i), Title: fmt.Sprintf("Show %d", i)},
			map[string]any{"local_key": fmt.Sprintf("show-%d", i)})
	}
	for i := range f.fresh {
		f.fresh[i] = cc1Entity(t, db, Entity{Library: lib, Kind: Show, Key: fmt.Sprintf("fresh:%d", i), Title: fmt.Sprintf("Fresh %d", i)},
			map[string]any{"local_key": fmt.Sprintf("fresh-%d", i)})
	}
	f.item = cc1Entity(t, db, Entity{Library: lib, Kind: Episode, Parent: f.containers[0], Key: "ep:item", Title: "Item", Added: "2026-01-01T00:00:00.000Z"},
		map[string]any{"show_id": f.containers[0], "number": 1, "numbering": "seasonal"})
	f.other = cc1Entity(t, db, Entity{Library: lib, Kind: Episode, Parent: f.containers[0], Key: "ep:other", Title: "Other", Added: "2026-01-01T00:00:00.000Z"},
		map[string]any{"show_id": f.containers[0], "number": 2, "numbering": "seasonal"})
	for _, container := range f.containers {
		cc1Exec(t, db, `INSERT INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,1)`, container, f.item)
	}
	// A metric membership that is not part of the measured membership path.
	cc1Exec(t, db, `INSERT INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,4)`, f.fresh[2], f.item)
	sourceTestAsset(t, db, []int64{f.item}, "/writes/base.mkv")
	f.move = sourceTestAsset(t, db, []int64{f.item}, "/writes/move.mkv")
	f.spare = sourceTestAsset(t, db, nil, "/writes/spare.mkv")
	return f
}

type browseCounterWriteDelta struct {
	before, after int64
}

func (d browseCounterWriteDelta) delta() int64 { return d.after - d.before }

// browseCounterWriteMeasure runs one statement on the pinned connection and
// reads SQLite's total_changes() immediately before and after it. total_changes
// counts the statement's own row plus every trigger row, on that connection.
func browseCounterWriteMeasure(t *testing.T, conn *sql.Conn, query string, args ...any) browseCounterWriteDelta {
	t.Helper()
	ctx := context.Background()
	var d browseCounterWriteDelta
	if err := conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&d.before); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if err := conn.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&d.after); err != nil {
		t.Fatal(err)
	}
	return d
}

type browseCounterWriteRun struct {
	reference map[string]browseCounterWriteDelta
	current   map[string]browseCounterWriteDelta
}

// TestBrowseCounterSourceWriteSlope compares the physical rows written by the
// six source-HEAD member_links triggers with the B triggers for every source
// path, at one and many memberships. The new link paths must not grow with the
// holder's membership count; the original ones do. No latency threshold is
// asserted: the log carries the exact total_changes before/after.
func TestBrowseCounterSourceWriteSlope(t *testing.T) {
	sizes := []int{1, 64}
	runs := map[int]browseCounterWriteRun{}
	for _, size := range sizes {
		reference := browseCounterReferenceDB(t, fmt.Sprintf("writes-reference-%d.sqlite", size))
		current := cc1OpenDB(t, fmt.Sprintf("writes-current-%d.sqlite", size))
		var metricUpdate int
		if err := current.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type='trigger' AND name='catalog_browse_metric_links_update'`).Scan(&metricUpdate); err != nil {
			t.Fatal(err)
		}
		t.Logf("memberships=%2d current schema metric update trigger present: %v", size, metricUpdate == 1)
		run := browseCounterWriteRun{
			reference: map[string]browseCounterWriteDelta{},
			current:   map[string]browseCounterWriteDelta{},
		}
		for _, side := range []struct {
			name string
			db   *sql.DB
			out  map[string]browseCounterWriteDelta
		}{
			{"reference", reference, run.reference},
			{"current", current, run.current},
		} {
			f := browseCounterWriteSeed(t, side.db, size)
			conn, err := side.db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			measure := func(path, query string, args ...any) {
				d := browseCounterWriteMeasure(t, conn, query, args...)
				side.out[path] = d
				t.Logf("%-6s memberships=%2d %-14s total_changes %d -> %d (%d rows)", side.name, size, path, d.before, d.after, d.delta())
			}
			measure("link-insert", `INSERT INTO catalog_asset_links(entity_id,asset_id,available) VALUES(?,?,1)`, f.item, f.spare)
			measure("link-update", `UPDATE catalog_asset_links SET available=0 WHERE entity_id=? AND asset_id=?`, f.item, f.spare)
			measure("link-delete", `DELETE FROM catalog_asset_links WHERE entity_id=? AND asset_id=?`, f.item, f.spare)
			measure("link-move", `UPDATE catalog_asset_links SET entity_id=? WHERE entity_id=? AND asset_id=?`, f.other, f.item, f.move)
			measure("member-insert", `INSERT INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,2)`, f.fresh[0], f.item)
			measure("member-update", `UPDATE catalog_browse_memberships SET source=3 WHERE entity_id=? AND item_id=? AND source=2`, f.fresh[0], f.item)
			measure("member-move", `UPDATE catalog_browse_memberships SET entity_id=? WHERE entity_id=? AND item_id=? AND source=3`, f.fresh[1], f.fresh[0], f.item)
			measure("member-delete", `DELETE FROM catalog_browse_memberships WHERE entity_id=? AND item_id=? AND source=3`, f.fresh[1], f.item)
			measure("metric-insert", `INSERT INTO catalog_browse_member_metrics(entity_id,item_id,source,available_links,unavailable_links) VALUES(?,?,4,1,1)`, f.fresh[2], f.item)
			measure("metric-update", `UPDATE catalog_browse_member_metrics SET available_links=2 WHERE entity_id=? AND item_id=? AND source=4`, f.fresh[2], f.item)
			measure("metric-delete", `DELETE FROM catalog_browse_member_metrics WHERE entity_id=? AND item_id=? AND source=4`, f.fresh[2], f.item)
			conn.Close()
		}
		runs[size] = run
	}

	one, many := runs[1], runs[64]
	for _, path := range []string{"link-insert", "link-update", "link-delete", "link-move"} {
		if got, want := many.current[path].delta(), one.current[path].delta(); got != want {
			t.Errorf("new %s wrote %d rows at 64 memberships and %d at 1: not a fixed slope", path, got, want)
		}
		if many.reference[path].delta() <= one.reference[path].delta() {
			t.Errorf("source-HEAD %s did not grow with memberships: %d rows at 1, %d at 64", path, one.reference[path].delta(), many.reference[path].delta())
		}
	}
	for _, path := range []string{"member-insert", "member-update", "member-move", "member-delete", "metric-insert", "metric-update", "metric-delete"} {
		if got, want := many.current[path].delta(), one.current[path].delta(); got != want {
			t.Errorf("new %s wrote %d rows at 64 memberships and %d at 1: not constant", path, got, want)
		}
	}
}
