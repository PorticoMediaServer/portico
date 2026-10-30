package ingestion

import (
	"context"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"time"
)

type directoryReadError struct{ error }

func (e *directoryReadError) Unwrap() error { return e.error }

// A failed path protects itself and its descendants, not the entire source.
// The separator is a bound argument, so wildcard characters in names are literal.
const unprotectedObjectSQL = `NOT EXISTS(SELECT 1 FROM inventory_scan_issues x WHERE x.job_id=r.job_id AND x.protect=1 AND (x.relative_path='.' OR o.relative_path=x.relative_path OR substr(o.relative_path,1,length(x.relative_path)+1)=x.relative_path||?))`

func (s *Service) excludeDirectory(ctx context.Context, job, relative, reason string) error {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	source, err := catalog.InventoryFenceTx(ctx, tx, job)
	if err != nil {
		return err
	}
	if err = catalog.InventoryIssueTx(ctx, tx, job, storage.InventoryIssue{Path: filepath.Clean(relative), Reason: reason, Protect: true, Warning: true}); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_directories SET state='blocked',cursor='' WHERE job_id=? AND relative_path=?`, job, relative); err != nil {
		return err
	}
	if reason == "directory_changed" {
		if err = recordRunNoticeTx(ctx, tx, job, source.ID, relative, time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	return gated.Commit()
}

// Inventory source limits are independent of scan speed: pacing between
// completed pages is controlled by dbwork.PaceBackground. Two raw names
// include filtered names/issues, not just returned entries; two folders bound
// progress/cache/ignore-diagnostic overhead even when every folder is empty.
// Normal inventory turns admit one name/folder before listing: two Reading
// entries plus their publication triggers exceed the ordinary 160-row target.
// Explicit listing batches retain the two-name transport ceiling.
// Focused on-disk measurements live in TestInventorySourcePageCost. These
// conservative producer caps do not qualify arbitrary existing-object fanout
// for the default WritePlan; the atomic inventory-pages exception remains.
const (
	inventoryPublicationNames = 1
	inventorySourceNames      = 2
	inventorySourceFolders    = 2
)

func (s *Service) inventoryBatch(context.Context) int {
	return inventorySourceNames
}

func waitScanPace(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
