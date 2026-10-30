package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
)

// EnsureInfoShapeTargetTx pins a source's exact NEW shape before pre-fence.
// Dictionary payload is charged to the existing source ledger. Ordinary link
// indexes remain proportionate base storage, not a 32MiB cache/reservation.
func EnsureInfoShapeTargetTx(ctx context.Context, tx *sql.Tx, change int64) (int64, error) {
	var target sql.NullInt64
	var phase, slot int
	var asset int64
	var token string
	err := tx.QueryRowContext(ctx, `SELECT target_shape_id,phase,slot,asset_id,asset_token FROM catalog_info_changes WHERE id=?`, change).
		Scan(&target, &phase, &slot, &asset, &token)
	if err != nil {
		return 0, err
	}
	if target.Valid {
		return target.Int64, nil
	}
	if phase != 0 {
		return 0, ErrInfoInvariant
	}
	// A queued OLD delta protects active NEW1 against raw NEW2. The queued
	// source's own target is raw NEW2 until that source is sealed.
	frame := `SELECT a.container,a.video_codec,a.height FROM catalog_assets a WHERE a.id=? AND a.token=?`
	args := []any{asset, token}
	if slot == 0 {
		frame = `SELECT
 COALESCE((SELECT o.old_text FROM catalog_info_changes n JOIN catalog_info_scalar_old o ON o.change_id=n.id AND o.field=4 WHERE n.asset_id=a.id AND n.asset_token=a.token AND n.slot=1),a.container) AS container,
 COALESCE((SELECT o.old_text FROM catalog_info_changes n JOIN catalog_info_scalar_old o ON o.change_id=n.id AND o.field=5 WHERE n.asset_id=a.id AND n.asset_token=a.token AND n.slot=1),a.video_codec) AS video_codec,
 COALESCE((SELECT o.old_integer FROM catalog_info_changes n JOIN catalog_info_scalar_old o ON o.change_id=n.id AND o.field=8 WHERE n.asset_id=a.id AND n.asset_token=a.token AND n.slot=1),a.height) AS height
 FROM catalog_assets a WHERE a.id=? AND a.token=?`
	}
	id, err := ensureInfoShapeFrame(ctx, tx, frame, args...)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_info_changes SET target_shape_id=? WHERE id=? AND phase=0`, id, change)
	return id, err
}

// ensureInfoShapeFrame keeps technical strings inside SQLite; even a legal
// wide shape does not become an unaccounted Go map/string capture. The trusted
// frame query yields exactly one asset's three accepted scalar values.
func ensureInfoShapeFrame(ctx context.Context, tx *sql.Tx, frame string, args ...any) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM catalog_info_shapes WHERE (container,video_codec,height)=(`+frame+`)`, args...).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	var room sql.NullBool
	err = tx.QueryRowContext(ctx, `SELECT 168+2*length(CAST(f.container AS BLOB))+2*length(CAST(f.video_codec AS BLOB))<=b.cap_bytes-b.used_bytes
 FROM (`+frame+`) f CROSS JOIN catalog_info_input_budget b WHERE b.singleton=1`, args...).Scan(&room)
	if err != nil {
		return 0, err
	}
	if !room.Valid || !room.Bool {
		return 0, ErrInfoInputDeferred
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO catalog_info_shapes(container,video_codec,height) `+frame+` ON CONFLICT(container,video_codec,height) DO NOTHING`, args...)
	if err != nil {
		return 0, infoInputClassifyWrite(err)
	}
	err = tx.QueryRowContext(ctx, `SELECT id FROM catalog_info_shapes WHERE (container,video_codec,height)=(`+frame+`)`, args...).Scan(&id)
	return id, err
}

// CollectInfoShapesTx retires <=8 maintained zero-reference candidates. The
// counter and partial index are maintained by point hooks; FKs and indexed
// absence checks still prove destruction. No all-dictionary empty-queue probe.
func CollectInfoShapesTx(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM catalog_info_shapes WHERE id IN(
 SELECT s.id FROM catalog_info_shapes s INDEXED BY catalog_info_shapes_unused
 WHERE s.ref_count=0 AND s.id>=-9223372036854775808 ORDER BY s.id LIMIT 8)`)
	return err
}
