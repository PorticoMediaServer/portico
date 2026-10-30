package ingestion

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strconv"
)

func (s *Service) attachExtrasPage(ctx context.Context, job string, source catalog.LibrarySource) error {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if _, err = catalog.InventoryFenceTx(ctx, tx, job); err != nil {
		return err
	}
	var cursor string
	if err = tx.QueryRowContext(ctx, `SELECT verify_cursor FROM inventory_runs WHERE job_id=?`, job).Scan(&cursor); err != nil {
		return err
	}
	after, _ := strconv.ParseInt(cursor, 10, 64)
	next, err := catalog.RefreshExtraOwnersTx(ctx, tx, source, after)
	if err != nil {
		return err
	}
	if next == 0 {
		_, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET phase='verifying',verify_cursor='' WHERE job_id=?`, job)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET verify_cursor=? WHERE job_id=?`, strconv.FormatInt(next, 10), job)
	}
	if err != nil {
		return err
	}
	return gated.Commit()
}
