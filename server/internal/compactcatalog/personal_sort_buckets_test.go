package compactcatalog

import (
	"context"
	"database/sql"
	"testing"
)

func drainPersonalSort(t *testing.T, db *sql.DB, limit int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		n, err := StepPersonalSortBuckets(context.Background(), tx, limit)
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
	t.Fatal("personal sort worker did not drain")
}

func TestPersonalSortLateBrowseRowKeepsSourceRating(t *testing.T) {
	db := cc1OpenDB(t, "personal-late-browse.sqlite")
	lib := cc1Library(t, db, "music", "Music", "music", "/music")
	artist := cc1Entity(t, db, Entity{Library: lib, Kind: Artist, Key: "artist:late", Title: "Artist"}, map[string]any{"local_key": "artist:late"})
	album := cc1Entity(t, db, Entity{Library: lib, Kind: Album, Parent: artist, Key: "album:late", Title: "Album", Year: 2000}, map[string]any{"artist_id": artist, "local_key": "album:late"})
	cc1Entity(t, db, Entity{Library: lib, Kind: Track, Parent: album, Key: "track:late", Title: "Song"}, map[string]any{"album_id": album, "track_number": 1})
	cc1Exec(t, db, `INSERT INTO personal_items(profile_id,item_id,favorite,rating,revision,last_played_at) VALUES('profile',?,1,9,1,'2026-01-01T00:00:00.000Z')`, album)
	// The personal item stream can run before the album has a browse row: it
	// finds no entity to reconcile and drops its job. The derived row insert's
	// entity job must then rebuild the member from the personal source, not
	// only refresh members that already exist.
	drainPersonalSort(t, db, 2)
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_rows WHERE entity_id=?`, album); got != 0 {
		t.Fatalf("album browse row existed before derivation: %d", got)
	}
	cc1Drain(t, db)
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_rows WHERE entity_id=?`, album); got != 1 {
		t.Fatalf("album browse row not derived: %d", got)
	}
	var rating int
	if err := db.QueryRow(`SELECT rating FROM catalog_personal_sort_rows WHERE profile_id='profile' AND entity_id=?`, album).Scan(&rating); err != nil || rating != 9 {
		t.Fatalf("container rating %d %v", rating, err)
	}
	if got := cc1Scalar(t, db, `SELECT total FROM catalog_personal_sort_buckets WHERE axis=7 AND value='9'`); got != 1 {
		t.Fatalf("container rating bucket: %d", got)
	}
}

func TestPersonalSortBucketsAndClassIntersection(t *testing.T) {
	db := cc1OpenDB(t, "personal.sqlite")
	lib := cc1Library(t, db, "films", "Films", "movie", "/films")
	movieA := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "file:a.mkv", Title: "Alpha"}, nil)
	cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "file:b.mkv", Title: "Beta"}, nil)
	cc1Drain(t, db)
	class := cc1Class(t, db, lib, "policy")
	cc1Visible(t, db, class, movieA, 1)
	cc1Exec(t, db, `INSERT INTO personal_items(profile_id,item_id,rating,revision,last_played_at) VALUES('profile',?,9,1,'2026-09-24T12:34:00Z')`, movieA)
	drainPersonalSort(t, db, 2)
	if got := cc1Scalar(t, db, `SELECT total FROM catalog_personal_sort_buckets WHERE axis=7 AND value='9'`); got != 1 {
		t.Fatalf("personal rating bucket: %d", got)
	}
	if got := cc1Scalar(t, db, `SELECT total FROM catalog_personal_sort_buckets WHERE axis=8 AND value='2026-09'`); got != 1 {
		t.Fatalf("personal month bucket: %d", got)
	}
	if got := cc1Scalar(t, db, `SELECT total FROM catalog_personal_visibility_sort_buckets WHERE axis=7 AND value='9'`); got != 1 {
		t.Fatalf("class personal rating bucket: %d", got)
	}
	cc1Exec(t, db, `UPDATE personal_items SET rating=5,last_played_at='2025-01-02T00:00:00Z',revision=2 WHERE profile_id='profile' AND item_id=?`, movieA)
	drainPersonalSort(t, db, 2)
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_personal_sort_buckets WHERE axis=7 AND value='9'`); got != 0 {
		t.Fatalf("old rating bucket retained: %d", got)
	}
	if got := cc1Scalar(t, db, `SELECT total FROM catalog_personal_visibility_sort_buckets WHERE axis=8 AND value='2025-01'`); got != 1 {
		t.Fatalf("class personal month not refreshed: %d", got)
	}
	cc1Exec(t, db, `DELETE FROM personal_items WHERE profile_id='profile' AND item_id=?`, movieA)
	drainPersonalSort(t, db, 2)
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_personal_sort_buckets`); got != 0 {
		t.Fatalf("deleted personal state retained buckets: %d", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM catalog_personal_visibility_sort_buckets`); got != 0 {
		t.Fatalf("deleted personal class state retained buckets: %d", got)
	}
}
