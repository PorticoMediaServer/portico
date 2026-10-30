package preparedmedia

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// Adapter makes domain progress/cancellation visible to the existing owner job
// console. Its StartTx reuses a saved explicit recipe; it cannot invent a source
// from a generic console resource string or admit arbitrary encoder arguments.
func (s *Service) Adapter() operations.Adapter {
	validate := func(ctx context.Context, tx *sql.Tx, id string) error {
		w, e := readWork(ctx, tx, id)
		if e != nil {
			return e
		}
		if _, e = s.check(ctx, tx, w.Principal, w.ItemID, true); e != nil {
			return e
		}
		if e = conversionAllowed(ctx, tx); e != nil {
			return e
		}
		return validateSelection(ctx, tx, w.Source, true)
	}
	return operations.Adapter{Kind: Kind, Lane: "background-media", Resource: operations.LaneOptimized, ResourceRequired: true, ValidateTx: validate,
		StartTx: func(ctx context.Context, tx *sql.Tx, operation, id string) (string, error) {
			// Domain work may finish before the console admits its observation.
			// A linked operation (including recovery) must observe that terminal
			// result, never turn cancellation/failure into an implicit new attempt.
			var domain, predecessor string
			if e := tx.QueryRowContext(ctx, `SELECT domain_id,predecessor FROM console_operations WHERE id=? AND kind=? AND resource=?`, operation, Kind, id).Scan(&domain, &predecessor); e != nil {
				return "", e
			}
			var state, item string
			if e := tx.QueryRowContext(ctx, `SELECT j.state,COALESCE(pid(e.public_id),'') FROM prepared_media_jobs j LEFT JOIN catalog_entities e ON e.id=j.item_id WHERE j.id=?`, id).Scan(&state, &item); e != nil {
				return "", e
			}
			if domain != "" || predecessor == "" || (state != "failed" && state != "cancelled") {
				if domain != "" && domain != id {
					return "", ErrConflict
				}
				return id, nil
			}
			// Only a new explicit console retry can queue a terminal domain job.
			// The scheduler records domain_id in this same transaction.
			if e := validate(ctx, tx, id); e != nil {
				return "", e
			}
			if ok, _ := s.configured(); !ok {
				return "", ErrConfiguration
			}
			if e := capacity(ctx, tx, item); e != nil {
				return "", e
			}
			_, e := tx.ExecContext(ctx, `UPDATE prepared_media_jobs SET state='queued',phase='waiting',bytes=0,error_code='',revision=revision+1,updated_ms=? WHERE id=?`, time.Now().UnixMilli(), id)
			return id, e
		},
		Observe: func(ctx context.Context, id string) (operations.JobObservation, error) {
			j, e := readJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM `+jobSource+` WHERE j.id=?`, id))
			state := j.State
			if state == "cancelling" {
				state = "running"
			}
			return operations.JobObservation{State: state, Phase: j.Phase, Processed: &j.Bytes, ErrorCode: j.ErrorCode}, e
		},
		CancelTx: cancelTx,
		Cancel: func(ctx context.Context, id string) error {
			gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
			if e != nil {
				return e
			}
			tx := gated.Tx()
			defer gated.Rollback()
			if e = cancelTx(ctx, tx, id); e != nil {
				return e
			}
			if e = gated.Commit(); e == nil {
				s.Interrupt(ctx, id)
			}
			return e
		},
		Interrupt: s.Interrupt,
	}
}
