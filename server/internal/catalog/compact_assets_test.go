package catalog

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

func TestCompactFilmSourcesFenceAndKeepWireFacts(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := catalogtest.New(t, db)
	films := c.Library("films", "Films", "movie", "/films")
	film := c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Movie, Key: "file:film", Title: "Film", Added: "2026-01-01T00:00:00Z"}, nil)
	assetB, tokenB := int64(0), ""
	assetA, tokenA := int64(0), ""
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		assetB, tokenB, err = compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/films/b.mkv", Size: 1, ModifiedNS: 1, Container: "matroska", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 12.345678901234567})
		if err != nil {
			return err
		}
		if err = compactcatalog.LinkAssetTx(ctx, tx, film.ID, assetB, compactcatalog.Link{Part: 1}); err != nil {
			return err
		}
		assetA, tokenA, err = compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/films/a.mkv", Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "h265", AudioCodec: "ac3", Width: 3840, Height: 2160, Duration: 23.456789012345678})
		if err != nil {
			return err
		}
		return compactcatalog.LinkAssetTx(ctx, tx, film.ID, assetA, compactcatalog.Link{Part: 2})
	})
	c.Drain()
	s := New(db)
	stored := func(id int64) float64 {
		t.Helper()
		var d float64
		if err := db.QueryRow(`SELECT duration FROM catalog_assets WHERE id=?`, id).Scan(&d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	got, err := s.Get("viewer", film.Public)
	if err != nil {
		t.Fatal(err)
	}
	sources := fileInfoSourcesForTest(t, s, "viewer", film.Public)
	if len(got.Sources) != 0 || len(sources) != 2 || sources[0].ID != tokenB || sources[1].ID != tokenA ||
		sources[0].Container != "matroska" || sources[1].VideoCodec != "h265" ||
		sources[0].Duration == nil || math.Float64bits(*sources[0].Duration) != math.Float64bits(stored(assetB)) ||
		math.Float64bits(got.Duration) != math.Float64bits(stored(assetA)) {
		t.Fatalf("film source order or exact facts changed: %+v", got)
	}
	// Inventory availability is non-catalogue source evidence. The catalogue
	// links and their probed facts remain present while both source locations
	// report missing.
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)
	 SELECT ?,s.id,?,s.incarnation,?, 'missing-revision','{}',1,1,'missing' FROM library_sources s WHERE s.library_id=?`, "missing-b", tokenB, "b.mkv", "films")
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state)
	 SELECT ?,s.id,?,s.incarnation,?, 'missing-revision','{}',1,1,'missing' FROM library_sources s WHERE s.library_id=?`, "missing-a", tokenA, "a.mkv", "films")
	c.Drain()
	got, err = s.Get("viewer", film.Public)
	if err != nil || got.Available {
		t.Fatalf("film was available after both source grants were removed: %+v %v", got, err)
	}
	// Raw NEW facts stay behind the accepted OLD frame until the bounded
	// producer publishes them; neither read loses the duration precision.
	oldDuration := got.Duration
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: "/films/a.mkv", Size: 1, ModifiedNS: 1, Container: "mp4", VideoCodec: "h265", AudioCodec: "ac3", Width: 3840, Height: 2160, Duration: 34.567890123456789})
		return err
	})
	got, err = s.Get("viewer", film.Public)
	if err != nil || math.Float64bits(got.Duration) != math.Float64bits(oldDuration) {
		t.Fatalf("raw source update replaced accepted OLD: %+v %v", got, err)
	}
	c.Drain()
	got, err = s.Get("viewer", film.Public)
	if err != nil || math.Float64bits(got.Duration) != math.Float64bits(stored(assetA)) {
		t.Fatalf("published source duration lost precision: %+v %v", got, err)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.UnlinkAssetTx(ctx, tx, film.ID, assetB)
	})
	c.Drain()
	got, err = s.Get("viewer", film.Public)
	sources = fileInfoSourcesForTest(t, s, "viewer", film.Public)
	if err != nil || len(got.Sources) != 0 || len(sources) != 1 || sources[0].ID != tokenA {
		t.Fatalf("removed source remained in film detail: %+v %v", got, err)
	}
}

func TestCompactAssetReadsCoverNonFilmAndEmptyMembership(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("films", "Films", "movie", "/films")
	extra := c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Extra, Key: "extra", Title: "Extra", Added: "2026-01-01T00:00:00Z"}, nil)
	empty := c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Extra, Key: "empty", Title: "Empty", Added: "2026-01-01T00:00:00Z"}, nil)
	_, token := c.File(extra.ID, "/films/extra.mkv", 10.125)
	c.Drain()
	s := New(c.DB)
	got, err := s.Get("viewer", extra.Public)
	sources := fileInfoSourcesForTest(t, s, "viewer", extra.Public)
	if err != nil || len(got.Sources) != 0 || len(sources) != 1 || sources[0].ID != token || got.Duration != 10.125 {
		t.Fatalf("non-film source did not come from compact facts: %+v %v", got, err)
	}
	noSources, err := s.Get("viewer", empty.Public)
	if err != nil || len(noSources.Sources) != 0 || noSources.Available || noSources.Duration != 0 || len(fileInfoSourcesForTest(t, s, "viewer", empty.Public)) != 0 {
		t.Fatalf("empty membership changed wire shape: %+v %v", noSources, err)
	}
}
