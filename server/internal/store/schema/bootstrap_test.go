package schema

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

func TestFreshFileBootstrap(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "fresh.sqlite")
	db, err := sql.Open("sqlite", path)
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
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
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
	reopened, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var tables int
	if err := reopened.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='table'").Scan(&tables); err != nil || tables != 682 {
		t.Fatalf("persisted tables = %d, %v; want 682", tables, err)
	}
}

func TestExistingFileIsUntouched(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "existing.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=ON; CREATE TABLE original(value TEXT); INSERT INTO original VALUES('keep')"); err != nil {
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
