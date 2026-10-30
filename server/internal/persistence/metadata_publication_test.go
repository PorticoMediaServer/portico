package persistence

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// This uses real SQLite and the
// root-wired installer, not a mirrored schema or callback-only approximation.
func TestMetadataPublicationCurrentInitializationDoesNotAdoptRows(t *testing.T) {
	db, err := Open(freshDatabaseCopy(t, t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	library := persistenceLibrary(t, db, "lib", "Movies", "movie", "/owned")
	var item int64
	persistenceWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		item, _, err = compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/owned", "/owned/film.mp4", 0), Title: "Film", Year: 2024})
		return err
	})
	if _, err = db.Exec(`DELETE FROM metadata_publication_heads WHERE item_id=?`, item); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TRIGGER block_adoption BEFORE INSERT ON metadata_publication_heads BEGIN SELECT RAISE(ABORT,'unexpected adoption'); END`); err != nil {
		t.Fatal(err)
	}
	if err = installBaseline(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM metadata_publication_heads`).Scan(&count); err != nil || count != 0 {
		t.Fatal("initializer invented missing authority", count, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM configuration WHERE key='metadata_publication_backfill_v1'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("obsolete marker", count, err)
	}
}

func TestMetadataPublicationMutationRevisionsAndCascade(t *testing.T) {
	for _, recursive := range []int{0, 1} {
		t.Run(map[int]string{0: "recursion_off", 1: "recursion_on"}[recursive], func(t *testing.T) {
			db, err := Open(freshDatabaseCopy(t, t.TempDir(), "catalog.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			statements := []string{`PRAGMA recursive_triggers=OFF`, `PRAGMA recursive_triggers=ON`}
			if _, err = db.Exec(statements[recursive]); err != nil {
				t.Fatal(err)
			}
			library := persistenceLibrary(t, db, "lib", "Movies", "movie", "/owned")
			a, asset, _ := persistenceMovie(t, db, library, "/owned", "a.mp4", "Film", 2024)
			b, _, _ := persistenceMovie(t, db, library, "/owned", "b.mp4", "Other", 2024)
			if _, err = db.Exec(`INSERT INTO metadata_jobs(item_id) VALUES(?)`, a); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`INSERT INTO detail_jobs(item_id,provider_id) VALUES(?,1)`, a); err != nil {
				t.Fatal(err)
			}
			var oldA, oldB, nextA, nextB int64
			if err = db.QueryRow(`SELECT source_revision FROM metadata_publication_heads WHERE item_id=?`, a).Scan(&oldA); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow(`SELECT source_revision FROM metadata_publication_heads WHERE item_id=?`, b).Scan(&oldB); err != nil {
				t.Fatal(err)
			}
			persistenceWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
				if err := compactcatalog.UnlinkAssetTx(ctx, tx, a, asset); err != nil {
					return err
				}
				return compactcatalog.LinkAssetTx(ctx, tx, b, asset, compactcatalog.Link{})
			})
			if err = db.QueryRow(`SELECT source_revision FROM metadata_publication_heads WHERE item_id=?`, a).Scan(&nextA); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow(`SELECT source_revision FROM metadata_publication_heads WHERE item_id=?`, b).Scan(&nextB); err != nil {
				t.Fatal(err)
			}
			if nextA <= oldA || nextB <= oldB {
				t.Fatal("moved link did not fence both associations")
			}
			var oldInc, newInc string
			if err = db.QueryRow(`SELECT incarnation FROM metadata_publication_heads WHERE item_id=?`, a).Scan(&oldInc); err != nil {
				t.Fatal(err)
			}
			persistenceWrite(t, db, func(ctx context.Context, tx *sql.Tx) error { return compactcatalog.DeleteEntityTx(ctx, tx, a) })
			var recreated int64
			persistenceWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
				var err error
				recreated, _, _, err = persistenceMovieTx(ctx, tx, library, "/owned", "/owned/a.mp4", "Film", 2024)
				return err
			})
			if err = db.QueryRow(`SELECT incarnation FROM metadata_publication_heads WHERE item_id=?`, recreated).Scan(&newInc); err != nil || newInc == oldInc {
				t.Fatal("item incarnation reused", err)
			}
			var count int
			if err = db.QueryRow(`SELECT count(*) FROM metadata_work WHERE item_id=?`, recreated).Scan(&count); err != nil || count != 0 {
				t.Fatal("deleted work survived", count, err)
			}
		})
	}
}
