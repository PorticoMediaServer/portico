package audiofacts

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// NEW-29: failures recorded before FailureVersion are retried once ("unreadable"
// only); other codes stay, and the retry never runs again once recorded.
func TestOldUnreadableFailuresAreRetriedOnce(t *testing.T) {
	c := catalogtest.Open(t)
	db := c.DB
	ctx := context.Background()
	music := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(music, "Test Artist")
	album := c.Album(artist, "Test Album", 2026)
	unreadableItem := c.Song(album, 1, "/music/a.flac", "Unreadable")
	silentItem := c.Song(album, 2, "/music/b.flac", "No Audio")
	c.Drain()
	unreadable, silent := unreadableItem.Token, silentItem.Token
	var err error
	if err = Fail(ctx, db, unreadable, "unreadable"); err != nil {
		t.Fatal(err)
	}
	if err = Fail(ctx, db, silent, "no_audio"); err != nil {
		t.Fatal(err)
	}
	if err = retryOldFailures(ctx, db); err != nil {
		t.Fatal(err)
	}
	if failed, _ := Failed(ctx, db, unreadable); failed {
		t.Fatal("an old unreadable failure wasn't cleared for a new measurement")
	}
	if failed, _ := Failed(ctx, db, silent); !failed {
		t.Fatal("a no_audio failure was cleared")
	}
	// Once recorded, the retry never runs again: a new unreadable failure stays.
	if err = Fail(ctx, db, unreadable, "unreadable"); err != nil {
		t.Fatal(err)
	}
	if err = retryOldFailures(ctx, db); err != nil {
		t.Fatal(err)
	}
	if failed, _ := Failed(ctx, db, unreadable); !failed {
		t.Fatal("the one-time retry ran twice")
	}
}
