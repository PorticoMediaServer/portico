package persistence

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestLibrarySourceProgressBaselineShape pins the approved fresh-baseline
// table: one sparse child per source, the TEXT primary key as the complete
// reverse-FK index, and no progress column left on the authority table.
func TestLibrarySourceProgressBaselineShape(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var sql string
	if err = db.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name='library_source_progress'`).Scan(&sql); err != nil {
		t.Fatal(err)
	}
	normalized := strings.Join(strings.Fields(sql), " ")
	for _, want := range []string{
		"CREATE TABLE library_source_progress (",
		"source_id TEXT NOT NULL PRIMARY KEY REFERENCES library_sources(id) ON DELETE CASCADE",
		"last_progress_at TEXT NOT NULL DEFAULT ''",
		") STRICT",
	} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("baseline table is missing %q:\n%s", want, normalized)
		}
	}
	type column struct {
		name, kind  string
		notNull, pk int
		defaultVal  any
	}
	rows, err := db.Query(`SELECT name,type,"notnull",pk,dflt_value FROM pragma_table_info('library_source_progress') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []column
	for rows.Next() {
		var c column
		if err = rows.Scan(&c.name, &c.kind, &c.notNull, &c.pk, &c.defaultVal); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, c)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(columns) != 2 || columns[0].name != "source_id" || columns[0].kind != "TEXT" || columns[0].notNull != 1 || columns[0].pk != 1 ||
		columns[1].name != "last_progress_at" || columns[1].kind != "TEXT" || columns[1].notNull != 1 || columns[1].pk != 0 || columns[1].defaultVal != "''" {
		t.Fatalf("progress columns: %+v", columns)
	}
	var parentColumn int
	if err = db.QueryRow(`SELECT count(*) FROM pragma_table_info('library_sources') WHERE name='last_progress_at'`).Scan(&parentColumn); err != nil {
		t.Fatal(err)
	}
	if parentColumn != 0 {
		t.Fatal("library_sources still carries the observational last_progress_at column")
	}
	var fkTable, fkFrom, fkTo, onDelete string
	if err = db.QueryRow(`SELECT "table","from","to",on_delete FROM pragma_foreign_key_list('library_source_progress')`).Scan(&fkTable, &fkFrom, &fkTo, &onDelete); err != nil {
		t.Fatal(err)
	}
	if fkTable != "library_sources" || fkFrom != "source_id" || fkTo != "id" || onDelete != "CASCADE" {
		t.Fatalf("progress foreign key: %s.%s → %s.%s ON DELETE %s", fkTable, fkFrom, fkTable, fkTo, onDelete)
	}
	// The primary key's implicit index is the complete reverse-FK index.
	indexRows, err := db.Query(`SELECT name,"unique",origin FROM pragma_index_list('library_source_progress')`)
	if err != nil {
		t.Fatal(err)
	}
	defer indexRows.Close()
	leading := ""
	for indexRows.Next() {
		var name, origin string
		var unique int
		if err = indexRows.Scan(&name, &unique, &origin); err != nil {
			t.Fatal(err)
		}
		if origin != "pk" || unique != 1 {
			continue
		}
		var seq int
		var column string
		if err = db.QueryRow(`SELECT seqno,name FROM pragma_index_info(?) WHERE seqno=0`, name).Scan(&seq, &column); err != nil {
			t.Fatal(err)
		}
		leading = column
	}
	if leading != "source_id" {
		t.Fatalf("the primary-key index does not lead with source_id: %q", leading)
	}
}

// TestLibrarySourceProgressMissingParentAndCascade proves the accepted writer
// shape: a missing parent is a no-op, an unconditional insert would be refused
// by the real foreign key, and deleting the parent removes its one child.
func TestLibrarySourceProgressMissingParentAndCascade(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var enforced int
	if err = db.QueryRow(`PRAGMA foreign_keys`).Scan(&enforced); err != nil {
		t.Fatal(err)
	}
	if enforced != 1 {
		t.Fatal("foreign keys are not enforced in this handle")
	}
	result, err := db.Exec(`INSERT INTO library_source_progress(source_id,last_progress_at) SELECT id,? FROM library_sources WHERE id=? ON CONFLICT(source_id) DO UPDATE SET last_progress_at=excluded.last_progress_at`, "2026-10-27T00:00:00Z", "absent-source")
	if err != nil {
		t.Fatalf("the agreed missing-parent upsert was refused: %v", err)
	}
	if rows, err := result.RowsAffected(); err != nil || rows != 0 {
		t.Fatalf("missing-parent upsert affected %d rows (%v); it must be a no-op", rows, err)
	}
	const library = "progress"
	persistenceLibrary(t, db, library, "Progress", "movie", "/progress")
	if _, err = db.Exec(`INSERT INTO library_source_progress(source_id,last_progress_at) VALUES(?,?)`, library, "2026-10-27T00:00:01Z"); err != nil {
		t.Fatal(err)
	}
	// The no-op above is meaningful only because an absent parent is refused.
	if _, err = db.Exec(`INSERT INTO library_source_progress(source_id,last_progress_at) VALUES('absent-source','x')`); err == nil {
		t.Fatal("an unconditional child insert for a missing source was accepted")
	}
	if _, err = db.Exec(`DELETE FROM library_sources WHERE id=?`, library); err != nil {
		t.Fatal(err)
	}
	var children int
	if err = db.QueryRow(`SELECT count(*) FROM library_source_progress WHERE source_id=?`, library).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if children != 0 {
		t.Fatalf("deleting the parent left %d progress children", children)
	}
	if _, err = db.Exec(`INSERT INTO library_sources(id,library_id,name,configured_root,root) VALUES(?,?,'Recreated',?,?)`, library, library, "/progress", "/progress"); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM library_source_progress WHERE source_id=?`, library).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if children != 0 {
		t.Fatalf("recreating the parent resurrected %d progress children", children)
	}
}
