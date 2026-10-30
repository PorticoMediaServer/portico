package persistence

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"

	"github.com/PorticoMediaServer/portico/server/internal/facettext"
)

// FoldFacetSQL folds a genre or person name for facet matching: case,
// separators and doubled spaces, with the sci-fi aliases. The catalogue's
// recommendation queries use the identical text.
func FoldFacetSQL(v string) string { return facettext.FoldFacetSQL(v) }

// identityProofBatch bounds one reclaim transaction. A factor challenge has at
// most one cascaded child, so a batch changes at most 128 rows.
const identityProofBatch = 64

// PruneIdentityProofs deletes expired factor challenges and Top Shelf tokens.
func PruneIdentityProofs(ctx context.Context, db *sql.DB, now string) error {
	for _, table := range []string{"identity_factor_challenges", "topshelf_tokens"} {
		// Retention is maintenance-class work. Each table is reclaimed in bounded
		// autocommit batches, in this order, so a failure in the second leaves
		// the first reclaimed exactly as before, and a large backlog never holds
		// the writer for longer than one batch.
		statement := `DELETE FROM ` + table + ` WHERE rowid IN(SELECT rowid FROM ` + table + ` WHERE expires_at<=? LIMIT ` + strconv.Itoa(identityProofBatch) + `)`
		for {
			result, err := dbwork.ExecWrite(ctx, db, dbwork.ClassMaintenance, statement, now)
			if err != nil {
				return err
			}
			if n, _ := result.RowsAffected(); n < identityProofBatch {
				break
			}
			if !dbwork.Yield(ctx) {
				return ctx.Err()
			}
		}
	}
	return nil
}

// ReclaimSpace moves at most 64 free pages per maintenance tick. It yields to
// foreground work and never converts a pre-release database with a full VACUUM.
func ReclaimSpace(ctx context.Context, db *sql.DB) error {
	if dbwork.WriteGate().ActiveOrWaiting() || dbwork.ForegroundWorkActive() {
		return nil
	}
	var free int
	if err := db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&free); err != nil || free == 0 {
		return err
	}
	write, err := dbwork.Begin(ctx, db, dbwork.ClassMaintenance)
	if err != nil {
		return err
	}
	defer write.Rollback()
	rows, err := write.Tx().QueryContext(ctx, "PRAGMA incremental_vacuum(64)")
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var remaining, pageSize int64
	if err = write.Tx().QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&remaining); err != nil {
		return err
	}
	if err = write.Tx().QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return err
	}
	if _, err = write.Tx().ExecContext(ctx, `UPDATE database_maintenance_status SET last_ran_ms=?,last_reclaimed_bytes=? WHERE singleton=1`, time.Now().UnixMilli(), max(0, int64(free)-remaining)*pageSize); err != nil {
		return err
	}
	return write.Commit()
}
