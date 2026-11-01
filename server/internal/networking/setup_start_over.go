package networking

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// RetireSetupClaim withdraws the active first-run setup claim before setup starts over
// (finding #2). It needs no owner or setup credential: whoever may start setup may end
// an unfinished one.
//
//   - An unapproved claim ends on this server alone, as a declined or abandoned one does.
//   - An approved, finishing or installed claim is cancelled with the server's original-key
//     deny-only assertion. It is queued durably (CancelPending) before anything is sent, so
//     a Portico Services outage leaves it waiting, never forgotten; a later call delivers it.
//
// pending reports whether a withdrawal is still waiting for Portico Services. A transport
// failure is not an error here: the queued cancellation is the record.
func (h *ClaimHandler) RetireSetupClaim(parent context.Context) (pending bool, err error) {
	if h == nil {
		return false, nil
	}
	ctx, stop := context.WithTimeout(parent, 15*time.Second)
	defer stop()
	var operation string
	err = h.runner.Do(ctx, func(ctx context.Context) error {
		active, err := h.activeSetupOperation(ctx)
		if err != nil || active == "" {
			return err
		}
		operation = active
		v, err := h.store.Load(ctx, active)
		if err != nil {
			return err
		}
		switch {
		case v.Purpose != "setup":
			return nil
		case v.Stage == Cancelled:
			return nil
		case v.Stage == Prepared && v.ApprovalRevision == 0:
			_, err = h.store.WithdrawSetupClaim(ctx, v, "cancelled")
			return err
		}
		coordinator, err := NewCoordinator(v.OperationID, h.store.audience, claimExpectedStore{Store: h.store, expected: v}, h.transport, h.signer)
		if err != nil {
			return err
		}
		next, err := coordinator.Cancel(ctx)
		if err != nil {
			// Queued but not delivered: the outbox holds it.
			current, loadErr := h.store.Load(ctx, active)
			if loadErr == nil && current.Stage == CancelPending {
				pending = true
				return nil
			}
			return err
		}
		pending = next.Stage == CancelPending
		return nil
	})
	if operation != "" {
		h.StopSetupWatch(operation)
	}
	return pending, err
}

// activeSetupOperation is the claim the server's identity row names as active, or "",
// and always "" once the server has an owner: a set-up server's installed claim is its
// own, and starting over never touches it.
func (h *ClaimHandler) activeSetupOperation(ctx context.Context) (string, error) {
	read, err := dbwork.BeginSnapshot(ctx, h.store.db)
	if err != nil {
		return "", err
	}
	defer read.Rollback()
	var accounts int
	if err = read.Tx().QueryRowContext(ctx, `SELECT count(*) FROM accounts`).Scan(&accounts); err != nil || accounts != 0 {
		return "", err
	}
	var active sql.NullString
	err = read.Tx().QueryRowContext(ctx, `SELECT active_operation_id FROM networking_claim_identity WHERE singleton=1`).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !active.Valid {
		return "", nil
	}
	return active.String, err
}
