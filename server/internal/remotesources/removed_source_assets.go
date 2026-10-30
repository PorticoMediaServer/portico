package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// removedSourceAssetBatch bounds the path slice, not just the direct UPDATEs:
// SetAssetAvailableTx also writes source fences, dirty work and missing-title
// counters for linked titles. Leave room for that fanout and the walk cursor
// in the ordinary background-media plan. The root counts toward the slice.
const removedSourceAssetBatch = 4

// markRemovedSourceAssets marks the next slice of one removed source's files
// unavailable and reports whether more remain. RemoveDAV tombstoned the
// source and queued the walk in the same transaction, so its grants already
// fail; this only brings the catalogue's availability into line. A file
// under the root of a live source is left alone, and a source that is no
// longer removed ends its walk.
func (s *Service) markRemovedSourceAssets(ctx context.Context) (bool, error) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return false, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	var source, root, after string
	var removed bool
	err = tx.QueryRowContext(ctx, `SELECT r.source_id,r.root,r.after_path,COALESCE(s.removed,0) FROM remote_source_removals r LEFT JOIN remote_sources s ON s.id=r.source_id ORDER BY r.source_id LIMIT 1`).Scan(&source, &root, &after, &removed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !removed {
		if _, err = tx.ExecContext(ctx, `DELETE FROM remote_source_removals WHERE source_id=?`, source); err != nil {
			return false, err
		}
		return true, gated.Commit()
	}
	live, err := liveSourceRootsTx(ctx, tx)
	if err != nil {
		return false, err
	}
	type asset struct {
		id   int64
		path string
	}
	var assets []asset
	if after == "" {
		// The root itself sorts before everything under it.
		var id int64
		switch err = tx.QueryRowContext(ctx, `SELECT id FROM catalog_assets WHERE path=?`, root).Scan(&id); {
		case err == nil:
			assets = append(assets, asset{id, root})
		case !errors.Is(err, sql.ErrNoRows):
			return false, err
		}
	}
	// The paths under root/ sort between root/ and root followed by the byte
	// after the separator: one range of the path index, read from after_path.
	low, high := root+string(filepath.Separator), root+string(rune(filepath.Separator)+1)
	if after > low {
		low = after
	}
	limit := removedSourceAssetBatch - len(assets)
	rows, err := tx.QueryContext(ctx, `SELECT id,path FROM catalog_assets WHERE path>=? AND path<? AND path>? ORDER BY path LIMIT ?`, low, high, after, limit)
	if err != nil {
		return false, err
	}
	ranged := 0
	for rows.Next() {
		var a asset
		if err = rows.Scan(&a.id, &a.path); err != nil {
			rows.Close()
			return false, err
		}
		assets = append(assets, a)
		ranged++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, a := range assets {
		if underAny(a.path, live) {
			continue
		}
		if err = compactcatalog.SetAssetAvailableTx(ctx, tx, a.id, false); err != nil {
			return false, err
		}
	}
	more := ranged == limit
	if more {
		_, err = tx.ExecContext(ctx, `UPDATE remote_source_removals SET after_path=? WHERE source_id=?`, assets[len(assets)-1].path, source)
	} else {
		_, err = tx.ExecContext(ctx, `DELETE FROM remote_source_removals WHERE source_id=?`, source)
	}
	if err != nil {
		return false, err
	}
	if err = gated.Commit(); err != nil {
		return false, err
	}
	// Another queued source, if any, is looked at on the next turn.
	return true, nil
}

func liveSourceRootsTx(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT root FROM remote_sources WHERE removed=0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roots []string
	for rows.Next() {
		var root string
		if err = rows.Scan(&root); err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	return roots, rows.Err()
}

func underAny(path string, roots []string) bool {
	for _, root := range roots {
		if relative, err := filepath.Rel(root, path); err == nil && filepath.IsLocal(relative) {
			return true
		}
	}
	return false
}
