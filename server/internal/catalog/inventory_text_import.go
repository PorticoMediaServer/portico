package catalog

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// CompleteInventoryTextImports closes the durable gap between publishing stream
// inventory and importing its text tracks. A crash before this commit leaves
// analysis_revision empty, so the next analysis run retries idempotent imports.
func (s *Service) CompleteInventoryTextImports(ctx context.Context, job, object, revision string, policyRevision int64) error {
	gate, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return e
	}
	defer gate.Rollback()
	tx := gate.Tx()
	source, e := InventoryFenceTx(ctx, tx, job)
	if e != nil {
		return e
	}
	policy, e := ScanPolicyTx(ctx, tx, source.LibraryID)
	if e != nil {
		return e
	}
	if policy.Revision != policyRevision || !policy.Allows("subtitles") {
		return ErrAdminQuery
	}
	state, queue := "complete", "done"
	for _, op := range DeepScanOperations {
		if policy.Allows(op) {
			state, queue = "pending", "pending"
		}
	}
	result, e := tx.ExecContext(ctx, `UPDATE inventory_objects SET analysis_revision=?,analysis_state=?,analysis_error='' WHERE id=? AND source_id=? AND revision=? AND analysis_policy=? AND retired=0 AND state='available'`, revision, state, object, source.ID, revision, policyRevision)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrAdminQuery
	}
	if _, e = tx.ExecContext(ctx, `UPDATE inventory_analysis_queue SET state=? WHERE job_id=? AND object_id=? AND revision=? AND policy_revision=?`, queue, job, object, revision, policyRevision); e != nil {
		return e
	}
	return gate.Commit()
}
