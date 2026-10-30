package persistence

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func openPlannerStatsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := dbwork.OpenHandle(filepath.Join(t.TempDir(), "state.sqlite"), dbwork.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func fillPlannerStatsTable(t *testing.T, db *sql.DB, rows int) {
	t.Helper()
	ctx := context.Background()
	for _, statement := range []string{
		`CREATE TABLE songs(id INTEGER PRIMARY KEY, album INTEGER NOT NULL, genre INTEGER NOT NULL, title TEXT NOT NULL)`,
		`CREATE INDEX songs_album ON songs(album)`,
		`CREATE INDEX songs_genre_album ON songs(genre, album)`,
		`CREATE UNIQUE INDEX songs_title ON songs(title)`,
		`CREATE INDEX songs_title_folded ON songs(lower(title) COLLATE NOCASE, id DESC)`,
		`CREATE INDEX songs_genre_three ON songs(album) WHERE genre=3`,
		`CREATE TABLE sign_ins(id INTEGER PRIMARY KEY, at INTEGER NOT NULL)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	// 12 songs to an album, 40 genres, albums spread across genres. Rowids are
	// spaced three apart, as deletions leave them, so random points land in
	// gaps and several reach the same row.
	if _, err := db.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i+1<?)
		INSERT INTO songs(id,album,genre,title) SELECT 1+3*i, i/12, (i/12)%40, printf('song %d', i) FROM n`, rows); err != nil {
		t.Fatal(err)
	}
}

func plannerStat(t *testing.T, db *sql.DB, index string) []int64 {
	t.Helper()
	var stat string
	if err := db.QueryRow(`SELECT stat FROM sqlite_stat1 WHERE idx=?`, index).Scan(&stat); err != nil {
		t.Fatalf("stat1 row for %s: %v", index, err)
	}
	out := []int64{}
	for _, field := range strings.Fields(stat) {
		n, err := strconv.ParseInt(field, 10, 64)
		if err != nil {
			t.Fatalf("stat1 %s = %q", index, stat)
		}
		out = append(out, n)
	}
	return out
}

// The startup regression (23 Sep): PRAGMA optimize under the write gate held it
// for as long as counting the largest unanalysed table took, and every sign-in
// queued behind it. The refresh must leave the gate to a sign-in-like writer
// within a small bound however large the table is.
func TestPlannerStatisticsNeverHoldTheWriteGateAcrossATable(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a 400k-row table")
	}
	db := openPlannerStatsDB(t)
	const rows = 400_000
	fillPlannerStatsTable(t, db, rows)

	// Structurally: the gate can be taken at the start of every table-sized read.
	var reads, gateFree atomic.Int64
	plannerReadStep = func() {
		reads.Add(1)
		// The concurrent writer below holds it for moments at a time; a read
		// under the gate would hold it for the whole read.
		wait, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if release, err := dbwork.WriteGate().Acquire(wait, dbwork.ClassInteractive); err == nil {
			gateFree.Add(1)
			release()
		}
	}
	t.Cleanup(func() { plannerReadStep = func() {} })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var worst atomic.Int64
	var writes atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			start := time.Now()
			_, err := dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive, `INSERT INTO sign_ins(at) VALUES(?)`, start.UnixNano())
			if err != nil {
				return
			}
			if wait := time.Since(start).Nanoseconds(); wait > worst.Load() {
				worst.Store(wait)
			}
			writes.Add(1)
			time.Sleep(2 * time.Millisecond)
		}
	}()
	began := time.Now()
	if err := RefreshPlannerStatistics(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	took := time.Since(began)
	cancel()
	<-done

	if reads.Load() == 0 || gateFree.Load() != reads.Load() {
		t.Fatalf("the write gate was free for %d of %d table-sized reads", gateFree.Load(), reads.Load())
	}
	if writes.Load() == 0 {
		t.Fatal("no sign-in-like write completed while the refresh ran")
	}
	if bound := 250 * time.Millisecond; time.Duration(worst.Load()) > bound {
		t.Fatalf("a sign-in-like write waited %v for the gate during a %v refresh; the bound is %v", time.Duration(worst.Load()), took, bound)
	}
	t.Logf("refresh %v; %d writes, worst gate wait %v", took, writes.Load(), time.Duration(worst.Load()))

	album := plannerStat(t, db, "songs_album")
	if album[0] != rows || album[1] < 6 || album[1] > 24 {
		t.Fatalf("songs_album stat = %v, want %d rows about 12 to an album", album, rows)
	}
	genre := plannerStat(t, db, "songs_genre_album")
	if genre[0] != rows || genre[1] < rows/80 || genre[1] > rows/20 || genre[2] < 6 || genre[2] > 24 {
		t.Fatalf("songs_genre_album stat = %v, want %d rows, about %d to a genre and 12 to an album", genre, rows, rows/40)
	}
	if title := plannerStat(t, db, "songs_title"); title[0] != rows || title[1] != 1 {
		t.Fatalf("songs_title stat = %v, want %d rows, one to a title", title, rows)
	}
	if folded := plannerStat(t, db, "songs_title_folded"); folded[0] != rows || folded[1] != 1 || folded[2] != 1 {
		t.Fatalf("expression index stat = %v, want %d rows, one to a title", folded, rows)
	}
	var genreThree int64
	if err := db.QueryRow(`SELECT count(*) FROM songs WHERE genre=3`).Scan(&genreThree); err != nil {
		t.Fatal(err)
	}
	if partial := plannerStat(t, db, "songs_genre_three"); partial[0] != genreThree || partial[1] != 12 {
		t.Fatalf("partial index stat = %v, want %d rows, 12 to an album", partial, genreThree)
	}
	var plain string
	if err := db.QueryRow(`SELECT stat FROM sqlite_stat1 WHERE tbl='sign_ins' AND idx IS NULL`).Scan(&plain); err != nil {
		t.Fatalf("sign_ins stat1 row: %v", err)
	}

	// A second pass finds nothing stale and writes nothing.
	var before int64
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_stat1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE sqlite_stat1 SET stat=stat||' ' WHERE tbl='songs'`); err != nil {
		t.Fatal(err)
	}
	if err := RefreshPlannerStatistics(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var marked, all int64
	if err := db.QueryRow(`SELECT count(*) FILTER (WHERE stat LIKE '% '),count(*) FROM sqlite_stat1 WHERE tbl='songs'`).Scan(&marked, &all); err != nil {
		t.Fatal(err)
	}
	if marked != all || all != 5 {
		t.Fatalf("the second pass rewrote fresh statistics (%d of %d rows untouched, want 5)", marked, all)
	}
}

// Statistics 10× away from the table are refreshed, as PRAGMA optimize would.
func TestPlannerStatisticsRefreshATableThatGrewTenfold(t *testing.T) {
	db := openPlannerStatsDB(t)
	fillPlannerStatsTable(t, db, 3_000)
	if err := RefreshPlannerStatistics(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if got := plannerStat(t, db, "songs_album"); got[0] != 3_000 || got[1] != 12 {
		t.Fatalf("small table stat = %v, want exact [3000 12]", got)
	}
	if _, err := db.Exec(`WITH RECURSIVE n(i) AS (SELECT 3000 UNION ALL SELECT i+1 FROM n WHERE i+1<40000)
		INSERT INTO songs(album,genre,title) SELECT i/12, (i/12)%40, printf('more %d', i) FROM n`); err != nil {
		t.Fatal(err)
	}
	if err := RefreshPlannerStatistics(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if got := plannerStat(t, db, "songs_album"); got[0] != 40_000 {
		t.Fatalf("grown table stat = %v, want 40000 rows", got)
	}
	// The planner in this process sees them: an album lookup uses the index.
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT id FROM songs WHERE album=7`)
	if err != nil {
		t.Fatal(err)
	}
	plan := ""
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail + ";"
	}
	rows.Close()
	if !strings.Contains(plan, "songs_album") {
		t.Fatalf("album lookup plan = %q", plan)
	}
}

func TestParseIndexDefinition(t *testing.T) {
	terms, where, ok := parseIndexDefinition(`CREATE INDEX items_added_browse ON items(library_id,kind,COALESCE(added_at,'') DESC,id DESC)`)
	if !ok || where != "" || strings.Join(terms, "|") != "library_id|kind|COALESCE(added_at,'')|id" {
		t.Fatalf("expression index: %q %q %v", terms, where, ok)
	}
	terms, where, ok = parseIndexDefinition("CREATE INDEX \"odd(name\" ON t(\"a,b\" COLLATE NOCASE ASC, substr(x, 1, 2)) WHERE state IN ('a)', 'b')")
	if !ok || where != "state IN ('a)', 'b')" || strings.Join(terms, "|") != `"a,b" COLLATE NOCASE|substr(x, 1, 2)` {
		t.Fatalf("quoted terms: %q %q %v", terms, where, ok)
	}
	if _, _, ok = parseIndexDefinition(`CREATE INDEX broken ON t(a`); ok {
		t.Fatal("an unterminated column list parsed")
	}
}

// Every index of the real schema is readable by the pass, expression and
// partial indexes included: none is skipped and left to SQLite's defaults. On a
// fresh install the pass writes statistics only for tables that hold rows; an
// empty table gets none, as ANALYZE gives it none (a stored "0 rows" made
// SQLite scan the table once it filled).
func TestPlannerStatisticsCoverTheWholeSchema(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err = RefreshPlannerStatistics(ctx, db); err != nil {
		t.Fatal(err)
	}
	tables, err := plannerTables(ctx, db)
	if err != nil || len(tables) == 0 {
		t.Fatal("no tables", err)
	}
	unread, stale := []string{}, []string{}
	for _, table := range tables {
		var listed int
		if err = db.QueryRow(`SELECT count(*) FROM pragma_index_list(?)`, table).Scan(&listed); err != nil {
			t.Fatal(err)
		}
		indexes, e := plannerIndexes(ctx, db, table)
		if e != nil {
			t.Fatal(table, e)
		}
		if len(indexes) != listed {
			unread = append(unread, table)
		}
		var rows, stats int
		if err = db.QueryRow(`SELECT count(*) FROM ` + quoteIdent(table)).Scan(&rows); err != nil {
			t.Fatal(table, err)
		}
		if err = db.QueryRow(`SELECT count(*) FROM sqlite_stat1 WHERE tbl=?`, table).Scan(&stats); err != nil {
			t.Fatal(table, err)
		}
		if rows == 0 && stats != 0 {
			stale = append(stale, table)
		}
	}
	if len(unread) > 0 {
		t.Fatalf("%d tables have an index the pass cannot read: %v", len(unread), unread)
	}
	if len(stale) > 0 {
		t.Fatalf("%d empty tables were given statistics: %v", len(stale), stale)
	}
}

func TestPlannerRowidBoundsUseIndexedEndpoints(t *testing.T) {
	db := openPlannerStatsDB(t)
	if _, err := db.Exec(`CREATE TABLE probe(id INTEGER PRIMARY KEY); INSERT INTO probe VALUES(7),(50001)`); err != nil {
		t.Fatal(err)
	}
	queries := []string{
		plannerRowidBoundsSQL("probe"),
		`SELECT COALESCE((SELECT max(rowid) FROM "probe")-(SELECT min(rowid) FROM "probe")+1,0)`,
	}
	for _, query := range queries {
		rows, err := db.Query(`EXPLAIN QUERY PLAN ` + query)
		if err != nil {
			t.Fatal(err)
		}
		searches := 0
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(detail, "SCAN probe") {
				t.Fatalf("rowid endpoint scanned table: %s", detail)
			}
			if strings.Contains(detail, "SEARCH probe") {
				searches++
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if searches != 2 {
			t.Fatalf("query needs two endpoint probes, got %d: %s", searches, query)
		}
	}
	var low, high int64
	if err := db.QueryRow(queries[0]).Scan(&low, &high); err != nil || low != 7 || high != 50001 {
		t.Fatalf("sparse endpoints: %d,%d %v", low, high, err)
	}
	var span int64
	if err := db.QueryRow(queries[1]).Scan(&span); err != nil || span != 49995 {
		t.Fatalf("sparse span: %d %v", span, err)
	}
	if _, err := db.Exec(`DELETE FROM probe`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(queries[1]).Scan(&span); err != nil || span != 0 {
		t.Fatalf("empty span: %d %v", span, err)
	}
}
