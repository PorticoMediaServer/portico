package catalog

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestGroupOrderWindowMatchesBrowseAcrossPages(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	var all []catalogtest.Item
	for i := 0; i < 12; i++ {
		item := c.Movie(lib, fmt.Sprintf("/m/%d.mkv", i), fmt.Sprintf("Movie %02d", i), 2000+i)
		if i%3 != 0 {
			c.Genres(item.ID, "fixture", "Drama")
		}
		all = append(all, item)
	}
	shelf := c.Collection(lib, "Shelf", all[8], all[2], all[11], all[0], all[5], all[9], all[1], all[4], all[7])
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	for _, direction := range []string{"asc", "desc"} {
		t.Run(direction, func(t *testing.T) {
			target := BrowseNode{Field: "collection", Operator: "equals", Value: shelf.Public}
			request := BrowseRequest{Library: "m", Pivot: "movies", Query: &BrowseNode{All: []BrowseNode{target, {Field: "genre", Operator: "equals", Value: "Drama"}}}, Sort: []BrowseSortSelection{{Field: "groupOrder", Direction: direction}}, Limit: 100}
			basis, witness := smallOrderBasis(t, s, "m", "p")
			var template *BrowseSelectionBatchState
			var want []int64
			err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
				read := s.WithContext(ctx)
				page, err := read.BrowseEntities(v, request)
				if err != nil {
					return err
				}
				for _, entry := range page.Entries {
					want = append(want, c.ID(entry.ID))
				}
				template, err = read.NewBrowseSelectionBatch(request, v, *page.Bound, basis)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			step := func(ctx context.Context, p *BrowseSelectionBatchState, w SelectionBatchWitness, l SmallSelectionOrderLimits) (*BrowseSelectionBatchState, SelectionBatchWork, error) {
				return s.StepBrowseSelectionBatch(ctx, p, w, SelectionBatchLimits{Rows: l.Rows, Bytes: l.Bytes, RetainedBytes: l.RetainedBytes})
			}
			var got []int64
			var cursor *GroupOrderWindowCursor
			units := 0
			for page := 0; ; page++ {
				if page > 10 {
					t.Fatal("iterator did not reach EOF")
				}
				window, err := NewGroupOrderWindow(template, &target, direction, 2, cursor)
				if err != nil {
					t.Fatal(err)
				}
				for turn := 0; !window.Complete(); turn++ {
					if turn > 20000 {
						t.Fatal("window did not reach raw EOF")
					}
					before := window.clone()
					next, work, err := s.StepGroupOrderWindow(context.Background(), window, witness, step, SmallSelectionOrderLimits{Rows: 1, Bytes: 128})
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(window, before) {
						t.Fatal("window mutated input")
					}
					if work.Rows > 1 {
						t.Fatal("unbounded raw unit", work)
					}
					units += work.Rows
					window = next
				}
				ids, after, eof, err := window.Result()
				if err != nil {
					t.Fatal(err)
				}
				if len(ids) > 2 {
					t.Fatal("window escaped output capacity")
				}
				got = append(got, ids...)
				cursor = after
				if eof {
					break
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("window order %v, Browse %v", got, want)
			}
			if units < 3*len(all) {
				t.Fatalf("raw rejected source rows hidden: %d units", units)
			}
			window, err := NewGroupOrderWindow(template, &target, direction, 2, nil)
			if err != nil {
				t.Fatal(err)
			}
			c.Exec(`UPDATE catalog_collection_members SET order_key=order_key||'x' WHERE collection_id=? AND item_id=?`, shelf.ID, all[8].ID)
			_, _, err = s.StepGroupOrderWindow(context.Background(), window, witness, step, SmallSelectionOrderLimits{})
			if !errors.Is(err, ErrSelectionBatchStale) {
				t.Fatalf("source drift accepted: %v", err)
			}
		})
	}
}
