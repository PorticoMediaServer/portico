package catalog

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
)

// referenceContextualFacets is the per-request SQL contextual facets ran
// before B2: the grouped page and its separately counted total.
func referenceContextualFacets(t *testing.T, s *Service, viewer Viewer, request FacetRequest) FacetPage {
	t.Helper()
	out := FacetPage{Values: []FacetValue{}}
	source := facetSources[request.Field]
	request.Query = withoutFacet(request.Query, request.Field)
	scope, args, _, err := s.browseMeasureScope(viewer, request)
	if err != nil {
		t.Fatal(err)
	}
	prefix := `WITH targets AS MATERIALIZED (` + scope + `)`
	memberVisible, memberArgs := viewer.itemVisibilitySQL("member.item_id")
	prefix += `, e AS MATERIALIZED (SELECT DISTINCT target.entity_id,target.library_id,target.year,member.item_id FROM targets target JOIN catalog_browse_memberships member ON member.entity_id=target.entity_id WHERE ` + memberVisible + `)`
	bound := append(append([]any{}, args...), memberArgs...)
	where := source.Value + ` IS NOT NULL AND CAST(` + source.Value + ` AS TEXT)<>''`
	if request.Field == "year" || request.Field == "decade" {
		where += ` AND e.year BETWEEN 1800 AND 2199`
	}
	if request.Q != "" {
		where += ` AND instr(portico_fold_text(` + source.Label + `),?)>0`
		bound = append(bound, sorttext.Fold(request.Q))
	}
	counted := ` SELECT CAST(` + source.Value + ` AS TEXT) AS value,` + source.Label + ` AS label,count(DISTINCT e.entity_id) AS total FROM e ` + source.Join + ` WHERE ` + where + ` GROUP BY value`
	order := `total DESC,label COLLATE NOCASE,value`
	if strings.HasPrefix(request.Sort, "title:") {
		order = `label COLLATE NOCASE,value`
		if request.Sort == "title:desc" {
			order = `label COLLATE NOCASE DESC,value DESC`
		}
	}
	rows, err := s.read().Query(prefix+counted+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(bound, request.Limit+1, max(0, request.Offset))...)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var value FacetValue
		if err = rows.Scan(&value.Value, &value.Label, &value.Count); err != nil {
			t.Fatal(err)
		}
		if value.Count > browseTotalThreshold {
			value.Count, value.CountIsLowerBound = browseTotalThreshold, true
		}
		out.Values = append(out.Values, value)
	}
	rows.Close()
	if len(out.Values) > request.Limit {
		out.Values = out.Values[:request.Limit]
	}
	if err = s.read().QueryRow(prefix+` SELECT count(*) FROM (`+counted+` LIMIT `+fmt.Sprint(browseTotalThreshold+1)+`)`, bound...).Scan(&out.Total); err != nil {
		t.Fatal(err)
	}
	if out.Total > browseTotalThreshold {
		out.Total, out.TotalIsLowerBound = browseTotalThreshold, true
	}
	return out
}

// Contextual facet panels count a revision's whole value list once and serve
// every typeahead, window and order from it, with the answers the grouped SQL
// gave per request (B2).
func TestContextualFacetsServeEveryWindowFromOnePass(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	films := c.Library("a", "Movies", "movie", "/a")
	for i := 0; i < 60; i++ {
		item := c.Movie(films, fmt.Sprintf("/a/%02d.mkv", i), fmt.Sprintf("Film %02d", i), 1990+i%25)
		genres := []string{[]string{"Action", "drama", "Comedy", "ACTION thriller", "Éclair"}[i%5]}
		if i%7 == 0 {
			genres = append(genres, "Rare "+fmt.Sprint(i))
		}
		c.Genres(item.ID, "tmdb", genres...)
		if i%3 == 0 {
			c.Attributes(item.ID, "contentRating", "R")
		}
	}
	c.Exec(`INSERT INTO content_rating_ages(value_key,minimum_age) VALUES('r',17) ON CONFLICT(value_key) DO UPDATE SET minimum_age=17`)
	c.Drain()
	s := New(c.DB)
	defer func(n int) { browseTotalThreshold = n }(browseTotalThreshold)
	age := 12
	viewers := []Viewer{
		{Profile: "p", Fence: "open", Libraries: []string{"a"}},
		{Profile: "p", Fence: "kid", Libraries: []string{"a"}, Restrictions: identity.ContentRestrictions{MaximumAge: &age}},
	}
	for _, threshold := range []int{10000, 3} {
		browseTotalThreshold = threshold
		for _, viewer := range viewers {
			for _, field := range []string{"genre", "year", "decade"} {
				for _, q := range []string{"", "act", "ACTION", "e", "zzz", "é"} {
					for _, order := range []string{"", "title:asc", "title:desc"} {
						for _, window := range [][2]int{{200, 0}, {2, 0}, {2, 3}, {5, 1000}} {
							request := FacetRequest{Library: "a", Pivot: "movies", Field: field, Q: q, Sort: order, Limit: window[0], Offset: window[1], Query: &BrowseNode{Field: "year", Operator: "at-least", Value: 1991.0}}
							got, err := s.Facets(viewer, request)
							if err != nil {
								t.Fatal(err)
							}
							want := referenceContextualFacets(t, s, viewer, request)
							if !reflect.DeepEqual(got.Values, want.Values) || got.Total != want.Total || got.TotalIsLowerBound != want.TotalIsLowerBound {
								t.Fatalf("%s %s q=%q sort=%q window=%v threshold=%d:\n got %+v total %d/%v\nwant %+v total %d/%v", viewer.Fence, field, q, order, window, threshold, got.Values, got.Total, got.TotalIsLowerBound, want.Values, want.Total, want.TotalIsLowerBound)
							}
						}
					}
				}
			}
		}
	}
	// Once counted, typeahead keystrokes, windows and orders do not count again.
	browseTotalThreshold = 10000
	viewer := Viewer{Profile: "q", Fence: "fresh", Libraries: []string{"a"}}
	if _, err := s.Facets(viewer, FacetRequest{Library: "a", Pivot: "movies", Field: "genre"}); err != nil {
		t.Fatal(err)
	}
	ctx, statements := dbwork.TraceStatements(context.Background())
	for _, q := range []string{"a", "ac", "act", "acti"} {
		if _, err := s.WithContext(ctx).Facets(viewer, FacetRequest{Library: "a", Pivot: "movies", Field: "genre", Q: q, Limit: 10, Offset: len(q)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range statements() {
		if strings.Contains(statement, "MATERIALIZED") {
			t.Fatalf("a keystroke recounted the panel: %s", statement)
		}
	}
}
