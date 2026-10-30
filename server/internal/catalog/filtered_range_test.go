package catalog

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestFilteredRangesMatchEveryAdvertisedOrderAndContinuation(t *testing.T) {
	db, s, names := tl10BrowseFixture(t)
	for i, name := range []string{"m1", "m3", "m4"} {
		if _, err := db.Exec(`INSERT INTO personal_items(profile_id,item_id,rating,revision,last_played_at) VALUES('p',?,?,1,?)`, names[name].ID, i+3, fmt.Sprintf("2026-09-%02dT00:00:00Z", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	if err := compactcatalog.Drain(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE catalog_personal_sort_rows SET play_count=rating WHERE profile_id='p'`); err != nil {
		t.Fatal(err)
	}
	caps, err := s.BrowseCapabilitiesFor("a", "movies")
	if err != nil {
		t.Fatal(err)
	}
	viewer := Viewer{Profile: "p", Fence: "range", Libraries: []string{"a"}}
	for _, sort := range caps.Sorts {
		if sort.RequiresGroup {
			// A group's own order needs a group; TestGroupOrderSurvivesFiltersAndPages ranges it.
			continue
		}
		for _, direction := range sort.Directions {
			t.Run(sort.ID+"/"+direction, func(t *testing.T) {
				selection := BrowseSortSelection{Field: sort.ID, Direction: direction}
				if sort.ID == "random" {
					selection.Seed = 2000000000
				}
				request := BrowseRequest{Library: "a", Pivot: "movies", Limit: 2, Sort: []BrowseSortSelection{selection}, Query: &BrowseNode{Field: "year", Operator: "at-least", Value: float64(1980)}}
				want := []string{}
				for pages := 0; pages < 10; pages++ {
					page, err := s.BrowseEntities(viewer, request)
					if err != nil {
						t.Fatal(err)
					}
					for _, entry := range page.Entries {
						want = append(want, entry.ID)
					}
					if page.PageInfo.NextCursor == "" {
						break
					}
					request.Cursor = page.PageInfo.NextCursor
				}
				if len(want) != 5 {
					t.Fatalf("continuation returned %d", len(want))
				}
				for start := 1; start < 5; start++ {
					request.Cursor = ""
					request.Range = &BrowseRange{Start: start}
					ctx, trace := dbwork.TraceQueryPlans(context.Background())
					page, err := s.WithContext(ctx).BrowseEntities(viewer, request)
					if err != nil {
						t.Fatal(err)
					}
					got := []string{}
					for _, entry := range page.Entries {
						got = append(got, entry.ID)
					}
					if !reflect.DeepEqual(got, want[start:min(start+2, 5)]) {
						t.Fatalf("start %d: got %v want %v", start, got, want)
					}
					selects := 0
					for _, q := range trace() {
						if strings.Contains(q, "WITH page AS MATERIALIZED") {
							selects++
							if strings.Contains(q, "row_number()") || !strings.Contains(q, "LIMIT ? OFFSET ?") {
								t.Fatalf("range ranked the whole match set: %s", q)
							}
						}
					}
					if sort.ID != "forYou" && selects != 1 {
						t.Fatalf("evaluated range %d times", selects)
					}
					if page.PageInfo.NextCursor != "" {
						request.Range = nil
						request.Cursor = page.PageInfo.NextCursor
						next, err := s.BrowseEntities(viewer, request)
						if err != nil {
							t.Fatal(err)
						}
						tail := []string{}
						for _, entry := range next.Entries {
							tail = append(tail, entry.ID)
						}
						if !reflect.DeepEqual(tail, want[min(start+2, 5):min(start+4, 5)]) {
							t.Fatalf("range continuation %v", tail)
						}
					}
				}
			})
		}
	}
}
