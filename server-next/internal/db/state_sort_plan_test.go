package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// Under a sort by the viewer's own state, the run of titles they have no value for walks the
// library's titles of the kind in id order and stops at a page: sorted whole, it cost 100 to 120
// ms for a 60,000-film library on every page past the titles with a value.
func TestAStateSortsTitlesWithNoValueWalkInIDOrder(t *testing.T) {
	ctx := t.Context()
	d, err := Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.read.SetMaxOpenConns(1) // one read connection, so its kept statements hold every run
	lib, err := d.CreateLibrary(ctx, "Films", LibraryMovies, "en", []string{"/films"})
	if err != nil {
		t.Fatal(err)
	}
	twelve := 12
	for _, p := range []SeedProfile{{Name: "all"}, {Name: "rated", MaxRatingAge: &twelve}} {
		v, err := d.CreateSeedProfile(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, sort := range []string{SortUserRating, SortLastPlayed, SortPlayCount} {
			if _, err := d.Items(ctx, v, ItemQuery{Library: lib.PublicID, Kinds: []ItemKind{KindMovie}, Sort: sort, Limit: 40}); err != nil {
				t.Fatal(sort, err)
			}
		}
	}
	conn, err := d.read.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var runs []string
	conn.Raw(func(c any) error {
		for e := c.(*keptConn).used.Front(); e != nil; e = e.Next() {
			if text := e.Value.(*keptStatement).text; strings.HasPrefix(text, "SELECT i.id, ") && strings.Contains(text, " IS NULL ORDER BY i.id") {
				runs = append(runs, text)
			}
		}
		return nil
	})
	if len(runs) < 3 {
		t.Fatalf("%d runs of titles with no value, want one for each sort", len(runs))
	}
	for _, run := range runs {
		rows, err := conn.QueryContext(ctx, "EXPLAIN QUERY PLAN "+run, sql.Named("viewer_profile", 1))
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		joined := strings.Join(plan, "; ")
		if !strings.Contains(joined, "INDEX items_by_id") || strings.Contains(joined, "TEMP B-TREE FOR ORDER BY") {
			t.Fatalf("the run is sorted, not walked in id order:\n%s\n%s", run, joined)
		}
	}
}
