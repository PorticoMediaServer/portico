package playback

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestAlbumLoudnessReadsCompactSiblingAndAssetFacts(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "audio.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalogtest.New(t, db)
	library := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(library, "Artist")
	album := c.Album(artist, "Album", 2020)
	songA := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Track, Parent: album.ID, Key: "a", Title: "A"}, map[string]any{"album_id": album.ID})
	songB := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Track, Parent: album.ID, Key: "b", Title: "B"}, map[string]any{"album_id": album.ID})
	var tokenA, tokenB string
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		assetA, createdTokenA, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/music/a", Size: 1, ModifiedNS: 1, AudioCodec: "flac", Duration: 100})
		if err != nil {
			return err
		}
		assetB, createdTokenB, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/music/b", Size: 1, ModifiedNS: 1, AudioCodec: "flac", Duration: 200})
		if err != nil {
			return err
		}
		tokenA, tokenB = createdTokenA, createdTokenB
		if err := compactcatalog.LinkAssetTx(ctx, tx, songA.ID, assetA, compactcatalog.Link{}); err != nil {
			return err
		}
		return compactcatalog.LinkAssetTx(ctx, tx, songB.ID, assetB, compactcatalog.Link{})
	})
	c.Drain()
	itemA := songA.Public
	for _, query := range []string{
		`INSERT INTO analysis_results(id,object_id,asset_id,source_revision,root_incarnation,configuration_generation,policy_revision,stage,algorithm,evidence,tool_digest,duration_us,summary_json,created_ms) VALUES('ra','oa','` + tokenA + `','1','1',1,1,'loudness','EBU-R128','','',100000000,'{"integratedLUFS":-14,"truePeakLinear":0.5}',1),('rb','ob','` + tokenB + `','1','1',1,1,'loudness','EBU-R128','','',200000000,'{"integratedLUFS":-20,"truePeakLinear":0.7}',1)`,
	} {
		if _, err = db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	read := func() (float64, float64, bool, error) {
		tx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		return albumLoudness(context.Background(), tx, itemA)
	}
	lufs, peak, okay, err := read()
	want := 10 * math.Log10((100*math.Pow(10, -14.0/10)+200*math.Pow(10, -20.0/10))/300)
	if err != nil || !okay || math.Abs(lufs-want) > 1e-9 || peak != 0.7 {
		t.Fatalf("compact sibling loudness %v %v %v %v", lufs, peak, okay, err)
	}
	// Facts are synchronous: an updated duration reads on the next call.
	updateCatalogAsset(t, db, tokenB, func(a *compactcatalog.Asset) { a.Duration = 300 })
	lufs, _, okay, err = read()
	want = 10 * math.Log10((100*math.Pow(10, -14.0/10)+300*math.Pow(10, -20.0/10))/400)
	if err != nil || !okay || math.Abs(lufs-want) > 1e-9 {
		t.Fatalf("updated compact duration %v want %v: %v", lufs, want, err)
	}
}
