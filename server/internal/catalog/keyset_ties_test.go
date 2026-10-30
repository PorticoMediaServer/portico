package catalog

import (
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// A keyset page costs its own rows, not the tie cohort or library prefix
// before its cursor (B1). Every stream here is one long run of equal keys (or,
// for random and listening titles, a long run of distinct keys), walked to
// the end: the last pages must cost about what the first continuation did,
// and the walk must return each title exactly once.
func TestKeysetPagesDoNotRescanTheirPrefix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	const n, limit = 1200, 40
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	music := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Album", 2020)
	for i := 0; i < n; i++ {
		path := fmt.Sprintf("/m/%04d.mkv", i)
		item := c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/m", path, 0), Title: fmt.Sprintf("Film %04d", i), Year: 2000, Added: "2026-01-01T00:00:00.000Z"}, nil)
		c.Exec(`INSERT INTO personal_items(profile_id,item_id,rating,revision,last_played_at) VALUES('p',?,3,1,'2026-09-01T00:00:00Z')`, item.ID)
		c.Exec(`INSERT INTO personal_play_counts(profile_id,item_id,plays) VALUES('p',?,2)`, item.ID)
		c.Song(album, i+1, fmt.Sprintf("/music/%04d.mp3", i), fmt.Sprintf("Song %04d", i))
	}
	c.Drain()
	c.Exec(`ANALYZE`)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m", "music"}}

	// walk pages a stream to its end and returns each page's VM steps.
	walk := func(t *testing.T, want int, page func(s *Service, cursor string) (ids []string, next string, err error)) []int64 {
		t.Helper()
		seen := map[string]bool{}
		steps := []int64{}
		cursor := ""
		for pages := 0; pages <= want/limit+1; pages++ {
			var ids []string
			var next string
			cost := readSteps(t, c, func(s *Service) error {
				var err error
				ids, next, err = page(s, cursor)
				return err
			})
			steps = append(steps, cost)
			for _, id := range ids {
				if seen[id] {
					t.Fatalf("page %d repeated %s", pages, id)
				}
				seen[id] = true
			}
			if next == "" {
				break
			}
			cursor = next
		}
		if len(seen) != want {
			t.Fatalf("walk returned %d titles, want %d", len(seen), want)
		}
		return steps
	}
	bounded := func(t *testing.T, steps []int64) {
		t.Helper()
		if len(steps) < 10 {
			t.Fatalf("only %d pages", len(steps))
		}
		// The first continuation (page 1) against the deepest full pages.
		early := steps[1]
		for i := len(steps) - 4; i < len(steps)-1; i++ {
			if steps[i] > early*2 {
				t.Fatalf("page %d took %d VM steps against the first continuation's %d: %v", i, steps[i], early, steps)
			}
		}
		t.Logf("steps per page: %v", steps)
	}
	browse := func(sort BrowseSortSelection) func(s *Service, cursor string) ([]string, string, error) {
		return func(s *Service, cursor string) ([]string, string, error) {
			out, err := s.BrowseEntities(viewer, BrowseRequest{Library: "m", Pivot: "movies", Sort: []BrowseSortSelection{sort}, Limit: limit, Cursor: cursor})
			ids := []string{}
			for _, entry := range out.Entries {
				ids = append(ids, entry.ID)
			}
			return ids, out.PageInfo.NextCursor, err
		}
	}
	for _, sort := range []BrowseSortSelection{
		{Field: "personalRating", Direction: "desc"},
		{Field: "personalRating", Direction: "asc"},
		{Field: "playCount", Direction: "desc"},
		{Field: "lastPlayed", Direction: "desc"},
		{Field: "random", Direction: "asc", Seed: 7},
		{Field: "random", Direction: "desc", Seed: 7},
	} {
		t.Run(sort.Field+"/"+sort.Direction, func(t *testing.T) {
			bounded(t, walk(t, n, browse(sort)))
		})
	}
	for _, sort := range []string{"year", "added", "title"} {
		for _, order := range []string{"asc", "desc"} {
			t.Run("titles/"+sort+"/"+order, func(t *testing.T) {
				bounded(t, walk(t, n, func(s *Service, cursor string) ([]string, string, error) {
					page, err := s.QueryTitles(viewer, TitleQuery{Library: "m", Kind: "movie", Sort: sort, Order: order, Limit: limit, Cursor: cursor})
					ids := []string{}
					for _, item := range page.Items {
						ids = append(ids, item.ID)
					}
					return ids, page.NextCursor, err
				}))
			})
		}
	}
	for _, direction := range []string{"asc", "desc"} {
		t.Run("listening/songs/"+direction, func(t *testing.T) {
			bounded(t, walk(t, n, func(s *Service, cursor string) ([]string, string, error) {
				request := ContentRequest{Viewer: viewer, ServerID: "server", Library: "music", Profile: "p", ViewerFence: "f", View: "songs", Limit: limit, Cursor: cursor, Direction: direction}
				page, err := s.Content(request)
				if err != nil {
					return nil, "", err
				}
				for _, section := range page.Sections {
					if section.ID == "songs" {
						ids := []string{}
						for _, entry := range section.Entries {
							ids = append(ids, entry.ID)
						}
						return ids, section.NextCursor, nil
					}
				}
				return nil, "", fmt.Errorf("no songs section")
			}))
		})
	}
}
