package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func queueMissingSource(ctx context.Context, tx *sql.Tx, item int64) error {
	var lifetime string
	err := tx.QueryRowContext(ctx, `SELECT lower(hex(public_id)) FROM catalog_entities WHERE id=?`, item).Scan(&lifetime)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return queueSource(ctx, tx, DomainMissingSources, item, lifetime, 0)
}
func (w *upstreamSource) publishMissing(ctx context.Context, tx *sql.Tx, present bool) error {
	if present {
		_, err := tx.ExecContext(ctx, `DELETE FROM catalog_missing_titles WHERE entity_id=?`, w.id)
		return err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO catalog_missing_titles(entity_id,library_id,missing_since_ms)
 SELECT e.id,e.library_id,? FROM catalog_entities e CROSS JOIN catalog_kinds k ON k.id=e.kind WHERE e.id=? AND k.playable=1
 ON CONFLICT(entity_id) DO NOTHING`, time.Now().UnixMilli(), w.id)
	return err
}

// missing preserves presentFileSQL's exact predicate. It never scans all links
// or all inventory objects inside an EXISTS: both levels have raw PK cursors,
// and rejected inventory objects advance. A positive witness may finish early.
func (w *upstreamSource) missing(ctx context.Context, tx *sql.Tx, limit int) (int, bool, error) {
	var pending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_dirty WHERE domain=41) OR EXISTS(SELECT 1 FROM catalog_derivation_failures WHERE domain=41)`).Scan(&pending); err != nil {
		return 0, false, err
	}
	if pending {
		return 0, false, nil
	}
	var epoch int64
	if err := tx.QueryRowContext(ctx, `SELECT source_epoch FROM catalog_facet_source_state WHERE singleton=1`).Scan(&epoch); err != nil {
		return 0, false, err
	}
	if w.epoch != epoch {
		w.epoch = epoch
		w.has = false
		w.after = 0
		w.clearInner()
	}
	var lifetime string
	err := tx.QueryRowContext(ctx, `SELECT lower(hex(public_id)) FROM catalog_entities WHERE id=?`, w.id).Scan(&lifetime)
	if errors.Is(err, sql.ErrNoRows) || err == nil && lifetime != w.token {
		return 0, true, nil
	}
	if err != nil {
		return 0, false, err
	}
	if w.active.Valid {
		q := `SELECT id,retired=0 AND state='available' FROM inventory_objects INDEXED BY inventory_objects_reading_asset_object WHERE asset_id=?`
		args := []any{w.activeKey}
		if w.innerHas {
			q += ` AND id>?`
			args = append(args, w.innerKey)
		}
		q += ` ORDER BY id LIMIT ?`
		args = append(args, limit)
		rows, e := querySourceEdges(ctx, tx, 4, q, args...)
		if e != nil {
			return 0, false, e
		}
		for _, r := range rows {
			w.innerKey, w.innerHas, w.seen = r.key, true, true
			if r.flag {
				e = w.publishMissing(ctx, tx, true)
				return len(rows), true, e
			}
		}
		if len(rows) > 0 {
			return len(rows), false, nil
		}
		if !w.seen && w.available {
			e = w.publishMissing(ctx, tx, true)
			return 1, true, e
		}
		w.clearInner()
		return 1, false, nil
	}
	q := `SELECT l.asset_id,a.token,a.available FROM catalog_asset_links l CROSS JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=?`
	args := []any{w.id}
	if w.has {
		q += ` AND l.asset_id>?`
		args = append(args, w.after)
	}
	q += ` ORDER BY l.asset_id LIMIT 1`
	var asset int64
	var token string
	var available bool
	err = tx.QueryRowContext(ctx, q, args...).Scan(&asset, &token, &available)
	if errors.Is(err, sql.ErrNoRows) {
		err = w.publishMissing(ctx, tx, false)
		return 0, true, err
	}
	if err != nil {
		return 0, false, err
	}
	w.after, w.has = asset, true
	w.active = sql.NullInt64{Int64: asset, Valid: true}
	w.activeKey, w.available = token, available
	return 1, false, nil
}
