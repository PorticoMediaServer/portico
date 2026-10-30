package catalog

import (
	"context"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// A deep filtered range seeks the result's nearest sparse anchor instead of
// walking every earlier match, returns exactly the rows a keyset walk reaches
// at that position, and the title rail is the one the rail query gave (B4).
func TestFilteredRangesSeekSparseAnchors(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	defer func(n int) { rangeAnchorEvery = n }(rangeAnchorEvery)
	rangeAnchorEvery = 64
	const n = 700
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	for i := 0; i < n; i++ {
		path := fmt.Sprintf("/m/%04d.mkv", i)
		title := fmt.Sprintf("%c film %04d", "AbcDEfgH"[i%8], (i*7919)%n)
		c.Entity(compactcatalog.Entity{Library: films, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/m", path, 0), Title: title, Year: 1990 + i%5, Added: fmt.Sprintf("2026-0%d-01T00:00:00.000Z", 1+i%4)}, nil)
	}
	c.Drain()
	c.Exec(`ANALYZE`)
	viewer := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	filter := &BrowseNode{Field: "year", Operator: "at-least", Value: 1991.0}
	const limit = 23
	for _, sort := range []BrowseSortSelection{{Field: "title", Direction: "asc"}, {Field: "title", Direction: "desc"}, {Field: "year", Direction: "desc"}, {Field: "added", Direction: "asc"}} {
		t.Run(sort.Field+"/"+sort.Direction, func(t *testing.T) {
			s := New(c.DB)
			request := BrowseRequest{Library: "m", Pivot: "movies", Query: filter, Sort: []BrowseSortSelection{sort}, Limit: 100}
			whole := []string{}
			var first BrowseResult
			for pages := 0; pages < n; pages++ {
				page, err := s.BrowseEntities(viewer, request)
				if err != nil {
					t.Fatal(err)
				}
				if pages == 0 {
					first = page
				}
				for _, entry := range page.Entries {
					whole = append(whole, entry.ID)
				}
				if page.PageInfo.NextCursor == "" {
					break
				}
				request.Cursor = page.PageInfo.NextCursor
			}
			if len(whole) != n*4/5 {
				t.Fatalf("walk returned %d", len(whole))
			}
			if sort.Field == "title" {
				where, args := first.Bound.Where, first.Bound.Args
				order, orderArgs := browseOrderSQL("p", []BrowseSortSelection{sort})
				want, err := s.browsePositionIndex(where, order, args, orderArgs)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(first.PositionIndex, want) || len(want) < 5 {
					t.Fatalf("rail %v want %v", first.PositionIndex, want)
				}
			}
			ranged := func(start int) ([]string, int64) {
				var ids []string
				ctx, cost := dbwork.Measure(context.Background())
				if err := dbwork.WithReadSnapshot(ctx, c.DB, func(ctx context.Context) error {
					page, err := s.WithContext(ctx).BrowseEntities(viewer, BrowseRequest{Library: "m", Pivot: "movies", Query: filter, Sort: []BrowseSortSelection{sort}, Limit: limit, Range: &BrowseRange{Start: start}})
					for _, entry := range page.Entries {
						ids = append(ids, entry.ID)
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
				return ids, cost().VMSteps
			}
			ranged(rangeAnchorEvery) // reads the anchors once
			for _, start := range []int{63, 64, 65, 127, 128, 300, 447, 448, 540, 559, 600} {
				got, _ := ranged(start)
				// A start past the end reads the last row, as browse clamps it.
				from := min(start, len(whole)-1)
				want := whole[from:min(from+limit, len(whole))]
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("range %d: %v want %v", start, got, want)
				}
			}
			// Two rows past the first anchor and two rows past the seventh cost
			// the same: neither walks the matches before its anchor.
			_, near := ranged(rangeAnchorEvery + 2)
			_, deep := ranged(7*rangeAnchorEvery + 2)
			if deep > near+near/10 {
				t.Fatalf("a range two rows past the seventh anchor took %d VM steps against %d past the first", deep, near)
			}
			t.Logf("near %d deep %d", near, deep)
		})
	}
}
