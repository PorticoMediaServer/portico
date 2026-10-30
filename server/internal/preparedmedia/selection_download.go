package preparedmedia

import (
	"context"
	"database/sql"
	"errors"
)

// DownloadSinglePartTx preserves count(DISTINCT part_index)==1 using two
// endpoint seeks in catalog_asset_links_order. Work does not grow with a
// title's version count; an empty title has no single downloadable part.
func DownloadSinglePartTx(ctx context.Context, tx *sql.Tx, item string) (bool, error) {
	var first, last sql.NullInt64
	e := tx.QueryRowContext(ctx, `SELECT
 (SELECT part_index FROM catalog_asset_links WHERE entity_id=e.id ORDER BY part_index,asset_id LIMIT 1),
 (SELECT part_index FROM catalog_asset_links WHERE entity_id=e.id ORDER BY part_index DESC,asset_id DESC LIMIT 1)
 FROM catalog_entities e WHERE e.public_id=pid_blob(?)`, item).Scan(&first, &last)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	return first.Valid && last.Valid && first.Int64 == last.Int64, e
}

// DownloadVersionTx selects the same first selectable, valid-digest published
// version as VersionsTx followed by the download source/profile filter. Its
// index restricts the scan to this exact source revision, not every version of
// a title. The caller has already authorized the item and device.
func DownloadVersionTx(ctx context.Context, tx *sql.Tx, item, asset, profile string) (Version, bool, error) {
	if profile == "" {
		return Version{}, false, nil
	}
	sel, e := selectSource(ctx, tx, item, asset, false)
	if errors.Is(e, sql.ErrNoRows) || errors.Is(e, ErrUnsupported) || errors.Is(e, ErrUnavailable) {
		return Version{}, false, nil
	}
	if e != nil {
		return Version{}, false, e
	}
	v, e := readVersion(tx.QueryRowContext(ctx, `SELECT `+versionColumns+` FROM `+versionSource+`
 WHERE v.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND v.asset_id=? AND v.profile_id=? AND v.source_revision=?
 AND v.state='published' AND length(v.digest)=64 AND v.digest NOT GLOB '*[^0-9a-f]*'
 ORDER BY v.created_ms,v.id LIMIT 1`, item, asset, profile, hash(sel)))
	if errors.Is(e, sql.ErrNoRows) {
		return Version{}, false, nil
	}
	if e != nil {
		return Version{}, false, e
	}
	v.Selectable = true
	return v, true, nil
}
