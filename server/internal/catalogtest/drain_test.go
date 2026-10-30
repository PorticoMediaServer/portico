package catalogtest

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestDrainReusesThePreparedWorkerWhileAQueryCapturePinsBackground(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := Open(t)
	library := c.Library("movies", "Movies", "movie", "/movies")
	item := c.Movie(library, "/movies/a.mkv", "Before", 2000)
	c.Drain()
	ctx := dbwork.WithClass(context.Background(), dbwork.ClassBackgroundMedia)
	bg := dbwork.ReadHandle(ctx, c.DB)
	pinned, err := bg.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	c.Fields(item.ID, map[string]any{"title": "After"})
	c.Drain()
	var title string
	if err := c.DB.QueryRow(`SELECT title FROM catalog_browse_rows WHERE entity_id=?`, item.ID).Scan(&title); err != nil || title != "After" {
		t.Fatalf("writer drain did not publish: %q %v", title, err)
	}
	// Fixture reuse must not bypass the real one-connection background policy.
	wait, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	if second, err := bg.Conn(wait); err == nil {
		second.Close()
		t.Fatal("drain increased background read capacity")
	}
}
