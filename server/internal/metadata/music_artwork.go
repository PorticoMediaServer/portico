package metadata

import (
	"context"
	"database/sql"
	"errors"
)

// Album backgrounds are scan-owned cache origins, including the artist-file
// fallback. Reuse the ordinary fenced candidate/selection worker; never fetch
// an inferred URL or replace an occupied owner selection here.
func seedMusicAlbumBackdrop(ctx context.Context, tx *sql.Tx, target RepairTarget, entity int64, fence, now string) error {
	if target.Kind != "album" {
		return nil
	}
	var origin string
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(backdrop_url,'') FROM catalog_item_details WHERE entity_id=?`, entity).Scan(&origin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if artworkProvider(origin) != "local" {
		return nil
	}
	return seedArtworkCandidate(ctx, tx, target, "backdrop", "", "local", publicationDigest(origin), origin, artworkAttribution("local"), fence, now, 0)
}
