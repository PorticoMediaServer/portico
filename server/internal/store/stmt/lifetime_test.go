package stmt_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Temporary test DSN until store/internal/sqliteconn lands. Every replacement
// physical connection inherits these settings; no one-off PRAGMA setup is used.
const connectionPragmas = "_pragma=foreign_keys(1)&_pragma=synchronous(1)&_pragma=wal_autocheckpoint(0)&_pragma=cache_size(-2048)&_pragma=busy_timeout(0)&_pragma=mmap_size(0)&_pragma=query_only(0)"

func TestRegistryBundledEngineAndIterationCancellation(t *testing.T) {
	db := database(t)
	var engine string
	if err := db.QueryRow("SELECT sqlite_version()").Scan(&engine); err != nil || engine != "3.53.4" {
		t.Fatalf("bundled engine: %q %v", engine, err)
	}
	d := stmt.Definition{ID: "cancel.iteration", SQL: `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<5000000) SELECT 1 UNION ALL SELECT sum(x) FROM n`, Owner: "test", CallSites: []string{"lifetime_test.go"}, Kind: stmt.Read, Cohorts: []stmt.Cohort{{Name: "second-row-work"}}}
	registry, err := stmt.New([]stmt.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := registry.Lookup(d.ID)
	b, _ := s.Bind()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rows, err := b.QueryContext(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("first row missing")
	}
	var value int64
	if err := rows.Scan(&value); err != nil || value != 1 {
		t.Fatalf("first row: %d %v", value, err)
	}
	timer := time.AfterFunc(10*time.Millisecond, cancel)
	defer timer.Stop()
	started := time.Now()
	if rows.Next() {
		t.Fatal("cancelled second row exhausted recursive work")
	}
	if !errors.Is(rows.Err(), context.Canceled) {
		t.Fatalf("iteration cancellation: %v", rows.Err())
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancelled iteration retained reader for %s", elapsed)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT 42").Scan(&value); err != nil || value != 42 {
		t.Fatalf("connection not reusable: %d %v", value, err)
	}
}
func TestRegistryReaderCleanupReleasesWAL(t *testing.T) {
	for _, cleanup := range []string{"early-close", "cancel", "scan-error"} {
		t.Run(cleanup, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "registry.db")
			uri := url.URL{Scheme: "file", Path: path}
			db, err := sql.Open("sqlite", uri.String()+"?"+connectionPragmas+"&_pragma=journal_mode(WAL)")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(2)
			writer, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			assertConnection(t, writer)
			if _, err := writer.ExecContext(ctx, "CREATE TABLE items(id INTEGER PRIMARY KEY,value TEXT); INSERT INTO items VALUES(1,'not-an-integer'),(2,'next')"); err != nil {
				t.Fatal(err)
			}
			reader, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			assertConnection(t, reader)
			d := stmt.Definition{ID: "read.window", SQL: "SELECT value FROM items ORDER BY id", Owner: "test", CallSites: []string{"lifetime_test.go"}, Kind: stmt.Read, Cohorts: []stmt.Cohort{{Name: "two-rows"}}}
			registry, err := stmt.New([]stmt.Definition{d})
			if err != nil {
				t.Fatal(err)
			}
			s, _ := registry.Lookup(d.ID)
			b, _ := s.Bind()
			readCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			rows, err := b.QueryContext(readCtx, reader)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if !rows.Next() {
				t.Fatal("reader did not establish snapshot")
			}
			if _, err := writer.ExecContext(ctx, "INSERT INTO items VALUES(3,'after-snapshot')"); err != nil {
				t.Fatal(err)
			}
			var busy, log, checkpointed int
			if err := writer.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &log, &checkpointed); err != nil {
				t.Fatal(err)
			}
			if log <= checkpointed {
				t.Fatalf("control did not pin WAL: busy=%d log=%d checkpointed=%d", busy, log, checkpointed)
			}
			switch cleanup {
			case "early-close":
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
				if rows.Next() {
					t.Fatal("cancelled window returned row")
				}
				if !errors.Is(rows.Err(), context.Canceled) {
					t.Fatalf("cancelled rows: %v", rows.Err())
				}
				rows.Close()
			case "scan-error":
				var number int64
				if err := rows.Scan(&number); err == nil {
					t.Fatal("scan error control did not fail")
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
			}
			// QueryContext callers close Rows, including after Scan errors. QueryRow
			// closes internally even when conversion fails.
			var number int64
			if cleanup != "cancel" {
				if err := b.QueryRowContext(ctx, reader).Scan(&number); err == nil {
					t.Fatal("QueryRow conversion control did not fail")
				}
			}
			if err := writer.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed); err != nil {
				t.Fatal(err)
			}
			if busy != 0 || log != 0 || checkpointed != 0 {
				t.Fatalf("reader retained WAL: %d/%d/%d", busy, log, checkpointed)
			}
			info, err := os.Stat(path + "-wal")
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() != 0 {
				t.Fatalf("WAL not reset: %d", info.Size())
			}
			// Force a physical replacement after the cancellation/cleanup boundary;
			// the DSN must restore every setting on that new connection as well.
			if err := reader.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatalf("replace connection: %v", err)
			}
			reader.Close()
			replacement, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer replacement.Close()
			assertConnection(t, replacement)
		})
	}
}
func assertConnection(t *testing.T, conn *sql.Conn) {
	t.Helper()
	for name, want := range map[string]int64{"foreign_keys": 1, "synchronous": 1, "wal_autocheckpoint": 0, "cache_size": -2048, "busy_timeout": 0, "mmap_size": 0, "query_only": 0} {
		var got int64
		if err := conn.QueryRowContext(context.Background(), "PRAGMA "+name).Scan(&got); err != nil || got != want {
			t.Fatalf("connection %s: got %d, want %d: %v", name, got, want, err)
		}
	}
}
