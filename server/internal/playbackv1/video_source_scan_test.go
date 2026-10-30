package playbackv1

import (
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"strings"
	"testing"
)

func TestVideoStreamScanComesOnlyFromNormalizedObservation(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("movies", "Movies", "movie", "/private")
	movie := c.Movie(lib, "/private/movie.mkv", "Movie", 2026)
	_, asset := c.File(movie.ID, "/private/source.mkv", 60)
	s := &Service{DB: c.DB}
	for _, state := range []string{assets.InterlaceFieldOrderProgressive, assets.InterlaceFieldOrderInterlaced, assets.InterlaceDecodedInterlaced, "decoded_frames", ""} {
		tx, err := c.DB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		err = assets.PersistStreams(tx, asset, 1000, 1, []assets.Stream{{Index: 0, Type: "video", Codec: "hevc", Detail: assets.StreamDetail{Width: 1920, Height: 1080, Interlaced: true, StreamBreadth: assets.StreamBreadth{InterlaceAnalysis: state}}}})
		if err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		var version Version
		if err = s.streams(context.Background(), tx, asset, &version); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if len(version.Video) != 1 {
			t.Fatal(version)
		}
		known := state == assets.InterlaceFieldOrderProgressive || state == assets.InterlaceFieldOrderInterlaced || state == assets.InterlaceDecodedInterlaced
		got := version.Video[0].Interlaced
		if (got != nil) != known || got != nil && *got != (state != assets.InterlaceFieldOrderProgressive) {
			t.Fatal(state, got)
		}
		raw, err := json.Marshal(version.Video[0])
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), `"interlaced"`) != known {
			t.Fatal("unknown became false", state, string(raw))
		}
	}
}

func TestVideoHDRNamesUseTypedFactsRatherThanJSONWords(t *testing.T) {
	for _, c := range []struct{ transfer, detail, want string }{
		{"smpte2084", `{"hdr10Plus":false}`, "hdr10"},
		{"smpte2084", `{"note":"dolby_vision hdr10Plus dovi"}`, "hdr10"},
		{"smpte2084", `{"hdr10Plus":true}`, "hdr10plus"},
		{"smpte2084", `{"dynamicRange":"dolby_vision"}`, "dolbyvision"},
		{"arib-std-b67", `{}`, "hlg"},
		{"", `{"hdr10Plus":false}`, ""},
	} {
		if got := hdrName(c.transfer, c.detail); got != c.want {
			t.Fatalf("%+v got %s", c, got)
		}
	}
}

func TestAudioStreamRetainsActualProfileAndExplicitObjectFacts(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("movies", "Movies", "movie", "/private")
	movie := c.Movie(lib, "/private/movie.mkv", "Movie", 2026)
	_, asset := c.File(movie.ID, "/private/source.mkv", 60)
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	err = assets.PersistStreams(tx, asset, 1000, 1, []assets.Stream{
		{Index: 1, Type: "audio", Codec: "dts", Channels: 8, ChannelLayout: "7.1", Detail: assets.StreamDetail{Profile: "DTS-HD MA", ObjectAudio: "dtsx", StreamBreadth: assets.StreamBreadth{AudioVariant: "dtsx"}}},
		{Index: 2, Type: "audio", Codec: "eac3", Title: "Atmos", Channels: 6},
	})
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	var version Version
	s := &Service{DB: c.DB}
	if err = s.streams(context.Background(), tx, asset, &version); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if len(version.Audio) != 2 || version.Audio[0].Profile != "DTS-HD MA" || !version.Audio[0].DTSX || version.Audio[0].ObjectAudio != "dtsx" {
		t.Fatal(version.Audio)
	}
	if version.Audio[1].Atmos || version.Audio[1].ObjectAudio != "" {
		t.Fatal("codec/title invented objects", version.Audio[1])
	}
}
