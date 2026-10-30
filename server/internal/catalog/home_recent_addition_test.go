package catalog

import (
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Recently Added in Plex's shape: a show stands for what was added to it. One
// new episode reads as that episode, a season delivered together as the
// season, and several seasons at once as the show with its season count.
func TestRecentlyAddedNamesTheEpisodeOrSeasonThatArrived(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV Shows", "tv", "/tv")
	n := 0
	episode := func(show, season catalogtest.Item, number int, added string) catalogtest.Item {
		n++
		path := fmt.Sprintf("/tv/%03d.mkv", n)
		it := c.Entity(compactcatalog.Entity{Library: tv, Kind: compactcatalog.Episode, Parent: season.ID, Key: compactcatalog.ItemKey("/tv", path, 0), Title: fmt.Sprintf("Episode %d", number), Added: added},
			map[string]any{"show_id": show.ID, "season_id": season.ID, "numbering": "seasonal", "number": number})
		c.File(it.ID, path, 2700)
		return it
	}
	// Weekly: three episodes in January, the fourth a week later.
	weekly := c.Show(tv, "Weekly", 2020)
	weeklyOne := c.Season(weekly, 1)
	for i := 1; i <= 3; i++ {
		episode(weekly, weeklyOne, i, fmt.Sprintf("2026-01-01T00:00:0%d.000Z", i))
	}
	newest := episode(weekly, weeklyOne, 4, "2026-01-08T20:00:00.000Z")
	// Season pack: season 1 in January, all of season 2 together in February.
	pack := c.Show(tv, "Season Pack", 2021)
	packOne, packTwo := c.Season(pack, 1), c.Season(pack, 2)
	episode(pack, packOne, 1, "2026-01-02T00:00:00.000Z")
	episode(pack, packOne, 2, "2026-01-02T00:00:01.000Z")
	for i := 1; i <= 3; i++ {
		episode(pack, packTwo, i, fmt.Sprintf("2026-02-01T00:00:0%d.000Z", i))
	}
	// Whole show: two seasons arriving in one scan.
	whole := c.Show(tv, "Whole Show", 2022)
	wholeOne, wholeTwo := c.Season(whole, 1), c.Season(whole, 2)
	for i := 1; i <= 2; i++ {
		episode(whole, wholeOne, i, fmt.Sprintf("2026-03-01T00:00:0%d.000Z", i))
		episode(whole, wholeTwo, i, fmt.Sprintf("2026-03-01T00:00:1%d.000Z", i))
	}
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Libraries: []string{"tv"}}
	row, err := s.HomeSingleRow(HomeRequest{Viewer: viewer, ServerID: "server", Profile: "p", Libraries: []string{"tv"}}, "new_on_server", HomeRowPage{Limit: 12})
	if err != nil {
		t.Fatal(err)
	}
	if len(row.Entries) != 3 {
		t.Fatalf("recently added: %d entries, want 3", len(row.Entries))
	}
	got := row.Entries
	if got[0].Kind != "show" || got[0].ID != whole.Public || got[0].SeasonCount == nil || *got[0].SeasonCount != 2 {
		t.Fatalf("several seasons at once read as the show with its seasons: %+v", got[0])
	}
	if got[1].Kind != "season" || got[1].ID != packTwo.Public || got[1].Subtitle != "Season Pack" || got[1].SeasonNumber == nil || *got[1].SeasonNumber != 2 || got[1].Navigation == nil || got[1].Navigation.View != "season" {
		t.Fatalf("a season delivered together reads as the season under its show: %+v", got[1])
	}
	if got[2].Kind != "episode" || got[2].ID != newest.Public || got[2].Subtitle != "Weekly" || got[2].EpisodeNumber == nil || *got[2].EpisodeNumber != 4 {
		t.Fatalf("one new episode reads as that episode: %+v", got[2])
	}
}

func TestHomeRecentDeliveryAnchorAndCursorKeepRealTargets(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "Delivered", 2026)
	season := c.Season(show, 2)
	one := c.Episode(show, season, 1, "/tv/one.mkv")
	two := c.Episode(show, season, 2, "/tv/two.mkv")
	c.Fields(one.ID, map[string]any{"added_text": "2026-10-20T00:00:01.000Z"})
	c.Fields(two.ID, map[string]any{"added_text": "2026-10-20T00:00:02.000Z"})
	older := c.Show(tv, "Older", 2025)
	olderSeason := c.Season(older, 1)
	oldEpisode := c.Episode(older, olderSeason, 1, "/tv/old.mkv")
	c.Fields(oldEpisode.ID, map[string]any{"added_text": "2025-10-20T00:00:01.000Z"})
	c.Drain()
	s := New(c.DB)
	r := HomeRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"tv"}}}
	first, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 1})
	if err != nil || len(first.Entries) != 1 || first.Entries[0].ID != season.Public || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	next, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(next.Entries) != 1 || next.Entries[0].ID != oldEpisode.Public || next.NextCursor != "" {
		t.Fatal(next, err)
	}
	anchored, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 1, AnchorID: oldEpisode.Public})
	if err != nil || anchored.Start != 1 || len(anchored.Entries) != 1 || anchored.Entries[0].ID != oldEpisode.Public {
		t.Fatal("delivery anchor lost its canonical work", anchored, err)
	}
	if len(first.DisplayTargets) != 1 || first.DisplayTargets[0].MembershipID != show.Public || first.DisplayTargets[0].EntryID != season.Public {
		t.Fatal("missing Home-origin mapping", first.DisplayTargets)
	}
	projected, err := s.ProjectHomeRowDisplayTargets(r, "new_on_server", []string{show.Public, older.Public})
	if err != nil || len(projected) != 2 || projected[0].MembershipID != show.Public || projected[0].Entry.ID != season.Public || projected[1].Entry.ID != oldEpisode.Public {
		t.Fatal("Home See all projection", projected, err)
	}
	r.Viewer.Libraries = nil
	if _, err = s.ProjectHomeRowDisplayTargets(r, "new_on_server", []string{show.Public}); !errors.Is(err, ErrStaleContinuation) {
		t.Fatal("projection admitted a revoked membership", err)
	}
}
