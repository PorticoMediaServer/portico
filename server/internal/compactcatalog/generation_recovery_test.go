package compactcatalog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestBrowseGenerationSurvivesRollbackAndDatabaseReopen(t *testing.T) {
	db, show := failingFixture(t, "restart-generation.sqlite")
	w := NewWorker(db)
	if err := w.Rebuild(context.Background(), DomainBrowseRows); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE catalog_browse_rows SET title='Rolled back' WHERE entity_id=?`, show); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_rows_previous`); n != 0 {
		t.Fatalf("rolled back retention rows: %d", n)
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetFieldsTx(ctx, tx, show, Automatic, map[string]any{"title": "Replacement"})
	})
	retained := false
	for i := 0; i < 100; i++ {
		if _, err = w.Step(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
		if cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_rows_previous WHERE entity_id=? AND present=1`, show) > 0 {
			retained = true
			break
		}
	}
	if !retained || cc1Scalar(t, db, `SELECT retaining FROM catalog_generations`) != 1 {
		t.Fatal("fixture did not stop during the rebuild")
	}
	var seq int
	var name, path string
	if err = db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var title string
	if err = reopened.QueryRow(`SELECT title FROM catalog_browse_rows_serving WHERE entity_id=?`, show).Scan(&title); err != nil || title != "Show" {
		t.Fatalf("reopened serving title %q: %v", title, err)
	}
	if n := cc1Scalar(t, reopened, `SELECT count(*) FROM catalog_browse_rows_serving WHERE entity_id=?`, show); n != 1 {
		t.Fatalf("duplicate serving rows after reopen: %d", n)
	}
	cc1Drain(t, reopened)
	if cc1Scalar(t, reopened, `SELECT retaining FROM catalog_generations`) != 0 {
		t.Fatal("restarted worker did not publish")
	}
	if err = reopened.QueryRow(`SELECT title FROM catalog_browse_rows_serving WHERE entity_id=?`, show).Scan(&title); err != nil || title != "Replacement" {
		t.Fatalf("published title %q: %v", title, err)
	}
}
