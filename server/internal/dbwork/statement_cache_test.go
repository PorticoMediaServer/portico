package dbwork

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func statementCacheDB(t *testing.T) (*sql.DB, *sql.Conn) {
	t.Helper()
	db, err := sql.Open(DriverName, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); db.Close() })
	return db, conn
}
func cacheEntryFor(t *testing.T, conn *sql.Conn, query string) *statementEntry {
	t.Helper()
	var entry *statementEntry
	if err := conn.Raw(func(raw any) error {
		c := raw.(*observedConn)
		c.statements.mu.Lock()
		defer c.statements.mu.Unlock()
		entry = c.statements.entries[query]
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestStatementCacheReusesBytecodeNotBindingsOrResults(t *testing.T) {
	_, conn := statementCacheDB(t)
	ctx, cost := Measure(context.Background())
	const query = `SELECT ? || ':' || ?`
	var first *statementEntry
	for i := range 5 {
		var actual string
		if err := conn.QueryRowContext(ctx, query, fmt.Sprint(i), "different").Scan(&actual); err != nil {
			t.Fatal(err)
		}
		if actual != fmt.Sprintf("%d:different", i) {
			t.Fatalf("stale binding: %s", actual)
		}
		entry := cacheEntryFor(t, conn, query)
		if entry == nil || entry.leased {
			t.Fatal("query did not return an idle bytecode handle")
		}
		if first == nil {
			first = entry
		} else if entry != first {
			t.Fatal("statement was reparsed")
		}
	}
	if c := cost(); c.Statements != 5 || c.Acquisitions != 5 {
		t.Fatalf("instrumentation changed: %+v", c)
	}
}

func TestStatementCacheNestedIdenticalRowsUseDistinctHandles(t *testing.T) {
	_, conn := statementCacheDB(t)
	ctx := context.Background()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	const query = `SELECT ? UNION ALL SELECT ?`
	a, err := tx.QueryContext(ctx, query, "a1", "a2")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := tx.QueryContext(ctx, query, "b1", "b2")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, rows := range []*sql.Rows{a, b} {
		for i := 1; i <= 2; i++ {
			if !rows.Next() {
				t.Fatal("active iterator was reset by another reader", rows.Err())
			}
			var value string
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			prefix := "a"
			if rows == b {
				prefix = "b"
			}
			if value != fmt.Sprintf("%s%d", prefix, i) {
				t.Fatalf("wrong active binding: %s", value)
			}
		}
		rows.Close()
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if e := cacheEntryFor(t, conn, query); e == nil || e.leased {
		t.Fatal("cached iterator still leased")
	}
}

func TestStatementCacheCanceledAndFailedQueriesAreSafeToReuse(t *testing.T) {
	_, conn := statementCacheDB(t)
	const query = `SELECT ?`
	var value string
	if err := conn.QueryRowContext(context.Background(), query, "before").Scan(&value); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := conn.QueryRowContext(ctx, query, "canceled").Scan(&value); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	// Failed binding can leave partially bound driver parameters; never retain it.
	if err := conn.QueryRowContext(context.Background(), query).Scan(&value); err == nil {
		t.Fatal("missing binding succeeded")
	}
	if e := cacheEntryFor(t, conn, query); e != nil {
		t.Fatal("failed VM retained")
	}
	if err := conn.QueryRowContext(context.Background(), query, "after").Scan(&value); err != nil || value != "after" {
		t.Fatalf("after error: %q %v", value, err)
	}
	const expensive = `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x < ?) SELECT sum(x) FROM n`
	deadline, stop := context.WithTimeout(context.Background(), time.Millisecond)
	defer stop()
	var sum int64
	if err := conn.QueryRowContext(deadline, expensive, 50000).Scan(&sum); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("in-flight cancellation: %v", err)
	}
	if e := cacheEntryFor(t, conn, expensive); e != nil {
		t.Fatal("interrupted VM retained")
	}
	if err := conn.QueryRowContext(context.Background(), expensive, 3).Scan(&sum); err != nil || sum != 6 {
		t.Fatalf("after interrupted query: %d %v", sum, err)
	}
}

func TestStatementCacheSchemaConfigurationRollbackAndAuthority(t *testing.T) {
	_, conn := statementCacheDB(t)
	ctx := context.Background()
	for _, query := range []string{`CREATE TABLE accounts(id INTEGER PRIMARY KEY, enabled INTEGER)`, `INSERT INTO accounts(id,enabled) VALUES(1,1)`} {
		if _, err := conn.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	const selectAccount = `SELECT enabled FROM accounts WHERE id=?`
	var enabled int
	if err := conn.QueryRowContext(ctx, selectAccount, 1).Scan(&enabled); err != nil || enabled != 1 {
		t.Fatal(err)
	}
	before := AuthorityGeneration()
	if _, err := conn.ExecContext(ctx, `UPDATE accounts SET enabled=? WHERE id=?`, 0, 1); err != nil {
		t.Fatal(err)
	}
	if AuthorityGeneration() <= before {
		t.Fatal("cached write bypassed authority fencing")
	}
	if err := conn.QueryRowContext(ctx, selectAccount, 1).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("cached authority result: %d %v", enabled, err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE accounts SET enabled=? WHERE id=?`, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(ctx, selectAccount, 1).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("rollback leaked: %d %v", enabled, err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	if cacheEntryFor(t, conn, selectAccount) != nil {
		t.Fatal("configuration did not invalidate")
	}
	if _, err := conn.ExecContext(ctx, `UPDATE accounts SET enabled=? WHERE id=?`, 1, 1); err == nil {
		t.Fatal("prepared write bypassed query_only")
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA query_only=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `ALTER TABLE accounts ADD COLUMN label TEXT`); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(ctx, selectAccount, 1).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("schema invalidation: %d %v", enabled, err)
	}
}

func TestStatementCacheOtherConnectionSchemaChangesAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.sqlite")
	db, err := sql.Open(DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Exec(`CREATE TABLE items(value TEXT)`); err != nil {
		t.Fatal(err)
	}
	const query = `SELECT * FROM items`
	rows, err := conn.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if _, err := other.Exec(`ALTER TABLE items ADD COLUMN second INTEGER`); err != nil {
		t.Fatal(err)
	}
	rows, err = conn.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	columns, err := rows.Columns()
	rows.Close()
	if err != nil || len(columns) != 2 {
		t.Fatalf("cross-connection schema stayed stale: %v %v", columns, err)
	}
	// Closing the physical pool must finalize all retained statements.
	var retained *observedConn
	if err := conn.Raw(func(raw any) error { retained = raw.(*observedConn); return nil }); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	db.Close()
	if !retained.statements.closed || len(retained.statements.entries) != 0 || retained.statements.bytes != 0 {
		t.Fatal("pool close left retained resources")
	}
	reopened, err := sql.Open(DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows, err = reopened.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
}

func TestStatementCacheBoundsAndBypass(t *testing.T) {
	_, conn := statementCacheDB(t)
	conn.Raw(func(raw any) error { raw.(*observedConn).statements.budget = statementCacheBudget; return nil })
	ctx := context.Background()
	for i := range statementCacheEntries + 72 {
		query := fmt.Sprintf("SELECT ? AS column%d", i)
		var value int
		if err := conn.QueryRowContext(ctx, query, i).Scan(&value); err != nil || value != i {
			t.Fatal(err)
		}
	}
	conn.Raw(func(raw any) error {
		c := raw.(*observedConn)
		if c.statements.lru.Len() > statementCacheEntries || c.statements.bytes > statementCacheBudget {
			t.Fatal("cache exceeded bounds")
		}
		return nil
	})
	for _, q := range []string{`SELECT 1; SELECT 2`, `PRAGMA user_version`, `CREATE TEMP TABLE scratch(x)`, `SELECT * FROM temp.scratch`, "SELECT 1 /*" + strings.Repeat("x", statementCacheBudget) + "*/"} {
		rows, err := conn.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if cacheEntryFor(t, conn, q) != nil {
			t.Fatalf("retained excluded SQL: %.50s", q)
		}
	}
}

func BenchmarkStatementCache(b *testing.B) {
	base, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer base.Close()
	const complexQuery = `WITH permitted AS (SELECT ? AS library_id UNION ALL SELECT ?),
 candidate AS (SELECT library_id, ? AS profile_id FROM permitted),
 ranked AS (SELECT library_id, profile_id, CASE WHEN library_id=? THEN 1 ELSE 2 END AS priority FROM candidate)
 SELECT library_id,profile_id,priority FROM ranked WHERE profile_id=? ORDER BY priority, library_id`
	queries := map[string]string{"Point": "SELECT ?", "ScopeCTE": complexQuery}
	for name, query := range queries {
		for _, cached := range []bool{false, true} {
			mode := "Uncached"
			if cached {
				mode = "Cached"
			}
			b.Run(name+"/"+mode, func(b *testing.B) {
				inner, err := base.Driver().Open(":memory:")
				if err != nil {
					b.Fatal(err)
				}
				conn := &observedConn{inner: inner}
				defer conn.Close()
				args := []driver.NamedValue{{Ordinal: 1, Value: int64(1)}}
				if name == "ScopeCTE" {
					args = []driver.NamedValue{{Ordinal: 1, Value: int64(1)}, {Ordinal: 2, Value: int64(2)}, {Ordinal: 3, Value: "profile"}, {Ordinal: 4, Value: int64(1)}, {Ordinal: 5, Value: "profile"}}
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					var rows driver.Rows
					var release func(error) error
					var err error
					if cached {
						rows, release, err = conn.cachedQuery(context.Background(), query, args)
					} else {
						rows, err = inner.(driver.QueryerContext).QueryContext(context.Background(), query, args)
					}
					if err != nil {
						b.Fatal(err)
					}
					dest := make([]driver.Value, len(rows.Columns()))
					for {
						err = rows.Next(dest)
						if err == io.EOF {
							break
						}
						if err != nil {
							b.Fatal(err)
						}
					}
					err = rows.Close()
					if release != nil {
						if closeErr := release(err); err == nil {
							err = closeErr
						}
					}
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func TestStatementCacheCancellationAfterRowsReturned(t *testing.T) {
	_, conn := statementCacheDB(t)
	const query = `SELECT ? UNION ALL SELECT ?`
	for i := range 50 {
		ctx, cancel := context.WithCancel(context.Background())
		rows, err := conn.QueryContext(ctx, query, i, i+1)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { cancel(); close(done) }()
		rows.Next()
		<-done
		rows.Close()
		if e := cacheEntryFor(t, conn, query); e != nil {
			t.Fatal("canceled reader's VM remained reusable")
		}
		var value int
		if err := conn.QueryRowContext(context.Background(), query, i+2, i+3).Scan(&value); err != nil || value != i+2 {
			t.Fatalf("after canceled iterator: %d %v", value, err)
		}
	}
}

func TestStatementCacheSnapshotMutationStillRejectedOnReuse(t *testing.T) {
	db := testDB(t)
	db.SetMaxOpenConns(1)
	// The second snapshot executes a VM left by the first rolled-back mutation.
	for range 2 {
		snapshot, err := BeginSnapshot(context.Background(), db)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = snapshot.Tx().ExecContext(context.Background(), `INSERT INTO rows(value) VALUES(?)`, "forbidden"); err != nil {
			snapshot.Rollback()
			t.Fatal(err)
		}
		if err = snapshot.Commit(); !errors.Is(err, ErrSnapshotWrite) {
			t.Fatalf("cached mutation escaped snapshot guard: %v", err)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM rows`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("snapshot mutation persisted: %d %v", count, err)
	}
}

func TestStatementCacheSchemaRollbackRepreparesAndExplicitDDLInvalidates(t *testing.T) {
	_, conn := statementCacheDB(t)
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, `CREATE TABLE items(value TEXT)`); err != nil {
		t.Fatal(err)
	}
	const query = `SELECT * FROM items`
	columns := func(want int) {
		t.Helper()
		rows, err := conn.QueryContext(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		c, err := rows.Columns()
		rows.Close()
		if err != nil || len(c) != want {
			t.Fatalf("schema columns: %v %v", c, err)
		}
	}
	columns(1)
	ddl, err := conn.PrepareContext(ctx, `ALTER TABLE items ADD COLUMN second INTEGER`)
	if err != nil {
		t.Fatal(err)
	}
	defer ddl.Close()
	if _, err := ddl.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	if cacheEntryFor(t, conn, query) != nil {
		t.Fatal("explicit prepared DDL did not invalidate")
	}
	columns(2)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE items ADD COLUMN third INTEGER`); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	c, err := rows.Columns()
	rows.Close()
	if err != nil || len(c) != 3 {
		tx.Rollback()
		t.Fatal(c, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	columns(2)
}

func TestStatementCacheExecConstraintErrorAndEarlyClose(t *testing.T) {
	_, conn := statementCacheDB(t)
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, `CREATE TABLE items(value INTEGER UNIQUE)`); err != nil {
		t.Fatal(err)
	}
	const insert = `INSERT INTO items VALUES(?)`
	if _, err := conn.ExecContext(ctx, insert, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, insert, 1); err == nil {
		t.Fatal("unique constraint violation succeeded")
	}
	if cacheEntryFor(t, conn, insert) != nil {
		t.Fatal("errored write VM retained")
	}
	if _, err := conn.ExecContext(ctx, insert, 2); err != nil {
		t.Fatal("write after constraint error", err)
	}
	const query = `SELECT ? UNION ALL SELECT ? UNION ALL SELECT ?`
	rows, err := conn.QueryContext(ctx, query, "old1", "old2", "old3")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	// Close while additional rows remain: reset must finish before lease release.
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	entry := cacheEntryFor(t, conn, query)
	if entry == nil || entry.leased {
		t.Fatal("early close did not return an idle handle")
	}
	var value string
	if err := conn.QueryRowContext(ctx, query, "new1", "new2", "new3").Scan(&value); err != nil || value != "new1" {
		t.Fatalf("early close reused stale state: %s %v", value, err)
	}
	if cacheEntryFor(t, conn, query) != entry {
		t.Fatal("early close unnecessarily discarded healthy bytecode")
	}
}

type countedCacheStmt struct {
	driver.Stmt
	closes int
}

func (s *countedCacheStmt) Close() error { s.closes++; return s.Stmt.Close() }

type countedCacheConn struct {
	driver.Conn
	statements []*countedCacheStmt
}

func (c *countedCacheConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	inner, err := c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
	if err != nil {
		return nil, err
	}
	stmt := &countedCacheStmt{Stmt: inner}
	c.statements = append(c.statements, stmt)
	return stmt, nil
}
func TestStatementCacheRetirementAndEvictionCloseEachHandleOnce(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	inner, err := db.Driver().Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	conn := &countedCacheConn{Conn: inner}
	cache := statementCache{budget: statementCacheBudget}
	query := func(n int) string { return fmt.Sprintf("SELECT %d /*%s*/", n, strings.Repeat("x", 40000)) }
	_, release, err := cache.lease(context.Background(), conn, query(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := release(nil); err != nil {
		t.Fatal(err)
	}
	_, secondRelease, err := cache.lease(context.Background(), conn, query(2))
	if err != nil {
		t.Fatal(err)
	}
	if conn.statements[0].closes != 1 {
		t.Fatal("idle LRU eviction did not finalize")
	}
	// Invalidation/close cannot finalize a leased VM. Its rows still own it.
	cache.invalidate()
	cache.close()
	if conn.statements[1].closes != 0 {
		t.Fatal("cache close finalized an active iterator")
	}
	if err := secondRelease(nil); err != nil {
		t.Fatal(err)
	}
	secondRelease(nil)
	if conn.statements[1].closes != 1 {
		t.Fatal("retired active VM not finalized exactly once")
	}
	if _, _, err := cache.lease(context.Background(), conn, query(3)); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal("closed cache accepted new work", err)
	}
}

func TestStatementCacheSQLBoundariesRespectSQLiteQuotesAndComments(t *testing.T) {
	cases := []struct {
		query              string
		retain, invalidate bool
	}{
		{`SELECT ';', 'it''s; safe', 'temp.'`, true, false},
		{"SELECT 1 -- comment ; DROP TABLE private\n", true, false},
		{`/* leading ; */ WITH x(v) AS (SELECT ?) SELECT v FROM x; /* trailing ; */`, true, false},
		{"WITH x(v) AS (SELECT ?) -- scoped selection; preserve all rows\n SELECT v FROM x", true, false},
		{"SELECT 1 AS \"quoted;identifier\", 2 AS `tick;identifier`, 3 AS [bracket;identifier]", true, false},
		{`SELECT 1 AS "escaped"";identifier"`, true, false},
		{`SELECT ';'; UPDATE accounts SET enabled=0`, false, true},
		{"SELECT 1; /* comment ; */ PRAGMA query_only=OFF", false, true},
		{`SELECT 'unclosed`, false, true},
		{`SELECT * FROM "temp".scratch`, false, true},
		{`SELECT * FROM temp.scratch`, false, true},
		{`PRAGMA query_only=ON`, false, true},
		{`ALTER TABLE items ADD COLUMN label TEXT`, false, true},
		{`SAVEPOINT inventory_page`, false, false},
		{`RELEASE inventory_page`, false, false},
		{`RELEASE SAVEPOINT inventory_page`, false, false},
		{`SAVEPOINT temp`, false, false},
		{`SAVEPOINT "temp"`, false, false},
		{`SAVEPOINT "name;with""quote"; -- trailing`, false, false},
		{`RELEASE /* boundary */ SAVEPOINT [named point];`, false, false},
		{`SAVEPOINT 'quoted name'`, false, false},
		{`SAVEPOINT café`, false, false},
		{`ROLLBACK TO inventory_page`, false, true},
		{`ROLLBACK TO SAVEPOINT inventory_page`, false, true},
		{`SAVEPOINT`, false, true},
		{`RELEASE SAVEPOINT`, false, true},
		{`SAVEPOINT name extra`, false, true},
		{`'prefix' SAVEPOINT`, false, true},
		{`RELEASE name extra`, false, true},
		{`SAVEPOINT name; RELEASE name`, false, true},
		{`RELEASE name; PRAGMA query_only=OFF`, false, true},
		{`SAVEPOINT "unclosed`, false, true},
		{`EXPLAIN QUERY PLAN SELECT 1`, false, false},
		{"SELECT 1 /*" + strings.Repeat("x", statementCacheBudget) + "*/", false, false},
	}
	for i, test := range cases {
		shape := describeStatement(test.query)
		if shape.retain() != test.retain || shape.invalidates() != test.invalidate {
			t.Fatalf("case%d retain=%v invalidate=%v", i, shape.retain(), shape.invalidates())
		}
	}
}

// The inventory page's savepoint must not evict hot statements. DDL still
// invalidates immediately, and rolling that DDL back invalidates any bytecode
// prepared in the temporary schema, even before the savepoint is released.
func TestStatementCacheSavepointDDLThenRollbackToInvalidates(t *testing.T) {
	_, conn := statementCacheDB(t)
	ctx := context.Background()
	exec := func(query string) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE items(value TEXT)`)
	exec(`INSERT INTO items VALUES('original')`)
	const query = `SELECT * FROM items`
	columns := func(want int) *statementEntry {
		t.Helper()
		rows, err := conn.QueryContext(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		rows.Close()
		if err != nil || len(columns) != want {
			t.Fatalf("schema columns: %v %v, want %d", columns, err, want)
		}
		entry := cacheEntryFor(t, conn, query)
		if entry == nil || entry.leased {
			t.Fatal("schema read did not retain idle bytecode")
		}
		return entry
	}
	original := columns(1)
	exec(`SAVEPOINT inventory_page`)
	if cacheEntryFor(t, conn, query) != original {
		t.Fatal("SAVEPOINT evicted the hot read")
	}
	exec(`ALTER TABLE items ADD COLUMN transient TEXT`)
	if cacheEntryFor(t, conn, query) != nil {
		t.Fatal("DDL inside a savepoint did not invalidate")
	}
	duringDDL := columns(2)
	exec(`ROLLBACK TO inventory_page`)
	if cacheEntryFor(t, conn, query) != nil || !duringDDL.retired {
		t.Fatal("ROLLBACK TO retained bytecode from the rolled-back schema")
	}
	restored := columns(1)
	if restored == duringDDL {
		t.Fatal("rolled-back schema bytecode was reused")
	}
	exec(`RELEASE SAVEPOINT inventory_page`)
	if cacheEntryFor(t, conn, query) != restored {
		t.Fatal("RELEASE evicted the restored-schema read")
	}
	if after := columns(1); after != restored {
		t.Fatal("restored schema stopped reusing valid bytecode")
	}
}

func TestStatementCacheHomeShapedCommentsDoNotFlushHotStatements(t *testing.T) {
	_, conn := statementCacheDB(t)
	ctx := context.Background()
	const hot = `SELECT ?`
	const home = "/* Home composition */ WITH visible(value) AS (SELECT ?) -- one scope; all categories\n SELECT value FROM visible; -- trailing terminator\n"
	var value string
	if err := conn.QueryRowContext(ctx, hot, "initial").Scan(&value); err != nil {
		t.Fatal(err)
	}
	initial := cacheEntryFor(t, conn, hot)
	for range 3 {
		if err := conn.QueryRowContext(ctx, home, "current").Scan(&value); err != nil || value != "current" {
			t.Fatal(err)
		}
		if cacheEntryFor(t, conn, hot) != initial {
			t.Fatal("ordinary Home SQL flushed unrelated hot bytecode")
		}
	}
	homeEntry := cacheEntryFor(t, conn, home)
	if homeEntry == nil {
		t.Fatal("commented/terminated Home statement not retained")
	}
	// Oversized ordinary SQL is a retention bypass, not an environment change.
	oversized := "SELECT ? /*" + strings.Repeat("x", statementCacheBudget) + "*/"
	if err := conn.QueryRowContext(ctx, oversized, "large").Scan(&value); err != nil || value != "large" {
		t.Fatal(err)
	}
	if cacheEntryFor(t, conn, home) != homeEntry || cacheEntryFor(t, conn, hot) != initial {
		t.Fatal("oversized ordinary SQL invalidated existing bytecode")
	}
	if cacheEntryFor(t, conn, oversized) != nil {
		t.Fatal("oversized raw query text retained")
	}
	// Configuration changes still invalidate both ordinary retained handles.
	if _, err := conn.ExecContext(ctx, `PRAGMA query_only=ON`); err != nil {
		t.Fatal(err)
	}
	if cacheEntryFor(t, conn, home) != nil || cacheEntryFor(t, conn, hot) != nil {
		t.Fatal("configuration failed to invalidate retained handles")
	}
}

func TestStatementBudgetTracksPhysicalOrContainerMemory(t *testing.T) {
	for _, tc := range []struct {
		memory int64
		want   int
	}{{0, 512 << 10}, {-1, 512 << 10}, {64 << 20, 512 << 10}, {256 << 20, 512 << 10}, {512 << 20, 1 << 20}, {1 << 30, 2 << 20}, {2 << 30, 4 << 20}, {64 << 30, 4 << 20}} {
		if got := statementBudgetForMemory(tc.memory); got != tc.want {
			t.Fatalf("memory%d budget%d want%d", tc.memory, got, tc.want)
		}
	}
}

type memoryCacheStmt struct {
	*countedCacheStmt
	memory int64
}

func (s *memoryCacheStmt) StatementMemoryBytes() int64 { return s.memory }

type memoryCacheConn struct {
	driver.Conn
	memory     int64
	statements []*memoryCacheStmt
}

func (c *memoryCacheConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	inner, err := c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	s := &memoryCacheStmt{countedCacheStmt: &countedCacheStmt{Stmt: inner}, memory: c.memory}
	c.statements = append(c.statements, s)
	return s, nil
}

func TestStatementCacheMeasuredGrowthRetiresWithoutClosingActiveRows(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	inner, err := db.Driver().Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	conn := &memoryCacheConn{Conn: inner, memory: 4096}
	cache := statementCache{budget: 32 << 10}
	_, first, err := cache.lease(context.Background(), conn, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := cache.lease(context.Background(), conn, "SELECT 2")
	if err != nil {
		t.Fatal(err)
	}
	conn.statements[1].memory = 30 << 10
	if err := second(nil); err != nil {
		t.Fatal(err)
	}
	if conn.statements[0].closes != 0 || conn.statements[1].closes != 1 || cache.bytes > cache.budget {
		t.Fatal("growth evicted an active VM or escaped budget")
	}
	first(nil)
	conn.memory = 64 << 10
	_, large, err := cache.lease(context.Background(), conn, "SELECT 3")
	if err != nil {
		t.Fatal(err)
	}
	if cache.entries["SELECT 1"] == nil || cache.entries["SELECT 3"] != nil {
		t.Fatal("oversized preparation flushed unrelated idle VM")
	}
	large(nil)
	large(nil)
	if conn.statements[2].closes != 1 {
		t.Fatal("oversized VM closure was not exactly once")
	}
	cache.close()
	if conn.statements[0].closes != 1 {
		t.Fatal("idle measured VM leaked")
	}
}

func TestStatementCacheNativeAccountingClearsBindingsAndRefreshesSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-memory.sqlite")
	db, err := sql.Open(DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.Exec("CREATE TABLE source(x INTEGER)"); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	const query = "SELECT * FROM source"
	rows, err := conn.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	entry := cacheEntryFor(t, conn, query)
	if entry == nil {
		t.Fatal("native VM not retained")
	}
	oldSize := entry.size
	for i := 0; i < 24; i++ {
		if _, err := other.Exec(fmt.Sprintf("ALTER TABLE source ADD COLUMN c%d TEXT", i)); err != nil {
			t.Fatal(err)
		}
	}
	rows, err = conn.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	columns, err := rows.Columns()
	if err != nil || len(columns) != 25 {
		t.Fatal(columns, err)
	}
	rows.Close()
	entry = cacheEntryFor(t, conn, query)
	if entry == nil || entry.size <= oldSize || entry.size != retainedStatementSize(entry.stmt, query) {
		t.Fatal("schema reprepare footprint was not refreshed")
	}
	const bound = "SELECT ?"
	var text string
	if err := conn.QueryRowContext(context.Background(), bound, strings.Repeat("x", 256<<10)).Scan(&text); err != nil || len(text) != 256<<10 {
		t.Fatal("large binding", err)
	}
	entry = cacheEntryFor(t, conn, bound)
	if entry == nil || entry.size > 32<<10 {
		t.Fatal("accounting retained the cleared large binding")
	}
}
