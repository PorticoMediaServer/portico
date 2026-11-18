package scan_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// An in-file track's id is its place among the file's video, audio and subtitle streams (API.md,
// playback-options): the desktop app's mpv picks the chosen track by that place. Streams of any
// other kind, such as fonts attached to the file, are left out and do not move the others.
func TestATracksIDIsItsPlaceAmongTheFilesVideoAudioAndSubtitleStreams(t *testing.T) {
	found := mediafacts.Facts{Streams: []mediafacts.Stream{
		{Index: 0, Type: "video", Codec: "hevc"}, {Index: 1, Type: "data", Codec: "bin_data"}, {Index: 2, Type: "audio", Codec: "ac3"},
		{Index: 3, Type: "attachment", Codec: "ttf"}, {Index: 4, Type: "subtitle", Codec: "ass"}, {Index: 5, Type: "audio", Codec: "aac"},
	}}
	streams := scan.FromMedia(found).Streams
	want := []db.StreamKind{db.StreamVideo, db.StreamAudio, db.StreamSubtitle, db.StreamAudio}
	if len(streams) != len(want) {
		t.Fatalf("kept %d streams, want %d", len(streams), len(want))
	}
	for place, s := range streams {
		if s.Kind != want[place] {
			t.Errorf("stream %d is %v, want %v", place, s.Kind, want[place])
		}
	}
	// The database stores each kept stream at its place in this list (`insertStreams`).
	if got := playback.TrackID(db.Track{Index: 3}); got != "3" {
		t.Errorf("the fourth kept stream's id is %q", got)
	}
}
