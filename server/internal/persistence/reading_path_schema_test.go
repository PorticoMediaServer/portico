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

	}
	for _, date := range []string{"2024-02-29", "2026-01", "9999-12-31"} {
		exec(`UPDATE reading_path_head SET issue_date=?`, date)
	}
	// Accepted heads cascade only with their publication association, never
	// the source/object witness. Obsolete folder representations do not exist.
	exec(`DELETE FROM reading_publications WHERE item_id=1`)
	var heads int
	if err := db.QueryRow(`SELECT count(*) FROM reading_path_head`).Scan(&heads); err != nil || heads != 0 {
		t.Fatal("association did not cascade", heads, err)
	}
	for _, table := range []string{"reading_path_scope", "reading_path_build", "reading_path_capture", "reading_path_representative", "reading_path_stage", "reading_path_slot", "reading_path_inputs"} {
		var present int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&present); err != nil || present != 0 {
			t.Fatal("obsolete folder state remains", table, present, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO reading_path_dirty_work(library_id,kind,work_key) VALUES(1,'bogus','x')`); err == nil || !strings.Contains(err.Error(), "CHECK constraint") {
		t.Fatal("untyped feed kind accepted", err)
	}

	for _, q := range []struct{ sql, index string }{
		{`SELECT id FROM inventory_objects WHERE asset_id='asset' AND id>'after' ORDER BY id LIMIT 128`, "inventory_objects_reading_asset_object"},
		{`SELECT id FROM inventory_objects WHERE source_id='source' AND id>'after' ORDER BY id LIMIT 128`, "inventory_objects_reading_source_object"},
		{`SELECT object_id FROM reading_path_object_keys WHERE library_id=1 AND object_id>'after' ORDER BY object_id LIMIT 128`, "reading_path_object_library"},
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

// The existing availability feed supplies the retry event; per-file publication
// debt never needs a quiet-gap timer or a folder wake.
func TestReadingPathPerFilePendingEvents(t *testing.T) {
	db, err := OpenFresh(filepath.Join(t.TempDir(), "pending.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lib := persistenceLibrary(t, db, "pending-schema", "Reading", "book", "/read")
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
	exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head) VALUES(1,randomblob(16),?,20,'Book','book','b')`, lib)
	exec(`DELETE FROM reading_path_dirty_work`)
	exec(`DELETE FROM reading_path_availability_pending`)
	exec(`DELETE FROM reading_path_dirty_work`)
	exec(`INSERT INTO reading_path_availability_pending(entity_id,library_id) VALUES(1,?)`, lib)
	if count(`SELECT requested FROM reading_path_dirty_work WHERE library_id=? AND kind='pending' AND work_key='1'`, lib) != 1 {
		t.Fatal("pending availability did not enqueue original item")
	}
	exec(`DELETE FROM reading_path_availability_pending WHERE entity_id=1`)
	if count(`SELECT requested FROM reading_path_dirty_work WHERE library_id=? AND kind='pending' AND work_key='1'`, lib) != 2 {
		t.Fatal("settled availability lost its coalesced retry event")
	}
}
