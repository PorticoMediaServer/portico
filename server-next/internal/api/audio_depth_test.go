package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

func TestAudioDepthIsOnlyPublishedForLosslessTracks(t *testing.T) {
	stored := []db.Stream{{Kind: db.StreamAudio, Codec: "dts", BitDepth: 24},
		{Kind: db.StreamAudio, Codec: "flac", BitDepth: 24}, {Kind: db.StreamVideo, Codec: "hevc", BitDepth: 10}}
	facts := streamShapes(stored)
	options := versionOut(db.PlaybackVersion{Tracks: []db.Track{{Stream: stored[0]}, {Stream: stored[1]}, {Stream: stored[2]}}})
	if facts[0].BitDepth != 0 || facts[1].BitDepth != 24 || facts[2].BitDepth != 10 ||
		options.Audio[0].BitDepth != 0 || options.Audio[1].BitDepth != 24 || options.Video[0].BitDepth != 10 {
		t.Fatalf("lossy depth leaked, or lossless/video depth was lost: %+v %+v", facts, options)
	}
	for _, response := range []any{facts[0], options.Audio[0]} {
		raw, err := json.Marshal(response)
		if err != nil || strings.Contains(string(raw), `"bitDepth"`) {
			t.Fatalf("lossy answer: %s, %v", raw, err)
		}
	}
	if stored[0].BitDepth != 24 {
		t.Fatal("stored facts changed")
	}
}
