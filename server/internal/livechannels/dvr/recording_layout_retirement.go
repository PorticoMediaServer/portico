package dvr

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
)

// retireRecordingLayout is called after the existing delete/cleanup grace,
// allocation, active-worker and shared-capture fences, before original-source
// removal. A prepared link-before-receipt crash has the same exact inode proof.
func (s *Store) retireRecordingLayout(ctx context.Context, id string, owner livechannels.Owner, key string, object mediaartifact.Object) error {
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	receipt, exists, err := readRecordingLayoutReceiptTx(ctx, snapshot.Tx(), id)
	if err == nil && exists {
		err = recordingLayoutRetirementTx(ctx, snapshot.Tx(), receipt, owner, key, object)
	}
	snapshot.Rollback()
	if err != nil || !exists {
		return err
	}
	if receipt.Phase == "retired" {
		return nil
	}
	driver, ok := s.driver.(RecordingLayoutDriver)
	if !ok {
		return ErrUnavailable
	}
	if err = driver.RemoveRecordingLayout(ctx, receipt); err != nil {
		return err
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassProtectedCapture)
	if err != nil {
		return err
	}
	defer gated.Rollback()
	current, found, err := readRecordingLayoutReceiptTx(ctx, gated.Tx(), id)
	if err != nil {
		return err
	}
	if !found || current != receipt {
		return ErrConflict
	}
	if err = recordingLayoutRetirementTx(ctx, gated.Tx(), current, owner, key, object); err != nil {
		return err
	}
	result, err := gated.Tx().ExecContext(ctx, `UPDATE dvr_recording_layouts SET phase='retired' WHERE recording_id=? AND phase IN('prepared','linked')`, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return gated.Commit()
}
func recordingLayoutRetirementTx(ctx context.Context, tx *sql.Tx, r RecordingLayoutReceipt, owner livechannels.Owner, key string, object mediaartifact.Object) error {
	if !owner.Valid() || r.OwnerKey != owner.Key() || r.ArtifactKey != key || r.Object != object || !r.Valid() {
		return ErrInvalid
	}
	var state, currentOwner, currentKey, digest string
	var size int64
	err := tx.QueryRowContext(ctx, `SELECT state,owner_key,artifact_id,artifact_digest,bytes FROM dvr_recordings WHERE id=?`, r.RecordingID).Scan(&state, &currentOwner, &currentKey, &digest, &size)
	if err != nil {
		return err
	}
	if state != "pending-delete" && state != "failed" && state != "cancelled" {
		return ErrConflict
	}
	if currentOwner != r.OwnerKey || currentKey != r.ArtifactKey || digest != object.Digest || size != object.Size {
		return ErrConflict
	}
	return nil
}
