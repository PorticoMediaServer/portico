package ingestion

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// QueueOperationTx captures every enabled source job in the console receipt's
// transaction. DomainID is the operation ID, not an arbitrary first source.
func (s *Service) QueueOperationTx(ctx context.Context, tx *sql.Tx, operation, library string) (string, error) {
	job, err := s.QueueTx(ctx, tx, library)
	if err != nil {
		return "", err
	}
	for _, id := range job.Jobs {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO inventory_operation_jobs(operation_id,job_id) VALUES(?,?)`, operation, id); err != nil {
			return "", err
		}
	}
	return operation, nil
}

type OperationObservation struct {
	State, Phase string
	Processed    int64
}

func (s *Service) ObserveOperation(ctx context.Context, operation string) (OperationObservation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT j.id,j.status,j.processed,r.analyzed,r.warnings FROM inventory_operation_jobs g JOIN jobs j ON j.id=g.job_id JOIN inventory_runs r ON r.job_id=j.id WHERE g.operation_id=? ORDER BY j.id`, operation)
	if err != nil {
		return OperationObservation{}, err
	}
	defer rows.Close()
	var out OperationObservation
	count, running, queued, paused, failed, cancelled := 0, 0, 0, 0, 0, 0
	var analyzed, warnings int64
	quiescent := true
	for rows.Next() {
		var id, state string
		var processed, a, w int64
		if err = rows.Scan(&id, &state, &processed, &a, &w); err != nil {
			return out, err
		}
		count++
		out.Processed += processed
		analyzed += a
		warnings += w
		switch state {
		case "running":
			running++
		case "queued":
			queued++
		case "paused":
			paused++
		case "failed":
			failed++
		case "cancelled":
			cancelled++
		}
		quiescent = quiescent && s.Quiescent(id)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if count == 0 {
		return out, sql.ErrNoRows
	}
	switch {
	case running > 0:
		out.State = "running"
	case queued > 0:
		out.State = "queued"
	case paused > 0:
		out.State = "paused"
	case !quiescent:
		out.State = "running"
	case failed > 0:
		out.State = "failed"
	case cancelled > 0:
		out.State = "cancelled"
	default:
		out.State = "succeeded"
	}
	out.Phase = fmt.Sprintf("%s: %d sources, %d analyzed, %d warnings", out.State, count, analyzed, warnings)
	return out, nil
}

// ControlOperationTx is called inside the authorized console command/receipt
// transaction. A pause/cancel therefore fences publication before acknowledgement.
func (s *Service) ControlOperationTx(ctx context.Context, tx *sql.Tx, operation, action string) error {
	rows, err := tx.QueryContext(ctx, `SELECT j.id,j.status FROM inventory_operation_jobs g JOIN jobs j ON j.id=g.job_id WHERE g.operation_id=? ORDER BY j.id`, operation)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id, state string
		if err = rows.Scan(&id, &state); err != nil {
			rows.Close()
			return err
		}
		if state == "queued" || state == "running" || state == "paused" {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = controlJobTx(ctx, tx, id, action); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) InterruptOperation(ctx context.Context, operation string) {
	rows, err := s.db.QueryContext(ctx, `SELECT g.job_id FROM inventory_operation_jobs g JOIN jobs j ON j.id=g.job_id WHERE g.operation_id=? AND j.status IN('paused','cancelled')`, operation)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			s.stopActive(id)
		}
	}
}
func (s *Service) CancelOperation(ctx context.Context, operation string) error {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if err = s.ControlOperationTx(ctx, tx, operation, "cancel"); err != nil {
		return err
	}
	if err = gated.Commit(); err != nil {
		return err
	}
	s.InterruptOperation(ctx, operation)
	return nil
}
