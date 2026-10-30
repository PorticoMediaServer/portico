package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

// A filtered list's total is exact at any size, and a range may start
// anywhere (under a filter, past the matches it is empty; unfiltered, it
// clamps to the last row). Nothing is refused for being deep.
func TestBrowseAdHocCountsAreExactAndRangesUnbounded(t *testing.T) {
	_, s, _ := browseFixture(t)
	query, err := ParseBrowseQuery(json.RawMessage(`{"variant":"predicate","field":"year","operator":"at-least","value":0}`), "query")
	if err != nil {
		t.Fatal(err)
	}
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	for _, start := range []int{0, 20000} {
		page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Query: query, Limit: 2, Range: &BrowseRange{Start: start}})
		if err != nil || page.PageInfo.Total != 5 {
			t.Fatalf("start %d: %+v %v", start, page.PageInfo, err)
		}
	}
	page, err := s.BrowseEntities(viewer, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Limit: 2, Sort: []BrowseSortSelection{{Field: "year", Direction: "desc"}}, Range: &BrowseRange{Start: 20000}})
	// An unfiltered range past the end clamps to the last row, as it always has.
	if err != nil || page.PageInfo.Start != 4 || len(page.Entries) != 1 {
		t.Fatalf("a deep range under year order: %+v %v", page.PageInfo, err)
	}
}

func TestBrowseAdHocPagesWithFilteredTitleAnchors(t *testing.T) {
	_, s, names := browseFixture(t)
	query, err := ParseBrowseQuery(json.RawMessage(`{"variant":"predicate","field":"year","operator":"at-least","value":0}`), "query")
	if err != nil {
		t.Fatal(err)
	}
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	request := BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Query: query, Limit: 2}
	seen := []string{}
	for {
		page, err := s.BrowseEntities(viewer, request)
		if err != nil {
			t.Fatal(err)
		}
		// D37 / CB-7 supersedes the old filtered-rail exclusion. Every
		// initial filtered title page carries the same authoritative positions.
		wantAnchors := []BrowsePositionAnchor{{Key: "#", Index: 0}, {Key: "A", Index: 1}, {Key: "B", Index: 2}, {Key: "D", Index: 3}, {Key: "G", Index: 4}}
		if page.PageInfo.Total != 5 || request.Cursor == "" && fmt.Sprint(page.PositionIndex) != fmt.Sprint(wantAnchors) || request.Cursor != "" && len(page.PositionIndex) != 0 {
			t.Fatalf("filtered page metadata %+v, anchors %+v", page.PageInfo, page.PositionIndex)
		}
		for _, entry := range page.Entries {
			seen = append(seen, entry.ID)
		}
		if page.PageInfo.NextCursor == "" {
			if page.PageInfo.HasMore {
				t.Fatalf("terminal page claims more: %+v", page.PageInfo)
			}
			break
		}
		if !page.PageInfo.HasMore {
			t.Fatalf("continuation lacks hasMore: %+v", page.PageInfo)
		}
		request.Cursor = page.PageInfo.NextCursor
	}
	want := []string{}
	for _, name := range []string{"m5", "m1", "m2", "m4", "m3"} {
		want = append(want, names[name].Public)
	}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Fatalf("cursor order %v", seen)
	}
	request.Cursor = ""
	request.Range = &BrowseRange{Start: 3}
	ranged, err := s.BrowseEntities(viewer, request)
	if err != nil {
		t.Fatal(err)
	}
	if ranged.PageInfo.Start != 3 || len(ranged.Entries) != 2 || ranged.Entries[0].ID != names["m4"].Public || ranged.PageInfo.HasMore {
		t.Fatalf("filtered range %+v, entries %+v", ranged.PageInfo, ranged.Entries)
	}
	request.Range.Start = 20000
	ranged, err = s.BrowseEntities(viewer, request)
	if err != nil {
		t.Fatal(err)
	}
	if ranged.PageInfo.Start != ranged.PageInfo.Total-1 || len(ranged.Entries) != 1 {
		t.Fatalf("out-of-result filtered range did not fall back: %+v, entries %+v", ranged.PageInfo, ranged.Entries)
	}
	request.Range = nil
	request.Sort = []BrowseSortSelection{{Field: "year", Direction: "desc"}}
	sorted, err := s.BrowseEntities(viewer, request)
	if err != nil {
		t.Fatal(err)
	}
	// Gamma (m3) and Delta (m4) share 2015; ties break in the sort's direction.
	if len(sorted.PositionIndex) != 0 || len(sorted.Entries) != 2 || sorted.Entries[0].ID != names["m4"].Public {
		t.Fatalf("filtered sorted page has anchors or wrong order: %+v, entries %+v", sorted.PositionIndex, sorted.Entries)
	}
}

// D37 requires the rail to work with filters, including direct letter jumps.
func TestBrowseAdHocSeekUsesFilteredTitleRanking(t *testing.T) {
	_, s, names := browseFixture(t)
	query := &BrowseNode{Field: "year", Operator: "at-least", Value: float64(2000)}
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}
	request := BrowseRequest{Library: "a", Pivot: "movies", Query: query, Limit: 2, Seek: &BrowseSeek{Prefix: "D"}}
	page, err := s.BrowseEntities(viewer, request)
	if err != nil || page.PageInfo.Start != 1 || len(page.Entries) != 2 || page.Entries[0].ID != names["m4"].Public {
		t.Fatalf("filtered seek: %+v %v", page, err)
	}
	request.Sort = []BrowseSortSelection{{Field: "year", Direction: "asc"}}
	_, err = s.BrowseEntities(viewer, request)
	var issue *BrowseValidationError
	if !errors.As(err, &issue) || issue.Path != "seek" {
		t.Fatalf("non-title seek must still refuse: %v", err)
	}
}

func TestBrowseAdHocTreatsAnAnchorAsAPositionHint(t *testing.T) {
	_, s, _ := browseFixture(t)
	query, err := ParseBrowseQuery(json.RawMessage(`{"variant":"predicate","field":"year","operator":"at-least","value":0}`), "query")
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.BrowseEntities(Viewer{Profile: "p", Fence: "f", Libraries: []string{"a"}}, BrowseRequest{Library: "a", Profile: "p", ViewerFence: "f", Pivot: "movies", Query: query, Limit: 2, Range: &BrowseRange{Start: 1, AnchorID: "missing"}})
	if err != nil || page.PageInfo.Start != 1 {
		t.Fatalf("position hint: %+v %v", page.PageInfo, err)
	}
}
