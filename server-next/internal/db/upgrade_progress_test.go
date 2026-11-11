package db

import (
	"context"
	"path/filepath"
	"testing"
)

// The Dashboard's upgrade task: none once every step's background part is done; the oldest
// unfinished one, with how far it has got, while one is left.
func TestUpgradeLeft(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, _, _, ok, err := d.UpgradeLeft(ctx); err != nil || ok {
		t.Fatalf("a new database has upgrade work left: %v %v", ok, err)
	}
	var newest int
	if err := d.read.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&newest); err != nil {
		t.Fatal(err)
	}
	if _, err := d.writeConn.ExecContext(ctx, `UPDATE schema_migrations SET background_after = 4200 WHERE version = ?`, newest); err != nil {
		t.Fatal(err)
	}
	// The background work has already stopped in this test (nothing was left at open), so the
	// step reads as waiting for the next start.
	if version, reached, running, ok, err := d.UpgradeLeft(ctx); err != nil || !ok || running || version != newest || reached != 4200 {
		t.Errorf("upgrade work left reads %d %d %v %v %v", version, reached, running, ok, err)
	}
}
