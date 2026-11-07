package schema

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func existingFixture(t *testing.T, path string, install bool) (*sql.DB, *sql.Conn) {
	t.Helper()
	db, err := openFixture(path, true)
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
		{"prior-change010-layout", true, "DROP INDEX scan_receipt_refused_directory_identity"},
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

func TestAssertExistingRejectsPersistedHeaderChanges(t *testing.T) {
	for _, header := range []string{"application_id", "user_version"} {
		t.Run(header, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "header.sqlite")
			db, conn := existingFixture(t, path, true)
			ctx := context.Background()
			if _, err := conn.ExecContext(ctx, "PRAGMA main."+header+"=123"); err != nil {
				t.Fatal(err)
			}
			if err := conn.Close(); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			_, reopened := existingFixture(t, path, false)
			if _, err := reopened.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
				t.Fatal(err)
			}
			if err := AssertExisting(ctx, reopened); !errors.Is(err, ErrLayoutMismatch) {
				t.Fatalf("changed persistent %s accepted: %v", header, err)
			}
			var actual int
			if err := reopened.QueryRowContext(ctx, "PRAGMA main."+header).Scan(&actual); err != nil || actual != 123 {
				t.Fatalf("header repaired or lost: %d, %v", actual, err)
			}
			// The native declarations alone still match: this must be a
			// separate persistent-header rejection, not a structural negative.
			fingerprint, count, err := nativeLayout(ctx, reopened)
			if err != nil || fingerprint != NativeLayoutSHA256 || count != NativeLayoutObjects {
				t.Fatalf("unexpected structural change: %s/%d, %v", fingerprint, count, err)
			}
		})
	}
}

func TestAssertExistingRejectsDifferentDatabaseEncoding(t *testing.T) {
	_, conn := existingFixture(t, filepath.Join(t.TempDir(), "encoding.sqlite"), false)
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, "PRAGMA encoding='UTF-16le'"); err != nil {
		t.Fatal(err)
	}
	// Install the declarations directly as a negative fixture. A matching
	// logical schema does not authorize a different persisted byte encoding.
	if _, err := conn.ExecContext(ctx, SQL()); err != nil {
		t.Fatal(err)
	}
	if err := AssertExisting(ctx, conn); !errors.Is(err, ErrLayoutMismatch) {
		t.Fatalf("different database encoding accepted: %v", err)
	}
}

func TestAssertExistingUsesSharedEnforcement(t *testing.T) {
	for _, pragma := range []string{"ignore_check_constraints", "defer_foreign_keys", "writable_schema", "read_uncommitted"} {
		t.Run(pragma, func(t *testing.T) {
			_, conn := existingFixture(t, filepath.Join(t.TempDir(), "policy.sqlite"), true)
			ctx := context.Background()
			if _, err := conn.ExecContext(ctx, "PRAGMA "+pragma+"=ON"); err != nil {
				t.Fatal(err)
			}
			if err := AssertExisting(ctx, conn); !errors.Is(err, sqliteconn.ErrPolicy) {
				t.Fatalf("weakened %s accepted: %v", pragma, err)
			}
			var after int
			if err := conn.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&after); err != nil || after != 1 {
				t.Fatalf("verification repaired policy: %d, %v", after, err)
			}
		})
	}
}

func TestBootstrapRejectsReservedHeaderAndWeakenedEnforcement(t *testing.T) {
	for _, mutation := range []string{"PRAGMA application_id=123", "PRAGMA user_version=123", "PRAGMA encoding='UTF-16le'", "PRAGMA ignore_check_constraints=ON"} {
		t.Run(mutation, func(t *testing.T) {
			_, conn := existingFixture(t, filepath.Join(t.TempDir(), "fresh.sqlite"), false)
			ctx := context.Background()
			if _, err := conn.ExecContext(ctx, mutation); err != nil {
				t.Fatal(err)
			}
			if err := InitializeEmpty(ctx, conn); err == nil {
				t.Fatal("bootstrap accepted incompatible header/enforcement")
			}
			var objects int
			if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema").Scan(&objects); err != nil || objects != 0 {
				t.Fatalf("failed bootstrap changed layout: %d, %v", objects, err)
			}
		})
	}
}
