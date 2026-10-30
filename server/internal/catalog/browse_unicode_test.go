package catalog

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestBrowseTextPredicatesFoldUnicodeAndAccents(t *testing.T) {
	db, s, names := browseFixture(t)
	c := catalogtest.New(t, db)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetFieldsTx(ctx, tx, names["m1"].ID, compactcatalog.Automatic, map[string]any{"title": "Amélie 100%_"})
	})
	c.Genres(names["m1"].ID, "tmdb", "Comédie")
	c.Drain()
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{`{"variant":"predicate","field":"title","operator":"contains","value":"amelie"}`, []string{"m1"}},
		{`{"variant":"predicate","field":"title","operator":"starts-with","value":"AMÉLIE"}`, []string{"m1"}},
		{`{"variant":"predicate","field":"title","operator":"equals","value":"AMELIE 100%_"}`, []string{"m1"}},
		{`{"variant":"predicate","field":"genre","operator":"contains-any","value":["COMÉDIE"]}`, []string{"m1"}},
		{`{"variant":"predicate","field":"genre","operator":"contains-all","value":["comedie"]}`, []string{"m1"}},
		{`{"variant":"predicate","field":"title","operator":"contains","value":"%_"}`, []string{"m1"}},
		{`{"variant":"predicate","field":"title","operator":"contains","value":"_100"}`, []string{}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			got := browseIDs(t, s, names, tc.query)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("want %v got %v", tc.want, got)
			}
		})
	}
}

func TestBrowseFilteredRailRetainsUnicodeAndPositions(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("movies", "Movies", "movie", "/movies")
	for i, title := range []string{"Alpha", "Ωμέγα", "Жизнь", "東京", "Beta"} {
		c.Movie(lib, "/movies/"+title+".mkv", title, 2000+i)
	}
	c.Drain()
	s := New(c.DB)
	v := Viewer{Profile: "p", Fence: "f", Libraries: []string{"movies"}}
	q := &BrowseNode{Field: "year", Operator: "at-least", Value: float64(2001)}
	page, err := s.BrowseEntities(v, BrowseRequest{Library: "movies", Pivot: "movies", Query: q, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := []BrowsePositionAnchor{}
	for i, entry := range page.Entries {
		got = append(got, BrowsePositionAnchor{Key: entry.SortLetter, Index: i})
	}
	if len(page.PositionIndex) != 4 || !reflect.DeepEqual(page.PositionIndex, got) {
		t.Fatalf("filtered Unicode positions %+v want %+v", page.PositionIndex, got)
	}
}
