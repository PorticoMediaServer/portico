package stmt_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

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
	connection, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	assertConnection(t, connection, sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 2048, Synchronous: sqliteconn.Normal})
	if err := connection.QueryRowContext(context.Background(), "SELECT 42").Scan(&value); err != nil || value != 42 {
		t.Fatalf("connection not reusable: %d %v", value, err)
	}
}
func TestRegistryReaderCleanupReleasesWAL(t *testing.T) {
	for _, cleanup := range []string{"early-close", "cancel", "scan-error"} {
		t.Run(cleanup, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "registry.db")
			writerCfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 2048, Synchronous: sqliteconn.Normal}
			db, startup, err := sqliteconn.Open(ctx, path, writerCfg)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			t.Logf("writer startup settings: %+v", startup)
			writer, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			assertConnection(t, writer, writerCfg)
			if _, err := writer.ExecContext(ctx, "CREATE TABLE items(id INTEGER PRIMARY KEY,value TEXT); INSERT INTO items VALUES(1,'not-an-integer'),(2,'next')"); err != nil {
				t.Fatal(err)
			}
			readerCfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 2048, Synchronous: sqliteconn.Normal}
			readers, readerStartup, err := sqliteconn.Open(ctx, path, readerCfg)
			if err != nil {
				t.Fatal(err)
			}
			defer readers.Close()
			t.Logf("reader startup settings: %+v", readerStartup)
			reader, err := readers.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			assertConnection(t, reader, readerCfg)
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
			replacement, err := readers.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer replacement.Close()
			assertConnection(t, replacement, readerCfg)
		})
	}
}
func assertConnection(t *testing.T, conn *sql.Conn, cfg sqliteconn.Config) {
	t.Helper()
	settings, err := sqliteconn.Assert(context.Background(), conn, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("acquired settings: %+v", settings)
}
