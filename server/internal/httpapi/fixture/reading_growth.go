package fixture

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// SeedReadingGrowth extends scalebench's synthetic catalogue with the same
// publication and attribution facts used by reading browse fixtures. Files
// are not read or decoded here; reader probes provide their own lawful source.
func SeedReadingGrowth(ctx context.Context, db *sql.DB, library int64, kind compactcatalog.Kind, root, format string, count int) error {
	for off := 0; off < count; off += 100 {
		if e := dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			for i := off; i < min(count, off+100); i++ {
				key := fmt.Sprintf("growth-%09d", i)
				path := filepath.Join(root, key+"."+format)
				item, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: kind, Key: path, Title: fmt.Sprintf("Title %09d", i), Added: "2026-01-01T00:00:00.000Z"})
				if e != nil {
					return e
				}
				asset, _, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000, ModifiedNS: 1, Container: format})
				if e != nil {
					return e
				}
				if e = compactcatalog.LinkAssetTx(ctx, tx, item, asset, compactcatalog.Link{}); e != nil {
					return e
				}
				if _, e = tx.ExecContext(ctx, `INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,?)`, item, asset, format); e != nil {
					return e
				}
				for field, value := range map[string]string{"author": fmt.Sprintf("Author %06d", i/20), "series": fmt.Sprintf("Series %06d", i/5)} {
					if _, e = tx.ExecContext(ctx, `INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,?,?,'',1,'fixture','now')`, item, field, value); e != nil {
						return e
					}
				}
			}
			return nil
		}); e != nil {
			return e
		}
	}
	return nil
}
