package catalog

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestDetailRowsCursorExhaustionStableRevision(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	library := c.Library("m", "Movies", "movie", "/m")
	source := c.Movie(library, "/m/source.mkv", "Source", 2020)
	c.Genres(source.ID, "tmdb", "Drama")
	expected := []string{}
	credits := []compactcatalog.Credit{}
	for n := 0; n < 537; n++ {
		film := c.Movie(library, fmt.Sprintf("/m/%04d.mkv", n), fmt.Sprintf("Film %04d", n), 2020)
		c.Genres(film.ID, "tmdb", "Drama")
		expected = append(expected, film.Public)
		credits = append(credits, personCredit(fmt.Sprintf("tmdb:%d", n), fmt.Sprint(n), fmt.Sprintf("Actor %04d", n), "Character", "Acting", n))
	}
	compactCredits(c, source.ID, credits...)
	private := c.Library("private", "Private", "movie", "/private")
	hidden := c.Movie(private, "/private/hidden.mkv", "Hidden", 2020)
	c.Genres(hidden.ID, "tmdb", "Drama")
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	cursor := ""
	loaded := []string{}
	firstNext := ""
	var replay HomeRow
	for pages := 0; pages < 100; pages++ {
		page, err := s.DetailMoreLike(viewer, source.Public, cursor, 37)
		if err != nil {
			t.Fatal(err)
		}
		if rowTotal(page) != 537 {
			t.Fatalf("eligible total %d", page.Total)
		}
		if cursor == "" {
			firstNext = page.NextCursor
		} else if replay.ID == "" {
			replay = page
		}
		for _, entry := range page.Entries {
			loaded = append(loaded, entry.ID)
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if !reflect.DeepEqual(loaded, expected) {
		t.Fatalf("recommendation exhaustion %d/%d", len(loaded), len(expected))
	}
	repeated, err := s.DetailMoreLike(viewer, source.Public, firstNext, 37)
	if err != nil || !reflect.DeepEqual(repeated.Entries, replay.Entries) {
		t.Fatalf("replay moved loaded titles: %v", err)
	}
	creditCursor := ""
	seen := map[string]bool{}
	creditNext := ""
	for pages := 0; pages < 100; pages++ {
		page, e := s.ItemCredits(viewer, source.Public, "cast", creditCursor, 37)
		if e != nil {
			t.Fatal(e)
		}
		if creditCursor == "" {
			creditNext = page.NextCursor
		}
		for _, credit := range page.Credits {
			if seen[credit.ID] {
				t.Fatal("duplicate credit", credit.ID)
			}
			seen[credit.ID] = true
		}
		creditCursor = page.NextCursor
		if creditCursor == "" {
			break
		}
	}
	if len(seen) != 537 {
		t.Fatalf("credit exhaustion %d", len(seen))
	}
	c.Fields(source.ID, map[string]any{"title": "Source changed"})
	c.Drain()
	if _, err = s.DetailMoreLike(viewer, source.Public, firstNext, 37); !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("recommendation revision not explicit: %v", err)
	}
	if _, err = s.ItemCredits(viewer, source.Public, "cast", creditNext, 37); !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("credit revision not explicit: %v", err)
	}
}
