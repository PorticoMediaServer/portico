package catalog

import (
	"context"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
)

func discoverIDs(page ContentEnvelope) []string {
	ids := []string{}
	for _, section := range page.Sections {
		ids = append(ids, section.ID)
	}
	return ids
}

func discoverIndex(ids []string, id string) int {
	for i, v := range ids {
		if v == id {
			return i
		}
	}
	return -1
}

// J-29: an audiobook library's Discover draws Continue listening, Recently
// added and Authors (phone-app.html); the Authors rail opens the same authors
// as the Authors view.
func TestAudiobookDiscoverHasAnAuthorsRailAfterRecentlyAdded(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("books", "Books", "audiobook", "/books")
	for i := 0; i < 3; i++ {
		book := c.Book(lib, fmt.Sprintf("Book %d", i), fmt.Sprintf("Author %d", i))
		c.BookFile(book, 1, fmt.Sprintf("/books/book%d.m4b", i))
	}
	c.Drain()
	s := New(c.DB)
	for {
		more, err := s.RefreshListeningGroups(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	c.Drain()
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"books"}}
	base := ContentRequest{Viewer: viewer, ServerID: "server", Library: "books", Profile: "p", ViewerFence: "f", Limit: 12}
	discover := base
	discover.View = "discover"
	page, err := s.Content(discover)
	if err != nil {
		t.Fatal(err)
	}
	ids := discoverIDs(page)
	recent, authors := discoverIndex(ids, "recently_added"), discoverIndex(ids, "authors")
	if recent < 0 || authors < 0 || authors < recent {
		t.Fatalf("audiobook Discover rows %v, want recently_added then authors", ids)
	}
	rail := page.Sections[authors]
	if rail.Type != "rail" || rail.Heading.Key != "section.authors" || len(rail.Entries) != 3 || rail.TotalCount != 3 {
		t.Fatalf("authors rail: %+v", rail)
	}
	listing := base
	listing.View = "authors"
	view, err := s.Content(listing)
	if err != nil || len(view.Sections) != 1 || view.Sections[0].Entries[0].ID != rail.Entries[0].ID {
		t.Fatalf("authors rail and Authors view disagree: %+v, %v", view, err)
	}
}

// J-29: a video library's Discover keeps the mock-ups' order, Recently added
// before Recommended for you.
func TestVideoDiscoverPutsRecentlyAddedBeforeRecommended(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("films", "Films", "movie", "/films")
	for i := 0; i < 30; i++ {
		c.Movie(lib, fmt.Sprintf("/films/film%02d.mkv", i), fmt.Sprintf("Film %02d", i), 1990+i)
	}
	c.Drain()
	s := New(c.DB)
	discovery, err := s.Discover("films", "p")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, section := range discovery.Sections {
		ids = append(ids, section.ID)
	}
	t.Logf("Discover rows: %v", ids)
	recent, recommended := discoverIndex(ids, "recently_added"), discoverIndex(ids, "recommended")
	if recent < 0 {
		t.Fatalf("no Recently added row: %v", ids)
	}
	if recommended < 0 || recommended < recent {
		t.Fatalf("Discover rows %v put Recommended before Recently added", ids)
	}
}

// Book and comic libraries are read through /v1/reading. Asking the library
// content API for one is a request error, never a server failure (the demo
// answered 500 for view=discover).
func TestReadingLibraryContentIsARequestError(t *testing.T) {
	c := catalogtest.Open(t)
	c.Library("books", "Books", "book", "/read")
	c.Library("comics", "Comics", "comic", "/comics")
	c.Drain()
	s := New(c.DB)
	for _, library := range []string{"books", "comics"} {
		for _, view := range []string{"", "discover", "browse", "categories"} {
			_, err := s.Content(ContentRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{library}}, ServerID: "server", Library: library, Profile: "p", ViewerFence: "f", View: view, Limit: 12})
			if !requestinput.Is(err) {
				t.Fatalf("%s view %q: %v, want a request error", library, view, err)
			}
		}
	}
}
