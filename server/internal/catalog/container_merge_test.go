package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func albumNamed(t *testing.T, db *sql.DB, title string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT e.id FROM catalog_entities e JOIN catalog_albums a ON a.entity_id=e.id WHERE e.title=?`, title).Scan(&id); err != nil {
		t.Fatalf("album %q: %v", title, err)
	}
	return id
}

func mergeEntities(t *testing.T, db *sql.DB, from, into int64) {
	t.Helper()
	if err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error { return MergeTitlesTx(context.Background(), tx, from, into) }); err != nil {
		t.Fatal(err)
	}
	catalogtest.New(t, db).Drain()
}

// Two editions of one album merge: the shared track becomes one track with two
// versions, the bonus track joins, a favourite moves, and rescanning every
// file leaves them where the merge put them.
func TestAlbumMergeMovesTracksAndHoldsAcrossScans(t *testing.T) {
	s, db := openEmptyAlbumDB(t)
	c := catalogtest.New(t, db)
	standard := func(title, track string) map[string]string {
		return map[string]string{"title": title, "artist": "Band", "album_artist": "Band", "album": "Record", "date": "2001", "track": track}
	}
	deluxe := func(title, track string) map[string]string {
		return map[string]string{"title": title, "artist": "Band", "album_artist": "Band", "album": "Record (Deluxe)", "date": "2001", "track": track}
	}
	commitAlbumSong(t, s, db, "a1", "Record", standard("Opening", "1"), false)
	commitAlbumSong(t, s, db, "b1", "Deluxe", deluxe("Opening", "1"), false)
	commitAlbumSong(t, s, db, "b2", "Deluxe", deluxe("Bonus", "12"), false)
	into, from := albumNamed(t, db, "Record"), albumNamed(t, db, "Record (Deluxe)")
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,favorite,revision) VALUES('p',?,1,1)`, from)
	mergeEntities(t, db, from, into)
	check := func(when string) {
		t.Helper()
		if n := albumCount(t, db); n != 1 {
			t.Fatalf("%s: %d albums", when, n)
		}
		if n := rows(t, c, `SELECT count(*) FROM catalog_songs WHERE album_id=?`, into); n != 2 {
			t.Fatalf("%s: %d songs on the album that stayed", when, n)
		}
		if n := rows(t, c, `SELECT count(*) FROM catalog_asset_links l JOIN catalog_songs x ON x.entity_id=l.entity_id WHERE x.album_id=?`, into); n != 3 {
			t.Fatalf("%s: %d files on the album that stayed", when, n)
		}
	}
	check("after the merge")
	if n := rows(t, c, `SELECT count(*) FROM personal_items WHERE item_id=? AND favorite=1`, into); n != 1 {
		t.Fatal("the favourite did not follow the album")
	}
	commitAlbumSong(t, s, db, "b1", "Deluxe", deluxe("Opening", "1"), false)
	commitAlbumSong(t, s, db, "b2", "Deluxe", deluxe("Bonus", "12"), false)
	commitAlbumSong(t, s, db, "a1", "Record", standard("Opening", "1"), false)
	check("after a rescan")
}

// Two spellings of one artist merge: the albums and song credits move to the
// artist that stays and the other artist goes.
func TestArtistMergeMovesAlbumsAndCredits(t *testing.T) {
	s, db := openEmptyAlbumDB(t)
	c := catalogtest.New(t, db)
	commitAlbumSong(t, s, db, "a1", "One", map[string]string{"title": "Song", "artist": "The Band", "album_artist": "The Band", "album": "One", "date": "2001", "track": "1"}, false)
	commitAlbumSong(t, s, db, "b1", "Two", map[string]string{"title": "Other", "artist": "Band, The", "album_artist": "Band, The", "album": "Two", "date": "2003", "track": "1"}, false)
	var into, from int64
	if err := db.QueryRow(`SELECT entity_id FROM catalog_artists WHERE local_key='the band'`).Scan(&into); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT entity_id FROM catalog_artists WHERE local_key='band, the'`).Scan(&from); err != nil {
		t.Fatal(err)
	}
	mergeEntities(t, db, from, into)
	if n := rows(t, c, `SELECT count(*) FROM catalog_albums WHERE artist_id=?`, into); n != 2 {
		t.Fatalf("%d albums on the artist that stayed", n)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_song_artists WHERE artist_id=?`, into); n != 2 {
		t.Fatalf("%d songs credited to the artist that stayed", n)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_entities WHERE id=?`, from); n != 0 {
		t.Fatal("the merged artist stayed")
	}
	commitAlbumSong(t, s, db, "b1", "Two", map[string]string{"title": "Other", "artist": "Band, The", "album_artist": "Band, The", "album": "Two", "date": "2003", "track": "1"}, false)
	if n := rows(t, c, `SELECT count(*) FROM catalog_artists`); n != 1 {
		t.Fatalf("a rescan brought back a second artist: %d", n)
	}
}

// Two copies of one audiobook merge: the files of the one that goes join the
// one that stays, with its resume point where the stayer has none.
func TestBookMergeMovesFilesAndResume(t *testing.T) {
	db, s := openBookDB(t)
	c := catalogtest.New(t, db)
	commitBookAt(t, s, db, "/books/A/a.m4b", map[string]string{"title": "Part", "album": "Novel", "artist": "Writer"})
	commitBookAt(t, s, db, "/books/B/b.m4b", map[string]string{"title": "Part two", "album": "Novel (Unabridged)", "artist": "Writer"})
	var into, from, file int64
	if err := db.QueryRow(`SELECT id FROM catalog_entities WHERE kind=8 AND title='Novel'`).Scan(&into); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT id FROM catalog_entities WHERE kind=8 AND title='Novel (Unabridged)'`).Scan(&from); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT entity_id FROM catalog_book_files WHERE book_id=?`, from).Scan(&file); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO book_resume(profile_id,book_id,item_id,playback_id,position) VALUES('p',?,?,'x',30)`, from, file)
	mergeEntities(t, db, from, into)
	if n := rows(t, c, `SELECT count(*) FROM catalog_book_files WHERE book_id=?`, into); n != 2 {
		t.Fatalf("%d files on the book that stayed", n)
	}
	if n := rows(t, c, `SELECT count(*) FROM book_resume WHERE book_id=? AND item_id=?`, into, file); n != 1 {
		t.Fatal("the resume point did not follow the book")
	}
	commitBookAt(t, s, db, "/books/B/b.m4b", map[string]string{"title": "Part two", "album": "Novel (Unabridged)", "artist": "Writer"})
	if n := rows(t, c, `SELECT count(*) FROM catalog_books b JOIN catalog_entities e ON e.id=b.entity_id AND e.retired=0`); n != 1 {
		t.Fatalf("a rescan split the merged book again: %d books", n)
	}
}

func openBookDB(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	catalogtest.New(t, db).Library("books", "Audiobooks", "audiobook", "/books")
	return db, New(db)
}

func commitBookAt(t *testing.T, s *Service, db *sql.DB, path string, tags map[string]string) {
	t.Helper()
	c := catalogtest.New(t, db)
	var token string
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, tok, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "mp4", AudioCodec: "aac", Duration: 90})
		token = tok
		return err
	})
	f := assets.Facts{Container: "mp4", Duration: 90, AudioCodec: "aac"}
	assets.CopyEmbeddedAudioTags(&f, tags)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return s.commitAudio(ctx, tx, "books", "audiobook", "/books", token, path, f)
	})
	c.Drain()
}
