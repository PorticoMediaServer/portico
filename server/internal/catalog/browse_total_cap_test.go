package catalog

import (
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// A posted filter counts its matches up to the threshold. Above it the total
// is the threshold, marked as a lower bound, the count stops growing with the
// matches, and pages still continue by cursor and by direct range.
func TestFilteredTotalsStopAtTheThreshold(t *testing.T) {
	defer func(threshold, groups int) { browseTotalThreshold, groupMatchLimit = threshold, groups }(browseTotalThreshold, groupMatchLimit)
	browseTotalThreshold, groupMatchLimit = 20, 30
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	for i := 0; i < 60; i++ {
		year := 1990 + i%10 // forty in the 1990s, twenty in the 2000s
		if i >= 40 {
			year += 10
		}
		item := c.Movie(lib, fmt.Sprintf("/m/%02d.mkv", i), fmt.Sprintf("Film %02d", i), year)
		genre := "Drama"
		if i%10 == 0 {
			genre = "Rare"
		}
		c.Genres(item.ID, "fixture", genre)
	}
	c.Drain()
	v := Viewer{Profile: "p", Fence: "cap", Libraries: []string{"m"}}
	s := New(c.DB)
	browse := func(query *BrowseNode, cursor string, r *BrowseRange) BrowseResult {
		t.Helper()
		page, err := s.BrowseEntities(v, BrowseRequest{Library: "m", Pivot: "movies", Query: query, Limit: 8, Cursor: cursor, Range: r})
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	// Exact below the threshold.
	rare := &BrowseNode{Field: "genre", Operator: "contains", Value: "Rare"}
	if page := browse(rare, "", nil); page.PageInfo.Total != 6 || page.PageInfo.TotalIsLowerBound {
		t.Fatalf("selective filter: %+v", page.PageInfo)
	}
	// Exact at the threshold.
	early := &BrowseNode{Field: "title", Operator: "starts-with", Value: "Film 0"}
	if page := browse(&BrowseNode{Any: []BrowseNode{*early, {Field: "title", Operator: "starts-with", Value: "Film 1"}}}, "", nil); page.PageInfo.Total != 20 || page.PageInfo.TotalIsLowerBound {
		t.Fatalf("filter at the threshold: %+v", page.PageInfo)
	}
	// Capped above it; every match is still reachable by cursor.
	broad := &BrowseNode{Field: "title", Operator: "contains", Value: "Film"}
	first := browse(broad, "", nil)
	if first.PageInfo.Total != 20 || !first.PageInfo.TotalIsLowerBound || !first.PageInfo.HasMore {
		t.Fatalf("broad filter: %+v", first.PageInfo)
	}
	seen := map[string]bool{}
	for page := first; ; {
		for _, entry := range page.Entries {
			seen[entry.ID] = true
		}
		if !page.PageInfo.HasMore {
			break
		}
		page = browse(broad, page.PageInfo.NextCursor, nil)
		if page.PageInfo.Total != 20 || !page.PageInfo.TotalIsLowerBound {
			t.Fatalf("continuation lost its lower bound: %+v", page.PageInfo)
		}
	}
	if len(seen) != 60 {
		t.Fatalf("paged %d of 60 matches past the threshold", len(seen))
	}
	// A direct range past the threshold is not clamped to it.
	if deep := browse(broad, "", &BrowseRange{Start: 48}); deep.PageInfo.Start != 48 || len(deep.Entries) != 8 {
		t.Fatalf("range past the threshold: %+v with %d entries", deep.PageInfo, len(deep.Entries))
	}
	// Groupings: a narrow filter groups exactly, a broad one counts each
	// group up to the threshold.
	groups := func(query *BrowseNode, sort ...BrowseSortSelection) map[string]ContentEntry {
		page, err := s.BrowseEntities(v, BrowseRequest{Library: "m", Pivot: "categories", Query: query, Sort: sort, Limit: 40})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]ContentEntry{}
		for _, entry := range page.Entries {
			out[entry.Title] = entry
		}
		return out
	}
	narrow := groups(&BrowseNode{Field: "year", Operator: "equals", Value: float64(1990)})
	if len(narrow) != 1 || *narrow["1990s"].Count != 4 || narrow["1990s"].TotalAtLeast != nil {
		t.Fatalf("narrow grouping: %+v", narrow)
	}
	for _, order := range [][]BrowseSortSelection{nil, {{Field: "count", Direction: "desc"}}, {{Field: "recentActivity", Direction: "desc"}}} {
		wide := groups(broad, order...)
		if len(wide) != 2 || *wide["1990s"].Count != 20 || wide["1990s"].TotalAtLeast == nil || *wide["2000s"].Count != 20 || wide["2000s"].TotalAtLeast != nil {
			t.Fatalf("broad grouping by %v: %+v", order, wide)
		}
	}
	wide := groups(broad)
	if len(wide) != 2 || *wide["1990s"].Count != 20 || wide["1990s"].TotalAtLeast == nil || *wide["1990s"].TotalAtLeast != 20 || *wide["2000s"].Count != 20 || wide["2000s"].TotalAtLeast != nil {
		t.Fatalf("broad grouping: %+v", wide)
	}
}

func TestBrowseFacetAndDurationLowerBoundsNeverCapSource(t *testing.T) {
	defer func(n int) { browseTotalThreshold = n }(browseTotalThreshold)
	browseTotalThreshold = 20
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	for i := 0; i < 61; i++ {
		item := c.Movie(lib, fmt.Sprintf("/m/%03d.mkv", i), fmt.Sprintf("Film %03d", i), 2026)
		c.Genres(item.ID, "fixture", "Drama")
		if i == 60 {
			c.Genres(item.ID, "fixture", "Rare beyond count bound")
		}
	}
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	q := &BrowseNode{Field: "year", Operator: "equals", Value: float64(2026)}
	facet, err := s.Facets(v, FacetRequest{Library: "m", Pivot: "movies", Field: "genre", Query: q})
	if err != nil || len(facet.Values) != 2 || facet.Values[0].Count != 20 || !facet.Values[0].CountIsLowerBound || facet.Total != 2 || facet.TotalIsLowerBound {
		t.Fatalf("capped facet %+v %v", facet, err)
	}
	rare, err := s.Facets(v, FacetRequest{Library: "m", Pivot: "movies", Field: "genre", Query: q, Q: "Rare beyond"})
	if err != nil || len(rare.Values) != 1 || rare.Values[0].Count != 1 || rare.Values[0].CountIsLowerBound {
		t.Fatalf("facet source truncated %+v %v", rare, err)
	}
	page, err := s.BrowseEntities(v, BrowseRequest{Library: "m", Pivot: "movies", Query: q, Limit: 7})
	if err != nil || !page.PageInfo.DurationIsLowerBound || page.PageInfo.DurationMs <= 0 {
		t.Fatalf("duration lower bound %+v %v", page, err)
	}
	// Candidate/count bounds never become source-membership limits.
	got := 0
	for {
		got += len(page.Entries)
		if page.PageInfo.NextCursor == "" {
			break
		}
		page, err = s.BrowseEntities(v, BrowseRequest{Library: "m", Pivot: "movies", Query: q, Limit: 7, Cursor: page.PageInfo.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
	}
	if got != 61 {
		t.Fatalf("source capped at %d", got)
	}
}
