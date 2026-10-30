package downloads

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/selectionactions"
)

const selectionTransferGrace = time.Hour

// OpenSelectionActionTransferTx is called in the confirmed mutation's accepting
// writer. It records actual device transfer ownership, never query membership.
func (s *Service) OpenSelectionActionTransferTx(ctx context.Context, tx *sql.Tx, p identity.Principal, job selectionactions.Job, options selectionactions.Options) error {
	if job.Action != "download" {
		return nil
	}
	if err := dbwork.RequireGatedWriteTx(tx); err != nil {
		return err
	}
	if !validID.MatchString(job.OperationID) || !validID.MatchString(options.DeviceID) {
		return ErrInput
	}
	if _, _, err := s.requestAccess(ctx, tx, p, options.DeviceID); err != nil {
		return err
	}
	var present bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM download_action_transfers WHERE profile_key=? AND device_id=? AND operation_id=?)`, operations.ViewerKey(p), options.DeviceID, job.OperationID).Scan(&present); err != nil {
		return err
	}
	if present {
		return ErrRequestConflict
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO download_action_transfers(profile_key,device_id,operation_id,created_ms) VALUES(?,?,?,?)`, operations.ViewerKey(p), options.DeviceID, job.OperationID, s.millis())
	return err
}

// CloseSelectionActionTransferTx is part of terminal/cancel publication BEFORE
// any command journal is purged. It grants no effect and deliberately does not
// require still-live authority to acknowledge the end of already accepted work.
func (s *Service) CloseSelectionActionTransferTx(ctx context.Context, tx *sql.Tx, p identity.Principal, job selectionactions.Job, options selectionactions.Options) error {
	if job.Action != "download" {
		return nil
	}
	if err := dbwork.RequireGatedWriteTx(tx); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE download_action_transfers SET closed=1,closed_ms=CASE WHEN closed=0 THEN ? ELSE closed_ms END WHERE profile_key=? AND device_id=? AND operation_id=?`, s.millis(), operations.ViewerKey(p), options.DeviceID, job.OperationID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

func selectionActionTransferClosedTx(ctx context.Context, tx *sql.Tx, p identity.Principal, operationID, deviceID string) (bool, error) {
	var closed bool
	err := tx.QueryRowContext(ctx, `SELECT closed FROM download_action_transfers WHERE profile_key=? AND device_id=? AND operation_id=?`, operations.ViewerKey(p), deviceID, operationID).Scan(&closed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return closed, err
}

// cleanupSelectionActionTransfers scans <=20 RAW base ownership rows, including
// still-active/claimed rows. Its process-local cursor cannot starve later keys.
// A closed row is retained while real preparation claims exist; after their FK
// lifetime ends, one short follower grace is enough. No per-operation result list
// or permanent history is retained here.
func (s *Service) cleanupSelectionActionTransfers(ctx context.Context) error {
	rows, err := dbwork.Query(ctx, s.db, `SELECT id FROM download_action_transfers WHERE id>? ORDER BY id LIMIT 20`, s.transferSweep)
	if err != nil {
		return err
	}
	ids := make([]int64, 0, 20)
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		s.transferSweep = 0
		return nil
	}
	cutoff := s.millis() - selectionTransferGrace.Milliseconds()
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		for _, id := range ids {
			var profile, device, operation string
			var closed bool
			var closedMS int64
			err := tx.QueryRowContext(ctx, `SELECT profile_key,device_id,operation_id,closed,closed_ms FROM download_action_transfers WHERE id=?`, id).Scan(&profile, &device, &operation, &closed, &closedMS)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if !closed || closedMS > cutoff {
				continue
			}
			var claimed bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM download_action_claims WHERE profile_key=? AND device_id=? AND operation_id=? LIMIT 1)`, profile, device, operation).Scan(&claimed); err != nil {
				return err
			}
			if claimed {
				continue
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM download_action_transfers WHERE id=? AND closed=1 AND closed_ms<=?`, id, cutoff); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		s.transferSweep = ids[len(ids)-1]
	}
	return err
}
