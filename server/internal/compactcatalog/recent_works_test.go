package compactcatalog_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// A show is one Recently Added work, dated by its newest available episode;
// its episodes carry no recent_text of their own, and a standalone movie is
// dated by itself. An episode leaving availability moves the show back.
func TestRecentWorksFollowTheNewestAvailableMember(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	films := c.Library("m", "Movies", "movie", "/m")
	show := c.Show(tv, "Harbor", 2020)
	season := c.Season(show, 1)
	old := c.Episode(show, season, 1, "/tv/Harbor/s01e01.mkv")
	newest := c.Episode(show, season, 2, "/tv/Harbor/s01e02.mkv")
	movie := c.Movie(films, "/m/One.mkv", "One", 2020)
	c.Fields(old.ID, map[string]any{"added_text": "2026-01-01T00:00:00Z"})
	c.Fields(newest.ID, map[string]any{"added_text": "2026-03-01T00:00:00Z"})
	c.Fields(movie.ID, map[string]any{"added_text": "2026-02-01T00:00:00Z"})
	c.Drain()
	recent := func(id int64) string {
		t.Helper()
		var v sql.NullString
		if err := c.DB.QueryRow(`SELECT recent_text FROM catalog_browse_rows WHERE entity_id=?`, id).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v.String
	}
	if got := recent(show.ID); got != "2026-03-01T00:00:00Z" {
		t.Fatalf("show dated %q, want its newest episode", got)
	}
	if recent(newest.ID) != "" || recent(season.ID) != "" {
		t.Fatal("an episode or season is a Recently Added work of its own")
	}
	if got := recent(movie.ID); got != "2026-02-01T00:00:00Z" {
		t.Fatalf("movie dated %q", got)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, newest.Asset, false)
	})
	c.Drain()
	if got := recent(show.ID); got != "2026-01-01T00:00:00Z" {
		t.Fatalf("after its newest episode left, show dated %q, want the older one", got)
	}
}
