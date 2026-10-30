package compactcatalog

import (
	"context"
	"database/sql"
	"testing"
)

func categorySummary(t *testing.T, db *sql.DB, kind int, value string) (int, string) {
	t.Helper()
	var total int
	var posters string
	if err := db.QueryRow(`SELECT total,posters_json FROM catalog_movie_category_summaries WHERE kind=? AND value=?`, kind, value).Scan(&total, &posters); err != nil {
		t.Fatalf("category %d/%q: %v", kind, value, err)
	}
	return total, posters
}

// categoryMovie creates a 1999 movie with a poster, the Drama genre from
// tmdb and the studio "Studio A".
func categoryMovie(t *testing.T, db *sql.DB, library int64, key, title string) int64 {
	t.Helper()
	id := cc1Entity(t, db, Entity{Library: library, Kind: Movie, Key: "file:" + key, Title: title, Year: 1999, Added: "2026-01-01T00:00:00.000Z"}, map[string]any{"poster_url": "/" + key + ".jpg"})
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := SetTermsTx(ctx, tx, id, VocabGenre, "tmdb", []Term{{SourceID: "1", Name: "Drama"}}); err != nil {
			return err
		}
		return SetAttributesTx(ctx, tx, id, "studio", []string{"Studio A"})
	})
	return id
}

func TestBrowseCategoriesCountsPostersAndRetirement(t *testing.T) {
	db := cc1OpenDB(t, "categories.sqlite")
	films := cc1Library(t, db, "films", "Films", "movie", "/films")
	ids := map[string]int64{}
	for _, pair := range [][2]string{{"a", "Alpha"}, {"b", "Beta"}, {"c", "Charlie"}, {"d", "Delta"}, {"e", "Echo"}} {
		ids[pair[0]] = categoryMovie(t, db, films, pair[0], pair[1])
	}
	// Two providers may repeat the display genre, but a category counts one item.
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetTermsTx(ctx, tx, ids["a"], VocabGenre, "imdb", []Term{{SourceID: "2", Name: "Drama"}})
	})
	cc1Drain(t, db)
	for _, category := range []struct {
		kind  int
		value string
	}{{0, "1990"}, {1, "Drama"}, {2, "Studio A"}} {
		total, posters := categorySummary(t, db, category.kind, category.value)
		if total != 5 || posters != `["/a.jpg","/b.jpg","/c.jpg","/d.jpg"]` {
			t.Fatalf("category %d/%q: count=%d posters=%s", category.kind, category.value, total, posters)
		}
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetFieldsTx(ctx, tx, ids["e"], Automatic, map[string]any{"title": "Aaron", "poster_url": "/new-e.jpg", "year": 2001})
	})
	cc1Drain(t, db)
	if total, posters := categorySummary(t, db, 1, "Drama"); total != 5 || posters != `["/new-e.jpg","/a.jpg","/b.jpg","/c.jpg"]` {
		t.Fatalf("resorted genre poster sample: %d %s", total, posters)
	}
	if total, _ := categorySummary(t, db, 0, "1990"); total != 4 {
		t.Fatalf("old decade count: %d", total)
	}
	if total, _ := categorySummary(t, db, 0, "2000"); total != 1 {
		t.Fatalf("new decade count: %d", total)
	}
	cc1Exec(t, db, `INSERT INTO dvr_catalog_retirements(item_id,requested_ms) VALUES(?,1)`, ids["a"])
	cc1Drain(t, db)
	if total, _ := categorySummary(t, db, 1, "Drama"); total != 4 {
		t.Fatalf("retired item remained counted: %d", total)
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return DeleteEntityTx(ctx, tx, ids["b"]) })
	cc1Drain(t, db)
	if total, _ := categorySummary(t, db, 1, "Drama"); total != 3 {
		t.Fatalf("deleted item remained counted: %d", total)
	}
}

func TestBrowseCategoriesReplaysMidFanoutEdit(t *testing.T) {
	db := cc1OpenDB(t, "categories.sqlite")
	films := cc1Library(t, db, "films", "Films", "movie", "/films")
	a := categoryMovie(t, db, films, "a", "Alpha")
	cc1Drain(t, db)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetFieldsTx(ctx, tx, a, Automatic, map[string]any{"year": 2001})
	})
	step := func() bool {
		t.Helper()
		var complete bool
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			var revision int64
			if err := tx.QueryRowContext(ctx, `SELECT revision FROM catalog_dirty WHERE domain=? AND entity_id=?`, DomainCategories, a).Scan(&revision); err != nil {
				return err
			}
			var err error
			complete, err = deriveCategories(ctx, tx, Key{ID: a, Revision: revision}, 1)
			return err
		})
		return complete
	}
	if step() {
		t.Fatal("fanout completed before all categories")
	}
	// A genre edit mid-fanout requeues the movie: the cursor restarts.
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetTermsTx(ctx, tx, a, VocabGenre, "tmdb", []Term{{SourceID: "1", Name: "Comedy", Key: "comedy"}})
	})
	for i := 0; ; i++ {
		if step() {
			break
		}
		if i == 11 {
			t.Fatal("category replay did not complete")
		}
	}
	cc1Drain(t, db)
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_movie_category_summaries WHERE kind=1 AND value='Drama'`); got != 0 {
		t.Fatalf("old genre retained: %d", got)
	}
	if total, _ := categorySummary(t, db, 1, "Comedy"); total != 1 {
		t.Fatalf("new genre missing: %d", total)
	}
}
