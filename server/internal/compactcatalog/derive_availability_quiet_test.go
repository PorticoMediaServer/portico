package compactcatalog_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Deriving an item again rewrites nothing about its files when their
// availability is unchanged. The link triggers ported from item_assets (which
// had no availability) must not read a re-derivation as a file change: that
// re-queued the item's metadata work and bumped its library's revision on
// every fact change.
func TestUnchangedAvailabilityIsNotALinkChange(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("m", "Movies", "movie", "/m")
	movie := c.Movie(films, "/m/One.mkv", "One", 2020)
	c.Drain()
	var generation, links int64
	read := func() (int64, int64) {
		t.Helper()
		var g, r int64
		if err := c.DB.QueryRow(`SELECT generation FROM screen_metadata_work WHERE target_kind='item' AND target_id=?`, movie.ID).Scan(&g); err != nil {
			t.Fatal(err)
		}
		if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_asset_links WHERE entity_id=? AND available=1`, movie.ID).Scan(&r); err != nil {
			t.Fatal(err)
		}
		return g, r
	}
	generation, links = read()
	if links != 1 {
		t.Fatalf("the movie has %d available files", links)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetFactsTx(ctx, tx, movie.ID, map[string]any{"overview": "A new synopsis."})
	})
	c.Drain()
	if g, r := read(); g != generation || r != links {
		t.Fatalf("an overview change re-queued metadata work or changed files: generation %d -> %d, available files %d -> %d", generation, g, links, r)
	}
}
