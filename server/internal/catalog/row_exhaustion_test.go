package catalog

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestRowCursorExhaustionStableRevision(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	hidden := c.Library("hidden", "Hidden", "movie", "/hidden")
	want := make([]string, 1203)
	for i := range want {
		item := c.Movie(movies, fmt.Sprintf("/movies/%04d.mkv", i), fmt.Sprintf("Film %04d", i), 2026)
		c.Fields(item.ID, map[string]any{"added_text": time.Date(2026, 10, 20, 0, 0, i, 0, time.UTC).Format(time.RFC3339Nano)})
		want[len(want)-1-i] = item.Public
	}
	for i := 0; i < 3; i++ {
		denied := c.Movie(movies, fmt.Sprintf("/movies/adult%d.mkv", i), "Restricted title", 2026)
		c.Attributes(denied.ID, "contentRating", "R")
		c.Movie(hidden, fmt.Sprintf("/hidden/%d.mkv", i), "Outside library", 2026)
	}
	c.Exec(`INSERT OR REPLACE INTO content_rating_ages(value_key,minimum_age)VALUES('r',17)`)
	c.Drain()
	age := 13
	r := HomeRequest{Viewer: Viewer{Profile: "p", Fence: "f", Libraries: []string{"movies"}, Restrictions: identity.ContentRestrictions{MaximumAge: &age}}, Now: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)}
	s := New(c.DB)
	first, err := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 37})
	if err != nil {
		t.Fatal(err)
	}
	revision := fmt.Sprintf("%d:%d", first.Revision.Catalog, first.Revision.Viewer)
	all := []string{}
	cursor := ""
	pages := 0
	for {
		page, e := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 37, Cursor: cursor, Revision: revision})
		if e != nil {
			t.Fatal(e)
		}
		replay, e := s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 37, Cursor: cursor, Revision: revision})
		if e != nil || !reflect.DeepEqual(page.Entries, replay.Entries) {
			t.Fatalf("replay page %d: %v", pages, e)
		}
		prefix := append([]string{}, all...)
		for _, entry := range page.Entries {
			all = append(all, entry.ID)
		}
		if !reflect.DeepEqual(all[:len(prefix)], prefix) || !reflect.DeepEqual(all, want[:len(all)]) {
			t.Fatalf("prefix/order changed at page %d", pages)
		}
		pages++
		if rowTotal(page) != 1203 {
			t.Fatalf("count %d", page.Total)
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor || pages > 40 {
			t.Fatal("nonprogressing cursor")
		}
		cursor = page.NextCursor
	}
	if pages != 33 || !reflect.DeepEqual(all, want) {
		t.Fatalf("exhaustion pages=%d items=%d", pages, len(all))
	}
	source, err := s.ResolveHomeRowMembership(r, "new_on_server")
	if err != nil {
		t.Fatal(err)
	}
	group := BrowseNode{Field: "homeRow", Operator: "equals", Value: "new_on_server"}
	var oldBrowse string
	for _, sort := range []string{"groupOrder", "title"} {
		request := AcrossRequest{Pivot: "titles", Query: &group, Sort: []BrowseSortSelection{{Field: sort, Direction: "asc"}}, Limit: 37, RowSources: map[string]HomeRowMembership{"new_on_server": source}}
		got := []string{}
		for {
			page, e := s.BrowseAcross(r.Viewer, request)
			if e != nil {
				t.Fatal(e)
			}
			replay, e := s.BrowseAcross(r.Viewer, request)
			if e != nil || !reflect.DeepEqual(page.Entries, replay.Entries) {
				t.Fatalf("See all replay %v", e)
			}
			for _, entry := range page.Entries {
				got = append(got, entry.ID)
			}
			if page.PageInfo.Total != 1203 || page.PageInfo.TotalIsLowerBound {
				t.Fatalf("See all capped %+v", page.PageInfo)
			}
			if page.PageInfo.NextCursor == "" {
				break
			}
			if oldBrowse == "" {
				oldBrowse = page.PageInfo.NextCursor
			}
			request.Cursor = page.PageInfo.NextCursor
		}
		expected := append([]string{}, want...)
		if sort == "title" {
			for i := range expected {
				expected[i] = want[len(want)-1-i]
			}
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("See all %s: %d", sort, len(got))
		}
	}
	// A real filter still pages beyond the former 500-member boundary.
	filtered := BrowseNode{All: []BrowseNode{group, {Field: "title", Operator: "starts-with", Value: "Film 0"}}}
	request := AcrossRequest{Pivot: "titles", Query: &filtered, Sort: titleAscending(), Limit: 37, RowSources: map[string]HomeRowMembership{"new_on_server": source}}
	filteredIDs := []string{}
	for {
		page, e := s.BrowseAcross(r.Viewer, request)
		if e != nil {
			t.Fatal(e)
		}
		for _, entry := range page.Entries {
			filteredIDs = append(filteredIDs, entry.ID)
		}
		if page.PageInfo.Total != 1000 {
			t.Fatal("filtered membership count", page.PageInfo)
		}
		if page.PageInfo.NextCursor == "" {
			break
		}
		request.Cursor = page.PageInfo.NextCursor
	}
	expectedTitles := make([]string, len(want))
	for i := range want {
		expectedTitles[i] = want[len(want)-1-i]
	}
	if !reflect.DeepEqual(filteredIDs, expectedTitles[:1000]) {
		t.Fatal("filtered See all reordered or truncated its membership")
	}
	// Saved and a saved view on Home share true-end semantics, without a client
	// split, a first-60 materialization, or fabricated book progress.
	actor := ResourceActor{Authority: "local", AccountID: "account", ProfileID: "profile"}
	savedViewer := r.Viewer
	savedViewer.Profile = actorKey(actor)
	c.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,revision) SELECT ?,id,1,1 FROM catalog_entities WHERE library_id=? AND kind=1 AND title LIKE 'Film %'`, savedViewer.Profile, movies)
	savedIDs := []string{}
	savedCursor := ""
	for {
		page, e := s.Saved(ContentRequest{Viewer: savedViewer, View: "watchlist", Shape: "posters", Limit: 37, Cursor: savedCursor}, savedViewer.Libraries)
		if e != nil {
			t.Fatal(e)
		}
		if page.ShapeCounts == nil || page.ShapeCounts.Posters != 1203 {
			t.Fatal("Saved count", page.ShapeCounts)
		}
		if len(page.Sections) != 1 {
			t.Fatal("Saved missing a page")
		}
		section := page.Sections[0]
		for _, entry := range section.Entries {
			savedIDs = append(savedIDs, entry.ID)
		}
		if section.NextCursor == "" {
			break
		}
		savedCursor = section.NextCursor
	}
	if !reflect.DeepEqual(savedIDs, expectedTitles) {
		t.Fatal("Saved truncated or reordered its source")
	}
	name := "All films"
	made, e := s.MutateSavedResource(actor, "", "create", SavedResourceMutation{OperationID: "save-view", Kind: "view", Name: &name, Definition: &SavedViewDefinition{LibraryID: "movies", Pivot: "movies", Sort: titleAscending(), Presentation: "grid"}}, allow)
	if e != nil {
		t.Fatal(e)
	}
	viewRequest := HomeRequest{Viewer: savedViewer, RowOrder: []string{homeViewPrefix + made.ResourceID}}
	viewIDs := []string{}
	viewCursor := ""
	for {
		page, e := s.HomeSingleRow(viewRequest, homeViewPrefix+made.ResourceID, HomeRowPage{Limit: 37, Cursor: viewCursor})
		if e != nil {
			t.Fatal(e)
		}
		for _, entry := range page.Entries {
			viewIDs = append(viewIDs, entry.ID)
		}
		if rowTotal(page) != 1203 {
			t.Fatal("saved Home view total", page.Total)
		}
		if page.NextCursor == "" {
			break
		}
		viewCursor = page.NextCursor
	}
	if !reflect.DeepEqual(viewIDs, expectedTitles) {
		t.Fatal("saved Home view truncated or reordered the source")
	}

	c.Movie(movies, "/movies/new.mkv", "New publication", 2026)
	c.Drain()
	if _, err = s.HomeSingleRow(r, "new_on_server", HomeRowPage{Limit: 37, Cursor: first.NextCursor}); !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("Home changed revision: %v", err)
	}
	fresh, err := s.ResolveHomeRowMembership(r, "new_on_server")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.BrowseAcross(r.Viewer, AcrossRequest{Pivot: "titles", Query: &group, Sort: []BrowseSortSelection{{Field: "groupOrder", Direction: "asc"}}, Limit: 37, Cursor: oldBrowse, RowSources: map[string]HomeRowMembership{"new_on_server": fresh}}); !errors.Is(err, ErrStaleContinuation) {
		t.Fatalf("See all changed revision: %v", err)
	}
	revoked := r
	revoked.Viewer.Fence = "revoked"
	revoked.Viewer.Libraries = nil
	if _, err = s.HomeSingleRow(revoked, "new_on_server", HomeRowPage{Limit: 37, Cursor: first.NextCursor}); !errors.Is(err, ErrCursor) {
		t.Fatalf("revoked cursor: %v", err)
	}
}
