package catalog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

func TestCompactSearchUsesUpdatedTitle(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("films", "Films", "movie", "/films")
	film := c.Movie(films, "/films/harbor.mkv", "Harbor Light", 2020)
	c.Drain()
	s := New(c.DB)

	search := func(term string) SearchGroupResult {
		t.Helper()
		libraries := []string{"films"}
		out, err := s.Search(context.Background(), SearchRequest{
			Viewer:   Viewer{Profile: "viewer", Fence: "fence", Libraries: libraries},
			ServerID: "server", Profile: "viewer", ViewerFence: "fence", Q: term, Group: "movies", Limit: 10, Libraries: libraries,
		})
		if err != nil || len(out.Groups) != 1 || out.Groups[0].Status != "success" {
			t.Fatalf("search %q: %+v %v", term, out, err)
		}
		return out.Groups[0]
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetFieldsTx(ctx, tx, film.ID, compactcatalog.Automatic, map[string]any{"title": "Amber Light"})
	})
	c.Drain()

	newTitle := search("Amber")
	if len(newTitle.Items) != 1 || newTitle.Items[0].ID != film.Public || newTitle.Items[0].Title != "Amber Light" {
		t.Fatalf("new title search: %+v", newTitle)
	}
	oldTitle := search("Harbor")
	if oldTitle.TotalCount != 0 || len(oldTitle.Items) != 0 {
		t.Fatalf("old title remained searchable: %+v", oldTitle)
	}
}
