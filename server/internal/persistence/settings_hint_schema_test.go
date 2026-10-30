package persistence

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"
)

// This guard lands with the normalized Settings writer and revision trigger.
// Reading or seeding Settings is silent; only a committed authority revision
// publishes a coalesced change, and a rolled-back write publishes nothing.
func TestNormalizedSettingsHintsFollowCommittedRevision(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var current int64
	if err := db.QueryRow(`SELECT revision FROM console_settings_revision WHERE singleton=1`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	hint := func(want string) {
		t.Helper()
		var revision string
		err := db.QueryRow(`SELECT revision FROM api_change_pending WHERE audience='server' AND type='settings.changed' AND resource_kind='settings' AND resource_id='server'`).Scan(&revision)
		if want == "" {
			if err != sql.ErrNoRows {
				t.Fatalf("unexpected Settings hint %q: %v", revision, err)
			}
			return
		}
		if err != nil || revision != want {
			t.Fatalf("Settings hint = %q, want %q: %v", revision, want, err)
		}
	}
	hint("")
	if _, err := db.Exec(`UPDATE console_settings_revision SET revision=revision WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	hint("")
	// A retired document is not a second settings authority.
	if _, err := db.Exec(`INSERT INTO console_documents(scope,revision,body,updated_ms) VALUES('server',1,'{}',0) ON CONFLICT(scope) DO UPDATE SET revision=revision+1`); err != nil {
		t.Fatal(err)
	}
	hint("")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE console_settings_revision SET revision=revision+1 WHERE singleton=1`); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	hint("")
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE console_settings_revision SET revision=revision+1 WHERE singleton=1; UPDATE console_settings_revision SET revision=revision+1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	hint(strconv.FormatInt(current+2, 10))
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM api_change_pending WHERE audience='server' AND type='settings.changed' AND resource_kind='settings' AND resource_id='server'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("Settings changes did not coalesce: %d, %v", count, err)
	}
}
