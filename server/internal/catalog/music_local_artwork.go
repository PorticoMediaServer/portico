package catalog

import (
	"context"
	"database/sql"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Album sidecars use the same automatic local: field source as embedded track
// covers. The normal artwork resolver gives an owner selection precedence.
func musicAlbumBackdropTx(ctx context.Context, tx *sql.Tx, album int64, extra map[string]string) error {
	key := extra["album/backdrop"]
	if key == "" {
		return nil
	}
	var old sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT backdrop_url FROM catalog_item_details WHERE entity_id=?`, album).Scan(&old); err != nil && err != sql.ErrNoRows {
		return err
	}
	next := "local:" + key
	if old.String == next {
		return nil
	}
	if previous, local := strings.CutPrefix(old.String, "local:"); local && previous != "" {
		// A new sidecar must retire the automatic selection and its in-flight
		// job before normal discovery can replace it. Owner choices stay locked.
		if err := releaseAutomaticSidecar(ctx, tx, sidecarCarrier{kind: "album", entity: album, role: "backdrop", key: previous}); err != nil {
			return err
		}
	}
	return compactcatalog.SetFieldsTx(ctx, tx, album, compactcatalog.Automatic, map[string]any{"backdrop_url": next})
}
