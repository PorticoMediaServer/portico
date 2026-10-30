package metadata

import (
	"context"
	"database/sql"
)

// readingArtworkSourceFence is descriptive source currentness, not admission or
// permission to read publication bytes. It uses Reading's existing monotonic
// attribution lineage and the actual primary asset/version. No facts or AV
// artwork/publication heads are fabricated during an editor read.
func readingArtworkSourceFence(ctx context.Context, tx *sql.Tx, entity int64) (string, error) {
	var namespace, generation, kind int64
	var asset, size, modified, sourceRevision sql.NullInt64
	var format, token, path sql.NullString
	var available, linked sql.NullBool
	err := tx.QueryRowContext(ctx, `SELECT state.namespace,state.input_generation,e.kind,
 p.asset_id,p.format,a.token,a.path,a.size,a.modified_ns,a.available,link.available,source.revision
 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id
 JOIN libraries lib ON lib.id=cl.library_id
 JOIN reading_attribution_state state ON state.library_id=cl.id
 LEFT JOIN reading_publications p ON p.item_id=e.id AND p.asset_id=(
  SELECT primary_version.asset_id FROM reading_publications primary_version
  WHERE primary_version.item_id=e.id
  AND ((e.kind=20 AND primary_version.format IN('epub','pdf','mobi','azw3'))
   OR (e.kind=21 AND primary_version.format IN('cbz','pdf','cb7','cbr')))
  ORDER BY EXISTS(SELECT 1 FROM inventory_item_availability eligible
   WHERE eligible.item_id=primary_version.item_id
   AND eligible.asset_id=(SELECT token FROM catalog_assets WHERE id=primary_version.asset_id)
   AND eligible.available=1) DESC,primary_version.asset_id LIMIT 1)
 LEFT JOIN catalog_asset_links link ON link.entity_id=p.item_id AND link.asset_id=p.asset_id
 LEFT JOIN catalog_assets a ON a.id=p.asset_id
 LEFT JOIN prepared_media_source_revisions source ON source.asset_id=a.token
 WHERE e.id=? AND e.retired=0 AND cl.retired=0
 AND ((e.kind=20 AND lib.kind='book') OR (e.kind=21 AND lib.kind='comic'))`, entity).Scan(
		&namespace, &generation, &kind, &asset, &format, &token, &path, &size, &modified, &available, &linked, &sourceRevision)
	if err != nil {
		return "", err
	}
	if asset.Valid && !sourceRevision.Valid {
		return "", sql.ErrNoRows
	}
	return publicationDigest([]any{"reading-artwork-source-v1", namespace, generation, kind,
		asset, format, token, path, size, modified, available, linked, sourceRevision}), nil
}
