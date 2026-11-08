package schema

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

func TestFreshFileBootstrap(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fresh.sqlite")
	db, err := openFixture(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := InitializeEmpty(ctx, conn); err != nil {
		t.Fatal(err)
	}
	var integrity string
	if err := conn.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity = %q, %v", integrity, err)
	}
	rows, err := conn.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("foreign key violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if err := InitializeEmpty(ctx, conn); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("repeat bootstrap = %v", err)
	}
	if _, err := conn.ExecContext(ctx, "SELECT id FROM catalog_entities LIMIT 0"); err != nil {
		t.Fatalf("canonical entity table missing: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// The installed schema survives closing and reopening the fresh file.
	reopened, err := openFixture(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var tables int
	// CHANGE-012 adds seven guide/channel/DVR relations; CHANGE-015 adds16
	// security/root tables; CHANGE-016 adds13 artwork tables and017 adds10
	// provider/person tables; CHANGE-018 adds25 media/reading tables.
	// CHANGE-013 and014 add no tables.
	if err := reopened.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='table'").Scan(&tables); err != nil || tables != 772 {
		t.Fatalf("persisted tables = %d, %v; want 772", tables, err)
	}
}

func TestExistingFileIsUntouched(t *testing.T) {
	ctx := context.Background()
	db, err := openFixture(filepath.Join(t.TempDir(), "existing.sqlite"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "CREATE TABLE original(value TEXT); INSERT INTO original VALUES('keep')"); err != nil {
		t.Fatal(err)
	}
	if err := InitializeEmpty(ctx, conn); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("existing bootstrap = %v", err)
	}
	var value string
	if err := conn.QueryRowContext(ctx, "SELECT value FROM original").Scan(&value); err != nil || value != "keep" {
		t.Fatalf("original data = %q, %v", value, err)
	}
	var objects int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'").Scan(&objects); err != nil || objects != 1 {
		t.Fatalf("existing objects = %d, %v", objects, err)
	}
}

// Runtime and fixtures share the physical policy, including replacements.
func openFixture(path string, foreignKeys bool) (*sql.DB, error) {
	db, _, err := sqliteconn.Open(context.Background(), path, sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 4096, Synchronous: sqliteconn.Normal})
	if err == nil && !foreignKeys {
		// Deliberate negative control on the sole acquired physical connection.
		_, err = db.Exec("PRAGMA foreign_keys=OFF")
		if err != nil {
			db.Close()
		}
	}
	return db, err
}

func TestForeignKeysRequired(t *testing.T) {
	ctx := context.Background()
	db, err := openFixture(filepath.Join(t.TempDir(), "fk-off.sqlite"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := InitializeEmpty(ctx, conn); !errors.Is(err, ErrForeignKeysDisabled) {
		t.Fatalf("FK-disabled bootstrap = %v", err)
	}
	var objects int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema").Scan(&objects); err != nil || objects != 0 {
		t.Fatalf("FK-disabled schema = %d, %v", objects, err)
	}
}

func TestSchemaFixturesRejectQuotedTypos(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture-policy.sqlite")
	for range 2 { // fresh open and clean reopen use the same policy
		db, err := openFixture(path, true)
		if err != nil {
			t.Fatal(err)
		}
		conn, err := db.Conn(t.Context())
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		ddl, dml, err := sqlite.DQS(conn)
		if err != nil || ddl != 0 || dml != 0 {
			t.Fatalf("fixture flags %d/%d: %v", ddl, dml, err)
		}
		for _, q := range []string{`SELECT "fixture_missing_literal"`, `CREATE TABLE bad_fixture(v TEXT CHECK(v <> "fixture_missing_literal"))`} {
			st, err := conn.PrepareContext(t.Context(), q)
			if st != nil {
				st.Close()
			}
			if err == nil {
				t.Fatalf("fixture accepted typo: %s", q)
			}
		}
		conn.Close()
		db.Close()
	}
}
