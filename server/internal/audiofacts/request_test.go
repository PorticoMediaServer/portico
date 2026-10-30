package audiofacts

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"testing"
	"time"
)

func TestPlaybackQueuesMeasurementInsteadOfDecoding(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	lib := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(lib, "Artist")
	album := c.Album(artist, "Album", 2020)
	song := c.Song(album, 1, "/music/song.flac", "Song")
	c.Drain()
	calls := 0
	measure := func(context.Context, string, string) (Facts, error) {
		calls++
		return Facts{Container: "flac", Codec: "flac", SampleRate: 48000, Channels: 2, RawFrames: 48000, DurationFrames: 48000, TrimSource: "none"}, nil
	}
	_, ok, e := Ensure(context.Background(), c.DB, measure, song.Public, song.Token, time.Second)
	if e != nil || ok || calls != 0 {
		t.Fatal(ok, e, calls)
	}
	backfill := Backfill{DB: c.DB, Measure: measure}
	backfill.Step(context.Background())
	f, ok, e := Read(context.Background(), c.DB, song.Token)
	if e != nil || !ok || calls != 1 || f.RawFrames != 48000 {
		t.Fatal(f, ok, e, calls)
	}
}
