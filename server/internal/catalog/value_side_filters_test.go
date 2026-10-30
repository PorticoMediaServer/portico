package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Versions, duration, resolution, credited-name and missing-genre filters select from the
// value side (maintained version counts, the files' indexes, the credited
// names); they must match exactly the titles the per-title test matches.
func TestValueSideFiltersMatchTheTitleTest(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("m", "Movies", "movie", "/m")
	for i := 0; i < 24; i++ {
		item := c.Movie(lib, fmt.Sprintf("/m/%d.mkv", i), fmt.Sprintf("Film %d", i), 2000)
		if i%3 == 0 {
			c.File(item.ID, fmt.Sprintf("/m/%d-b.mkv", i), float64(3000+i))
		}
		if i%2 == 0 {
			c.Genres(item.ID, "fixture", "Drama")
		}
		c.Exec(`UPDATE catalog_assets SET height=?,duration=? WHERE id=?`, []int{480, 720, 1080, 2160}[i%4], float64(5000+i%5), item.Asset)
		name := fmt.Sprintf("Actor %d", i%6)
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.SetCreditsTx(ctx, tx, item.ID, "fixture", []compactcatalog.Credit{
				{PersonKey: "p:" + name, PersonName: name, CreditedName: name, Role: "Actor", Department: "Acting"},
				{PersonKey: "d:" + name, PersonName: "Dir " + name, CreditedName: "Dir " + name, Role: "Director", Department: "Directing"}})
		})
	}
	c.Drain()
	v := Viewer{Profile: "p", Fence: "value-side", Libraries: []string{"m"}}
	s := New(c.DB)
	nodes := []BrowseNode{
		{Field: "versions", Operator: "equals", Value: float64(2)},
		{Field: "versions", Operator: "at-least", Value: float64(1)},
		{Field: "versions", Operator: "less-than", Value: float64(2)},
		{Field: "versions", Operator: "not-equals", Value: float64(2)},
		{Field: "durationMs", Operator: "equals", Value: float64(5002000)},
		{Field: "durationMs", Operator: "less-than", Value: float64(4000000)},
		{Field: "durationMs", Operator: "between", Value: []any{float64(5001000), float64(5003000)}},
		{Field: "resolution", Operator: "equals", Value: "4k"},
		{Field: "resolution", Operator: "in", Value: []any{"sd", "720p"}},
		{Field: "resolution", Operator: "not-equals", Value: "1080p"},
		{Field: "actor", Operator: "equals", Value: "actor 3"},
		{Field: "actor", Operator: "contains", Value: "Actor 1"},
		{Field: "director", Operator: "starts-with", Value: "Dir Actor"},
		{Field: "actor", Operator: "not-in", Value: []any{"Actor 2", "Actor 4"}},
		{Field: "writer", Operator: "equals", Value: "Actor 1"},
		{Field: "genre", Operator: "is-missing", Value: nil},
	}
	for _, node := range nodes {
		node := node
		got := map[string]bool{}
		page, err := s.BrowseEntities(v, BrowseRequest{Library: "m", Pivot: "movies", Query: &node, Limit: 100})
		if err != nil {
			t.Fatalf("%+v: %v", node, err)
		}
		for _, entry := range page.Entries {
			got[entry.ID] = true
		}
		where, args, err := CompileBrowseSelection(&node)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := c.DB.Query(`SELECT pid(ce.public_id) FROM catalog_browse_rows e JOIN catalog_entities ce ON ce.id=e.entity_id WHERE e.kind=1 AND `+where, args...)
		if err != nil {
			t.Fatalf("%+v: %v", node, err)
		}
		want := []string{}
		for rows.Next() {
			var id string
			rows.Scan(&id)
			want = append(want, id)
		}
		rows.Close()
		gotList := []string{}
		for id := range got {
			gotList = append(gotList, id)
		}
		sort.Strings(want)
		sort.Strings(gotList)
		if fmt.Sprint(want) != fmt.Sprint(gotList) {
			t.Fatalf("%s %s %v: value side %d titles, title test %d", node.Field, node.Operator, node.Value, len(gotList), len(want))
		}
		t.Logf("%s %s %v: %d titles", node.Field, node.Operator, node.Value, len(want))
	}
	var versions, names int
	c.DB.QueryRow(`SELECT count(*) FROM catalog_item_version_counts WHERE versions=2`).Scan(&versions)
	c.DB.QueryRow(`SELECT count(*) FROM catalog_credit_names`).Scan(&names)
	if versions != 8 || names != 12 {
		t.Fatalf("maintained versions=%d names=%d", versions, names)
	}
}
