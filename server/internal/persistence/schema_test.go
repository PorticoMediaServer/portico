package persistence

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Updating the pre-release schema means directly editing the baseline and this
// expected digest. Existing state must then be reset, never upgraded.
const schemaSourceDigest = "a807a8409ec3cbe0dc65f847c1e2e46a6b07d81190264c4aab7f1ec4bf0a245d"

func TestSchemaBaselineDigest(t *testing.T) {
	if BaselineDigest() != schemaSourceDigest {
		t.Fatalf("set schemaSourceDigest in this file to: %s", BaselineDigest())
	}
	entries, err := os.ReadDir("migrations")
	if err != nil || len(entries) != 1 || entries[0].Name() != "0001_baseline.sql" {
		t.Fatalf("expected one baseline, got %v (%v)", entries, err)
	}
}

func TestASecondOpenRunsNoSchemaWork(t *testing.T) {
	path := freshDatabaseCopy(t, t.TempDir(), "state.sqlite")
	first, err := OpenFresh(path)
	if err != nil {
		t.Fatal(err)
	}
	var digest string
	if err = first.QueryRow(`SELECT value FROM configuration WHERE key=?`, baselineDigestKey).Scan(&digest); err != nil || digest != BaselineDigest() {
		t.Fatal(digest, err)
	}
	var ledger int
	if err = first.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='schema_migrations'`).Scan(&ledger); err != nil || ledger != 0 {
		t.Fatalf("migration ledger survives: %d %v", ledger, err)
	}
	if _, err = first.Exec(`DROP TABLE detail_jobs`); err != nil {
		t.Fatal(err)
	}
	first.Close()
	second, err := OpenFresh(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var exists int
	if err = second.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='detail_jobs'`).Scan(&exists); err != nil || exists != 0 {
		t.Fatalf("reopen ran DDL: %d %v", exists, err)
	}
}

func TestSchemaMismatchRefusesWithoutChangingState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, change := range []string{
		`UPDATE configuration SET value='another-baseline' WHERE key='schema_baseline_digest'`,
		// The previous pre-release baseline digest. A state still carrying it is
		// not upgraded in place; it must take the ordinary reset path.
		`UPDATE configuration SET value='28847dd0f50505c2d7faeabeaf8da93359f870f85f1f88d76ec0a0e16c77bb95' WHERE key='schema_baseline_digest'`,
		`DELETE FROM configuration WHERE key='schema_baseline_digest'`,
		`UPDATE configuration SET value='99999' WHERE key='schema_version'`,
		`UPDATE configuration SET value='invalid' WHERE key='schema_version'`,
		`DELETE FROM configuration WHERE key='schema_version'`,
	} {
		t.Run(change, func(t *testing.T) {
			path := freshDatabaseCopy(t, t.TempDir(), "state.sqlite")
			db, err := OpenFresh(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`INSERT INTO configuration VALUES('preserve_me','private-state');` + change); err != nil {
				t.Fatal(err)
			}
			db.Close()
			assertResetRefusal(t, path)
			if _, err = InspectCandidate(context.Background(), filepath.Dir(path), filepath.Base(path)); !errors.Is(err, ErrSchemaResetRequired) {
				t.Fatalf("restore accepted mismatch: %v", err)
			}
			db, err = dbwork.OpenHandle(path, dbwork.DefaultPolicy())
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var marker string
			if err = db.QueryRow(`SELECT value FROM configuration WHERE key='preserve_me'`).Scan(&marker); err != nil || marker != "private-state" {
				t.Fatalf("refusal changed state: %q %v", marker, err)
			}
			if _, err = os.Stat(filepath.Join(filepath.Dir(path), "migration-backups")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("refusal created upgrade copy: %v", err)
			}
		})
	}
}

func assertResetRefusal(t *testing.T, path string) {
	t.Helper()
	db, err := OpenFresh(path)
	if db != nil {
		db.Close()
		t.Fatal("mismatched state returned a database")
	}
	if !errors.Is(err, ErrSchemaResetRequired) || !strings.Contains(err.Error(), "state directory") || !strings.Contains(err.Error(), "Stop Portico") {
		t.Fatalf("missing plain reset instruction: %v", err)
	}
}

func TestNonemptyUnrecognizedStateIsNeverInstalledOver(t *testing.T) {
	for _, setup := range []string{
		`CREATE TABLE unrelated(id INTEGER PRIMARY KEY); INSERT INTO unrelated VALUES(7)`,
		`CREATE TABLE configuration(wrong TEXT); INSERT INTO configuration VALUES('old')`,
		`CREATE TABLE configuration(key TEXT PRIMARY KEY,value TEXT NOT NULL); INSERT INTO configuration VALUES('schema_version','265')`,
	} {
		t.Run(setup, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "old.sqlite")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(setup); err != nil {
				t.Fatal(err)
			}
			db.Close()
			assertResetRefusal(t, path)
			db, err = sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var installed int
			if err = db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name='segment_files'`).Scan(&installed); err != nil || installed != 0 {
				t.Fatalf("refusal installed baseline: %d %v", installed, err)
			}
		})
	}
}

func TestFreshBaselineIncludesSegmentAndGatedSchema(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	path := filepath.Join(t.TempDir(), "fresh.sqlite")
	db, err := OpenFresh(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"segment_files", "segment_shows", "segment_catchup", "managed_mounts", "mount_allocations", "mount_backend_configs", "mount_controls", "mount_operations", "mount_revision", "remote_native_listings", "identity_recovery_codes"} {
		var count int
		if err = db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
			t.Errorf("missing table %s: %d %v", table, count, err)
		}
	}
	for _, column := range []string{"recovery_account", "recovery_confirmed", "remote_recovery"} {
		var count int
		if err = db.QueryRow(`SELECT count(*) FROM pragma_table_info('onboarding_state_v1') WHERE name=?`, column).Scan(&count); err != nil || count != 0 {
			t.Errorf("retired owner-setup column remains %s: %d %v", column, count, err)
		}
	}
	if err = VerifySchemaObjects(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err = VerifySeededRows(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

func TestSeparateFolderMinimalCursorSchema(t *testing.T) {
	db, err := OpenFresh(freshDatabaseCopy(t, t.TempDir(), "cursor.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name FROM pragma_table_info('catalog_purge_jobs') ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if got := strings.Join(columns, ","); got != "cursor,generation,mutation_id,plan_digest,state" {
		t.Fatalf("cursor contract: %s", got)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name IN('catalog_purge_identities','catalog_purge_due','catalog_purge_jobs_local_binding','catalog_purge_jobs_owner_value','retained_title_state_capture')`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("foreign cohort schema: %d %v", n, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('retained_title_state') WHERE name='capture_mutation_id'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("foreign capture column: %d %v", n, err)
	}
}
