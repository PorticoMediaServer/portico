package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Hosted nightly only: exercise the real cutover, rather than lowering a
// production capacity to make a tiny fixture enter the large-library path.
func TestMusicRadioIndexedPoolBoundAndRotation(t *testing.T) {
	if testing.Short() {
		t.Skip("large fixture runs in hosted nightly")
	}
	c := catalogtest.Open(t)
	lib := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(lib, "Artist")
	album := c.Album(artist, "Album", 2026)
	ids := []int64{}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < recommendationPoolLimit+200; i++ {
			path := fmt.Sprintf("/music/%04d.flac", i)
			id, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: lib, Kind: compactcatalog.Track, Parent: album.ID, Key: compactcatalog.ItemKey("/music", path, 0), Title: path, Added: "2026-01-01T00:00:00Z"})
			if err != nil {
				return err
			}
			if err = compactcatalog.SetFactsTx(ctx, tx, id, map[string]any{"album_id": album.ID, "track_number": i + 1}); err != nil {
				return err
			}
			token := compactcatalog.NewAssetToken()
			asset, err := compactcatalog.CreateAssetTx(ctx, tx, token, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "flac", AudioCodec: "flac", Duration: 2}, true)
			if err != nil {
				return err
			}
			if err = compactcatalog.LinkAssetTx(ctx, tx, id, asset, compactcatalog.Link{}); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) SELECT ?,id,?,incarnation,?,'r','{}',1,1 FROM library_sources WHERE library_id='music' LIMIT 1`, fmt.Sprint(id), token, fmt.Sprintf("%04d.flac", i)); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Libraries: []string{"music"}}
	read := func(pivot int64) []string {
		t.Helper()
		pool, err := s.radioPool(viewer, ids[0], pivot, nil, nil)
		if err != nil || len(pool) == 0 || len(pool) > recommendationPoolLimit || len(pool) >= len(ids) {
			t.Fatal("unbounded/empty pool", len(pool), err)
		}
		seen := map[string]bool{}
		for _, id := range pool {
			if seen[id] {
				t.Fatal("duplicate", id)
			}
			seen[id] = true
		}
		if !seen[c.Public(ids[0])] {
			t.Fatal("start song lost")
		}
		return pool
	}
	first := read(ids[0])
	second := read(ids[recommendationPoolLimit/2])
	if reflect.DeepEqual(first, second) {
		t.Fatal("refill reused a fixed song head")
	}
	// A long unavailable prefix must not hide the remaining authorized songs.
	c.Exec(`UPDATE inventory_objects SET state='unavailable' WHERE CAST(id AS INTEGER)<?`, ids[recommendationPoolLimit])
	third := read(ids[0])
	if len(third) != 201 {
		t.Fatal("eligible index window did not cross unavailable prefix", len(third))
	}
}
