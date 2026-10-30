package identity

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestSetupMaintenanceWakesOnlyForItsDurableInputs(t *testing.T) {
	db, err := dbwork.OpenHandle(filepath.Join(t.TempDir(), "wakes.sqlite"), dbwork.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Only routing is under test: use small tables so every write below has a
	// proven table scope, independent of identity fixture/setup side effects.
	for _, table := range []string{
		"telemetry_rollups", "quick_connect_v1", "authorization_session_families",
		"identity_sign_in_policy", "identity_factor_challenges", "topshelf_tokens",
	} {
		if _, err = db.Exec(`CREATE TABLE ` + table + `(id INTEGER PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
	}
	wake, stop := setupMaintenanceWake()
	defer stop()
	commit := func(table string, id int) {
		t.Helper()
		if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO `+table+` VALUES(?)`, id)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	commit("telemetry_rollups", 1)
	if wake.Take() {
		t.Fatal("telemetry persistence woke identity maintenance")
	}
	for _, table := range []string{
		"quick_connect_v1", "authorization_session_families", "identity_sign_in_policy",
		"identity_factor_challenges", "topshelf_tokens",
	} {
		commit(table, 1)
		if !wake.Take() {
			t.Fatalf("committed %s did not wake maintenance", table)
		}
	}
	held, err := dbwork.Begin(context.Background(), db, dbwork.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = held.Tx().Exec(`INSERT INTO quick_connect_v1 VALUES(2)`); err != nil {
		t.Fatal(err)
	}
	if err = held.Rollback(); err != nil {
		t.Fatal(err)
	}
	if wake.Take() {
		t.Fatal("rolled-back setup write woke maintenance")
	}
	stop()
	commit("quick_connect_v1", 3)
	if wake.Take() {
		t.Fatal("stopped maintenance subscription was retained")
	}
}
