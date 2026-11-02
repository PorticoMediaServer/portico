package catalog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// A book's "More audiobooks" row is never empty on a library with other books
// (lead, 2 Nov): with no similar ranking at all (no genres, no co-play), the
// same author's books come first, then the library's recently added books;
// each book appears once, never the seed, never an unavailable one.
func TestListeningBookRowFallsBackToAuthorThenRecentlyAdded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}
	s, c, n := phase34ListeningFixture(t)
	books := c.Handle("books")
	other := c.Book(books, "Other author's book", "Someone Else")
	gone := c.Book(books, "Unavailable book", "Someone Else")
	otherPart := c.BookFile(other, 1, "/books/other.m4b")
	gonePart := c.BookFile(gone, 1, "/books/gone.m4b")
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetAvailableTx(ctx, tx, gonePart.Asset, false)
	})
	c.Drain()
	_ = otherPart

	v := listeningRequest("books", "book", n["book-a"].Public).Viewer
	page, err := s.ListeningSimilar(v, n["book-a"].Public, 12)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, e := range page.Entries {
		if seen[e.ID] {
			t.Fatalf("duplicate %s in %v", e.ID, ids)
		}
		seen[e.ID] = true
		ids = append(ids, e.ID)
	}
	if len(ids) != 2 || ids[0] != n["book-b"].Public || ids[1] != other.Public {
		t.Fatalf("same author first, then recently added: got %v, want [%s %s]", ids, n["book-b"].Public, other.Public)
	}
	if seen[n["book-a"].Public] || seen[gone.Public] {
		t.Fatalf("seed or unavailable book listed: %v", ids)
	}
	// A library with no other available book still has no row.
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for _, a := range []int64{n["part-b1"].Asset, otherPart.Asset} {
			if err := compactcatalog.SetAssetAvailableTx(ctx, tx, a, false); err != nil {
				return err
			}
		}
		return nil
	})
	c.Drain()
	if page, err = s.ListeningSimilar(v, n["book-a"].Public, 12); err != nil || len(page.Entries) != 0 {
		t.Fatalf("no other available book: %+v %v", page.Entries, err)
	}
}

// "More releases by …" is the artist's row. The related ranking reads a
// bounded pool (relatedPoolLimit), so in a large library the artist's other
// releases can miss it; the row then lists them from the artist's own facet:
// never the item's own album, never one already shown, at most the row size.
func TestListeningFacetFillListsTheFacetsOtherWorks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}
	s, c, n := phase34ListeningFixture(t)
	next := c.Album(n["artist"], "Second Release", 2021)
	third := c.Album(n["artist"], "Third Release", 2022)
	c.Song(next, 1, "/music/second.mp3", "Second")
	c.Song(third, 1, "/music/third.mp3", "Third")
	c.Drain()
	item := Item{ID: n["song-001"].Public}
	facet := "a:" + n["artist"].Public
	got, err := s.listeningFacetFill(item, facet, map[string]bool{next.Public: true}, listeningRowSize)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != third.Public {
		t.Fatalf("the artist's other release not already shown: got %v, want [%s] (own album %s, shown %s)", got, third.Public, n["album"].Public, next.Public)
	}
	if got, err = s.listeningFacetFill(item, facet, map[string]bool{}, 1); err != nil || len(got) != 1 {
		t.Fatalf("capped at n: %v %v", got, err)
	}
}
