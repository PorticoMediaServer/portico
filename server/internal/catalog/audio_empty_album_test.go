package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
)

func commitAlbumSong(t *testing.T, s *Service, db *sql.DB, asset, folder string, tags map[string]string, inventoryOnly bool) {
	t.Helper()
	path := "/music/" + folder + "/" + asset + ".flac"
	c := catalogtest.New(t, db)
	var assetToken string
	c.Write(func(ctx context.Context, writeTx *sql.Tx) error {
		_, token, err := compactcatalog.UpsertAssetTx(ctx, writeTx, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "flac", AudioCodec: "flac", Duration: 60})
		assetToken = token
		return err
	})
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	f := assets.Facts{Container: "flac", Duration: 60, InventoryOnly: inventoryOnly}
	if !inventoryOnly {
		f.AudioCodec = "flac"
	}
	if tags != nil {
		assets.CopyEmbeddedAudioTags(&f, tags)
	}
	if err = s.commitAudio(context.Background(), tx, "lib", "music", "/music", assetToken, path, f); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	c.Drain()
}

func openEmptyAlbumDB(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	catalogtest.New(t, db).Library("lib", "Music", "music", "/music")
	return New(db), db
}

func albumCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_albums`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// NEW-30: a tagless first import keys a phantom album ("Unknown artist", year
// 0); the tagged pass must leave exactly one album (the tagged one) with the
// song on it, and no "Unknown artist" artist row.
func TestTaglessFirstImportThenTagsLeavesOneAlbum(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, db := openEmptyAlbumDB(t)
	commitAlbumSong(t, s, db, "track1", "Cool Album", nil, true)
	var oldID int64
	if err := db.QueryRow(`SELECT entity_id FROM catalog_albums`).Scan(&oldID); err != nil {
		t.Fatal("tagless pass created no album", err)
	}
	oldPublic, err := entityid.Public(context.Background(), db, oldID)
	if err != nil {
		t.Fatal(err)
	}
	var unknown int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_artists WHERE local_key='unknown artist'`).Scan(&unknown); err != nil || unknown != 1 {
		t.Fatal("tagless setup missing unknown-artist phantom", unknown, err)
	}
	commitAlbumSong(t, s, db, "track1", "Cool Album", map[string]string{
		"title": "Track One", "artist": "Real Artist", "album": "Cool Album",
		"album_artist": "Real Artist", "date": "2021",
	}, false)
	if n := albumCount(t, db); n != 1 {
		t.Fatalf("albums=%d, want exactly the tagged one", n)
	}
	var title string
	var year int
	var newID int64
	if err := db.QueryRow(`SELECT e.id,e.title,e.year FROM catalog_albums a JOIN catalog_entities e ON e.id=a.entity_id`).Scan(&newID, &title, &year); err != nil || newID == oldID || title != "Cool Album" || year != 2021 {
		t.Fatalf("phantom not replaced by tagged album: %d %q %d %v", newID, title, year, err)
	}
	var songs int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_songs s JOIN catalog_entities a ON a.id=s.album_id WHERE a.title='Cool Album' AND a.year=2021`).Scan(&songs); err != nil || songs != 1 {
		t.Fatalf("song not on tagged album: %d %v", songs, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM catalog_artists WHERE local_key='unknown artist'`).Scan(&unknown); err != nil || unknown != 0 {
		t.Fatalf("unknown-artist phantom remains: %d %v", unknown, err)
	}
	// The album page opened while the scan was still reading tags keeps working:
	// its id leads to the tagged album, through the path, body and query resolver.
	if got, err := entityid.Resolve(context.Background(), db, oldPublic); err != nil || got != newID {
		t.Fatalf("the first album id leads to %d (%v), want %d", got, err, newID)
	}
	newPublic, _ := entityid.Public(context.Background(), db, newID)
	if got := s.SurvivingTitleID(context.Background(), oldPublic); got != newPublic {
		t.Fatalf("surviving id %q, want %q", got, newPublic)
	}
	if err := db.QueryRow(`SELECT count(*) FROM catalog_identities i JOIN catalog_entities e ON e.public_id=i.public_id JOIN catalog_albums a ON a.entity_id=e.id WHERE e.id=?`, newID).Scan(&songs); err != nil || songs != 1 {
		t.Fatalf("tagged album identity rows=%d %v", songs, err)
	}
}

// NEW-30: the same sequence, but the first album carries personal state — it
// is kept (still empty) alongside the new tagged album, and nothing else
// changes.
func TestEmptiedAlbumWithStateIsKept(t *testing.T) {
	s, db := openEmptyAlbumDB(t)
	commitAlbumSong(t, s, db, "track1", "Cool Album", nil, true)
	var oldID int64
	if err := db.QueryRow(`SELECT entity_id FROM catalog_albums`).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO container_personal_state VALUES('profile','album',?,0,'',1,'')`, oldID); err != nil {
		t.Fatal(err)
	}
	commitAlbumSong(t, s, db, "track1", "Cool Album", map[string]string{
		"title": "Track One", "artist": "Real Artist", "album": "Cool Album",
		"album_artist": "Real Artist", "date": "2021",
	}, false)
	if n := albumCount(t, db); n != 2 {
		t.Fatalf("albums=%d, want the kept empty album plus the tagged one", n)
	}
	var kept int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_albums a JOIN catalog_entities e ON e.id=a.entity_id WHERE e.id=? AND e.year=0`, oldID).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("state-carrying album not kept intact: %d %v", kept, err)
	}
	var empty int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_songs WHERE album_id=?`, oldID).Scan(&empty); err != nil || empty != 0 {
		t.Fatalf("kept album should be empty: %d %v", empty, err)
	}
	var moved int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_songs s JOIN catalog_entities a ON a.id=s.album_id WHERE a.id<>? AND a.year=2021`, oldID).Scan(&moved); err != nil || moved != 1 {
		t.Fatalf("song not on tagged album: %d %v", moved, err)
	}
}

// NEW-30: a manual MusicBrainz pin on the first album holds the song there —
// the pinned album is kept and the pin stays intact.
func TestEmptiedPinnedAlbumIsKept(t *testing.T) {
	s, db := openEmptyAlbumDB(t)
	commitAlbumSong(t, s, db, "track1", "Cool Album", nil, true)
	var oldID int64
	if err := db.QueryRow(`SELECT entity_id FROM catalog_albums`).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE mb_jobs SET manual=1 WHERE kind='album' AND entity_id=?`, oldID); err != nil {
		t.Fatal(err)
	}
	var pinned int
	if err := db.QueryRow(`SELECT count(*) FROM mb_jobs WHERE kind='album' AND entity_id=? AND manual=1`, oldID).Scan(&pinned); err != nil || pinned != 1 {
		t.Fatal("manual pin setup missing", pinned, err)
	}
	commitAlbumSong(t, s, db, "track1", "Cool Album", map[string]string{
		"title": "Track One", "artist": "Real Artist", "album": "Cool Album",
		"album_artist": "Real Artist", "date": "2021",
	}, false)
	if n := albumCount(t, db); n != 1 {
		t.Fatalf("albums=%d, want only the pinned album", n)
	}
	var id int64
	var onPinned, manual int
	if err := db.QueryRow(`SELECT entity_id FROM catalog_albums`).Scan(&id); err != nil || id != oldID {
		t.Fatalf("pinned album not kept: %d %v", id, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM catalog_songs WHERE album_id=?`, oldID).Scan(&onPinned); err != nil || onPinned != 1 {
		t.Fatalf("pinned song moved: %d %v", onPinned, err)
	}
	if err := db.QueryRow(`SELECT manual FROM mb_jobs WHERE kind='album' AND entity_id=?`, oldID).Scan(&manual); err != nil || manual != 1 {
		t.Fatalf("manual pin lost: %d %v", manual, err)
	}
}
