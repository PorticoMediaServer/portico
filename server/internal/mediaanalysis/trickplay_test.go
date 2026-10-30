package mediaanalysis

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestTrickplayGeometryFollowsPolicyAndBudget(t *testing.T) {
	defaults := trickplayGeometry(catalog.TrickplaySettings{}, 0)
	if defaults.TileWidth != 320 || defaults.TileHeight != 180 || defaults.Columns != 10 || defaults.Rows != 10 || defaults.PerSheet != 100 {
		t.Fatal("default sheet geometry changed", defaults)
	}
	if defaults.IntervalUS != 10000000 || defaults.MaxFrames != catalog.DefaultTrickplayMaxTiles {
		t.Fatal("default interval or frame budget changed", defaults)
	}
	wide := trickplayGeometry(catalog.TrickplaySettings{TileWidth: 640, IntervalSeconds: 4, MaxTiles: 500}, 0)
	if wide.TileWidth != 640 || wide.TileHeight != 360 || wide.Columns*wide.TileWidth > maxSheetPixels || wide.Rows*wide.TileHeight > maxSheetPixels {
		t.Fatal("wide tiles exceeded the sheet bound", wide)
	}
	if wide.PerSheet != wide.Columns*wide.Rows || wide.PerSheet > maxSheetFrames || wide.IntervalUS != 4000000 || wide.MaxFrames != 500 {
		t.Fatal("wide geometry", wide)
	}
	// The operator frame budget always narrows the library policy, never widens it.
	bounded := trickplayGeometry(catalog.TrickplaySettings{MaxTiles: 2000}, 48)
	if bounded.MaxFrames != 48 {
		t.Fatal("operator budget ignored", bounded)
	}
	// Descriptor tile counts are exact, including the trailing partial sheet.
	for _, c := range []struct{ frames, tiles int }{{1, 1}, {100, 1}, {101, 2}, {250, 3}} {
		d := defaults.descriptor(c.frames, 10000000, int64(c.frames)*10000000)
		if d.TileCount != c.tiles || !d.valid() {
			t.Fatal("tile count", c, d)
		}
		if d.Width != 3200 || d.Height != 1800 {
			t.Fatal("sheet dimensions", d)
		}
	}
}

func TestTrickplaySheetPlacesFramesInReadingOrder(t *testing.T) {
	layout := trickplayLayout{TileWidth: 16, TileHeight: 16, Columns: 3, Rows: 2, PerSheet: 6}
	sheet := newTrickplaySheet(layout)
	shades := []uint8{10, 60, 110, 160, 210, 250}
	for _, shade := range shades {
		frame := image.NewRGBA(image.Rect(0, 0, 16, 16))
		for x := 0; x < 16; x++ {
			for y := 0; y < 16; y++ {
				frame.SetRGBA(x, y, color.RGBA{shade, shade, shade, 255})
			}
		}
		sheet.place(frame)
	}
	if sheet.width() != 48 || sheet.height() != 32 {
		t.Fatal("sheet canvas", sheet.width(), sheet.height())
	}
	data, e := sheet.encode()
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := jpeg.Decode(bytes.NewReader(data))
	if e != nil {
		t.Fatal(e)
	}
	if decoded.Bounds().Dx() != 48 || decoded.Bounds().Dy() != 32 {
		t.Fatal("decoded sheet geometry", decoded.Bounds())
	}
	for i, shade := range shades {
		x, y := (i%3)*16+8, (i/3)*16+8
		r, _, _, _ := decoded.At(x, y).RGBA()
		if got := int(r >> 8); got < int(shade)-12 || got > int(shade)+12 {
			t.Fatal("frame placed in the wrong cell", i, got, shade)
		}
	}
	sheet.reset()
	if sheet.frames != 0 {
		t.Fatal("sheet not reusable")
	}
}

func TestTrickplayThumbnailTrackCuesKnownFrames(t *testing.T) {
	layout := trickplayLayout{TileWidth: 320, TileHeight: 180, Columns: 2, Rows: 2, PerSheet: 4}
	d := layout.descriptor(5, 10000000, 45000000)
	if d.TileCount != 2 || !d.valid() {
		t.Fatal("descriptor", d)
	}
	full := trickplayVTT(d, 0, 45000000)
	want := "WEBVTT\n" +
		"\n00:00:00.000 --> 00:00:10.000\ntiles/0.jpg#xywh=0,0,320,180\n" +
		"\n00:00:10.000 --> 00:00:20.000\ntiles/0.jpg#xywh=320,0,320,180\n" +
		"\n00:00:20.000 --> 00:00:30.000\ntiles/0.jpg#xywh=0,180,320,180\n" +
		"\n00:00:30.000 --> 00:00:40.000\ntiles/0.jpg#xywh=320,180,320,180\n" +
		"\n00:00:40.000 --> 00:00:45.000\ntiles/1.jpg#xywh=0,0,320,180\n"
	if full != want {
		t.Fatalf("thumbnail track\n got:%q\nwant:%q", full, want)
	}
	// A trimmed association is cued in item time, never in source time.
	trimmed := trickplayVTT(d, 15000000, 35000000)
	wantTrimmed := "WEBVTT\n" +
		"\n00:00:00.000 --> 00:00:05.000\ntiles/0.jpg#xywh=320,0,320,180\n" +
		"\n00:00:05.000 --> 00:00:15.000\ntiles/0.jpg#xywh=0,180,320,180\n" +
		"\n00:00:15.000 --> 00:00:20.000\ntiles/0.jpg#xywh=320,180,320,180\n"
	if trimmed != wantTrimmed {
		t.Fatalf("trimmed track\n got:%q\nwant:%q", trimmed, wantTrimmed)
	}
}

type trickplayTestFixture struct {
	db   *sql.DB
	item catalogtest.Item
}

func trickplayFixture(t *testing.T) trickplayTestFixture {
	t.Helper()
	c := catalogtest.Open(t)
	library := c.Library("library", "Movies", "movie", "/fixture")
	item := c.Movie(library, "/fixture/movie.mp4", "Movie", 2024)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, item.Asset, map[string]any{"duration": float64(60)})
	})
	c.Drain()
	if _, err := c.DB.Exec(`UPDATE library_sources SET incarnation='incarnation',generation=4 WHERE id='library'`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DB.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns,state) VALUES('object','library',?,'incarnation','movie.mp4','revision-1','{}',1000,1,'available')`, item.Token); err != nil {
		t.Fatal(err)
	}
	layout := trickplayLayout{TileWidth: 320, TileHeight: 180, Columns: 2, Rows: 2, PerSheet: 4}
	summary, err := json.Marshal(trickplaySummary{TrickplayDescriptor: layout.descriptor(6, 10000000, 60000000), Sampled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.DB.Exec(`INSERT INTO analysis_results(id,object_id,asset_id,source_revision,root_incarnation,configuration_generation,policy_revision,stage,algorithm,evidence,tool_digest,duration_us,summary_json,created_ms) VALUES('set-1','object',?,'revision-1','incarnation',4,1,'trickplay','fixture','fixture','fixture',60000000,?,1)`, item.Token, string(summary)); err != nil {
		t.Fatal(err)
	}
	if _, err = c.DB.Exec(`INSERT INTO analysis_heads(object_id,source_revision,stage,result_id) VALUES('object','revision-1','trickplay','set-1')`); err != nil {
		t.Fatal(err)
	}
	return trickplayTestFixture{db: c.DB, item: item}
}

func TestTrickplaySetsGoStaleWhenTheSourceRevisionChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := trickplayFixture(t)
	db := f.db
	service := &Service{db: db}
	access := Access{ServerID: "server", LibraryID: "library", ItemID: f.item.Public, AccountID: "account", ProfileID: "profile", Authority: "local", Authorize: func(*sql.Tx) error { return nil }}
	first, err := service.Trickplay(context.Background(), access)
	if err != nil || len(first.Sets) != 1 {
		t.Fatal("set not published", first, err)
	}
	set := first.Sets[0]
	if set.ID != "set-1" || set.SourceID != f.item.Token || set.SourceRevision != "revision-1" || set.Stale {
		t.Fatal("current set reported stale", set)
	}
	if set.Columns != 2 || set.Rows != 2 || set.TileWidth != 320 || set.TileHeight != 180 || set.Width != 640 || set.Height != 360 {
		t.Fatal("published geometry", set)
	}
	if set.FrameCount != 6 || set.TileCount != 2 || set.IntervalSeconds != 10 || set.DurationSeconds != 60 {
		t.Fatal("published timing", set)
	}
	if set.TilesURL != "/v1/items/"+f.item.Public+"/trickplay/set-1/tiles" || set.ThumbnailsURL != "/v1/items/"+f.item.Public+"/trickplay/set-1/thumbnails.vtt" {
		t.Fatal("published delivery URLs", set)
	}
	// A replaced file advances the inventory revision. The stored set keeps its
	// own revision, so it must be published as stale rather than silently reused.
	if _, err = db.Exec(`UPDATE inventory_objects SET revision='revision-2'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE analysis_heads SET source_revision='revision-1'`); err != nil {
		t.Fatal(err)
	}
	second, err := service.Trickplay(context.Background(), access)
	if err != nil || len(second.Sets) != 1 {
		t.Fatal(second, err)
	}
	if !second.Sets[0].Stale || second.Sets[0].Revision == set.Revision {
		t.Fatal("stale set not marked or revision unchanged", second.Sets[0])
	}
	// A reconfigured root is the same kind of change.
	if _, err = db.Exec(`UPDATE inventory_objects SET revision='revision-1';UPDATE library_sources SET generation=5`); err != nil {
		t.Fatal(err)
	}
	third, err := service.Trickplay(context.Background(), access)
	if err != nil || len(third.Sets) != 1 || !third.Sets[0].Stale {
		t.Fatal("configuration change not stale", third, err)
	}
	// A retired result publishes nothing at all.
	if _, err = db.Exec(`UPDATE library_sources SET generation=4;UPDATE analysis_results SET retired_ms=1`); err != nil {
		t.Fatal(err)
	}
	empty, err := service.Trickplay(context.Background(), access)
	if err != nil || len(empty.Sets) != 0 {
		t.Fatal("retired result published", empty, err)
	}
}

func TestTrickplaySetIsScopedToTheViewerItem(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := trickplayFixture(t)
	db := f.db
	service := &Service{db: db}
	access := Access{ServerID: "server", LibraryID: "library", ItemID: f.item.Public, AccountID: "account", ProfileID: "profile", Authority: "local", Authorize: func(*sql.Tx) error { return nil }}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.storedSetTx(context.Background(), tx, access, "set-1")
	tx.Rollback()
	if err == nil {
		t.Fatal("short set identifiers must be rejected")
	}
	padded := access
	padded.LibraryID = "other"
	if _, err = service.Trickplay(context.Background(), padded); err == nil {
		t.Fatal("a foreign library read its sets")
	}
	unauthorized := access
	unauthorized.Authorize = nil
	if _, err = service.Trickplay(context.Background(), unauthorized); err == nil {
		t.Fatal("missing authorization admitted")
	}
}
