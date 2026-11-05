package schema

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func existingFixture(t *testing.T, path string, install bool) (*sql.DB, *sql.Conn) {
	t.Helper()
	db, err := sql.Open("sqlite", fixtureDSN(path, true))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if install {
		if err := InitializeEmpty(context.Background(), conn); err != nil {
			t.Fatal(err)
		}
	}
	return db, conn
}

func TestAssertExistingRestartPreservesContents(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "restart.sqlite")
	db, conn := existingFixture(t, path, true)
	if _, err := conn.ExecContext(ctx, "INSERT INTO catalog_credit_labels(id,label) VALUES(5001,'retained')"); err != nil {
		t.Fatal(err)
	}
	if err := AssertExisting(ctx, conn); err != nil {
		t.Fatalf("fresh native layout: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_, reopened := existingFixture(t, path, false)
	// Read-only permission is sufficient; verification must not run bootstrap,
	// ANALYZE, repair, seed or any other write on an existing file.
	if _, err := reopened.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
		t.Fatal(err)
	}
	if err := AssertExisting(ctx, reopened); err != nil {
		t.Fatalf("reopened native layout: %v", err)
	}
	var label string
	if err := reopened.QueryRowContext(ctx, "SELECT label FROM catalog_credit_labels WHERE id=5001").Scan(&label); err != nil || label != "retained" {
		t.Fatalf("retained contents = %q, %v", label, err)
	}
	var changes int
	if err := reopened.QueryRowContext(ctx, "SELECT total_changes()").Scan(&changes); err != nil || changes != 0 {
		t.Fatalf("verification changes = %d, %v", changes, err)
	}
}

func TestAssertExistingRejectsLayoutChanges(t *testing.T) {
	cases := []struct {
		name    string
		install bool
		mutate  string
	}{
		{"empty", false, ""},
		{"legacy", false, "CREATE TABLE legacy(value TEXT); INSERT INTO legacy VALUES('keep')"},
		{"missing-index", true, "DROP INDEX catalog_entities_library"},
		{"changed-index-same-count", true, "DROP INDEX catalog_entities_library; CREATE INDEX catalog_entities_library ON catalog_entities(title)"},
		{"changed-table", true, "ALTER TABLE catalog_people ADD COLUMN unexpected BLOB"},
		{"changed-view-same-count", true, "DROP VIEW search_own_content; CREATE VIEW search_own_content AS SELECT 1 AS id"},
		{"extra-table", true, "CREATE TABLE extra(value TEXT)"},
		{"extra-statistics", true, "ANALYZE catalog_credit_labels"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, conn := existingFixture(t, filepath.Join(t.TempDir(), "layout.sqlite"), test.install)
			ctx := context.Background()
			if test.mutate != "" {
				if _, err := conn.ExecContext(ctx, test.mutate); err != nil {
					t.Fatal(err)
				}
			}
			before, count, err := nativeLayout(ctx, conn)
			if err != nil {
				t.Fatal(err)
			}
			if err := AssertExisting(ctx, conn); !errors.Is(err, ErrLayoutMismatch) {
				t.Fatalf("changed layout accepted or wrong error: %v", err)
			}
			after, afterCount, err := nativeLayout(ctx, conn)
			if err != nil || before != after || count != afterCount {
				t.Fatalf("mismatched file was changed: %s/%d -> %s/%d, %v", before, count, after, afterCount, err)
			}
			if test.name == "legacy" {
				var value string
				if err := conn.QueryRowContext(ctx, "SELECT value FROM legacy").Scan(&value); err != nil || value != "keep" {
					t.Fatalf("legacy data = %q, %v", value, err)
				}
			}
		})
	}
}

func TestAssertExistingRequiresConnectionPolicyAndContext(t *testing.T) {
	_, conn := existingFixture(t, filepath.Join(t.TempDir(), "policy.sqlite"), true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := AssertExisting(ctx, conn); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled verification = %v", err)
	}
	if _, err := conn.ExecContext(context.Background(), "PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	if err := AssertExisting(context.Background(), conn); !errors.Is(err, ErrForeignKeysDisabled) {
		t.Fatalf("FK-disabled verification = %v", err)
	}
}
