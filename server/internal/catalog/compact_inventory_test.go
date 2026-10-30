package catalog

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

func TestInventoryReadsCompactAssociation(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	film := c.Movie(films, "/m/a.mp4", "Alpha", 2001)
	c.Drain()
	var source string
	if err := c.DB.QueryRow(`SELECT id FROM library_sources WHERE library_id='m'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
	 SELECT 'object',id,?,incarnation,'a.mp4','r','{}',1,1 FROM library_sources WHERE id=?`, film.Token, source)

	s := New(c.DB)
	request := AdminRequest{LibraryID: "m", Limit: 20}
	page, err := s.Inventory(context.Background(), request, source, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].ItemID != film.Public || page.Items[0].Title != "Alpha" {
		t.Fatalf("inventory item: %+v %v", page, err)
	}

	c.Fields(film.ID, map[string]any{"title": "Changed"})
	c.Drain()
	page, err = s.Inventory(context.Background(), request, source, "")
	if err != nil || len(page.Items) != 1 || page.Items[0].ItemID != film.Public || page.Items[0].Title != "Changed" {
		t.Fatalf("inventory after title update: %+v %v", page, err)
	}
}
