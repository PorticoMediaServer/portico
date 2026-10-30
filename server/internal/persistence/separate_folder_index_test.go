package persistence

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// P2-59 O022 adds the eighth inventory lookup index for one separated folder:
//
//	CREATE INDEX inventory_objects_separate_folder
//	ON inventory_objects(asset_id,source_id,root_incarnation,directory_path,id)
//	WHERE retired=0;
//
// Production forces it with INDEXED BY in separateFolderObjectHeadSQL: equality
// on the first four key columns, the literal retired=0 and ORDER BY id LIMIT 1.
// It does not order or filter on availability before the limit: the caller
// checks the one raw row it gets and refuses an unavailable object instead of
// skipping it into a different row. The cost is one index entry per unretired
// inventory insert; the hot analysis/progress updates name no key column. This
// file proves the installed baseline index serves that exact literal, pins the
// literal to the production source so it cannot drift, censuses every
// production SET on inventory_objects, and measures the added entry from the
// statement's own EXPLAIN program. It makes no timing, page or WAL-frame claim.
const separateFolderIndexName = "inventory_objects_separate_folder"

var separateFolderIndexKey = []string{"asset_id", "source_id", "root_incarnation", "directory_path", "id"}

const separateFolderIndexWhere = "retired=0"

// separateFolderHeadLookup is the literal production runs, including the
// forced index. The plan and row tests require the baseline to carry the
// approved index as installed; they never create it. The static source check
// below pins this copy to separateFolderObjectHeadSQL so the two cannot drift.
const separateFolderHeadLookup = `SELECT id,revision,state,missing_since FROM inventory_objects INDEXED BY inventory_objects_separate_folder
 WHERE asset_id=?1 AND source_id=?2 AND root_incarnation=?3 AND directory_path=?4 AND retired=0 ORDER BY id LIMIT 1`

func separateFolderText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// separateFolderPredicate normalizes the partial-index WHERE clause so the
// comparison is about the literal, not its spacing.
func separateFolderPredicate(text string) string {
	return strings.ToLower(strings.ReplaceAll(separateFolderText(text), " ", ""))
}

// separateFolderIndexes returns every baseline index with this exact key and
// predicate, whatever its name.
func separateFolderIndexes(t *testing.T, db *sql.DB) []sqliteIndex {
	t.Helper()
	var matches []sqliteIndex
	for _, index := range sqliteIndexes(t, db, "inventory_objects") {
		if !index.partial || !sameStrings(index.columns, separateFolderIndexKey) {
			continue
		}
		if separateFolderPredicate(index.predicate) != separateFolderIndexWhere {
			continue
		}
		matches = append(matches, index)
	}
	return matches
}

func separateFolderDropIndex(t *testing.T, db *sql.DB) {
	t.Helper()
	matches := separateFolderIndexes(t, db)
	if len(matches) > 1 {
		names := make([]string, 0, len(matches))
		for _, index := range matches {
			names = append(names, index.name)
		}
		t.Fatalf("inventory_objects holds %d indexes with key (%s) WHERE %s: %s; the baseline must not carry a second copy",
			len(matches), strings.Join(separateFolderIndexKey, ","), separateFolderIndexWhere, strings.Join(names, ", "))
	}
	for _, index := range matches {
		if _, err := db.Exec("DROP INDEX " + quoteSQLite(index.name)); err != nil {
			t.Fatalf("drop %s in the test copy: %v", index.name, err)
		}
	}
}

// requireSeparateFolderIndex fails unless the installed baseline carries
// exactly the approved index: its name, key order and partial predicate. The
// plan and row tests use the schema as installed; they never create it.
func requireSeparateFolderIndex(t *testing.T, db *sql.DB) string {
	t.Helper()
	matches := separateFolderIndexes(t, db)
	names := make([]string, 0, len(matches))
	for _, index := range matches {
		names = append(names, index.name)
	}
	if len(matches) != 1 || matches[0].name != separateFolderIndexName {
		t.Fatalf("baseline indexes with key (%s) WHERE %s are [%s]; want exactly %s installed",
			strings.Join(separateFolderIndexKey, ","), separateFolderIndexWhere, strings.Join(names, ", "), separateFolderIndexName)
	}
	return matches[0].name
}

// createSeparateFolderIndex installs the ruled DDL into the disposable test
// copy. Only the before/after write-cost test calls it, after dropping the
// baseline copy so the same statement's program is measured both ways.
func createSeparateFolderIndex(t *testing.T, db *sql.DB) string {
	t.Helper()
	statement := "CREATE INDEX " + quoteSQLite(separateFolderIndexName) + " ON " + quoteSQLite("inventory_objects") +
		"(" + strings.Join(separateFolderIndexKey, ",") + ") WHERE " + separateFolderIndexWhere
	if _, err := db.Exec(statement); err != nil {
		t.Fatalf("create %s in the disposable test copy: %v", separateFolderIndexName, err)
	}
	installed := separateFolderIndexes(t, db)
	if len(installed) != 1 || installed[0].name != separateFolderIndexName {
		t.Fatalf("created %s but the schema reports %v", separateFolderIndexName, installed)
	}
	return installed[0].name
}

var separateFolderHeadConst = regexp.MustCompile("(?s)const separateFolderObjectHeadSQL = `([^`]*)`")

// separateFolderProductionHeadLiteral reads the one raw literal production
// runs. The test cannot import the unexported catalog constant, so it reads
// the source file directly and compares normalized text.
func separateFolderProductionHeadLiteral(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "catalog", "separate_folder_source.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read production head source %s: %v", path, err)
	}
	match := separateFolderHeadConst.FindStringSubmatch(string(source))
	if match == nil {
		t.Fatalf("%s no longer declares separateFolderObjectHeadSQL as one raw literal", path)
	}
	return match[1]
}

func separateFolderPlan(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain %s: %v", separateFolderText(query), err)
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
	if len(plan) == 0 {
		t.Fatal("plan is empty")
	}
	return plan
}

func TestSeparateFolderIndexServesTheExactFolderHeadLookup(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 2.57s")
	}
	db, err := OpenFresh(filepath.Join(t.TempDir(), "separate-folder-head.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	name := requireSeparateFolderIndex(t, db)

	plan := separateFolderPlan(t, db, separateFolderHeadLookup, "asset", "source", "incarnation", "Folder")
	joined := strings.Join(plan, " | ")
	if search := "SEARCH inventory_objects USING INDEX " + name; !strings.Contains(joined, search) {
		t.Fatalf("the folder head does not seek %s: %s", name, joined)
	}
	if strings.Contains(joined, "SCAN") {
		t.Errorf("the folder head scans instead of seeking: %s", joined)
	}
	if strings.Contains(joined, "TEMP B-TREE") {
		t.Errorf("the folder head sorts in a temp b-tree; id is not serving the order as the index tail: %s", joined)
	}
	// Each equality must be part of the index seek. A residual filter applied
	// after LIMIT 1 could consume the wrong raw row and then reject it, so the
	// plan is the proof that source, incarnation and folder are not hidden
	// post-limit predicates.
	for _, column := range separateFolderIndexKey[:4] {
		if !strings.Contains(joined, column+"=?") {
			t.Errorf("the folder head does not constrain index prefix column %s; a post-limit filter could skip an accepted row: %s", column, joined)
		}
	}
	if strings.Contains(joined, "USING COVERING INDEX") {
		t.Errorf("the plan claims a covering index, but the literal reads revision, state and missing_since, which the key does not hold: %s", joined)
	}
	if strings.Contains(joined, "inventory_objects_asset") {
		t.Errorf("the head still reads the availability-first asset index: %s", joined)
	}
	t.Logf("head seek: %s; the revision/state/missing_since columns are a table point lookup after the ordered key prefix, not a covering-index read", joined)
}

// The plan and row tests above only prove the owned literal. This check keeps
// that literal equal to the one production runs, so a production edit cannot
// leave the qualified query untested.
func TestSeparateFolderHeadLiteralMatchesProductionSource(t *testing.T) {
	owned := separateFolderText(separateFolderHeadLookup)
	production := separateFolderText(separateFolderProductionHeadLiteral(t))
	if owned != production {
		t.Fatalf("the owned head literal drifted from production:\n test: %s\n prod: %s", owned, production)
	}
	if !strings.Contains(production, "INDEXED BY "+separateFolderIndexName) {
		t.Fatalf("production head no longer forces %s: %s", separateFolderIndexName, production)
	}
	for _, column := range separateFolderIndexKey[:4] {
		if !strings.Contains(production, column+"=?") {
			t.Errorf("production head no longer constrains prefix column %s: %s", column, production)
		}
	}
	if !strings.Contains(production, separateFolderIndexWhere) {
		t.Errorf("production head no longer states the partial predicate %s: %s", separateFolderIndexWhere, production)
	}
}

type separateFolderObjectHead struct {
	id, revision, state, missing string
}

func separateFolderSeedFolderPool(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO libraries(id,name,kind,root) VALUES('lib','Library','local','/lib')`,
		`INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,generation,health,enabled)
 VALUES('src','lib','/lib/a','/lib/a','inc1',1,'ready',1)`,
		`INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,generation,health,enabled)
 VALUES('src-other','lib','/lib/b','/lib/b','inc2',1,'ready',1)`,
		`INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,available)
 VALUES(1,'asset','/lib/a/file.mkv',1,1,'','','',0,0,0,1)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("seed %s: %v", separateFolderText(statement), err)
		}
	}
}

func separateFolderSeedObject(t *testing.T, db *sql.DB, id, source, root, directory, relative string, retired int, state, missing string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,directory_path,state,missing_since,retired)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, source, "asset", root, relative, "rev-"+id, "{}", 1, 1, directory, state, missing, retired)
	if err != nil {
		t.Fatalf("seed object %s: %v", id, err)
	}
}

func separateFolderHeadRow(t *testing.T, db *sql.DB, asset, source, root, directory string) (separateFolderObjectHead, bool) {
	t.Helper()
	var head separateFolderObjectHead
	err := db.QueryRow(separateFolderHeadLookup, asset, source, root, directory).Scan(&head.id, &head.revision, &head.state, &head.missing)
	if errors.Is(err, sql.ErrNoRows) {
		return separateFolderObjectHead{}, false
	}
	if err != nil {
		t.Fatalf("read folder head: %v", err)
	}
	return head, true
}

// The head returns the first raw object by id, even when that row is
// unavailable, and it never turns a rejected row into a completed file: the
// caller refuses it. Rows that differ in source, incarnation, folder or
// retired sort before the match, so returning the match also proves those
// equalities belong to the seek rather than to a filter after LIMIT 1.
func TestSeparateFolderIndexReturnsTheFirstRawObjectRow(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 2.75s")
	}
	db, err := OpenFresh(filepath.Join(t.TempDir(), "separate-folder-row.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	requireSeparateFolderIndex(t, db)
	separateFolderSeedFolderPool(t, db)
	separateFolderSeedObject(t, db, "00-other-source", "src-other", "inc1", "Folder", "other-source.mkv", 0, "available", "")
	separateFolderSeedObject(t, db, "01-other-incarnation", "src", "inc-old", "Folder", "other-incarnation.mkv", 0, "available", "")
	separateFolderSeedObject(t, db, "02-other-directory", "src", "inc1", "Other", "other-directory.mkv", 0, "available", "")
	separateFolderSeedObject(t, db, "03-retired", "src", "inc1", "Folder", "retired.mkv", 1, "available", "")
	separateFolderSeedObject(t, db, "10-unavailable", "src", "inc1", "Folder", "unavailable.mkv", 0, "missing", "2026-01-01T00:00:00Z")
	separateFolderSeedObject(t, db, "20-available", "src", "inc1", "Folder", "available.mkv", 0, "available", "")
	separateFolderSeedObject(t, db, "04-retired-only", "src", "inc1", "RetiredOnly", "retired-only.mkv", 1, "available", "")

	head, ok := separateFolderHeadRow(t, db, "asset", "src", "inc1", "Folder")
	if !ok {
		t.Fatal("the first matching object is missing; a post-limit equality filter could have consumed a distractor")
	}
	if head.id != "10-unavailable" {
		t.Errorf("head returned %q; the first raw row by id is 10-unavailable", head.id)
	}
	if head.revision != "rev-10-unavailable" {
		t.Errorf("head revision = %q; want the revision of the returned row", head.revision)
	}
	if head.state != "missing" || head.missing == "" {
		t.Errorf("head availability = %q/%q; the unavailable first row must be returned with its state and missing time, never skipped to 20-available", head.state, head.missing)
	}

	for _, probe := range []struct{ what, source, root, directory string }{
		{"source", "src-absent", "inc1", "Folder"},
		{"root incarnation", "src", "inc-absent", "Folder"},
		{"retired literal", "src", "inc1", "RetiredOnly"},
	} {
		if row, found := separateFolderHeadRow(t, db, "asset", probe.source, probe.root, probe.directory); found {
			t.Errorf("%s probe returned %q; the %s is part of the literal head, not a filter after the limit", probe.what, row.id, probe.what)
		}
	}
}

// separateFolderObjectWrite is one complete production literal that inserts or
// assigns columns of inventory_objects.
type separateFolderObjectWrite struct {
	kind    string
	where   string
	text    string
	columns []string
}

// separateFolderIndexedColumns are the columns whose assignment adds, removes
// or replaces a gated index entry. relative_path is not a key of this index,
// but the rare scanner replacement writes it together with the key columns and
// inventory_objects_path indexes it, so it is classified with them rather than
// left uncensused.
var separateFolderIndexedColumns = []string{
	"asset_id", "source_id", "root_incarnation", "directory_path", "relative_path", "id", "retired",
}

func separateFolderObjectCensus(statements []productionStatement) (writes []separateFolderObjectWrite, problems []string) {
	for _, statement := range statements {
		text := separateFolderText(statement.text)
		for _, match := range separateFolderSQLInsert.FindAllStringSubmatchIndex(text, -1) {
			if strings.ToLower(text[match[2]:match[3]]) != "inventory_objects" {
				continue
			}
			write := separateFolderObjectWrite{kind: "insert", where: statement.where, text: text}
			if match[4] < 0 {
				problems = append(problems, fmt.Sprintf("%s: INSERT INTO inventory_objects writes without a column list: %s", statement.where, text))
			} else {
				write.columns = separateFolderSQLColumnList(text[match[4]:match[5]])
			}
			writes = append(writes, write)
		}
		for _, match := range separateFolderSQLUpdate.FindAllStringSubmatchIndex(text, -1) {
			if strings.ToLower(text[match[2]:match[3]]) != "inventory_objects" {
				continue
			}
			write := separateFolderObjectWrite{kind: "update", where: statement.where, text: text}
			write.columns = separateFolderSQLAssignedColumns(text[match[1]:])
			if len(write.columns) == 0 {
				problems = append(problems, fmt.Sprintf("%s: UPDATE inventory_objects has no readable SET list: %s", statement.where, text))
			}
			writes = append(writes, write)
		}
		for _, match := range separateFolderSQLConflict.FindAllStringSubmatchIndex(text, -1) {
			if separateFolderSQLInsertTableBefore(text, match[0]) != "inventory_objects" {
				// No production upsert names inventory_objects today. A future
				// one is censused here by its try statement, never guessed.
				continue
			}
			write := separateFolderObjectWrite{kind: "conflict update", where: statement.where, text: text}
			write.columns = separateFolderSQLAssignedColumns(text[match[1]:])
			if len(write.columns) == 0 {
				problems = append(problems, fmt.Sprintf("%s: ON CONFLICT DO UPDATE on inventory_objects has no readable SET list: %s", statement.where, text))
			}
			writes = append(writes, write)
		}
	}
	return writes, problems
}

func separateFolderAssignedIndexed(columns []string) []string {
	var assigned []string
	seen := map[string]bool{}
	for _, column := range columns {
		if seen[column] {
			continue
		}
		for _, indexed := range separateFolderIndexedColumns {
			if column == indexed {
				seen[column] = true
				assigned = append(assigned, column)
			}
		}
	}
	return assigned
}

func separateFolderSameSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	set := map[string]bool{}
	for _, column := range left {
		set[column] = true
	}
	for _, column := range right {
		if !set[column] {
			return false
		}
	}
	return true
}

// separateFolderRareMutations are the only production assignments of an
// indexed identity column. Each is pinned by file, exact statement prefix and
// the exact columns it assigns. A new assignment, a moved statement or a
// widened SET list fails the census instead of inheriting this approval.
var separateFolderRareMutations = []struct {
	what     string
	where    string
	contains string
	columns  []string
}{
	{
		what:     "replaced object retirement",
		where:    "internal/catalog/inventory.go:",
		contains: "UPDATE inventory_objects SET retired=1,state='unavailable' WHERE id=?",
		columns:  []string{"retired"},
	},
	{
		what:     "known-object rescan replacement",
		where:    "internal/catalog/inventory.go:",
		contains: "UPDATE inventory_objects SET root_incarnation=?,directory_path=?,relative_path=?",
		columns:  []string{"root_incarnation", "directory_path", "relative_path"},
	},
}

func separateFolderPinnedWrite(t *testing.T, writes []separateFolderObjectWrite, where, contains string) (separateFolderObjectWrite, bool) {
	t.Helper()
	for _, write := range writes {
		if strings.Contains(filepath.ToSlash(write.where), where) && strings.Contains(write.text, contains) {
			return write, true
		}
	}
	return separateFolderObjectWrite{}, false
}

func TestSeparateFolderIndexCensusClassifiesInventoryObjectWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 2.21s")
	}
	statements, _ := productionStatements(t)
	writes, problems := separateFolderObjectCensus(statements)
	if len(problems) != 0 {
		t.Fatalf("object-write census could not read %d write(s):\n%s", len(problems), strings.Join(problems, "\n"))
	}
	inserts, updates, conflicts, hot := 0, 0, 0, 0
	seen := make([]int, len(separateFolderRareMutations))
	for _, write := range writes {
		if write.kind == "insert" {
			inserts++
			// An insert writes one entry for the unretired object it creates.
			// The key columns without defaults must be named; directory_path
			// may keep its declared '.' default. A named retired column would
			// make the one-entry claim conditional.
			for _, column := range []string{"id", "asset_id", "source_id", "root_incarnation"} {
				if !separateFolderSQLHasColumn(write.columns, column) {
					t.Errorf("%s: INSERT INTO inventory_objects does not name key column %s: %s", write.where, column, write.text)
				}
			}
			if separateFolderSQLHasColumn(write.columns, "retired") {
				t.Errorf("%s: INSERT INTO inventory_objects names retired, so not every insert writes an index entry: %s", write.where, write.text)
			}
			continue
		}
		if write.kind == "update" {
			updates++
		} else {
			conflicts++
		}
		assigned := separateFolderAssignedIndexed(write.columns)
		if len(assigned) == 0 {
			hot++
			continue
		}
		matched := -1
		for i, pin := range separateFolderRareMutations {
			if strings.Contains(filepath.ToSlash(write.where), pin.where) && strings.Contains(write.text, pin.contains) {
				matched = i
				break
			}
		}
		if matched < 0 {
			t.Errorf("%s: %s assigns indexed column(s) %s with no pinned classification: %s", write.where, write.kind, strings.Join(assigned, ", "), write.text)
			continue
		}
		seen[matched]++
		if !separateFolderSameSet(assigned, separateFolderRareMutations[matched].columns) {
			t.Errorf("%s: %s assigns %s; the pinned %s classifies exactly %s: %s",
				write.where, write.kind, strings.Join(assigned, ", "), separateFolderRareMutations[matched].what,
				strings.Join(separateFolderRareMutations[matched].columns, ", "), write.text)
		}
	}
	if inserts == 0 || updates == 0 {
		t.Fatalf("object-write census is blind: %d INSERT, %d UPDATE statements", inserts, updates)
	}
	if hot < 10 {
		t.Fatalf("only %d inventory-object writes assign no indexed column; the hot analysis/missing path is not censused", hot)
	}
	for i, pin := range separateFolderRareMutations {
		if seen[i] == 0 {
			t.Errorf("census no longer sees the %s (%s): %s", pin.what, pin.where, pin.contains)
		}
	}

	// The hot literals this ruling protects: no key column appears in SET, so
	// SQLite maintains no gated index entry for them. Presence in SET is the
	// test; a same-value assignment still rewrites the entry.
	for _, want := range []struct{ what, where, contains string }{
		{"deferred analysis state", "internal/catalog/inventory.go:", "UPDATE inventory_objects SET analysis_state='deferred' WHERE id=? AND analysis_state<>'deferred'"},
		{"analysis revision compare-and-set", "internal/catalog/inventory.go:", "UPDATE inventory_objects SET analysis_revision=? WHERE id=? AND analysis_revision=?"},
		{"missing observation", "internal/catalog/missing.go:", "UPDATE inventory_objects SET missing_since=?,missing_observations=?,last_missing_job=?,state='missing' WHERE id=?"},
	} {
		write, found := separateFolderPinnedWrite(t, writes, want.where, want.contains)
		if !found {
			t.Errorf("census no longer sees the %s writer under %s; the hot write shape changed or the statement moved", want.what, want.where)
			continue
		}
		if assigned := separateFolderAssignedIndexed(write.columns); len(assigned) != 0 {
			t.Errorf("%s: the %s write now assigns indexed column(s) %s: %s", write.where, want.what, strings.Join(assigned, ", "), write.text)
		}
	}

	// Source health is a library_sources write, never an inventory_objects
	// index write. Count it so the scope boundary is visible evidence.
	healthWrites := 0
	for _, statement := range statements {
		text := separateFolderText(statement.text)
		for _, match := range separateFolderSQLUpdate.FindAllStringSubmatchIndex(text, -1) {
			if strings.ToLower(text[match[2]:match[3]]) != "library_sources" {
				continue
			}
			if separateFolderSQLHasColumn(separateFolderSQLAssignedColumns(text[match[1]:]), "health") {
				healthWrites++
			}
		}
	}
	if healthWrites == 0 {
		t.Error("no library_sources health update was censused; the scope boundary is blind")
	}
	rare := 0
	for _, count := range seen {
		rare += count
	}
	t.Logf("object writes: %d INSERT (one entry each), %d UPDATE, %d ON CONFLICT SET; %d hot writes assign no indexed column; %d pinned rare mutations; %d library_sources health updates are not object index writes",
		inserts, updates, conflicts, hot, rare, healthWrites)
}

func separateFolderPinnedStatement(t *testing.T, statements []productionStatement, where, contains string) string {
	t.Helper()
	found := ""
	for _, statement := range statements {
		if !strings.Contains(filepath.ToSlash(statement.where), where) {
			continue
		}
		text := separateFolderText(statement.text)
		if !strings.Contains(text, contains) {
			continue
		}
		if found != "" && found != text {
			t.Fatalf("two different production statements under %s contain %q", where, contains)
		}
		found = text
	}
	if found == "" {
		t.Fatalf("no production statement under %s contains %q", where, contains)
	}
	return found
}

// separateFolderBytecodeIndexWrites counts the statement program's own index
// inserts and deletes. Trigger bodies on inventory_objects are separate writes
// that exist before and after this index; comparing the same statement's
// program before and after isolates the added object entry.
func separateFolderBytecodeIndexWrites(t *testing.T, db *sql.DB, statement string) (inserts, deletes int) {
	t.Helper()
	args := make([]any, strings.Count(statement, "?"))
	rows, err := db.Query("EXPLAIN "+statement, args...)
	if err != nil {
		t.Fatalf("explain %s: %v", separateFolderText(statement), err)
	}
	defer rows.Close()
	for rows.Next() {
		var addr, p1, p2, p3, p5 int
		var op string
		var p4, comment any
		if err = rows.Scan(&addr, &op, &p1, &p2, &p3, &p4, &p5, &comment); err != nil {
			t.Fatal(err)
		}
		switch op {
		case "IdxInsert":
			inserts++
		case "IdxDelete":
			deletes++
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return inserts, deletes
}

func TestSeparateFolderIndexCostsOneInsertEntryAndNoHotWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("full tier: measured normal parent 4.36s")
	}
	db, err := OpenFresh(filepath.Join(t.TempDir(), "separate-folder-cost.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	statements, _ := productionStatements(t)
	insert := separateFolderPinnedStatement(t, statements, "internal/catalog/inventory.go:",
		"INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,object_identity,revision,evidence_json,size,modified_ns,seen_job,unsupported_reason,directory_path)")
	hot := separateFolderPinnedStatement(t, statements, "internal/catalog/inventory.go:",
		"UPDATE inventory_objects SET analysis_state='deferred' WHERE id=? AND analysis_state<>'deferred'")
	hotRevision := separateFolderPinnedStatement(t, statements, "internal/catalog/inventory.go:",
		"UPDATE inventory_objects SET analysis_revision=? WHERE id=? AND analysis_revision=?")

	// The test copy is disposable. Confirm the baseline carries the approved
	// index, remove only that copy, and measure the same statement's program
	// without it; it is recreated below by its exact key and predicate. Nothing
	// else in the schema is touched, and this is the only drop here.
	separateFolderDropCheck := requireSeparateFolderIndex(t, db)
	baseline := len(sqliteIndexes(t, db, "inventory_objects"))
	separateFolderDropIndex(t, db)
	without := len(sqliteIndexes(t, db, "inventory_objects"))
	insertBefore, insertDeletesBefore := separateFolderBytecodeIndexWrites(t, db, insert)
	hotBefore, hotDeletesBefore := separateFolderBytecodeIndexWrites(t, db, hot)
	revisionBefore, revisionDeletesBefore := separateFolderBytecodeIndexWrites(t, db, hotRevision)

	name := createSeparateFolderIndex(t, db)
	if name != separateFolderDropCheck {
		t.Fatalf("recreated %s; the baseline carried %s", name, separateFolderDropCheck)
	}
	with := len(sqliteIndexes(t, db, "inventory_objects"))
	if with != without+1 {
		t.Fatalf("%s changed the schema index count from %d to %d; the gated index must add exactly one entry to the list (baseline list was %d)", name, without, with, baseline)
	}
	insertAfter, insertDeletesAfter := separateFolderBytecodeIndexWrites(t, db, insert)
	if insertAfter != insertBefore+1 || insertDeletesAfter != insertDeletesBefore {
		t.Errorf("one unretired insert: IdxInsert %d->%d, IdxDelete %d->%d; the gated index must add exactly one entry and no delete", insertBefore, insertAfter, insertDeletesBefore, insertDeletesAfter)
	}
	hotAfter, hotDeletesAfter := separateFolderBytecodeIndexWrites(t, db, hot)
	if hotAfter != hotBefore || hotDeletesAfter != hotDeletesBefore {
		t.Errorf("the deferred-analysis update names no key column but its program changed: IdxInsert %d->%d, IdxDelete %d->%d", hotBefore, hotAfter, hotDeletesBefore, hotDeletesAfter)
	}
	revisionAfter, revisionDeletesAfter := separateFolderBytecodeIndexWrites(t, db, hotRevision)
	if revisionAfter != revisionBefore || revisionDeletesAfter != revisionDeletesBefore {
		t.Errorf("the analysis-revision update names no key column but its program changed: IdxInsert %d->%d, IdxDelete %d->%d", revisionBefore, revisionAfter, revisionDeletesBefore, revisionDeletesAfter)
	}
	t.Logf("schema indexes on inventory_objects: %d without %s, %d with it; one INSERT: IdxInsert %d->%d (+1 entry), IdxDelete %d->%d; hot analysis_state UPDATE: IdxInsert %d->%d, IdxDelete %d->%d; hot analysis_revision UPDATE: IdxInsert %d->%d, IdxDelete %d->%d",
		without, name, with, insertBefore, insertAfter, insertDeletesBefore, insertDeletesAfter,
		hotBefore, hotAfter, hotDeletesBefore, hotDeletesAfter, revisionBefore, revisionAfter, revisionDeletesBefore, revisionDeletesAfter)
}

// SQL census parsing is shared with the retained-state index proof; keep its
// complete assignment parsing while this regression is composed independently.
var (
	separateFolderSQLInsert   = regexp.MustCompile(`(?is)\bINSERT\s+(?:OR\s+(?:ABORT|FAIL|IGNORE|REPLACE|ROLLBACK)\s+)?INTO\s+([A-Za-z_][A-Za-z0-9_]*)\s*(?:\(([^()]*)\))?`)
	separateFolderSQLUpdate   = regexp.MustCompile(`(?is)\bUPDATE\s+([A-Za-z_][A-Za-z0-9_]*)(?:\s+(?:AS\s+)?[A-Za-z_][A-Za-z0-9_]*)?\s+SET\s+`)
	separateFolderSQLConflict = regexp.MustCompile(`(?is)\bON\s+CONFLICT\s*(?:\([^()]*\))?\s+DO\s+UPDATE\s+SET\s+`)
)

func separateFolderSQLInsertTableBefore(text string, before int) string {
	table := ""
	for _, match := range separateFolderSQLInsert.FindAllStringSubmatchIndex(text, -1) {
		if match[0] >= before {
			break
		}
		table = strings.ToLower(text[match[2]:match[3]])
	}
	return table
}

func separateFolderSQLColumnList(list string) []string {
	var columns []string
	for _, column := range strings.Split(list, ",") {
		column = strings.ToLower(strings.Trim(strings.TrimSpace(column), "\"`[]"))
		if column != "" {
			columns = append(columns, column)
		}
	}
	return columns
}

// separateFolderSQLAssignedColumns reads the left-hand sides of a SET list up to the
// first top-level WHERE or RETURNING. It deliberately discards the right-hand
// side, so item_id=item_id and item_id=excluded.item_id both count.
func separateFolderSQLAssignedColumns(text string) []string {
	text = separateFolderSQLUntil(text, "WHERE", "RETURNING")
	var columns []string
	for _, part := range separateFolderSQLSplit(text) {
		part = strings.TrimSpace(part)
		equals := strings.Index(part, "=")
		if equals < 0 {
			continue
		}
		left := strings.TrimSpace(part[:equals])
		if dot := strings.LastIndex(left, "."); dot >= 0 {
			left = left[dot+1:]
		}
		left = strings.ToLower(strings.Trim(strings.TrimSpace(left), "\"`[]"))
		if left != "" {
			columns = append(columns, left)
		}
	}
	return columns
}

// separateFolderSQLUntil cuts at the first keyword outside parentheses and quoted
// literals, so a subquery's WHERE cannot end the assignment list early.
func separateFolderSQLUntil(text string, keywords ...string) string {
	depth := 0
	var quote byte
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
			continue
		case '(':
			depth++
			continue
		case ')':
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 {
			continue
		}
		for _, keyword := range keywords {
			end := i + len(keyword)
			if end > len(text) || !strings.EqualFold(text[i:end], keyword) {
				continue
			}
			if i > 0 && separateFolderSQLWordByte(text[i-1]) {
				continue
			}
			if end < len(text) && separateFolderSQLWordByte(text[end]) {
				continue
			}
			return text[:i]
		}
	}
	return text
}

func separateFolderSQLWordByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func separateFolderSQLSplit(text string) []string {
	var parts []string
	start, depth := 0, 0
	var quote byte
	for i := 0; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, text[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, text[start:])
}

func separateFolderSQLHasColumn(columns []string, want string) bool {
	for _, column := range columns {
		if column == want {
			return true
		}
	}
	return false
}
