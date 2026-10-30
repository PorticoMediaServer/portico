package social

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// CastBootstrapCancel is authoritative even when the receiver start ACK was
// lost: cancellation wins before commit, or stops only the committed custody.
// It never restores a browser queue or stops a later different target transfer.
func (s *Store) CastBootstrapCancel(ctx context.Context, p identity.Principal, id string) (CastBootstrapResponse, error) {
	var out CastBootstrapResponse
	if !ValidID(id) || s.Identity == nil {
		return out, errInvalid
	}
	gate, err := s.begin(ctx)
	if err != nil {
		return out, err
	}
	defer gate.Rollback()
	tx := gate.Tx()
	status, err := s.Identity.CastQueueTransferStatusTx(ctx, tx, p, id, true)
	if err != nil {
		return out, err
	}
	if status == nil {
		return out, errNotFound
	}
	var after func()
	if status.State == "committed" {
		target, err := s.Identity.CastQueueTransferStopTargetTx(ctx, tx, p, id)
		if err != nil {
			return out, err
		}
		if target != "" {
			if s.Playback == nil {
				return out, identity.ErrForbidden
			}
			after, err = s.Playback.EndSessionTx(ctx, tx, p, target, "stopped")
			if err != nil {
				return out, err
			}
		}
		status, err = s.Identity.CastQueueTransferStatusTx(ctx, tx, p, id, false)
		if err != nil {
			return out, err
		}
	} else {
		if _, err = tx.ExecContext(ctx, `UPDATE social_cast_bootstraps SET state='cancelled' WHERE id=? AND state='pending'`, id); err != nil {
			return out, err
		}
	}
	if err = gate.Commit(); err != nil {
		return out, err
	}
	if after != nil {
		after()
	}
	return s.CastBootstrapStatus(ctx, p, id)
}
