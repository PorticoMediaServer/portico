package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestReadGenerationFailureCannotBecomeScalarContent(t *testing.T) {
	db, s, _ := browseFixture(t)
	err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		state := &readGeneration{snapshot: dbwork.Snapshot(ctx), err: errors.New("generation lookup failed")}
		state.once.Do(func() {})
		service := s.WithContext(context.WithValue(ctx, readGenerationKey{}, state))
		if rows, err := service.read().Query(`SELECT title FROM catalog_browse_rows`); err == nil {
			rows.Close()
			t.Fatal("generation failure returned content")
		}
		var count int
		if err := service.read().QueryRow(`SELECT count(*) FROM catalog_browse_rows`).Scan(&count); err == nil {
			t.Fatalf("generation failure returned scalar content: %d", count)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadGenerationUsesIndexesAndRetainsRebuildContent(t *testing.T) {
	db, s, names := browseFixture(t)
	id := names["m1"].ID
	read := func(want string, retained bool) {
		t.Helper()
		ctx, trace := dbwork.TraceStatements(context.Background())
		err := dbwork.WithReadSnapshot(ctx, db, func(ctx context.Context) error {
			service := s.WithContext(ctx)
			for i := 0; i < 3; i++ {
				var title string
				if err := service.read().QueryRow(`SELECT title FROM catalog_browse_rows WHERE entity_id=?`, id).Scan(&title); err != nil {
					return err
				}
				if title != want {
					t.Fatalf("title %q, want %q", title, want)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		checks := 0
		for _, q := range trace() {
			if strings.Contains(q, "SELECT retaining FROM catalog_generations") {
				checks++
			}
			if strings.Contains(q, "SELECT title FROM catalog_browse_rows") && strings.Contains(q, "_serving") != retained {
				t.Fatalf("wrong generation query: %s", q)
			}
		}
		if checks != 1 {
			t.Fatalf("generation checked %d times", checks)
		}
	}
	var original string
	if err := db.QueryRow(`SELECT title FROM catalog_browse_rows WHERE entity_id=?`, id).Scan(&original); err != nil {
		t.Fatal(err)
	}
	read(original, false)
	w := compactcatalog.NewWorker(db)
	if err := w.Rebuild(context.Background(), compactcatalog.DomainBrowseRows); err != nil {
		t.Fatal(err)
	}
	read(original, false) // no retained overlay exists until the first change
	if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE catalog_browse_rows SET title='New title' WHERE entity_id=?`, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	read(original, true)
	if err := compactcatalog.Drain(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	read(original, false)
}
