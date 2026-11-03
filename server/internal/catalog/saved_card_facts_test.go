package catalog

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// W2-A13: Saved's cards name what each title is ("Show · 1 season", "Anime · 2 seasons",
// "1982 · Movie"), so a Saved entry carries its library's kind and a show its season count,
// from the page's own lookup.
func TestSavedEntriesCarryLibraryKindAndSeasonCount(t *testing.T) {
	c := catalogtest.Open(t)
	tv := c.Library("tv", "TV", "tv", "/tv")
	anime := c.Library("anime", "Anime", "anime", "/anime")
	movies := c.Library("m", "Movies", "movie", "/m")
	silo := c.Show(tv, "Silo", 2023)
	c.Episode(silo, c.Season(silo, 1), 1, "/tv/Silo/S01E01.mkv")
	titan := c.Show(anime, "Attack on Titan", 2013)
	c.Episode(titan, c.Season(titan, 1), 1, "/anime/Titan/S01E01.mkv")
	c.Episode(titan, c.Season(titan, 2), 1, "/anime/Titan/S02E01.mkv")
	c.Season(titan, 3) // no episodes: not counted
	blade := c.Movie(movies, "/m/Blade Runner (1982).mkv", "Blade Runner", 1982)
	c.Drain()
	for _, id := range []int64{silo.ID, titan.ID, blade.ID} {
		c.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,rating,revision,watched,last_played_at) VALUES('p',?,1,0,NULL,1,0,'')`, id)
	}
	libraries := []string{"tv", "anime", "m"}
	out, err := New(c.DB).Saved(ContentRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: libraries}, ServerID: "s", Profile: "p", ViewerFence: "f", View: "watchlist", Limit: 40}, libraries)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Sections) != 1 {
		t.Fatalf("sections %+v", out.Sections)
	}
	got := map[string]ContentEntry{}
	for _, entry := range out.Sections[0].Entries {
		got[entry.Title] = entry
	}
	seasons := func(entry ContentEntry) int {
		if entry.SeasonCount == nil {
			return 0
		}
		return *entry.SeasonCount
	}
	if e := got["Silo"]; e.LibraryKind != "tv" || seasons(e) != 1 {
		t.Fatalf("Silo: kind %q seasons %d", e.LibraryKind, seasons(e))
	}
	if e := got["Attack on Titan"]; e.LibraryKind != "anime" || seasons(e) != 2 {
		t.Fatalf("Attack on Titan: kind %q seasons %d", e.LibraryKind, seasons(e))
	}
	if e := got["Blade Runner"]; e.LibraryKind != "movie" || e.SeasonCount != nil {
		t.Fatalf("Blade Runner: kind %q seasons %v", e.LibraryKind, e.SeasonCount)
	}
}
