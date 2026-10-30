package persistence

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestRecommendationsBaselineDoesNotInventPeopleOrRepeatWork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := OpenFresh(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	library := persistenceLibrary(t, db, "movies", "Movies", "movie", "/movies")
	item, _, _ := persistenceMovie(t, db, library, "/movies", "film.mp4", "Film", 2020)
	persistenceWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.SetTermsTx(ctx, tx, item, compactcatalog.VocabGenre, "tmdb", []compactcatalog.Term{{SourceID: "16", Name: "Animation"}}); err != nil {
			return err
		}
		return compactcatalog.SetCreditsTx(ctx, tx, item, "tmdb", []compactcatalog.Credit{{CreditID: "per-credit-id", CreditedName: "Actor", Role: "Lead", Department: "Acting", Ordinal: 0}})
	})
	persistenceDrain(t, db)
	var count, revision int
	if err = db.QueryRow(`SELECT count(*) FROM catalog_related_facets WHERE relation=1 AND provider<>'name'`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	// The folded genre name rides along as provider 'name'.
	if err = db.QueryRow(`SELECT count(*) FROM catalog_related_facets WHERE relation=1 AND provider='name' AND facet_id='animation'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("folded genre name", count, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM catalog_people`).Scan(&count); err != nil || count != 0 {
		t.Fatal("invented person", count, err)
	}
	if err = db.QueryRow(`SELECT revision FROM library_revisions WHERE library_id='movies'`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TRIGGER forbid_backfill BEFORE INSERT ON catalog_related_facets BEGIN SELECT RAISE(ABORT,'unexpected repeated backfill'); END`); err != nil {
		t.Fatal(err)
	}
	if err = installBaseline(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var after int
	if err = db.QueryRow(`SELECT revision FROM library_revisions WHERE library_id='movies'`).Scan(&after); err != nil || after != revision {
		t.Fatal("reopen changed catalog", after, err)
	}
}
