package compactcatalog

import (
	"context"
	"database/sql"
	"testing"
)

// Kinds are data: a kind the code has never heard of, added as one catalog_kinds
// row, is searchable, browsable and available like any shipped kind, through
// the generic paths alone.
func TestFutureKindNeedsNoCode(t *testing.T) {
	db := cc1OpenDB(t, "future.sqlite")
	cc1Exec(t, db, `INSERT INTO catalog_kinds VALUES(99,'fixture',1,0,1,1,0)`)
	library := cc1Library(t, db, "photos", "Photos", "movie", "/photos")
	const fixture Kind = 99
	id := cc1Entity(t, db, Entity{Library: library, Kind: fixture, Key: "file:beach.jpg", Title: "Beach at dusk", Year: 2024, Added: "2026-01-02T03:04:05.000Z"}, nil)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		if err := SetAttributesTx(ctx, tx, id, "contentRating", []string{"PG"}); err != nil {
			return err
		}
		asset, _, err := UpsertAssetTx(ctx, tx, Asset{Path: "/photos/beach.jpg", Size: 1, ModifiedNS: 1, Container: "jpg"})
		if err != nil {
			return err
		}
		return LinkAssetTx(ctx, tx, id, asset, Link{})
	})
	cc1Drain(t, db)
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_search_titles WHERE catalog_search_titles MATCH 'beach' AND rowid=?`, id); n != 1 {
		t.Fatalf("fixture kind is not searchable: %d", n)
	}
	var kind, available int
	var ratingKey string
	if err := db.QueryRow(`SELECT kind,available,rating_key FROM catalog_browse_rows WHERE entity_id=?`, id).Scan(&kind, &available, &ratingKey); err != nil {
		t.Fatalf("fixture kind has no browse row: %v", err)
	}
	if kind != 99 || available != 1 || ratingKey != "pg" {
		t.Fatalf("fixture browse row: kind=%d available=%d rating=%q", kind, available, ratingKey)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_buckets WHERE library_id=? AND kind=99 AND axis=0 AND total>0`, library); n == 0 {
		t.Fatal("fixture kind has no browse buckets")
	}
}

// The identity ledger: deleting a library and re-adding the same folder gives
// its entities their old public ids back.
func TestReaddedLibraryKeepsPublicIDs(t *testing.T) {
	db := cc1OpenDB(t, "readd.sqlite")
	first := cc1Library(t, db, "films", "Films", "movie", "/films")
	movie := cc1Entity(t, db, Entity{Library: first, Kind: Movie, Key: ItemKey("/films", "/films/Heat (1995)/Heat.mkv", 0), Title: "Heat", Year: 1995}, nil)
	var before []byte
	if err := db.QueryRow(`SELECT public_id FROM catalog_entities WHERE id=?`, movie).Scan(&before); err != nil {
		t.Fatal(err)
	}
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error { return DeleteEntityTx(ctx, tx, movie) })
	// The library itself goes (with everything that hangs off it); the ledger
	// row does not.
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`PRAGMA foreign_keys=OFF`, `DELETE FROM catalog_libraries WHERE library_id='films'`, `DELETE FROM libraries WHERE id='films'`, `PRAGMA foreign_keys=ON`} {
		if _, err = conn.ExecContext(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	conn.Close()
	again := cc1Library(t, db, "films-2", "Films again", "movie", "/films")
	back := cc1Entity(t, db, Entity{Library: again, Kind: Movie, Key: ItemKey("/films", "/films/Heat (1995)/Heat.mkv", 0), Title: "Heat", Year: 1995}, nil)
	var after []byte
	if err := db.QueryRow(`SELECT public_id FROM catalog_entities WHERE id=?`, back).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("a re-added library's movie got a new public id")
	}
	// A different folder is a different library: new ids.
	other := cc1Library(t, db, "films-3", "Other", "movie", "/elsewhere")
	fresh := cc1Entity(t, db, Entity{Library: other, Kind: Movie, Key: ItemKey("/elsewhere", "/elsewhere/Heat (1995)/Heat.mkv", 0), Title: "Heat", Year: 1995}, nil)
	var third []byte
	if err := db.QueryRow(`SELECT public_id FROM catalog_entities WHERE id=?`, fresh).Scan(&third); err != nil {
		t.Fatal(err)
	}
	if string(third) == string(before) {
		t.Fatal("a different folder reused another library's public id")
	}
}
