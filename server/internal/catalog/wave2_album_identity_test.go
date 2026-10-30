package catalog

import (
	"context"
	"database/sql"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
)

func TestWave2PhysicalAlbumEnrichesMissingEditionWithoutMergingConflicts(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown-first", true: "known-first"}[reverse], func(t *testing.T) {
			db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			c := catalogtest.New(t, db)
			c.Library("lib", "Music", "music", "/music")
			s := New(db)
			add := func(id, folder, release string) {
				t.Helper()
				path := "/music/" + folder + "/" + id + ".flac"
				var assetToken string
				c.Write(func(ctx context.Context, tx *sql.Tx) error {
					_, token, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "flac", AudioCodec: "flac", Duration: 60})
					assetToken = token
					return err
				})
				tx, e := db.Begin()
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback()
				f := assets.Facts{Container: "flac", AudioCodec: "flac", Duration: 60}
				assets.CopyEmbeddedAudioTags(&f, map[string]string{"title": id, "album": "Compilation", "album_artist": "Various Artists", "artist": id, "musicbrainz_albumid": release})
				if e = s.commitAudio(context.Background(), tx, "lib", "music", "/music", assetToken, path, f); e != nil {
					t.Fatal(e)
				}
				if e = tx.Commit(); e != nil {
					t.Fatal(e)
				}
				c.Drain()
			}
			release := "11111111-1111-1111-1111-111111111111"
			if reverse {
				add("one", "Album", release)
				add("two", "Album", "")
			} else {
				add("one", "Album", "")
				add("two", "Album", release)
			}
			var n int
			if err = db.QueryRow(`SELECT count(*) FROM catalog_albums`).Scan(&n); err != nil || n != 1 {
				t.Fatal("missing tags split physical album", n, err)
			}
			// A different known release in the same folder is a different
			// album. The same release in another folder is the same album: a
			// release identifier outranks the folder, so a multi-disc set kept
			// in two folders stays one album.
			add("three", "Album", "22222222-2222-2222-2222-222222222222")
			add("four", "Other", release)
			if err = db.QueryRow(`SELECT count(*) FROM catalog_albums`).Scan(&n); err != nil || n != 2 {
				t.Fatal("conflicting release IDs merged or the same release split across folders", n, err)
			}
			var largest, smallest int
			if err = db.QueryRow(`SELECT max(n),min(n) FROM (SELECT count(*) n FROM catalog_songs GROUP BY album_id)`).Scan(&largest, &smallest); err != nil || largest != 3 || smallest != 1 {
				t.Fatal("the tracks of one release are not together, apart from the other release", largest, smallest, err)
			}
		})
	}
}
