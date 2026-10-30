package tableretention_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/tableretention"
)

func TestRetentionUsesImportedAndOwnerSavedSpeedOnTheNextBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	ctx := context.Background()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t.Cleanup(func() { dbwork.SetBackgroundPolicy(dbwork.SpeedNormal, 0) })
	store := operations.New(db)
	store.ConfigurationDomains = []operations.ConfigurationDomain{administration.MaintenanceConfiguration{}}
	path := filepath.Join(root, "portico.toml")
	if err = os.WriteFile(path, []byte("[general]\nbackground_speed='gentle'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = store.InitializeConfiguration(ctx, path); err != nil {
		t.Fatal(err)
	}
	if err = dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		for n := 0; n < 130; n++ {
			if _, e := tx.ExecContext(ctx, `INSERT INTO admin_receipts VALUES('server',?,'digest','{}',0)`, fmt.Sprintf("old%d", n)); e != nil {
				return e
			}
		}
		if _, e := tx.ExecContext(ctx, `UPDATE maintenance_retention_rows SET created_ms=0 WHERE table_name='admin_receipts'`); e != nil {
			return e
		}
		// Retired page documents cannot override the real imported authority.
		_, e := tx.ExecContext(ctx, `INSERT INTO admin_documents(scope,revision,updated_ms,body) VALUES('maintenance',1,0,'{"backgroundSpeed":"fast"}')`)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	assertRemaining := func(want int) {
		t.Helper()
		var left int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM admin_receipts WHERE scope='server'`).Scan(&left); err != nil || left != want {
			t.Fatalf("retention quantum: %d remaining, want %d: %v", left, want, err)
		}
	}
	more, err := tableretention.Sweep(ctx, db, time.Now(), 256)
	if err != nil || !more {
		t.Fatalf("imported gentle sweep: more=%v: %v", more, err)
	}
	assertRemaining(66)

	service := administration.NewAt(db, root)
	service.ConfigurationCommitted = store.FlushConfiguration
	authorize := func(context.Context, *sql.Tx) error { return nil }
	doc, err := service.MaintenanceSettings(ctx, authorize)
	if err != nil {
		t.Fatal(err)
	}
	next := doc.Settings
	next.BackgroundSpeed = "fast"
	if _, err = service.SaveMaintenanceSettings(ctx, authorize, administration.Change[administration.MaintenanceSettingsDocument]{
		ExpectedRevision: doc.Revision, OperationID: "retention-speed", Settings: next,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE admin_documents SET body='{"backgroundSpeed":"gentle"}' WHERE scope='maintenance'`); err != nil {
		t.Fatal(err)
	}
	if _, err = tableretention.Sweep(ctx, db, time.Now(), 256); err != nil {
		t.Fatal(err)
	}
	assertRemaining(0)
	// The new owner receipt is not expired with the seeded old cohort.
	var receipts int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM admin_receipts WHERE scope='maintenance' AND operation_id='retention-speed'`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("fresh owner receipt removed: %d: %v", receipts, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "background_speed = 'fast'") {
		t.Fatalf("owner speed not projected: %s: %v", raw, err)
	}
}
