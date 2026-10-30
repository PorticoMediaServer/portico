package catalog

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// A list across libraries reads its group once for the count, the letter rail
// and its pages; continuations and range jumps are served from that order, and
// a list past the counted maximum still walks, jumps and letters exactly as
// before (B5).
func TestAcrossListsShareOneOrderedPass(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	a := c.Library("a", "Movies", "movie", "/a")
	b := c.Library("b", "More movies", "movie", "/b")
	const n = 150
	for i := 0; i < n; i++ {
		lib, root := a, "/a"
		if i%3 == 0 {
			lib, root = b, "/b"
		}
		title := fmt.Sprintf("%c%s film %03d", "AbcDE0zZ"[i%8], strings.Repeat("x", i%4), i)
		item := c.Movie(lib, fmt.Sprintf("%s/%03d.mkv", root, i), title, 1990+i%9)
		c.Exec(`INSERT INTO personal_items(profile_id,item_id,favorite,revision) VALUES('p',?,1,1)`, item.ID)
	}
	c.Drain()
	s := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"a", "b"}}
	favorites := &BrowseNode{Field: "favorite", Operator: "equals", Value: true}
	const limit = 17
	type walked struct {
		ids     []string
		letters []BrowsePositionAnchor
		total   int
		lower   bool
	}
	walk := func(t *testing.T, sort BrowseSortSelection) walked {
		t.Helper()
		out := walked{}
		request := AcrossRequest{Pivot: "titles", Query: favorites, Sort: []BrowseSortSelection{sort}, Limit: limit}
		for pages := 0; pages < n; pages++ {
			page, err := s.BrowseAcross(viewer, request)
			if err != nil {
				t.Fatal(err)
			}
			if pages == 0 {
				out.letters, out.total, out.lower = page.PositionIndex, page.PageInfo.Total, page.PageInfo.TotalIsLowerBound
			}
			for _, entry := range page.Entries {
				out.ids = append(out.ids, entry.ID)
			}
			if page.PageInfo.NextCursor == "" {
				if page.PageInfo.HasMore {
					t.Fatal("false end")
				}
				break
			}
			request.Cursor = page.PageInfo.NextCursor
		}
		return out
	}
	jump := func(t *testing.T, sort BrowseSortSelection, start int) []string {
		t.Helper()
		page, err := s.BrowseAcross(viewer, AcrossRequest{Pivot: "titles", Query: favorites, Sort: []BrowseSortSelection{sort}, Limit: limit, Range: &BrowseRange{Start: start}})
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, entry := range page.Entries {
			ids = append(ids, entry.ID)
		}
		return ids
	}
	for _, sort := range []BrowseSortSelection{{Field: "title", Direction: "asc"}, {Field: "title", Direction: "desc"}, {Field: "year", Direction: "desc"}, {Field: "random", Direction: "asc", Seed: 11}} {
		t.Run(sort.Field+"/"+sort.Direction, func(t *testing.T) {
			whole := walk(t, sort)
			if len(whole.ids) != n || whole.total != n || whole.lower {
				t.Fatalf("walked %d of %d, total %d/%v", len(whole.ids), n, whole.total, whole.lower)
			}
			if sort.Field == "title" && len(whole.letters) < 4 {
				t.Fatalf("letter rail %v", whole.letters)
			}
			for _, start := range []int{0, 33, 140, n - 1} {
				if got, want := jump(t, sort, start), whole.ids[start:min(start+limit, n)]; !reflect.DeepEqual(got, want) {
					t.Fatalf("range %d: %v want %v", start, got, want)
				}
			}
			// Past the counted maximum the list holds only its head; later
			// pages, jumps and the rail read the group as before.
			defer func(m int) { acrossMaximum = m }(acrossMaximum)
			acrossMaximum = 40
			capped := walk(t, sort)
			if !reflect.DeepEqual(capped.ids, whole.ids) || !reflect.DeepEqual(capped.letters, whole.letters) || capped.total != 40 || !capped.lower {
				t.Fatalf("capped walk differs: %d ids, total %d/%v, letters %v want %v", len(capped.ids), capped.total, capped.lower, capped.letters, whole.letters)
			}
			for _, start := range []int{0, 30, 35, 100} {
				if got, want := jump(t, sort, start), whole.ids[start:min(start+limit, n)]; !reflect.DeepEqual(got, want) {
					t.Fatalf("capped range %d: %v want %v", start, got, want)
				}
			}
		})
	}
	// A continuation is served from the first page's ordered pass.
	first, err := s.BrowseAcross(viewer, AcrossRequest{Pivot: "titles", Query: favorites, Limit: limit})
	if err != nil {
		t.Fatal(err)
	}
	ctx, statements := dbwork.TraceStatements(context.Background())
	if _, err := s.WithContext(ctx).BrowseAcross(viewer, AcrossRequest{Pivot: "titles", Query: favorites, Limit: limit, Cursor: first.PageInfo.NextCursor}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range statements() {
		if strings.Contains(statement, "anchor(id,pos) AS MATERIALIZED") {
			t.Fatalf("a continuation expanded the group again: %s", statement)
		}
	}
}
