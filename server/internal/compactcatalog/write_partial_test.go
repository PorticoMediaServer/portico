package compactcatalog_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// A partial write to an existing side row whose table has a required column
// (an album's artist) updates the row. As an upsert it failed: SQLite checks
// NOT NULL on the proposed insert row before ON CONFLICT.
func TestPartialFactsUpdateAnExistingSideRow(t *testing.T) {
	c := catalogtest.Open(t)
	music := c.Library("music", "Music", "music", "/music")
	album := c.Album(c.Artist(music, "Artist"), "Album", 2001)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetFactsTx(ctx, tx, album.ID, map[string]any{"edition_key": "deluxe", "local_key": "album-2"})
	})
	var edition, key string
	var artist int64
	if err := c.DB.QueryRow(`SELECT edition_key,local_key,artist_id FROM catalog_albums WHERE entity_id=?`, album.ID).Scan(&edition, &key, &artist); err != nil || edition != "deluxe" || key != "album-2" || artist == 0 {
		t.Fatalf("album after a partial write: %q %q %d %v", edition, key, artist, err)
	}
	// A first write creates the row, and a write that changes nothing is not a change.
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetFactsTx(ctx, tx, album.ID, map[string]any{"edition_key": "deluxe"})
	})
}
