package mediaanalysis

import (
	"encoding/json"
	"testing"
)

func TestTrickplayV1WireCompatibility(t *testing.T) {
	const descriptor = `{"version":1,"tileWidth":160,"tileHeight":90,"columns":2,"rows":1,"width":320,"height":90,"intervalUS":"2000000","frameCount":3,"tileCount":2,"durationUS":"6000000"}`
	t.Run("producer", func(t *testing.T) {
		// Actual sampling may use fewer frames and a longer interval than planned.
		// Pin the producer's arguments separately from those layout targets.
		layout := trickplayLayout{TileWidth: 160, TileHeight: 90, Columns: 2, Rows: 1, PerSheet: 2, IntervalUS: 1_000_000, MaxFrames: 9}
		produced := layout.descriptor(3, 2_000_000, 6_000_000)
		raw, err := json.Marshal(produced)
		if err != nil || string(raw) != descriptor {
			t.Fatalf("v1 descriptor producer changed: %s %v", raw, err)
		}
	})
	t.Run("stored-copy", func(t *testing.T) {
		var d TrickplayDescriptor
		if err := json.Unmarshal([]byte(descriptor), &d); err != nil || !d.valid() {
			t.Fatalf("stored v1 descriptor no longer readable: %v %+v", err, d)
		}
		raw, err := json.Marshal(d)
		if err != nil || string(raw) != descriptor {
			t.Fatal("v1 descriptor fields or microsecond strings changed", string(raw), err)
		}
	})
	t.Run("vtt", func(t *testing.T) {
		var d TrickplayDescriptor
		if err := json.Unmarshal([]byte(descriptor), &d); err != nil {
			t.Fatal(err)
		}
		const expected = "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\ntiles/0.jpg#xywh=160,0,160,90\n\n00:00:02.000 --> 00:00:04.000\ntiles/1.jpg#xywh=0,0,160,90\n"
		if got := trickplayVTT(d, 2_000_000, 6_000_000); got != expected {
			t.Fatalf("trimmed item clock, relative tile URLs or geometry changed: %q", got)
		}
	})
}
