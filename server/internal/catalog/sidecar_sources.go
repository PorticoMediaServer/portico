package catalog

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

type sidecarReadKey struct{}
type sidecarRead struct {
	asset    string
	carriers []sidecarCarrier
}

func withSidecarRead(ctx context.Context, asset string) (context.Context, *sidecarRead) {
	read := &sidecarRead{asset: asset}
	return context.WithValue(ctx, sidecarReadKey{}, read), read
}

// Complete artwork observation releases only automatic selections backed by
// the disappeared/replaced sidecar. Owner picks and other available copies keep
// their images, whose content-addressed bytes are never deleted here.
func settleSidecarRead(ctx context.Context, tx *sql.Tx, library string, read *sidecarRead) error {
	rows, err := tx.QueryContext(ctx, `SELECT kind,entity_id,role,art_key FROM inventory_artwork_sources WHERE asset_id=?`, read.asset)
	if err != nil {
		return err
	}
	old := []sidecarCarrier{}
	for rows.Next() {
		var c sidecarCarrier
		if err = rows.Scan(&c.kind, &c.entity, &c.role, &c.key); err != nil {
			rows.Close()
			return err
		}
		old = append(old, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM inventory_artwork_sources WHERE asset_id=?`, read.asset); err != nil {
		return err
	}
	for _, c := range read.carriers {
		if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_artwork_sources(asset_id,kind,entity_id,role,art_key) VALUES(?,?,?,?,?) ON CONFLICT(asset_id,kind,entity_id,role) DO UPDATE SET art_key=excluded.art_key`, read.asset, c.kind, c.entity, c.role, c.key); err != nil {
			return err
		}
	}
	for _, c := range old {
		kept := false
		for _, fresh := range read.carriers {
			if c == fresh {
				kept = true
				break
			}
		}
		if kept {
			continue
		}
		var elsewhere bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_artwork_sources s CROSS JOIN inventory_objects o INDEXED BY inventory_objects_asset ON o.asset_id=s.asset_id CROSS JOIN library_sources r ON r.id=o.source_id WHERE s.kind=? AND s.entity_id=? AND s.role=? AND s.art_key=? AND o.retired=0 AND o.state='available' AND o.root_incarnation=r.incarnation AND r.enabled=1)`, c.kind, c.entity, c.role, c.key).Scan(&elsewhere); err != nil {
			return err
		}
		if elsewhere {
			continue
		}
		if err = releaseAutomaticSidecar(ctx, tx, c); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM local_artwork WHERE library_id=? AND kind=? AND entity_id=? AND role=? AND art_key=?`, library, c.kind, c.entity, c.role, c.key)
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n > 0 && (c.kind == "item" || c.kind == "album") && (c.role == "poster" || c.role == "backdrop") {
			column := c.role + "_url"
			var current string
			if err = tx.QueryRowContext(ctx, `SELECT COALESCE(`+column+`,'') FROM catalog_item_details WHERE entity_id=?`, c.entity).Scan(&current); err != nil && err != sql.ErrNoRows {
				return err
			}
			if current == "local:"+c.key {
				if err = compactcatalog.SetFieldsTx(ctx, tx, c.entity, compactcatalog.Automatic, map[string]any{column: ""}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func releaseAutomaticSidecar(ctx context.Context, tx *sql.Tx, c sidecarCarrier) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM artwork_selections WHERE kind=? AND entity_id=? AND role=? AND locked=0 AND actor='' AND candidate_id IN(SELECT id FROM artwork_candidates WHERE provider='local' AND origin=?)`, c.kind, c.entity, c.role, "local:"+c.key); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE artwork_jobs SET status='stale',lease='',lease_until='' WHERE kind=? AND entity_id=? AND role=? AND provider='local' AND origin=? AND actor='' AND locked=0 AND status IN('pending','retry','running')`, c.kind, c.entity, c.role, "local:"+c.key); err != nil {
		return err
	}
	return markSidecarArtworkDirtyTx(ctx, tx, c.kind, c.entity)
}

func markSidecarArtworkDirtyTx(ctx context.Context, tx *sql.Tx, kind string, entity int64) error {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO artwork_dirty(kind,entity_id) VALUES(?,?)`, kind, entity)
	return err
}
