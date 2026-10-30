package ingestion

import (
	"context"
	"database/sql"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
)

func TestConsoleOperationOwnsEverySourceAndControlsPublication(t *testing.T) {
	ctx := context.Background()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "inventory.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	lib, err := cat.Create("Movies", "movie", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO library_sources(id,library_id,name,configured_root,root) VALUES('second',?,'Second',?,?)`, lib.ID, t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	s := New(db, cat, assets.Probe{})
	command := func(action string) {
		t.Helper()
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		if e = s.ControlOperationTx(ctx, tx, "operation", action); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
		s.InterruptOperation(ctx, "operation")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := s.QueueOperationTx(ctx, tx, "operation", lib.ID)
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	// A duplicate console/direct admission adopts the exact active source jobs.
	if _, err = s.QueueOperationTx(ctx, tx, "operation", lib.ID); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if domain != "operation" {
		t.Fatal("domain still points to only first source")
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM inventory_operation_jobs WHERE operation_id='operation'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("source jobs %d: %v", count, err)
	}
	observation := func(state string) {
		t.Helper()
		got, e := s.ObserveOperation(ctx, "operation")
		if e != nil || got.State != state {
			t.Fatalf("want %s, got %+v: %v", state, got, e)
		}
	}
	observation("queued")
	command("pause")
	observation("paused")
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	if err = tx.QueryRow(`SELECT job_id FROM inventory_operation_jobs WHERE operation_id='operation' LIMIT 1`).Scan(&id); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err = catalog.InventoryFenceTx(ctx, tx, id); err != context.Canceled {
		tx.Rollback()
		t.Fatal("paused publication not fenced", err)
	}
	tx.Rollback()
	command("resume")
	observation("queued")
	if _, err = db.Exec(`UPDATE jobs SET status='complete' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	observation("queued") // one completed source cannot complete the group
	command("cancel")
	observation("cancelled")
	if _, err = s.ObserveOperation(ctx, "absent"); err != sql.ErrNoRows {
		t.Fatal("missing operation not reported", err)
	}
}
