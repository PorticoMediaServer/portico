package catalog

import (
	"context"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// A restricted viewer's Recently Added walks its visibility class: hundreds
// of newer titles it may not see never hide the older ones it may.
func TestRestrictedRecentlyAddedIsCompleteBehindHiddenTitles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	add := func(i int, title, added string, mature bool) {
		path := fmt.Sprintf("/m/%04d.mkv", i)
		it := c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/m", path, 0), Title: title, Year: 2000, Added: added}, nil)
		c.File(it.ID, path, 5400)
		if mature {
			c.Attributes(it.ID, "contentRating", "R")
		} else {
			c.Attributes(it.ID, "contentRating", "G")
		}
	}
	for i := 0; i < 3; i++ {
		add(i, fmt.Sprintf("Family %d", i), fmt.Sprintf("2026-01-0%dT00:00:00.000Z", i+1), false)
	}
	for i := 0; i < 400; i++ {
		add(100+i, fmt.Sprintf("Mature %03d", i), fmt.Sprintf("2026-06-01T00:%02d:%02d.000Z", i/60, i%60), true)
	}
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('r',17),('g',0)`)
	c.Drain()
	s := New(c.DB)
	r := classesRestrictionOf(classesCeiling(13), false)
	if err := s.RebuildVisibilityClass(context.Background(), "m", r); err != nil {
		t.Fatal(err)
	}
	viewer := Viewer{Profile: "p", Fence: "kid", Libraries: []string{"m"}, Restrictions: r}
	row, err := s.HomeSingleRow(HomeRequest{Viewer: viewer, ServerID: "server", Profile: "p", ViewerFence: "kid", Libraries: []string{"m"}, Restrictions: r}, "new_on_server", HomeRowPage{Limit: 12})
	if err != nil {
		t.Fatal(err)
	}
	if row.Total != 3 || len(row.Entries) != 3 {
		t.Fatalf("restricted recently added: total %d, %d entries", row.Total, len(row.Entries))
	}
	for _, entry := range row.Entries {
		if entry.Title[:6] != "Family" {
			t.Fatalf("a hidden title reached the row: %s", entry.Title)
		}
	}
}
