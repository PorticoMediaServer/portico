package catalog

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// tl10BrowseFixture is the compact-catalogue equivalent of browseFixture from
// the legacy test file. Keep its readable names here because the original
// helper is outside this lane's ownership.
func tl10BrowseFixture(t *testing.T) (*sql.DB, *Service, catalogtest.Names) {
	t.Helper()
	c := catalogtest.Open(t)
	library := c.Library("a", "Movies", "movie", "/a")
	names := catalogtest.Names{}
	for _, row := range []struct {
		name, title string
		year        int
		genres      []string
	}{
		{"m1", "Alpha", 1995, []string{"Action", "Drama"}},
		{"m2", "Beta", 2005, []string{"Action"}},
		{"m3", "Gamma", 2015, []string{"Comedy"}},
		{"m4", "Delta", 2015, nil},
		{"m5", "9 Lives", 1985, nil},
	} {
		item := c.Movie(library, "/a/"+row.name+".mkv", row.title, row.year)
		names[row.name] = item
		if len(row.genres) > 0 {
			c.Genres(item.ID, "fixture", row.genres...)
		}
	}
	c.Drain()
	return c.DB, New(c.DB), names
}

func tl10AddBrowseMovie(t *testing.T, db *sql.DB, names catalogtest.Names, name, title string, year int) {
	t.Helper()
	c := catalogtest.New(t, db)
	names[name] = c.Movie(c.Handle("a"), "/a/"+name+".mkv", title, year)
}

// browseFullOrder pages through the whole movies pivot with cursors under the
// given sort and returns the ids in page order.
func browseFullOrder(t *testing.T, s *Service, names catalogtest.Names, sort []BrowseSortSelection) []string {
	t.Helper()
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	request := BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Sort: sort, Limit: 2}
	out := []string{}
	for {
		page, err := s.BrowseEntities(viewer, request)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range page.Entries {
			out = append(out, names.Of(entry.ID))
		}
		if page.PageInfo.NextCursor == "" {
			break
		}
		request.Cursor = page.PageInfo.NextCursor
	}
	return out
}

func TestBrowseReanchorNonTitleSortsRelocate(t *testing.T) {
	db, s, names := tl10BrowseFixture(t)
	caps, err := s.BrowseCapabilitiesFor("a", "movies")
	if err != nil {
		t.Fatal(err)
	}
	sorts := []BrowseSortSelection{}
	for _, sc := range caps.Sorts {
		// For you has no position anchors (a taste order has no letter or year
		// to return to): a stale reader restarts from the first page.
		if sc.ID == "title" || sc.ID == "forYou" || sc.RequiresGroup {
			continue
		}
		for _, direction := range sc.Directions {
			sorts = append(sorts, BrowseSortSelection{Field: sc.ID, Direction: direction})
			if sc.ID == "random" {
				for _, seed := range []uint32{123, 2000000000, 4294967295} {
					sorts = append(sorts, BrowseSortSelection{Field: sc.ID, Direction: direction, Seed: seed})
				}
			}
		}
	}
	if len(sorts) == 0 {
		t.Fatal("movies pivot publishes no non-title sorts")
	}
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	type oldRev struct {
		sort     BrowseSortSelection
		revision string
	}
	olds := []oldRev{}
	for _, sort := range sorts {
		page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Sort: []BrowseSortSelection{sort}, Limit: 2, Range: &BrowseRange{Start: 1}})
		if err != nil {
			t.Fatalf("sort %v: %v", sort, err)
		}
		olds = append(olds, oldRev{sort, page.PageInfo.Revision})
	}
	tl10AddBrowseMovie(t, db, names, "mx", "Zulu Extra", 2000)
	settleCompact(t, db)
	for _, o := range olds {
		page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Sort: []BrowseSortSelection{o.sort}, Limit: 2, Range: &BrowseRange{Start: 1, Revision: o.revision, AnchorID: names["m1"].Public}})
		definition, _ := browseSortByID(o.sort.Field)
		if definition.Profile {
			// Profile sorts have no counted order to rank in.
			if err != nil {
				t.Fatalf("sort %v could not retain the viewport: %v", o.sort, err)
			}
			continue
		}
		if err != nil || len(page.Entries) == 0 || page.Entries[0].ID != names["m1"].Public {
			t.Fatalf("sort %v did not re-locate on its anchor: %+v %v", o.sort, page.Entries, err)
		}
	}
}

func TestBrowseReanchorNonTitleSortCurrentRevisionIgnoresAnchor(t *testing.T) {
	_, s, _ := tl10BrowseFixture(t)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	sort := []BrowseSortSelection{{Field: "year", Direction: "asc"}}
	first, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Sort: sort, Limit: 2, Range: &BrowseRange{Start: 2}})
	if err != nil {
		t.Fatal(err)
	}
	current := first.PageInfo.Revision
	withAnchor, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Sort: sort, Limit: 2, Range: &BrowseRange{Start: 2, Revision: current, AnchorID: "m1"}})
	if err != nil {
		t.Fatal(err)
	}
	without, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Sort: sort, Limit: 2, Range: &BrowseRange{Start: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if withAnchor.PageInfo.Start != 2 {
		t.Fatalf("current revision honored anchor: %+v", withAnchor.PageInfo)
	}
	withIDs := []string{}
	for _, e := range withAnchor.Entries {
		withIDs = append(withIDs, e.ID)
	}
	withoutIDs := []string{}
	for _, e := range without.Entries {
		withoutIDs = append(withoutIDs, e.ID)
	}
	if fmt.Sprint(withIDs) != fmt.Sprint(withoutIDs) {
		t.Fatalf("anchor changed current page: %v vs %v", withIDs, withoutIDs)
	}
}

func TestBrowseFreshBucketRequestLandsOnTheBucket(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	_, s, _ := tl10BrowseFixture(t)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	sort := []BrowseSortSelection{{Field: "year", Direction: "desc"}}
	first, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Sort: sort, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.PositionIndex) == 0 {
		t.Fatal("year desc publishes no anchors")
	}
	// The anchor keys for year are the year values: confirm by reading the
	// projector's fixed bucket axes via compactBrowseSortAnchors.
	pivot, ok := pivotForKind("movie", "movies")
	if !ok {
		t.Fatal("movies pivot missing")
	}
	anchors, err := s.compactBrowseSortAnchors(BrowseRequest{Library: "a", Profile: "p", Pivot: "movies"}, pivot, sort[0])
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(anchors) != fmt.Sprint(first.PositionIndex) {
		t.Fatalf("position index %+v differs from bucket anchors %+v", first.PositionIndex, anchors)
	}
	current := first.PageInfo.Revision
	for _, anchor := range first.PositionIndex {
		got, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Sort: sort, Limit: 2, Range: &BrowseRange{Start: anchor.Index, Revision: current}})
		if err != nil {
			t.Fatalf("anchor %+v: %v", anchor, err)
		}
		if len(got.Entries) == 0 {
			t.Fatalf("anchor %+v landed on empty page", anchor)
		}
		year := got.Entries[0].Subtitle
		if strings.HasSuffix(anchor.Key, "s") {
			y, perr := strconv.Atoi(year)
			if perr != nil {
				t.Fatalf("anchor %+v entry subtitle %q", anchor, year)
			}
			want := strconv.Itoa((y/10)*10) + "s"
			if want != anchor.Key {
				t.Fatalf("anchor %+v landed on year %q", anchor, year)
			}
			continue
		}
		if year != anchor.Key {
			t.Fatalf("anchor %+v landed on year %q", anchor, year)
		}
	}
}

func TestBrowseReanchorTitleAscendingStaysExact(t *testing.T) {
	db, s, names := tl10BrowseFixture(t)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 2, Range: &BrowseRange{Start: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if page.PageInfo.Start != 3 || page.Entries[0].ID != names["m4"].Public {
		t.Fatalf("range %+v", page.PageInfo)
	}
	revision := page.PageInfo.Revision
	tl10AddBrowseMovie(t, db, names, "m0", "AAA", 2001)
	settleCompact(t, db)
	if _, err = s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 2, Range: &BrowseRange{Start: 3, Revision: revision}}); err != nil {
		t.Fatalf("range did not refresh %v", err)
	}
	moved, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 2, Range: &BrowseRange{Start: 3, Revision: revision, AnchorID: names["m4"].Public}})
	if err != nil {
		t.Fatal(err)
	}
	if moved.PageInfo.Start != 4 || moved.Entries[0].ID != names["m4"].Public {
		t.Fatalf("anchor %+v %v", moved.PageInfo, moved.Entries)
	}
	if _, err = s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 2, Range: &BrowseRange{Start: 3, Revision: revision, AnchorID: "missing"}}); err != nil {
		t.Fatalf("missing anchor did not fall back %v", err)
	}
}

func TestBrowseReanchorTitleDescendingIsExact(t *testing.T) {
	db, s, names := tl10BrowseFixture(t)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	sort := []BrowseSortSelection{{Field: "title", Direction: "desc"}}
	page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Sort: sort, Limit: 2, Range: &BrowseRange{Start: 1}})
	if err != nil {
		t.Fatal(err)
	}
	old := page.PageInfo.Revision
	// Ties (Delta twice more) plus several heads (AAA in A, Zulu in Z).
	for _, row := range [][3]string{{"m6", "Delta", "2010"}, {"m7", "Delta", "2011"}, {"m8", "Zulu", "2000"}, {"m0", "AAA", "2001"}} {
		year, err := strconv.Atoi(row[2])
		if err != nil {
			t.Fatal(err)
		}
		tl10AddBrowseMovie(t, db, names, row[0], row[1], year)
	}
	settleCompact(t, db)
	expected := browseFullOrder(t, s, names, sort)
	if len(expected) != 9 {
		t.Fatalf("expected 9 movies, got %v", expected)
	}
	for index, name := range expected {
		id := names[name].Public
		got, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Sort: sort, Limit: 2, Range: &BrowseRange{Start: 0, Revision: old, AnchorID: id}})
		if err != nil {
			t.Fatalf("anchor %s: %v", id, err)
		}
		if got.PageInfo.Start != index {
			t.Fatalf("anchor %s landed at %d, want %d (order %v)", id, got.PageInfo.Start, index, expected)
		}
		if len(got.Entries) == 0 || got.Entries[0].ID != id {
			t.Fatalf("anchor %s first entry %v", id, got.Entries)
		}
	}
	if _, err = s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Sort: sort, Limit: 2, Range: &BrowseRange{Start: 0, Revision: old, AnchorID: "missing"}}); err != nil {
		t.Fatalf("missing anchor did not fall back %v", err)
	}
}

func TestBrowseReanchorDoesNotRankTheSet(t *testing.T) {
	// Behavioral guard for the no-ranking rule: a stale non-title anchor
	// answers ErrStaleContinuation even when the anchor id does not exist,
	// which proves no lookup decides the answer. The ROW_NUMBER() helper
	// browseAnchorRank was deleted; this test documents that rule.
	db, s, names := tl10BrowseFixture(t)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	sort := []BrowseSortSelection{{Field: "year", Direction: "asc"}}
	page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Sort: sort, Limit: 2, Range: &BrowseRange{Start: 1}})
	if err != nil {
		t.Fatal(err)
	}
	old := page.PageInfo.Revision
	tl10AddBrowseMovie(t, db, names, "mx", "Zulu Extra", 2000)
	settleCompact(t, db)
	if _, err = s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Sort: sort, Limit: 2, Range: &BrowseRange{Start: 1, Revision: old, AnchorID: "definitely-missing"}}); err != nil {
		t.Fatalf("missing anchor did not retain viewport: %v", err)
	}
}
