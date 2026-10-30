package persistence

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// partialFKIndexAllow lists the only FK child column sets that a partial index
// may cover. Each entry is a nullable child column whose partial index excludes
// exactly NULL values; SQLite never runs a child-key check for a NULL child, and
// the guard below asserts the planner still selects that index for `col=?`.
var partialFKIndexAllow = map[string]string{
	`catalog_asset_links(info_pending_change)`:  `catalog_info_pending_links excludes only NULL pointers; FK checks skip NULL and the planner uses it`,
	`catalog_assets(info_owner_object)`:         `catalog_info_owner_object excludes only NULL pins; FK checks skip NULL and the planner uses it`,
	`catalog_info_changes(target_owner_object)`: `catalog_info_target_owner_object excludes only NULL target pins; FK checks skip NULL and the planner uses it`,
	`catalog_browse_rows(item_id)`:              `catalog_browse_item is item_id IS NOT NULL; FK checks skip NULL item_id and the planner uses it`,
	`catalog_entities(parent_id)`:               `catalog_entities_parent is parent_id IS NOT NULL; FK checks skip NULL parent_id and the planner uses it`,
	`catalog_episodes(season_id)`:               `catalog_episodes_season is season_id IS NOT NULL; FK checks skip NULL season_id and the planner uses it`,
	`catalog_playlist_entries(item_id)`:         `catalog_playlist_entries_item is item_id IS NOT NULL; FK checks skip NULL item_id and the planner uses it`,
	`retained_title_state(capture_mutation_id)`: `retained_title_state_capture excludes only NULL capture_mutation_id; FK checks skip NULL and the planner uses it`,
}

// Every foreign key in the baseline must have an indexed child side, or say why
// not. An unindexed child column makes a parent delete (and its cascade) scan the
// whole child table while the writer is held; PERF-DB-006 found the Reading one,
// and this guard keeps the next schema edit from adding another silently.
func TestEveryForeignKeyChildColumnSetHasLeadingIndexOrJustification(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "fk-index-guard.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	gaps, usedAllow := fkIndexGaps(t, db, partialFKIndexAllow)
	for key, reason := range partialFKIndexAllow {
		if !usedAllow[key] {
			t.Errorf("stale FK index allow-list entry %s (%s): an index now covers it or it matches no FK", key, reason)
		}
	}
	if len(gaps) != 0 {
		t.Fatalf("%d foreign-key child column sets have no leading index (add one, or allow-list a reason):\n%s",
			len(gaps), strings.Join(gaps, "\n"))
	}
}

// fkIndexGaps is the guard body, shared with the minimal-schema regressions below.
// An allow-list entry only counts when the key exists with a non-empty reason, and
// index key positions are never compressed: an expression key stays in the prefix
// as a sentinel so `(lower(label), parent_id)` cannot look like `(parent_id)`.
func fkIndexGaps(t *testing.T, db *sql.DB, allow map[string]string) ([]string, map[string]bool) {
	t.Helper()
	var gaps []string
	usedAllow := map[string]bool{}
	for _, table := range sqliteTableNames(t, db) {
		alias := rowidAliasColumn(t, db, table)
		indexes := sqliteIndexes(t, db, table)
		for _, fk := range sqliteForeignKeys(t, db, table) {
			key := table + "(" + strings.Join(fk.columns, ",") + ")"
			covered := alias != "" && len(fk.columns) == 1 && fk.columns[0] == alias
			partialOnly := ""
			if !covered {
				for _, index := range indexes {
					if len(index.columns) < len(fk.columns) || !sameStrings(index.columns[:len(fk.columns)], fk.columns) {
						continue
					}
					if !index.partial {
						covered = true
						break
					}
					if len(fk.columns) == 1 && strings.EqualFold(index.predicate, fk.columns[0]+" IS NOT NULL") {
						reason, declared := allow[key]
						if !declared || strings.TrimSpace(reason) == "" {
							partialOnly = index.name
							continue
						}
						// The predicate is complete for FK semantics, but only an
						// index the planner actually picks is worth allowing.
						assertIndexChosen(t, db, table, fk.columns[0], index.name)
						covered = true
						usedAllow[key] = true
						break
					}
				}
			}
			if !covered {
				detail := key + " -> " + fk.parent
				if partialOnly != "" {
					detail += fmt.Sprintf(" (only partial index %s; not allow-listed with a reason)", partialOnly)
				}
				gaps = append(gaps, detail)
			}
		}
	}
	return gaps, usedAllow
}

// A partial index that excludes only NULL children is only acceptable when an
// explicit allow-list entry says why. A missing or blank reason must fail, so the
// guard cannot quietly bless the next partial index on its own.
func TestForeignKeyIndexGuardRejectsUnlistedPartialIndex(t *testing.T) {
	for name, allow := range map[string]map[string]string{
		"no entry":     nil,
		"blank reason": {"child(parent_id)": "   "},
	} {
		t.Run(name, func(t *testing.T) {
			db := minimalForeignKeySchema(t, `
CREATE TABLE parent(id INTEGER PRIMARY KEY);
CREATE TABLE child(id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent(id));
CREATE INDEX child_partial ON child(parent_id) WHERE parent_id IS NOT NULL;`)
			gaps, used := fkIndexGaps(t, db, allow)
			if len(gaps) != 1 || !strings.Contains(gaps[0], "child(parent_id)") || !strings.Contains(gaps[0], "not allow-listed with a reason") {
				t.Fatalf("unlisted partial index was not rejected: gaps=%v", gaps)
			}
			if len(used) != 0 {
				t.Fatalf("unlisted/blank partial index was marked used: %v", used)
			}
		})
	}
}

// PRAGMA index_info reports an expression key with a NULL name. That position must
// stay in the index prefix: (lower(label), parent_id) does not lead with parent_id
// and must not be compressed into a false match.
func TestForeignKeyIndexGuardRejectsExpressionLeadingIndex(t *testing.T) {
	db := minimalForeignKeySchema(t, `
CREATE TABLE parent(id INTEGER PRIMARY KEY);
CREATE TABLE child(id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL REFERENCES parent(id), label TEXT);
CREATE INDEX child_expression ON child(lower(label), parent_id);`)
	gaps, used := fkIndexGaps(t, db, nil)
	if len(gaps) != 1 || !strings.Contains(gaps[0], "child(parent_id)") {
		t.Fatalf("expression-leading index falsely covered the FK: gaps=%v", gaps)
	}
	if len(used) != 0 {
		t.Fatalf("expression-leading index was marked used: %v", used)
	}
}

// The valid shapes stay accepted: a real leading index, an INTEGER PRIMARY KEY
// rowid alias, and one declared partial index with a non-empty reason.
func TestForeignKeyIndexGuardAcceptsLeadingRowidAndDeclaredPartial(t *testing.T) {
	db := minimalForeignKeySchema(t, `
CREATE TABLE parent(id INTEGER PRIMARY KEY);
CREATE TABLE child_leading(id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent(id));
CREATE INDEX child_leading_fk ON child_leading(parent_id);
CREATE TABLE child_rowid(id INTEGER PRIMARY KEY REFERENCES parent(id));
CREATE TABLE child_partial(id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent(id));
CREATE INDEX child_partial_fk ON child_partial(parent_id) WHERE parent_id IS NOT NULL;`)
	allow := map[string]string{
		"child_partial(parent_id)": "test: nullable child, partial index excludes only NULLs and the planner uses it",
	}
	gaps, used := fkIndexGaps(t, db, allow)
	if len(gaps) != 0 {
		t.Fatalf("valid index shapes were rejected: %v", gaps)
	}
	if !used["child_partial(parent_id)"] {
		t.Fatalf("declared partial index was not marked used: %v", used)
	}
}

func minimalForeignKeySchema(t *testing.T, ddl string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "minimal-fk.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(ddl); err != nil {
		t.Fatal(err)
	}
	return db
}

// The Reading path facts are the confirmed finding: keep an explicit regression
// for their exact index name and cascade, independent of the general guard.
func TestReadingSourceValuesInventoryObjectLookupAndCascade(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "reading-fk-index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	library := persistenceLibrary(t, db, "reading-fk", "Reading FK", "book", "/reading-fk")
	exec(`INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head) VALUES(1,randomblob(16),?,20,'Referenced','referenced','r'),(2,randomblob(16),?,20,'Unrelated','unrelated','u')`, library, library)
	exec(`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration) VALUES(1,'asset-ref','/reading-fk/ref.epub',1,1,'epub','','',0,0,0),(2,'asset-other','/reading-fk/other.epub',1,1,'epub','','',0,0,0)`)
	exec(`INSERT INTO catalog_asset_links(entity_id,asset_id) VALUES(1,1),(2,2)`)
	exec(`INSERT INTO reading_publications(item_id,asset_id,format) VALUES(1,1,'epub'),(2,2,'epub')`)
	exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES
		('object-ref','reading-fk','asset-ref','root','ref.epub','opaque:revision','{}',1,1),
		('object-other','reading-fk','asset-other','root','other.epub','opaque:revision','{}',1,1),
		('object-empty','reading-fk','asset-other','root','empty.epub','opaque:revision','{}',1,1)`)
	const insert = `INSERT INTO reading_source_values(item_id,asset_id,field,text_value,number_value,source,inventory_object_id,source_revision) VALUES(?,?,?,?,?,?,?,'opaque:revision')`
	for _, fact := range []struct {
		item, asset   int
		field, source string
		text, number  any
		object        string
	}{
		{1, 1, "author", "folder", "Referenced author", nil, "object-ref"},
		{1, 1, "series", "folder", "Referenced series", nil, "object-ref"},
		{1, 1, "seriesNumber", "folder", nil, 2.5, "object-ref"},
		{2, 2, "author", "file-metadata", "Unrelated author", nil, "object-other"},
		{2, 2, "series", "file-name", "Unrelated series", nil, "object-other"},
	} {
		exec(insert, fact.item, fact.asset, fact.field, fact.text, fact.number, fact.source, fact.object)
	}
	count := func(want int) {
		t.Helper()
		var got int
		if err := db.QueryRow(`SELECT count(*) FROM reading_source_values`).Scan(&got); err != nil || got != want {
			t.Fatalf("source rows=%d, want=%d: %v", got, want, err)
		}
	}
	plan := func(object string) string {
		t.Helper()
		rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT 1 FROM reading_source_values WHERE inventory_object_id=?`, object)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, detail)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(lines, " | ")
	}
	// Present and absent values must both use the keyed lookup amid other facts.
	for _, object := range []string{"object-ref", "object-other", "object-absent"} {
		if got := plan(object); !strings.Contains(got, "reading_source_values_inventory_object") {
			t.Errorf("lookup for %s is not keyed: %s", object, got)
		}
	}
	if rows, err := db.Query(`PRAGMA foreign_key_check`); err != nil {
		t.Fatal(err)
	} else {
		defer rows.Close()
		if rows.Next() {
			t.Fatal("foreign key violation before the cascade checks")
		}
	}
	count(5)
	// A parent with no children and an absent parent change nothing.
	exec(`DELETE FROM inventory_objects WHERE id='object-empty'`)
	exec(`DELETE FROM inventory_objects WHERE id='object-absent'`)
	count(5)
	// Deleting the referenced parent removes exactly its children, not the
	// unrelated facts that share the same table.
	exec(`DELETE FROM inventory_objects WHERE id='object-ref'`)
	count(2)
	var remainingObjects, remainingPublications int
	if err := db.QueryRow(`SELECT count(*) FROM inventory_objects`).Scan(&remainingObjects); err != nil || remainingObjects != 1 {
		t.Fatalf("inventory objects after cascade=%d, want=1: %v", remainingObjects, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM reading_publications`).Scan(&remainingPublications); err != nil || remainingPublications != 2 {
		t.Fatalf("reading publications after cascade=%d, want=2: %v", remainingPublications, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM reading_source_values WHERE inventory_object_id='object-other'`).Scan(&remainingObjects); err != nil || remainingObjects != 2 {
		t.Fatalf("unrelated facts after cascade=%d, want=2: %v", remainingObjects, err)
	}
}

type sqliteForeignKey struct {
	parent  string
	columns []string
}

type sqliteIndex struct {
	name      string
	partial   bool
	columns   []string
	predicate string
}

// expressionIndexColumn holds an index key position that is an expression (or
// otherwise unnamed). It is deliberately not a valid column name.
const expressionIndexColumn = "\x00expression"

func sqliteTableNames(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}

func rowidAliasColumn(t *testing.T, db *sql.DB, table string) string {
	t.Helper()
	var ddl sql.NullString
	if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if ddl.Valid && strings.Contains(strings.ToUpper(ddl.String), "WITHOUT ROWID") {
		return ""
	}
	rows, err := db.Query(`PRAGMA table_info(` + quoteSQLite(table) + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type column struct {
		name, kind string
		pk         int
	}
	var pks []column
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var dflt sql.NullString
		if err = rows.Scan(&cid, &name, &kind, &notNull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if pk > 0 {
			pks = append(pks, column{name: name, kind: kind, pk: pk})
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(pks) == 1 && strings.EqualFold(pks[0].kind, "INTEGER") {
		return pks[0].name
	}
	return ""
}

func sqliteForeignKeys(t *testing.T, db *sql.DB, table string) []sqliteForeignKey {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_list(` + quoteSQLite(table) + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var order []int
	byID := map[int]*sqliteForeignKey{}
	for rows.Next() {
		var id, seq int
		var parent, from string
		var to, onUpdate, onDelete, match sql.NullString
		if err = rows.Scan(&id, &seq, &parent, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatal(err)
		}
		if byID[id] == nil {
			byID[id] = &sqliteForeignKey{parent: parent}
			order = append(order, id)
		}
		byID[id].columns = append(byID[id].columns, from)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	fks := make([]sqliteForeignKey, 0, len(order))
	for _, id := range order {
		fks = append(fks, *byID[id])
	}
	return fks
}

func sqliteIndexes(t *testing.T, db *sql.DB, table string) []sqliteIndex {
	t.Helper()
	rows, err := db.Query(`PRAGMA index_list(` + quoteSQLite(table) + `)`)
	if err != nil {
		t.Fatal(err)
	}
	type listed struct {
		name    string
		partial bool
	}
	var listedIndexes []listed
	for rows.Next() {
		var seq, unique, partial int
		var name, origin string
		if err = rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatal(err)
		}
		listedIndexes = append(listedIndexes, listed{name: name, partial: partial != 0})
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	var indexes []sqliteIndex
	for _, item := range listedIndexes {
		index := sqliteIndex{name: item.name, partial: item.partial}
		info, err := db.Query(`PRAGMA index_info(` + quoteSQLite(item.name) + `)`)
		if err != nil {
			t.Fatal(err)
		}
		for info.Next() {
			var seqno, cid int
			var name sql.NullString
			if err = info.Scan(&seqno, &cid, &name); err != nil {
				info.Close()
				t.Fatal(err)
			}
			if name.Valid {
				index.columns = append(index.columns, name.String)
			} else {
				// Every key position must survive, even an expression key that
				// PRAGMA index_info reports with a NULL name; dropping it would
				// shift (lower(label), parent_id) down to (parent_id).
				index.columns = append(index.columns, expressionIndexColumn)
			}
		}
		if err = info.Err(); err != nil {
			info.Close()
			t.Fatal(err)
		}
		info.Close()
		if item.partial {
			var ddl sql.NullString
			if err := db.QueryRow(`SELECT sql FROM sqlite_schema WHERE type='index' AND name=?`, item.name).Scan(&ddl); err != nil {
				t.Fatal(err)
			}
			index.predicate = indexPredicate(ddl.String)
		}
		indexes = append(indexes, index)
	}
	return indexes
}

var indexWhere = regexp.MustCompile(`(?is)\bWHERE\b(.*)$`)

func indexPredicate(ddl string) string {
	match := indexWhere.FindStringSubmatch(ddl)
	if match == nil {
		return ""
	}
	return strings.Join(strings.Fields(match[1]), " ")
}

func assertIndexChosen(t *testing.T, db *sql.DB, table, column, index string) {
	t.Helper()
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT 1 FROM `+quoteSQLite(table)+` WHERE `+quoteSQLite(column)+`=?`, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, " | ")
	if !strings.Contains(joined, "INDEX "+index) {
		t.Errorf("allow-listed partial index %s is not chosen for %s.%s=?; FK lookups would scan: %s", index, table, column, joined)
	}
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func quoteSQLite(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
