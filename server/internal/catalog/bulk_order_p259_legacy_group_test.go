package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestP259LegacyBulkSortKeyCollectionGroupOrder(t *testing.T) {
	c := catalogtest.Open(t)
	library := c.Library("m", "Movies", "movie", "/m")
	alpha := c.Movie(library, "/m/alpha.mkv", "Alpha", 2000)
	zulu := c.Movie(library, "/m/zulu.mkv", "Zulu", 2001)
	bravo := c.Movie(library, "/m/bravo.mkv", "Bravo", 2002)
	excluded := c.Movie(library, "/m/excluded.mkv", "Excluded", 1999)
	shelf := c.Collection(library, "Shelf", alpha, zulu, bravo, excluded)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i, item := range []catalogtest.Item{zulu, excluded, alpha, bravo} {
			if err := compactcatalog.SetCollectionMemberTx(ctx, tx, shelf.ID, item.ID, string(rune('A'+i)), true); err != nil {
				return err
			}
		}
		return nil
	})
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"m"}}
	node := BrowseNode{All: []BrowseNode{
		{Field: "collection", Operator: "equals", Value: shelf.Public},
		{Field: "year", Operator: "at-least", Value: float64(2000)},
	}}
	filter, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	for _, direction := range []string{"asc", "desc"} {
		t.Run(direction, func(t *testing.T) {
			selection := []BrowseSortSelection{{Field: "groupOrder", Direction: direction}}
			want := []string{zulu.Public, alpha.Public, bravo.Public}
			if direction == "desc" {
				want = []string{bravo.Public, alpha.Public, zulu.Public}
			}
			page, err := s.BrowseEntities(v, BrowseRequest{Library: "m", Pivot: "movies", Query: &node, Sort: selection, Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			browseIDs := []string{}
			for _, entry := range page.Entries {
				browseIDs = append(browseIDs, entry.ID)
			}
			if !reflect.DeepEqual(browseIDs, want) {
				t.Fatalf("Browse order %v, want %v", browseIDs, want)
			}
			tx, done, err := dbwork.BeginRead(context.Background(), c.DB)
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			selector := JobSelector{Query: &JobQuery{LibraryID: "m", Pivot: "movies", Filter: filter, Sort: selection}}
			items := []catalogtest.Item{alpha, bravo, zulu}
			keys := map[int64]string{}
			for _, item := range items {
				key, err := s.bulkSortKey(context.Background(), tx, v, selector, item.ID)
				if err != nil {
					t.Fatalf("legacy command sort key for %s: %v", item.Public, err)
				}
				keys[item.ID] = key
			}
			if _, err := s.bulkSortKey(context.Background(), tx, v, selector, excluded.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("filtered-out command item: %v, want no rows", err)
			}
			sort.Slice(items, func(i, j int) bool { return keys[items[i].ID] < keys[items[j].ID] })
			got := []string{}
			for _, item := range items {
				got = append(got, item.Public)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("legacy command order %v, want %v", got, want)
			}
		})
	}
	t.Run("missing-group", func(t *testing.T) {
		tx, done, err := dbwork.BeginRead(context.Background(), c.DB)
		if err != nil {
			t.Fatal(err)
		}
		defer done()
		selector := JobSelector{Query: &JobQuery{LibraryID: "m", Pivot: "movies", Sort: []BrowseSortSelection{{Field: "groupOrder", Direction: "asc"}}}}
		if _, err := s.bulkSortKey(context.Background(), tx, v, selector, alpha.ID); !errors.Is(err, ErrBrowseGroupOrder) {
			t.Fatalf("group order without a group: %v", err)
		}
	})
}
