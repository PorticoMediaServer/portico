package mediaanalysis

import (
	"encoding/json"
	"testing"
)

func TestTrickplayV1WireCompatibility(t *testing.T) {
	const descriptor = `{"version":1,"tileWidth":160,"tileHeight":90,"columns":2,"rows":1,"width":320,"height":90,"intervalUS":"2000000","frameCount":3,"tileCount":2,"durationUS":"6000000"}`
	var d TrickplayDescriptor
	if err := json.Unmarshal([]byte(descriptor), &d); err != nil || !d.valid() {
		t.Fatalf("stored v1 descriptor no longer readable: %v %+v", err, d)
	}
	raw, err := json.Marshal(d)
	if err != nil || string(raw) != descriptor {
		t.Fatal("v1 descriptor fields or microsecond strings changed", string(raw), err)
	}
	const expected = "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\ntiles/0.jpg#xywh=160,0,160,90\n\n00:00:02.000 --> 00:00:04.000\ntiles/1.jpg#xywh=0,0,160,90\n"
	if got := trickplayVTT(d, 2_000_000, 6_000_000); got != expected {
		t.Fatalf("trimmed item clock, relative tile URLs or geometry changed: %q", got)
	}
}
