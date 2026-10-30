package catalog

import (
	"context"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"testing"
)

func TestMissingTitleCountsAndCursorFollowChanges(t *testing.T) {
	c := catalogtest.Open(t)
	a := c.Library("a", "Movies", "movie", "/a")
	b := c.Library("b", "Other", "movie", "/b")
	var first int64
	for i := 0; i < 12; i++ {
		item := c.Movie(a, fmt.Sprintf("/a/%d.mkv", i), "Movie", 2000)
		if i == 0 {
			first = item.ID
		}
		c.Exec(`INSERT INTO catalog_missing_titles(entity_id,library_id,missing_since_ms) VALUES(?,?,?)`, item.ID, a, 1000+i/3)
	}
	s := New(c.DB)
	ctx := context.Background()
	check := func(library string, want int) {
		t.Helper()
		p, err := s.MissingTitles(ctx, library, "", 2)
		if err != nil || p.Total != int64(want) {
			t.Fatalf("%s: %+v %v", library, p, err)
		}
	}
	check("a", 12)
	check("", 12)
	seen := map[string]bool{}
	cursor := ""
	for {
		p, err := s.MissingTitles(ctx, "a", cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range p.Items {
			if seen[item.ID] {
				t.Fatal("duplicate cursor row")
			}
			seen[item.ID] = true
		}
		cursor = p.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 12 {
		t.Fatalf("got %d rows", len(seen))
	}
	c.Exec(`UPDATE catalog_missing_titles SET library_id=? WHERE entity_id=?`, b, first)
	check("a", 11)
	check("b", 1)
	check("", 12)
	c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE id=?`, b)
	check("", 11)
	c.Exec(`DELETE FROM catalog_missing_titles WHERE entity_id=?`, first)
	c.Exec(`DELETE FROM catalog_missing_titles WHERE library_id=?`, a)
	check("", 0)
	check("a", 0)
}
