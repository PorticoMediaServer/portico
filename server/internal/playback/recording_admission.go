package playback

import (
	"context"
	"database/sql"
)

// Independent of source selection: retain this guard when composing prepared
// variants. Physical retirement remains the artifact reader's separate duty.
func recordingAdmittedTx(ctx context.Context, tx *sql.Tx, item string) error {
	if tx == nil {
		return ErrIncompatible
	}
	var retired bool
	if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dvr_catalog_retirements WHERE item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)))`, item).Scan(&retired); e != nil {
		return e
	}
	if retired {
		return ErrIncompatible
	}
	return nil
}
