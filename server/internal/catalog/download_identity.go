package catalog

import (
	"context"
	"database/sql"
)

// A download belongs to the title its selected file belongs to. Claim ids,
// transfer grants and offline receipts survive a merge or a split unchanged.
func moveDownloadClaimsTx(ctx context.Context, tx *sql.Tx, from, into int64, asset string) error {
	for _, query := range []string{
		`UPDATE prepared_media_jobs SET item_id=?2,revision=revision+1 WHERE item_id=?1 AND (?3='' OR asset_id=?3)`,
		`UPDATE prepared_media_versions SET item_id=?2,revision=revision+1 WHERE item_id=?1 AND (?3='' OR asset_id=?3)`,
		`UPDATE OR IGNORE download_request_items SET item_id=?2 WHERE item_id=?1 AND (?3='' OR preparation_id IN(SELECT id FROM download_preparations WHERE item_id=?1 AND source_asset_id=?3))`,
		`DELETE FROM download_request_items WHERE item_id=?1 AND ?3='' AND EXISTS(SELECT 1 FROM download_request_items other WHERE other.request_id=download_request_items.request_id AND other.item_id=?2)`,
		`UPDATE download_requests SET target_id=?2 WHERE target_id=?1 AND target_kind<>'playlist' AND ?3=''`,
		`UPDATE download_grants SET item_id=?2 WHERE item_id=?1 AND preparation_id IN(SELECT id FROM download_preparations WHERE item_id=?1 AND (?3='' OR source_asset_id=?3))`,
		`UPDATE download_receipts SET item_id=?2,item_public=(SELECT pid(public_id) FROM catalog_entities WHERE id=?2),revision=revision+1 WHERE item_id=?1 AND (?3='' OR preparation_id IN(SELECT id FROM download_preparations WHERE item_id=?1 AND source_asset_id=?3))`,
		`UPDATE download_preparations SET item_id=?2,item_public=(SELECT pid(public_id) FROM catalog_entities WHERE id=?2),revision=revision+1 WHERE item_id=?1 AND (?3='' OR source_asset_id=?3)`,
	} {
		if _, err := tx.ExecContext(ctx, query, from, into, asset); err != nil {
			return err
		}
	}
	return nil
}
