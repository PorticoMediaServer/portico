package dvr

import (
	"context"
	"database/sql"
)

// AdmitRootChange runs inside the caller's exclusive database write transaction.
// Hold its release through commit/rollback: no worker may register while root
// admission is being persisted. Any retained recording (including incomplete or
// cancelled rows), layout/artifact pin or prepared work refuses relocation.
func (s *Store) AdmitRootChange(ctx context.Context, tx *sql.Tx) (func(), error) {
	s.mu.Lock()
	if len(s.active) != 0 { s.mu.Unlock(); return nil, ErrConflict }
	if err := RetainedRootReferences(ctx, tx); err != nil { s.mu.Unlock(); return nil, err }
	return s.mu.Unlock, nil
}

// RetainedRootReferences is also used before any recording worker exists at
// startup, under the exclusive database write transaction.
func RetainedRootReferences(ctx context.Context, tx *sql.Tx) error {
	var retained bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dvr_recordings) OR EXISTS(SELECT 1 FROM dvr_recording_layouts) OR EXISTS(SELECT 1 FROM dvr_recording_shares) OR EXISTS(SELECT 1 FROM dvr_catalog_provenance) OR EXISTS(SELECT 1 FROM live_allocations WHERE kind='recording' AND state IN('active','preempting')) OR EXISTS(SELECT 1 FROM dvr_rule_work WHERE phase<>'idle') OR EXISTS(SELECT 1 FROM dvr_delete_work) OR EXISTS(SELECT 1 FROM dvr_cleanup_work) OR EXISTS(SELECT 1 FROM dvr_retention_retry)`).Scan(&retained)
	if err != nil { return err }
	if retained { return ErrConflict }
	return nil
}
