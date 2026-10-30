package compactcatalog_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

func TestReadingPublicationIsANonPlayableCatalogueLeaf(t *testing.T) {
	for _, tc := range []struct {
		library string
		kind    compactcatalog.Kind
	}{{"book", compactcatalog.Ebook}, {"comic", compactcatalog.Comic}} {
		t.Run(tc.library, func(t *testing.T) {
			c := catalogtest.Open(t)
			root := t.TempDir()
			library := c.Library("reading", "Reading", tc.library, root)
			path := filepath.Join(root, "Original.pdf")
			if err := os.WriteFile(path, readingtest.PDF(), 0600); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			item := c.Entity(compactcatalog.Entity{Library: library, Kind: tc.kind, Key: "reading:original", Title: "Original", Added: "2026-10-23T00:00:00Z"}, nil)
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "pdf"})
				if err != nil {
					return err
				}
				if err = compactcatalog.LinkAssetTx(ctx, tx, item.ID, asset, compactcatalog.Link{}); err != nil {
					return err
				}
				_, err = tx.ExecContext(ctx, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,'pdf')`, item.ID, asset)
				return err
			})
			c.Drain()
			check := func(wantAvailable bool) {
				t.Helper()
				var playable, listening, container, available bool
				var leaf int64
				if err := c.DB.QueryRow(`SELECT k.playable,k.listening,s.container,b.item_id,b.available FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind JOIN catalog_search_documents s ON s.entity_id=e.id JOIN catalog_browse_rows b ON b.entity_id=e.id WHERE e.id=?`, item.ID).Scan(&playable, &listening, &container, &leaf, &available); err != nil || playable || listening || container || leaf != item.ID || available != wantAvailable {
					t.Fatal(playable, listening, container, leaf, available, err)
				}
			}
			check(true)
			// Real source feed triggers must fan out to nonplayable Reading too.
			c.Exec(`UPDATE library_sources SET enabled=0 WHERE library_id='reading'`)
			c.Drain()
			check(false)
			c.Exec(`UPDATE library_sources SET enabled=1 WHERE library_id='reading'`)
			c.Drain()
			check(true)
		})
	}
}
