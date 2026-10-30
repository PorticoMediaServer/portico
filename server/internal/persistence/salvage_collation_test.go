package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// O-090: damaged-leaf salvage where the key ordering is not BINARY.
//
// The leaf salvage path reads a table's b-tree pages itself and then seeks each
// leaf back through SQL by the separators it found. That is only exact if the
// ordering the pages were written in is the ordering SQL compares with. A
// NOCASE or RTRIM key, declared by the table (or by an index over it), changes
// that ordering, so these fixtures declare the collation in the damaged file
// itself rather than pretending it in the query layer, and damage a real page
// of a real SQLite file. Nothing here simulates an error: the page is
// overwritten and recovery has to cope.

// columnIn reads one column of a query from an ordinary database file.
func columnIn(t *testing.T, path, query string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		out = append(out, value)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// candidateColumnIn reads one column of a query from the copy being tested.
func candidateColumnIn(t *testing.T, ctx context.Context, db *dbwork.Candidate, query string) []string {
	t.Helper()
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		out = append(out, value)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

const collatedNoteFiller = "note-filler-so-rows-span-several-pages-of-the-table-and-it-has-interior-pages-"

func collatedNote(i int) string {
	return fmt.Sprintf("%04d %s", i, strings.Repeat(collatedNoteFiller, 3))
}

// nocaseName varies the case of a name whose NOCASE ordering is still by
// number, so a byte-wise comparison would seek somewhere else entirely.
func nocaseName(i int) string {
	name := fmt.Sprintf("Name-%05d", i)
	switch i % 3 {
	case 0:
		return strings.ToUpper(name)
	case 1:
		return strings.ToLower(name)
	}
	return name
}

// rtrimName gives rows trailing spaces RTRIM ignores, so the stored key is not
// the trimmed one.
func rtrimName(i int) string {
	// In RTRIM order the shortened even key precedes its tab-suffixed peer;
	// in BINARY order its stored trailing space follows the tab (0x09).
	stem := fmt.Sprintf("row-%05d", i/2)
	if i%2 == 0 {
		return stem + strings.Repeat(" ", 1+i%4)
	}
	return stem + "\t"
}

// groupName is a composite key's leading column, case-varied like nocaseName.
func groupName(i int) string {
	name := fmt.Sprintf("Group-%02d", i/30)
	if (i/30)%2 == 0 {
		return strings.ToUpper(name)
	}
	return strings.ToLower(name)
}

// nonBinaryCase is one synthetic table whose keys use a collation other than
// BINARY, with the rows to fill it and one row's key as SQL text.
type nonBinaryCase struct {
	name    string
	ddl     string
	keyExpr string
	insert  func(tx *sql.Tx) ([]string, error)
}

// insertCollated writes count rows and returns their keys in the order the
// table stores them, which is the order they are inserted in.
func insertCollated(tx *sql.Tx, statement string, count int, values func(int) []any, key func(int) string) ([]string, error) {
	prepared, err := tx.Prepare(statement)
	if err != nil {
		return nil, err
	}
	defer prepared.Close()
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		if _, err = prepared.Exec(values(i)...); err != nil {
			return nil, err
		}
		out = append(out, key(i))
	}
	return out, nil
}

// nonBinaryCases are the supported non-BINARY orderings: NOCASE and RTRIM on a
// single text key, and NOCASE as the leading column of a composite key, which
// is what makes the seek ranges below use equality on a collated column.
func nonBinaryCases() []nonBinaryCase {
	return []nonBinaryCase{
		{
			name:    "nocase",
			ddl:     `CREATE TABLE collated(name TEXT PRIMARY KEY COLLATE NOCASE,note TEXT NOT NULL) WITHOUT ROWID`,
			keyExpr: `name`,
			insert: func(tx *sql.Tx) ([]string, error) {
				return insertCollated(tx, `INSERT INTO collated(name,note) VALUES(?,?)`, 600,
					func(i int) []any { return []any{nocaseName(i), collatedNote(i)} },
					nocaseName)
			},
		},
		{
			name:    "rtrim",
			ddl:     `CREATE TABLE collated(name TEXT PRIMARY KEY COLLATE RTRIM,note TEXT NOT NULL) WITHOUT ROWID`,
			keyExpr: `name`,
			insert: func(tx *sql.Tx) ([]string, error) {
				return insertCollated(tx, `INSERT INTO collated(name,note) VALUES(?,?)`, 600,
					func(i int) []any { return []any{rtrimName(i), collatedNote(i)} },
					rtrimName)
			},
		},
		{
			name:    "composite-nocase",
			ddl:     `CREATE TABLE collated(group_name TEXT COLLATE NOCASE NOT NULL,seq INTEGER NOT NULL,note TEXT NOT NULL,PRIMARY KEY(group_name,seq)) WITHOUT ROWID`,
			keyExpr: `group_name||'/'||seq`,
			insert: func(tx *sql.Tx) ([]string, error) {
				return insertCollated(tx, `INSERT INTO collated(group_name,seq,note) VALUES(?,?,?)`, 600,
					func(i int) []any { return []any{groupName(i), int64(i % 30), collatedNote(i)} },
					func(i int) string { return fmt.Sprintf("%s/%d", groupName(i), i%30) })
			},
		},
	}
}

// A damaged leaf of a table whose keys are ordered by a non-BINARY collation:
// the copy loses exactly the rows on that page and keeps every other readable
// row, in key order, and reports the table as damaged. The damaged page is a
// real page of a real file that has been overwritten, and the collation comes
// from the file's own schema.
func TestSalvageReadsLeavesOfNonBinaryCollatedTables(t *testing.T) {
	for _, tc := range nonBinaryCases() {
		t.Run(tc.name, func(t *testing.T) {
			copyNonBinaryTable(t, tc)
		})
	}
}

func copyNonBinaryTable(t *testing.T, tc nonBinaryCase) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	freshPath := filepath.Join(dir, "fresh.sqlite")
	damagedPath := filepath.Join(dir, "damaged.sqlite")
	for _, path := range []string{freshPath, damagedPath} {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err = file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	source, err := sql.Open("sqlite", damagedPath)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := source.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var intact []string
	if _, err = tx.Exec(tc.ddl); err == nil {
		intact, err = tc.insert(tx)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = source.Close(); err != nil {
		t.Fatal(err)
	}

	// The rows the damaged page held, counted while the page is still whole.
	intactPath := filepath.Join(dir, "intact.sqlite")
	copyFileForTest(t, damagedPath, intactPath)
	page := pageOf(t, intactPath, "collated", "leaf", 3)
	lost := cellsOn(t, intactPath, page)
	if lost == 0 {
		t.Fatal("the fixture's damaged leaf holds no rows")
	}
	spoil(t, damagedPath, page)

	fresh, err := dbwork.OpenCandidate(dir, "fresh.sqlite", dbwork.CandidateOptions{ForeignKeysOff: true})
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if _, err = fresh.ExecContext(ctx, tc.ddl); err != nil {
		t.Fatal(err)
	}
	attach := (&url.URL{Scheme: "file", Path: filepath.ToSlash(damagedPath)}).String()
	if _, err = fresh.ExecContext(ctx, `ATTACH DATABASE ? AS damaged`, attach); err != nil {
		t.Fatal(err)
	}
	tables, err := salvageTables(ctx, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || tables[0].name != "collated" {
		t.Fatalf("fresh copy holds %d tables, not the fixture's one", len(tables))
	}
	whole, err := copyTable(ctx, fresh, tables[0])
	if err != nil {
		t.Fatal(err)
	}
	if whole {
		t.Fatal("a table that lost a whole leaf was reported as copied whole")
	}

	kept := candidateColumnIn(t, ctx, fresh, `SELECT `+tc.keyExpr+` FROM main.collated`)
	keptSet := make(map[string]bool, len(kept))
	for _, key := range kept {
		if keptSet[key] {
			t.Fatalf("the copy holds %q twice", key)
		}
		keptSet[key] = true
	}
	if len(kept) != len(intact)-lost {
		t.Fatalf("the copy kept %d rows; %d of %d were readable (%d on the damaged leaf)",
			len(kept), len(intact)-lost, len(intact), lost)
	}
	original := make(map[string]int, len(intact))
	for i, key := range intact {
		original[key] = i
	}
	for _, key := range kept {
		if _, ok := original[key]; !ok {
			t.Fatalf("the copy invented the row %q", key)
		}
	}
	var missing []int
	for i, key := range intact {
		if !keptSet[key] {
			missing = append(missing, i)
		}
	}
	if len(missing) != lost {
		t.Fatalf("the copy lost %d rows; the damaged leaf held %d", len(missing), lost)
	}
	for i := 1; i < len(missing); i++ {
		if missing[i] != missing[i-1]+1 {
			t.Fatalf("the copy lost rows outside the damaged leaf: %v", missing)
		}
	}
	// integrity_check covers every attached database, and the attached one is
	// the damaged fixture on purpose: detach it and check the copy alone.
	if _, err = fresh.ExecContext(ctx, `DETACH DATABASE damaged`); err != nil {
		t.Fatal(err)
	}
	if lines, err := fresh.Check(ctx, false, 1); err != nil || !dbwork.Healthy(lines) {
		t.Fatalf("salvaged copy integrity %v %v", lines, err)
	}
}

// caseLibraries writes n libraries whose names differ only in case, so the
// NOCASE index over them holds entries a byte-wise comparison would collapse.
func caseLibraries(t *testing.T, n int) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "server.sqlite")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("Library %04d", i)
		if i%2 == 0 {
			name = strings.ToUpper(name)
		}
		if _, err = dbwork.ExecWrite(context.Background(), db, dbwork.ClassInteractive,
			`INSERT INTO libraries(id,name,kind,root) VALUES(?,?,'movie',?)`,
			fmt.Sprint(i), name, fmt.Sprintf("/media/%04d/%s", i, longName)); err != nil {
			t.Fatal(err)
		}
	}
	return db, path
}

// A damaged table under a non-BINARY index: every readable row is salvaged,
// the owner is told the table lost rows rather than that everything was
// repaired, and the NOCASE index agrees with the rows it kept, including two
// libraries whose names differ only in case.
func TestSalvageReportsLossUnderANonBinaryIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	db, path := caseLibraries(t, 600)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	// One name in each case, kept apart only by the index's own collation.
	exec(`INSERT INTO libraries(id,name,kind,root) VALUES('pair-a','Duplicate Name','movie',?),('pair-b','duplicate name','movie',?)`,
		"/media/pair/a/"+longName, "/media/pair/b/"+longName)
	folded(t, db, path)
	intact := columnIn(t, path, `SELECT id FROM libraries ORDER BY rowid`)
	if len(intact) != 602 {
		t.Fatalf("fixture holds %d libraries", len(intact))
	}
	page := pageOf(t, path, "libraries", "leaf", 3)
	lost := cellsOn(t, path, page)
	if lost == 0 {
		t.Fatal("the fixture's damaged leaf holds no rows")
	}
	spoil(t, path, page)

	report, err := InspectCandidate(ctx, filepath.Dir(path), filepath.Base(path))
	if err != nil || report.Integrity != "recovered" || report.Recovery == nil {
		t.Fatalf("table damage: %+v %v", report, err)
	}
	if !report.Recovery.Salvaged || report.Recovery.IndexesRebuilt {
		t.Fatalf("a table that lost rows was reported as an index repair: %+v", report.Recovery)
	}
	if damaged := fmt.Sprint(report.Recovery.DamagedTables); damaged != "[libraries]" {
		t.Fatalf("damaged tables %s", damaged)
	}
	if report.Recovery.RemovedRows == 0 {
		t.Fatal("the notice claims no rows pointed at the libraries that were lost")
	}
	kept := columnIn(t, path, `SELECT id FROM libraries ORDER BY rowid`)
	if len(kept) != len(intact)-lost {
		t.Fatalf("salvage kept %d libraries; %d of %d were readable (%d on the damaged leaf)",
			len(kept), len(intact)-lost, len(intact), lost)
	}
	keptSet := make(map[string]bool, len(kept))
	for _, id := range kept {
		if keptSet[id] {
			t.Fatalf("salvage kept %q twice", id)
		}
		keptSet[id] = true
	}
	original := make(map[string]int, len(intact))
	for i, id := range intact {
		original[id] = i
	}
	for _, id := range kept {
		if _, ok := original[id]; !ok {
			t.Fatalf("salvage invented the library %q", id)
		}
	}
	var missing []int
	for i, id := range intact {
		if !keptSet[id] {
			missing = append(missing, i)
		}
	}
	if len(missing) != lost {
		t.Fatalf("salvage lost %d libraries; the damaged leaf held %d", len(missing), lost)
	}
	for i := 1; i < len(missing); i++ {
		if missing[i] != missing[i-1]+1 {
			t.Fatalf("salvage lost libraries outside the damaged leaf: %v", missing)
		}
	}
	// The pair is on the table's last page, not the damaged one.
	if !keptSet["pair-a"] || !keptSet["pair-b"] {
		t.Fatalf("salvage dropped a case-varied pair: %v %v", keptSet["pair-a"], keptSet["pair-b"])
	}
	if n := countIn(t, path, `SELECT count(*) FROM libraries WHERE name='DUPLICATE NAME' COLLATE NOCASE`); n != 2 {
		t.Fatalf("the NOCASE index kept %d of the two case-varied libraries", n)
	}
	if n := countIn(t, path, `SELECT count(*) FROM libraries WHERE name='library 0002' COLLATE NOCASE`); n != 1 {
		t.Fatalf("a NOCASE lookup found %d libraries named library 0002", n)
	}
	if n := countIn(t, path, `SELECT count(*) FROM pragma_foreign_key_check`); n != 0 {
		t.Fatalf("salvage left %d dangling references", n)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("salvaged database does not open: %v", err)
	}
	defer reopened.Close()
	var ok string
	if err = reopened.QueryRow(`PRAGMA integrity_check`).Scan(&ok); err != nil || ok != "ok" {
		t.Fatalf("salvaged integrity %q %v", ok, err)
	}
}

// A non-BINARY index damaged on its own: its collation keeps case-varied
// entries as separate entries, REINDEX rebuilds them from the rows, nothing is
// lost, and the notice says so rather than reporting a salvage.
func TestRestoreRebuildsANonBinaryIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	// Few enough rows that the index is a single leaf page, so writing an
	// older copy of it back leaves the b-tree's shape alone and only the
	// entries are missing. Case-varied names keep two distinct entries.
	db, path := caseLibraries(t, 40)
	for _, name := range []string{"Duplicate Name", "duplicate name"} {
		if _, err := dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive,
			`INSERT INTO libraries(id,name,kind,root) VALUES(?,?,'movie',?)`,
			"pair-"+name, name, "/media/pair/"+name+"/"+longName); err != nil {
			t.Fatal(err)
		}
	}
	folded(t, db, path)
	page := pageOf(t, path, "libraries_admin_name", "leaf", 0)
	stale := readPage(t, path, page)
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("!Added %04d", i)
		if i%2 == 0 {
			name = strings.ToUpper(name)
		}
		if _, err = dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive,
			`INSERT INTO libraries(id,name,kind,root) VALUES(?,?,'movie',?)`,
			fmt.Sprintf("added-%d", i), name, fmt.Sprintf("/media/added/%04d/%s", i, longName)); err != nil {
			t.Fatal(err)
		}
	}
	folded(t, db, path)
	// The stale page no longer lists the rows added since it was written; its
	// keys sort first in the index, so they are missing from this very page.
	overwritePage(t, path, page, stale)
	report, err := InspectCandidate(ctx, filepath.Dir(path), filepath.Base(path))
	if err != nil || report.Integrity != "recovered" || report.Recovery == nil {
		t.Fatalf("index damage: %+v %+v %v", report, report.Recovery, err)
	}
	if !report.Recovery.IndexesRebuilt || report.Recovery.Salvaged {
		t.Fatalf("a rebuilt index was reported as a salvage: %+v", report.Recovery)
	}
	if n := countIn(t, path, `SELECT count(*) FROM libraries`); n != 47 {
		t.Fatalf("index repair lost rows: %d", n)
	}
	if n := countIn(t, path, `SELECT count(*) FROM libraries WHERE name='!ADDED 0002' COLLATE NOCASE`); n != 1 {
		t.Fatalf("the rebuilt NOCASE index found %d libraries named !added 0002", n)
	}
	if n := countIn(t, path, `SELECT count(*) FROM libraries WHERE name='DUPLICATE NAME' COLLATE NOCASE`); n != 2 {
		t.Fatalf("the rebuilt NOCASE index kept %d of the two case-varied libraries", n)
	}
}
