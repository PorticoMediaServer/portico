package catalog

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// The owner's "pause background work" holds the visibility builder at its
// batch boundary; resuming lets it finish.
func TestVisibilityBuilderHonoursBackgroundPause(t *testing.T) {
	c := catalogtest.Open(t)
	music := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Release", 2020)
	for i := 1; i <= 3; i++ {
		c.Song(album, i, fmt.Sprintf("/music/%03d.mp3", i), fmt.Sprintf("Song %03d", i))
	}
	c.Drain()
	s := New(c.DB)
	defer dbwork.SetBackgroundPolicy(dbwork.SpeedNormal, 0)
	dbwork.SetBackgroundPolicy(dbwork.SpeedNormal, time.Now().Add(time.Hour).UnixMilli())
	done := make(chan error, 1)
	go func() {
		done <- s.RebuildVisibilityClass(context.Background(), "music", visibilityRestrictionOf(nil, false, "adult"))
	}()
	select {
	case err := <-done:
		t.Fatalf("builder ran while background work was paused: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	dbwork.SetBackgroundPolicy(dbwork.SpeedNormal, 0)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("builder did not resume")
	}
}
