package persistence

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadingCompleteTypedBundleConstraintsAndRawDriverIndexes(t *testing.T) {
	db, err := OpenFresh(filepath.Join(t.TempDir(), "path.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Minimal actual association; witness deliberately has NO object/source FK.
	lib := persistenceLibrary(t, db, "path-schema", "Reading", "book", "/read")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head) VALUES(1,randomblob(16),?,20,'Book','book','b')`, lib)
	exec(`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(1,'path-asset','/read/Book.epub',1,1,'epub','','',0,0,0)`)
	exec(`INSERT INTO catalog_asset_links(entity_id,asset_id) VALUES(1,1)`)
	exec(`INSERT INTO reading_publications VALUES(1,1,'epub')`)
	exec(`INSERT INTO reading_path_head(item_id,asset_id,publisher_sequence,object_id,source_revision) VALUES(1,1,1,'deleted-object','revision')`)
	exec(`INSERT INTO reading_path_scope(library_id,source_id,incarnation,root_prefix,directory_key) VALUES(?,'s','i','/read/','Shelf')`, lib)
	exec(`INSERT INTO reading_path_build(scope_id,desired,input_revision,source_generation,root_identity,job_id,phase) VALUES(1,1,1,1,'r','job','stage')`)
	exec(`INSERT INTO reading_path_stage(build_id,item_id,asset_id,object_id,source_revision) VALUES(1,1,1,'deleted-object','revision')`)
	for _, change := range []struct {
		sql  string
		args []any
	}{
		{`publisher_sequence=0`, nil}, {`object_id=''`, nil}, {`source_revision=''`, nil},
		{`author='',author_source='folder'`, nil}, {`author=?,author_source='folder'`, []any{strings.Repeat("é", 2049)}},
		{`author=?,author_source='folder'`, []any{"bad\ntext"}}, {`author='Name',author_source=NULL`, nil},
		{`series_number=?`, []any{math.Inf(1)}}, {`series_number=-1`, nil},
		{`issue_date='2026-02-30'`, nil}, {`issue_date='2026-13'`, nil}, {`issue_date='0000-01'`, nil},
	} {
		if _, err := db.Exec(`UPDATE reading_path_head SET `+change.sql, change.args...); err == nil {
			t.Fatalf("invalid bundle accepted: %s", change.sql)
		}
		if change.sql != `publisher_sequence=0` {
			if _, err := db.Exec(`UPDATE reading_path_stage SET `+change.sql, change.args...); err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
				t.Fatalf("invalid staged bundle not rejected by CHECK: %s: %v", change.sql, err)
			}
		}
	}
	for _, date := range []string{"2024-02-29", "2026-01", "9999-12-31"} {
		exec(`UPDATE reading_path_head SET issue_date=?`, date)
	}
	// Both representation cascades use the publication association, never witness.
	for _, q := range []string{
		`UPDATE reading_path_scope SET queued=2`,
		`UPDATE reading_path_scope SET sealed=2`,
		`UPDATE reading_path_scope SET pending=-1`,
		`UPDATE reading_path_build SET phase='bogus'`,
		`UPDATE reading_path_build SET publish_sequence=0`,
		`INSERT INTO reading_path_dirty_work(library_id,kind,work_key) VALUES(1,'bogus','x')`,
		`UPDATE reading_path_scope SET parked=2`,
		`INSERT INTO reading_path_inputs(library_id,aborts) VALUES(1,17)`,
		`INSERT INTO reading_path_inputs(library_id,hold_until_ms) VALUES(1,-1)`,
		`INSERT INTO reading_path_capture VALUES(1,1,1,'o','r','Book.epub',20,2)`,
		`INSERT INTO reading_path_representative VALUES(1,1,1,2)`,
	} {
		if _, err := db.Exec(q); err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
			t.Fatalf("invalid coordinator state not rejected by CHECK: %s: %v", q, err)
		}
	}
	for _, phase := range []string{"capture", "stage", "ready", "cleanup", "abort"} {
		exec(`UPDATE reading_path_build SET phase=?,publish_sequence=1 WHERE id=1`, phase)
		if _, err := db.Exec(`UPDATE reading_path_slot SET build_id=1,overlay_id=1 WHERE singleton=1`); err == nil || !strings.Contains(err.Error(), "Reading overlay requires a published build") {
			t.Fatalf("non-published %s overlay not refused: %v", phase, err)
		}
	}
	exec(`UPDATE reading_path_build SET phase='flatten',publish_sequence=NULL WHERE id=1`)
	if _, err := db.Exec(`UPDATE reading_path_slot SET build_id=1,overlay_id=1 WHERE singleton=1`); err == nil {
		t.Fatal("unsequenced flatten overlay accepted")
	}
	exec(`DELETE FROM reading_path_slot`)
	if _, err := db.Exec(`INSERT INTO reading_path_slot VALUES(1,1,1)`); err == nil || !strings.Contains(err.Error(), "Reading overlay requires a published build") {
		t.Fatalf("insert bypassed overlay publication guard: %v", err)
	}
	exec(`UPDATE reading_path_build SET publish_sequence=1 WHERE id=1`)
	exec(`INSERT INTO reading_path_slot VALUES(1,1,1)`)
	exec(`DELETE FROM reading_publications WHERE item_id=1`)
	for _, table := range []string{"reading_path_head", "reading_path_stage"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("association did not cascade %s: %d %v", table, n, err)
		}
	}
	for _, q := range []struct{ sql, index string }{
		{`SELECT id FROM inventory_objects WHERE asset_id='asset' AND id>'after' ORDER BY id LIMIT 128`, "inventory_objects_reading_asset_object"},
		{`SELECT id FROM inventory_objects WHERE source_id='source' AND id>'after' ORDER BY id LIMIT 128`, "inventory_objects_reading_source_object"},
		{`SELECT object_id FROM reading_path_object_keys WHERE scope_id=1 AND object_id>'after' ORDER BY object_id LIMIT 128`, "reading_path_object_scope"},
	} {
		rows, err := db.Query(`EXPLAIN QUERY PLAN ` + q.sql)
		if err != nil {
			t.Fatal(err)
		}
		var plan string
		for rows.Next() {
			var a, b, c int
			var detail string
			if err := rows.Scan(&a, &b, &c, &detail); err != nil {
				t.Fatal(err)
			}
			plan += detail
		}
		rows.Close()
		if !strings.Contains(plan, q.index) || strings.Contains(plan, "TEMP B-TREE") {
			t.Fatalf("unbounded raw driver plan: %s", plan)
		}
	}
}

// The scheduler's liveness rests on these triggers: parked scopes are never
// timer-polled, so each park reason must have a real unpark event.
func TestReadingPathParkedScopesHaveUnparkEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "park.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lib := persistenceLibrary(t, db, "park-schema", "Reading", "book", "/read")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := db.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	var source string
	if err := db.QueryRow(`SELECT id FROM library_sources WHERE library_id='park-schema'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO reading_path_scope(library_id,source_id,incarnation,root_prefix,directory_key,parked,pending) VALUES(?,?,'i','/read/','A',1,1)`, lib, source)
	// Own new obligation.
	exec(`UPDATE reading_path_scope SET desired=desired+1`)
	if count(`SELECT parked FROM reading_path_scope`) != 0 {
		t.Fatal("desired change did not unpark")
	}
	// Availability settling; a still-pending change does not unpark.
	exec(`UPDATE reading_path_scope SET parked=1,pending=2`)
	exec(`UPDATE reading_path_scope SET pending=1`)
	if count(`SELECT parked FROM reading_path_scope`) != 1 {
		t.Fatal("unsettled availability unparked")
	}
	exec(`UPDATE reading_path_scope SET pending=0`)
	if count(`SELECT parked FROM reading_path_scope`) != 0 {
		t.Fatal("settled availability did not unpark")
	}
	// Barrier insert and every meaningful barrier change queue one coalesced
	// wake; the issue cursor does not.
	exec(`INSERT INTO reading_path_source_barrier VALUES(?,'job','i',1,'r','/read',0,0,'open','')`, source)
	wake := func() int {
		return count(`SELECT COALESCE((SELECT requested FROM reading_path_dirty_work WHERE kind='wake' AND work_key=?),0)`, source)
	}
	if wake() != 1 {
		t.Fatal("barrier insert did not queue a wake")
	}
	exec(`UPDATE reading_path_source_barrier SET after_issue=9`)
	exec(`UPDATE reading_path_source_barrier SET phase='open'`)
	if wake() != 1 {
		t.Fatal("cursor-only or same-value barrier write queued a wake")
	}
	for i, change := range []string{`job_id='job2'`, `coverage_revision=1`, `phase='sealed'`, `reason='untrusted_coverage'`, `root_identity='r2'`, `source_generation=2`} {
		exec(`UPDATE reading_path_source_barrier SET ` + change)
		if wake() != i+2 {
			t.Fatalf("barrier change %s did not queue a coalesced wake", change)
		}
	}
	if count(`SELECT count(*) FROM reading_path_dirty_work WHERE kind='wake'`) != 1 {
		t.Fatal("wakes did not coalesce per source")
	}
}
