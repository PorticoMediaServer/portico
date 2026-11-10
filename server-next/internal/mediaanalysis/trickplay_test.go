package mediaanalysis

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

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
func TestTrickplayGeometryFollowsPolicyAndBudget(t *testing.T) {
	defaults := trickplayGeometry(TrickplaySettings{}, 0)
	if defaults.TileWidth != 320 || defaults.TileHeight != 180 || defaults.Columns != 10 || defaults.Rows != 10 || defaults.PerSheet != 100 {
		t.Fatal("default sheet geometry changed", defaults)
	}
	if defaults.IntervalUS != 10000000 || defaults.MaxFrames != DefaultTrickplayMaxTiles {
		t.Fatal("default interval or frame budget changed", defaults)
	}
	wide := trickplayGeometry(TrickplaySettings{TileWidth: 640, IntervalSeconds: 4, MaxTiles: 500}, 0)
	if wide.TileWidth != 640 || wide.TileHeight != 360 || wide.Columns*wide.TileWidth > maxSheetPixels || wide.Rows*wide.TileHeight > maxSheetPixels {
		t.Fatal("wide tiles exceeded the sheet bound", wide)
	}
	if wide.PerSheet != wide.Columns*wide.Rows || wide.PerSheet > maxSheetFrames || wide.IntervalUS != 4000000 || wide.MaxFrames != 500 {
		t.Fatal("wide geometry", wide)
	}
	// The operator frame budget always narrows the library policy, never widens it.
	bounded := trickplayGeometry(TrickplaySettings{MaxTiles: 2000}, 48)
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
