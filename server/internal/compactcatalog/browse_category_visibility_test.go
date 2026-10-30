package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func drainCategoryVisibility(t *testing.T, db *sql.DB, limit int) {
	t.Helper()
	for i := 0; i < 100; i++ {
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		n, err := StepCategoryVisibility(context.Background(), tx, limit)
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("category visibility worker did not drain")
}

func setMovieGenre(t *testing.T, db *sql.DB, entity int64, name string) {
	t.Helper()
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return SetTermsTx(ctx, tx, entity, VocabGenre, "tmdb", []Term{{SourceID: "1", Name: name}})
	})
}

// deriveMovieCategories runs only the category derivation for one movie,
// without the worker's other domains or step workers: the member triggers
// queue the pair jobs the visibility steps below consume.
func deriveMovieCategories(t *testing.T, db *sql.DB, entity int64) {
	t.Helper()
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var rev int64
		if err := tx.QueryRowContext(ctx, `SELECT revision FROM catalog_dirty WHERE domain=? AND entity_id=?`, DomainCategories, entity).Scan(&rev); err != nil {
			return err
		}
		done, err := deriveCategories(ctx, tx, Key{ID: entity, Revision: rev}, 32)
		if err != nil {
			return err
		}
		if !done {
			return fmt.Errorf("category derivation did not finish in one step")
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM catalog_dirty WHERE domain=? AND entity_id=? AND revision=?`, DomainCategories, entity, rev)
		return err
	})
}

func TestBrowseCategoryVisibleContributionsAndReplay(t *testing.T) {
	db := cc1OpenDB(t, "category-visible.sqlite")
	lib := cc1Library(t, db, "films", "Films", "movie", "/films")
	movies := map[string]int64{}
	for _, id := range []string{"a", "b"} {
		movies[id] = cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "file:" + id + ".mkv", Title: id, Year: 1999},
			map[string]any{"poster_url": "/" + id})
		setMovieGenre(t, db, movies[id], "Drama")
	}
	cc1Drain(t, db)
	classes := map[string]int64{}
	for _, policy := range []string{"one", "two"} {
		classes[policy] = cc1Class(t, db, lib, policy)
		cc1Visible(t, db, classes[policy], movies["a"], 1)
	}
	drainCategoryVisibility(t, db, 1)
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_movie_category_visible_summaries WHERE kind=1 AND value='Drama' AND total=1 AND posters_json='["/a"]'`); got != 2 {
		t.Fatalf("class category samples missing: %d", got)
	}
	setMovieGenre(t, db, movies["a"], "Comedy")
	deriveMovieCategories(t, db, movies["a"])
	// Pair work has two classes. A source edit after its first bounded slice
	// must restart the cursor before the pair job can be acknowledged.
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := stepCategoryVisibilityPair(context.Background(), tx, 1); err != nil || n != 1 {
		tx.Rollback()
		t.Fatalf("first pair step: %d %v", n, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	setMovieGenre(t, db, movies["a"], "Action")
	deriveMovieCategories(t, db, movies["a"])
	drainCategoryVisibility(t, db, 1)
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_movie_category_visible_summaries WHERE kind=1 AND value='Drama'`); got != 0 {
		t.Fatalf("stale class genre remained: %d", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_movie_category_visible_summaries WHERE kind=1 AND value='Comedy'`); got != 0 {
		t.Fatalf("intermediate class genre remained: %d", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_movie_category_visible_summaries WHERE kind=1 AND value='Action' AND total=1`); got != 2 {
		t.Fatalf("both classes did not replay: %d", got)
	}
	cc1Exec(t, db, `DELETE FROM compact_visibility_rows WHERE class_id=? AND entity_id=?`, classes["one"], movies["a"])
	drainCategoryVisibility(t, db, 1)
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_movie_category_visible_summaries WHERE kind=1 AND value='Action'`); got != 1 {
		t.Fatalf("class removal retained count: %d", got)
	}
}
