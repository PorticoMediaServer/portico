package ingestion

import (
	"context"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"strings"
)

// Previously catalogued files absent on the first upgraded scan must enter the
// same grace/absence lifecycle. Preserve their logical/asset IDs and never read
// their contents or call the old path-only missing sweep.
func (s *Service) adoptLegacyPage(ctx context.Context, job string, source catalog.LibrarySource) error {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if _, err = catalog.InventoryFenceTx(ctx, tx, job); err != nil {
		return err
	}
	prefix := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSuffix(source.ResolvedPath, string(filepath.Separator))+string(filepath.Separator)) + "%"
	rows, err := tx.QueryContext(ctx, `SELECT a.token,a.path,a.size,a.modified_ns FROM catalog_assets a JOIN inventory_runs r ON r.job_id=? WHERE a.token>r.reconcile_cursor AND a.path LIKE ? ESCAPE '\' AND EXISTS(SELECT 1 FROM catalog_asset_links l JOIN catalog_entities e ON e.id=l.entity_id JOIN catalog_libraries cl ON cl.id=e.library_id WHERE l.asset_id=a.id AND cl.library_id=?) AND NOT EXISTS(SELECT 1 FROM inventory_objects o JOIN library_sources x ON x.id=o.source_id WHERE o.asset_id=a.token AND x.library_id=?) ORDER BY a.token LIMIT 32`, job, prefix, source.LibraryID, source.LibraryID)
	if err != nil {
		return err
	}
	type legacy struct {
		id string
		v  storage.Snapshot
	}
	batch := []legacy{}
	for rows.Next() {
		var v legacy
		if err = rows.Scan(&v.id, &v.v.Path, &v.v.Size, &v.v.ModifiedNS); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range batch {
		relative, e := filepath.Rel(source.ResolvedPath, v.v.Path)
		if e != nil || !filepath.IsLocal(relative) || relative == "." {
			continue
		}
		// A pathname replaced by a newly observed object has its own new identity;
		// the collision retirement path already retained the old logical asset.
		var occupied bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_objects WHERE source_id=? AND relative_path=? AND retired=0)`, source.ID, relative).Scan(&occupied); err != nil {
			return err
		}
		if !occupied {
			if err = s.catalog.AdoptLegacyInventoryTx(ctx, tx, source, v.id, relative, v.v); err != nil {
				return err
			}
		}
	}
	if len(batch) > 0 {
		_, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET reconcile_cursor=? WHERE job_id=?`, batch[len(batch)-1].id, job)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET phase='extras',reconcile_cursor='',verify_cursor='' WHERE job_id=?`, job)
	}
	if err != nil {
		return err
	}
	return gated.Commit()
}
