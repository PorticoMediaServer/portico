package catalog

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// A relation row pages to its true end (JUSTIN-ANSWERS #34): the title
// page's first page and the row route's continuations are one order, nearest
// the title's year first, with nothing repeated or skipped.
func TestRelationRowPagesNearestYearToExhaustion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	library := c.Library("m", "Movies", "movie", "/m")
	source := c.Movie(library, "/m/source.mkv", "Source", 2010)
	c.Genres(source.ID, "tmdb", "Drama")
	type film struct {
		id     int64
		public string
		year   int
	}
	films := []film{}
	for n := 0; n < 260; n++ {
		year := 1990 + n%41
		if n%37 == 0 {
			year = 0
		}
		m := c.Movie(library, fmt.Sprintf("/m/%04d.mkv", n), fmt.Sprintf("Film %04d", n), year)
		c.Genres(m.ID, "tmdb", "Drama")
		films = append(films, film{m.ID, m.Public, year})
	}
	c.Drain()
	distance := func(year int) int {
		if year <= 0 {
			return 100000
		}
		if year >= 2010 {
			return year - 2010
		}
		return 2010 - year
	}
	sort.Slice(films, func(i, j int) bool {
		a, b := films[i], films[j]
		if distance(a.year) != distance(b.year) {
			return distance(a.year) < distance(b.year)
		}
		if a.year != b.year {
			return a.year > b.year
		}
		if a.year >= 2010 {
			return a.id > b.id
		}
		return a.id < b.id
	})
	expected := []string{}
	for _, f := range films {
		expected = append(expected, f.public)
	}
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	rows, _, err := s.ItemRecommendationRows(viewer, source.Public, 12, false)
	if err != nil {
		t.Fatal(err)
	}
	var genre HomeRow
	for _, row := range rows {
		if row.Relation == "genre" {
			genre = row
		}
	}
	if genre.ID == "" || !genre.CursorCapable || !genre.HasMore || genre.NextCursor == "" || genre.Endpoint != "/v1/items/"+source.Public+"/recommendations/rows/"+genre.ID {
		t.Fatalf("genre row is not a first page: %+v", genre)
	}
	loaded := []string{}
	for _, entry := range genre.Entries {
		loaded = append(loaded, entry.ID)
	}
	cursor := genre.NextCursor
	var second HomeRow
	for pages := 0; cursor != "" && pages < 100; pages++ {
		page, err := s.ItemRecommendationRow(viewer, source.Public, genre.ID, cursor, 12, false)
		if err != nil {
			t.Fatal(err)
		}
		if pages == 0 {
			second = page
		}
		for _, entry := range page.Entries {
			loaded = append(loaded, entry.ID)
		}
		cursor = page.NextCursor
	}
	if !reflect.DeepEqual(loaded, expected) {
		t.Fatalf("genre row exhaustion %d/%d", len(loaded), len(expected))
	}
	replay, err := s.ItemRecommendationRow(viewer, source.Public, genre.ID, genre.NextCursor, 12, false)
	if err != nil || !reflect.DeepEqual(replay.Entries, second.Entries) {
		t.Fatalf("replay moved loaded titles: %v", err)
	}
	// More like X answers to its short name too.
	if page, err := s.ItemRecommendationRow(viewer, source.Public, "more_like", "", 5, false); err != nil || page.ID != "more_like:local:"+source.Public || len(page.Entries) != 5 || !page.HasMore {
		t.Fatalf("more like row: %+v %v", page, err)
	}
	if _, err = s.ItemRecommendationRow(viewer, source.Public, "genre:tmdb:missing", "", 5, false); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("an undisplayed row was admitted: %v", err)
	}
	// A cursor names one row of one title.
	if _, err = s.ItemRecommendationRow(viewer, source.Public, "more_like", genre.NextCursor, 12, false); !errors.Is(err, ErrCursor) {
		t.Fatalf("a genre cursor continued More like: %v", err)
	}
	c.Fields(films[0].id, map[string]any{"title": "Changed"})
	c.Drain()
	if _, err = s.ItemRecommendationRow(viewer, source.Public, genre.ID, genre.NextCursor, 12, false); !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("a new publication continued the old order: %v", err)
	}
}
