package catalog

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func settleCompact(t *testing.T, db *sql.DB) {
	t.Helper()
	catalogtest.New(t, db).Drain()
}

func TestCompactFilmFactsUseRequestSnapshot(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalogtest.New(t, db)
	films := c.Library("films", "Films", "movie", "/films")
	path := "/films/film.mkv"
	film := c.Movie(films, path, "Before", 2000)
	c.Drain()
	s := New(db)
	err = dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		bound := s.WithContext(ctx)
		first, err := bound.Get("viewer", film.Public)
		if err != nil {
			return err
		}
		// A separate catalogue writer commits while this request retains its WAL frame.
		c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/films", path, 0), Title: "After", Year: 2000}, nil)
		c.Drain()
		second, err := bound.Get("viewer", film.Public)
		if err == nil && (first.Title != "Before" || second.Title != "Before") {
			t.Fatal("request mixed catalogue snapshots")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err := s.Get("viewer", film.Public)
	if err != nil || item.Title != "After" {
		t.Fatalf("next request did not see committed facts: %+v %v", item, err)
	}
}

func TestCompactFilmFactsFenceVersionedRebuild(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalogtest.New(t, db)
	films := c.Library("films", "Films", "movie", "/films")
	path := "/films/film.mkv"
	film := c.Movie(films, path, "First title", 2000)
	c.Fields(film.ID, map[string]any{"added_text": "2026-09-23T12:34:56.123456Z"})
	c.Drain()
	s := New(db)
	item, err := s.Get("viewer", film.Public)
	if err != nil || item.Title != "First title" || item.AddedAt == nil || *item.AddedAt != "2026-09-23T12:34:56.123456Z" {
		t.Fatalf("catalogue facts changed: %+v %v", item, err)
	}
	c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/films", path, 0), Title: "Next title", Year: 2000}, nil)
	c.Fields(film.ID, map[string]any{"added_text": nil})
	c.Drain()
	item, err = s.Get("viewer", film.Public)
	if err != nil || item.Title != "Next title" || item.AddedAt != nil {
		t.Fatalf("updated facts changed: %+v %v", item, err)
	}
	if err = compactcatalog.NewWorker(db).Rebuild(context.Background(), compactcatalog.DomainAvailability); err != nil {
		t.Fatal(err)
	}
	// Reads no longer wait for a queued publication; the historical pending
	// availability fence assertion is retired. Drain the rebuild before the
	// remaining deletion check.
	c.Drain()
	if _, err = s.Get("viewer", film.Public); err != nil {
		t.Fatal(err)
	}
	c.Delete(film.ID)
	c.Drain()
	if _, err = s.Get("viewer", film.Public); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted entity still returned: %v", err)
	}
}
