package persistence

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

type originFixture struct {
	t                  *testing.T
	db                 *sql.DB
	libraryA, libraryB int64
	one, two           int64
	asset              int64
	token              string
}

func newOriginFixture(t *testing.T, recursive bool) originFixture {
	t.Helper()
	db, err := Open(freshDatabaseCopy(t, t.TempDir(), "origins.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if recursive {
		originExec(t, db, `PRAGMA recursive_triggers=ON`)
	} else {
		originExec(t, db, `PRAGMA recursive_triggers=OFF`)
	}
	f := originFixture{t: t, db: db}
	f.libraryA = persistenceLibrary(t, db, "a", "A", "movie", "/a")
	f.libraryB = persistenceLibrary(t, db, "b", "B", "movie", "/b")
	persistenceWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		f.one, _, err = compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: f.libraryA, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/a", "/a/one.mp4", 0), Title: "One"})
		if err != nil {
			return err
		}
		f.two, _, err = compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: f.libraryA, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/a", "/a/two.mp4", 0), Title: "Two"})
		if err != nil {
			return err
		}
		f.asset, f.token, err = compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/a/movie.mkv", Size: 10, ModifiedNS: 1, Container: "mkv"})
		if err != nil {
			return err
		}
		if err = compactcatalog.LinkAssetTx(ctx, tx, f.one, f.asset, compactcatalog.Link{}); err != nil {
			return err
		}
		return compactcatalog.LinkAssetTx(ctx, tx, f.two, f.asset, compactcatalog.Link{})
	})
	return f
}

func originExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(q, err)
	}
}

func (f originFixture) exec(q string, args ...any) { originExec(f.t, f.db, q, args...) }

func (f originFixture) text(q string, args ...any) string {
	f.t.Helper()
	var value string
	if err := f.db.QueryRow(q, args...).Scan(&value); err != nil {
		f.t.Fatal(q, err)
	}
	return value
}

func (f originFixture) rev(table, predicate string, args ...any) int64 {
	f.t.Helper()
	var value int64
	if err := f.db.QueryRow("SELECT revision FROM playback_origin_"+table+" WHERE "+predicate, args...).Scan(&value); err != nil {
		f.t.Fatal(err)
	}
	return value
}

func TestPlaybackOriginsRootAndSharedAssetABA(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		t.Run(map[bool]string{false: "recursive_off", true: "recursive_on"}[recursive], func(t *testing.T) {
			f := newOriginFixture(t, recursive)
			root, asset := f.rev("roots", "id=?", "a"), f.rev("assets", "id=?", f.token)
			one, two := f.rev("associations", "item_id=?", f.one), f.rev("associations", "item_id=?", f.two)
			f.exec(`UPDATE libraries SET root='/moved' WHERE id='a'`)
			f.exec(`UPDATE libraries SET root='/a' WHERE id='a'`)
			if f.rev("roots", "id=?", "a") != root+2 {
				t.Fatal("root ABA did not advance durable fence")
			}
			persistenceWrite(t, f.db, func(ctx context.Context, tx *sql.Tx) error {
				if err := compactcatalog.SetAssetAvailableTx(ctx, tx, f.asset, false); err != nil {
					return err
				}
				return compactcatalog.SetAssetAvailableTx(ctx, tx, f.asset, true)
			})
			if f.rev("assets", "id=?", f.token) != asset+2 {
				t.Fatal("shared asset availability ABA lost")
			}
			if f.rev("associations", "item_id=?", f.one) != one || f.rev("associations", "item_id=?", f.two) != two {
				t.Fatal("asset change unnecessarily rewrote each association head")
			}
			persistenceWrite(t, f.db, func(ctx context.Context, tx *sql.Tx) error {
				if _, err := tx.Exec(`UPDATE libraries SET name='Renamed' WHERE id='a'`); err != nil {
					return err
				}
				return compactcatalog.SetFieldsTx(ctx, tx, f.one, compactcatalog.Automatic, map[string]any{"title": "Better title"})
			})
			if f.rev("roots", "id=?", "a") != root+2 || f.rev("items", "id=?", f.one) != 1 {
				t.Fatal("unrelated display metadata invalidated source selection")
			}
		})
	}
}

func TestPlaybackOriginsAssociationLifetimesAndBoundary(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		t.Run(map[bool]string{false: "recursive_off", true: "recursive_on"}[recursive], func(t *testing.T) {
			f := newOriginFixture(t, recursive)
			old := f.text(`SELECT incarnation FROM playback_origin_associations WHERE item_id=? AND asset_id=?`, f.one, f.token)
			persistenceWrite(t, f.db, func(ctx context.Context, tx *sql.Tx) error {
				if err := compactcatalog.UnlinkAssetTx(ctx, tx, f.one, f.asset); err != nil {
					return err
				}
				return compactcatalog.LinkAssetTx(ctx, tx, f.one, f.asset, compactcatalog.Link{})
			})
			if f.text(`SELECT incarnation FROM playback_origin_associations WHERE item_id=? AND asset_id=?`, f.one, f.token) == old {
				t.Fatal("identical reinsert reused lifetime")
			}
			old = f.text(`SELECT incarnation FROM playback_origin_associations WHERE item_id=? AND asset_id=?`, f.one, f.token)
			originExec(t, f.db, `INSERT OR REPLACE INTO catalog_asset_links(entity_id,asset_id,part_index,available,start_seconds,end_seconds) VALUES(?,?,0,1,0,NULL)`, f.one, f.asset)
			if f.text(`SELECT incarnation FROM playback_origin_associations WHERE item_id=? AND asset_id=?`, f.one, f.token) == old {
				t.Fatal("REPLACE reused the association lifetime", recursive)
			}
			r := f.rev("associations", "item_id=? AND asset_id=?", f.one, f.token)
			originExec(t, f.db, `UPDATE catalog_asset_links SET end_seconds=0 WHERE entity_id=? AND asset_id=?`, f.one, f.asset)
			originExec(t, f.db, `UPDATE catalog_asset_links SET end_seconds=NULL WHERE entity_id=? AND asset_id=?`, f.one, f.asset)
			originExec(t, f.db, `INSERT INTO episode_asset_boundaries(item_id,asset_id,status) VALUES(?,?,'whole_source')`, f.one, f.token)
			originExec(t, f.db, `UPDATE episode_asset_boundaries SET status='unknown_multi_episode' WHERE item_id=? AND asset_id=?`, f.one, f.token)
			originExec(t, f.db, `DELETE FROM episode_asset_boundaries WHERE item_id=? AND asset_id=?`, f.one, f.token)
			if f.rev("associations", "item_id=? AND asset_id=?", f.one, f.token) != r+5 {
				t.Fatal("nullable bounds or episode availability history lost")
			}
			old = f.text(`SELECT incarnation FROM playback_origin_associations WHERE item_id=? AND asset_id=?`, f.one, f.token)
			persistenceWrite(t, f.db, func(ctx context.Context, tx *sql.Tx) error {
				if err := compactcatalog.UnlinkAssetTx(ctx, tx, f.one, f.asset); err != nil {
					return err
				}
				return compactcatalog.LinkAssetTx(ctx, tx, f.two, f.asset, compactcatalog.Link{})
			})
			if f.text(`SELECT count(*) FROM playback_origin_associations WHERE item_id=? AND asset_id=?`, f.one, f.token) != "0" || f.text(`SELECT incarnation FROM playback_origin_associations WHERE item_id=? AND asset_id=?`, f.two, f.token) == old {
				t.Fatal("ownership move retained old association")
			}
		})
	}
}

func TestPlaybackOriginsParentAndRelationOwnership(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		t.Run(map[bool]string{false: "recursive_off", true: "recursive_on"}[recursive], func(t *testing.T) {
			f := newOriginFixture(t, recursive)
			var show, season, episode int64
			persistenceWrite(t, f.db, func(ctx context.Context, tx *sql.Tx) error {
				var err error
				show, err = persistenceShowTx(ctx, tx, f.libraryA, "show", "Show", 2000)
				if err != nil {
					return err
				}
				episode, err = persistenceEpisodeTx(ctx, tx, f.libraryA, show, "show", 1, 1, "Episode")
				if err != nil {
					return err
				}
				return tx.QueryRow(`SELECT entity_id FROM catalog_seasons WHERE show_id=? AND number=1`, show).Scan(&season)
			})
			item := f.rev("items", "id=?", episode)
			f.exec(`UPDATE catalog_episodes SET ordering_basis='dvd' WHERE entity_id=?`, episode)
			if f.rev("items", "id=?", episode) != item+1 {
				t.Fatal("episode ordering did not invalidate item")
			}
			persistenceWrite(t, f.db, func(ctx context.Context, tx *sql.Tx) error {
				if _, err := tx.Exec(`UPDATE catalog_entities SET library_id=(SELECT id FROM catalog_libraries WHERE library_id='b') WHERE id=?`, show); err != nil {
					return err
				}
				_, err := tx.Exec(`UPDATE catalog_seasons SET number=2 WHERE entity_id=?`, season)
				return err
			})
			if f.rev("shows", "id=?", show) != 2 || f.rev("seasons", "id=?", season) != 2 || f.rev("items", "id=?", episode) != item+1 {
				t.Fatal("parent fences failed or fan-out introduced")
			}

			var book, part int64
			persistenceWrite(t, f.db, func(ctx context.Context, tx *sql.Tx) error {
				var err error
				book, _, err = compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: f.libraryA, Kind: compactcatalog.Book, Key: compactcatalog.BookKey("book"), Title: "Book"})
				if err != nil {
					return err
				}
				if err = compactcatalog.SetFieldsTx(ctx, tx, book, compactcatalog.Automatic, map[string]any{"library_id": f.libraryA, "local_key": "book"}); err != nil {
					return err
				}
				part, _, err = compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: f.libraryA, Kind: compactcatalog.Part, Parent: book, Key: "part:book/1", Title: "Part"})
				if err != nil {
					return err
				}
				return compactcatalog.SetFieldsTx(ctx, tx, part, compactcatalog.Automatic, map[string]any{"book_id": book, "part_number": 1})
			})
			item = f.rev("items", "id=?", part)
			f.exec(`UPDATE catalog_book_files SET part_number=0 WHERE entity_id=?`, part)
			f.exec(`UPDATE catalog_book_files SET part_number=NULL WHERE entity_id=?`, part)
			if f.rev("items", "id=?", part) != item+2 {
				t.Fatal("book nullable part changes lost")
			}

			var artist, album, track int64
			persistenceWrite(t, f.db, func(ctx context.Context, tx *sql.Tx) error {
				var err error
				artist, _, err = compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: f.libraryA, Kind: compactcatalog.Artist, Key: compactcatalog.ArtistKey("artist"), Title: "Artist"})
				if err != nil {
					return err
				}
				if err = compactcatalog.SetFieldsTx(ctx, tx, artist, compactcatalog.Automatic, map[string]any{"local_key": "artist"}); err != nil {
					return err
				}
				album, _, err = compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: f.libraryA, Kind: compactcatalog.Album, Parent: artist, Key: compactcatalog.AlbumKey("album"), Title: "Album"})
				if err != nil {
					return err
				}
				if err = compactcatalog.SetFieldsTx(ctx, tx, album, compactcatalog.Automatic, map[string]any{"artist_id": artist, "local_key": "album"}); err != nil {
					return err
				}
				track, _, err = compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: f.libraryA, Kind: compactcatalog.Track, Parent: album, Key: "track:album/1", Title: "Track"})
				if err != nil {
					return err
				}
				return compactcatalog.SetFieldsTx(ctx, tx, track, compactcatalog.Automatic, map[string]any{"album_id": album, "track_number": 0})
			})
			item = f.rev("items", "id=?", track)
			persistenceWrite(t, f.db, func(ctx context.Context, tx *sql.Tx) error {
				if _, err := tx.Exec(`UPDATE catalog_songs SET track_number=1 WHERE entity_id=?`, track); err != nil {
					return err
				}
				_, err := tx.Exec(`UPDATE catalog_entities SET library_id=(SELECT id FROM catalog_libraries WHERE library_id='b') WHERE id=?`, album)
				return err
			})
			if f.rev("items", "id=?", track) != item+1 || f.rev("albums", "id=?", album) != 2 {
				t.Fatal("song or album scope not fenced")
			}
		})
	}
}

func TestPlaybackOriginsRollbackAndRevisionIntegrity(t *testing.T) {
	f := newOriginFixture(t, true)
	before := f.rev("assets", "id=?", f.token)
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE catalog_assets SET size=99 WHERE id=?`, f.asset); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if f.rev("assets", "id=?", f.token) != before || f.text(`SELECT size FROM catalog_assets WHERE id=?`, f.asset) != "10" {
		t.Fatal("rolled-back mutation leaked")
	}
	for _, q := range []string{`UPDATE playback_origin_assets SET revision=revision-1`, `UPDATE playback_origin_assets SET incarnation=lower(hex(randomblob(16)))`, `UPDATE playback_origin_assets SET revision=revision`} {
		if _, err = f.db.Exec(q); err == nil {
			t.Fatal("invalid direct head mutation accepted", q)
		}
	}
	// Synthetic near-overflow state: valid retained catalog history, not billions
	// of application mutations. The next real source mutation must fail atomically.
	f.exec(`DELETE FROM playback_origin_assets WHERE id=?`, f.token)
	f.exec(`INSERT INTO playback_origin_assets(id,revision) VALUES(?,9223372036854775807)`, f.token)
	if _, err = f.db.Exec(`UPDATE catalog_assets SET size=11 WHERE id=?`, f.asset); err == nil {
		t.Fatal("revision overflow accepted")
	}
	if f.text(`SELECT size FROM catalog_assets WHERE id=?`, f.asset) != "10" || f.rev("assets", "id=?", f.token) != 9223372036854775807 {
		t.Fatal("overflow changed retained source state")
	}
}
