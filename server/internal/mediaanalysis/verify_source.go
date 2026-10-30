package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Verify before reusing a descriptor's cached stages: an unchanged .strm file
// does not mean its remote target is unchanged. This checkpoint has the same
// durable retry and cancellation rules as every other analysis stage.
func (s *Service) verifySTRM(ctx context.Context, b binding) (Input, bool, string, error) {
	b.WorkAlgorithm = Algorithm
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return nil, false, "", e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if _, e = s.bindingTx(ctx, tx, b.Request); e != nil {
		return nil, false, "", e
	}
	var state, code string
	var next int64
	e = tx.QueryRowContext(ctx, `SELECT state,error_code,next_ms FROM analysis_stage_runs WHERE job_id=? AND object_id=? AND source_revision=? AND policy_revision=? AND stage='source_verify' AND algorithm=?`, b.JobID, b.ObjectID, b.SourceRevision, b.PolicyRevision, Algorithm).Scan(&state, &code, &next)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, false, "", e
	}
	if state == "failed" || state == "unsupported" {
		return nil, false, code, nil
	}
	if next > nowMS() {
		return nil, false, "", nil
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO analysis_stage_runs(job_id,object_id,source_revision,policy_revision,stage,algorithm,state,attempt,updated_ms) VALUES(?,?,?,?,'source_verify',?,'running',1,?) ON CONFLICT(job_id,object_id,source_revision,policy_revision,stage,algorithm) DO UPDATE SET state='running',attempt=attempt+1,updated_ms=excluded.updated_ms`, b.JobID, b.ObjectID, b.SourceRevision, b.PolicyRevision, Algorithm, nowMS())
	if e != nil {
		return nil, false, "", e
	}
	if e = gated.Commit(); e != nil {
		return nil, false, "", e
	}
	input, e := s.options.Open(ctx, b.ItemID, b.AssetID)
	if e == nil {
		if invocation, ok := ReadInvocationFromContext(ctx); ok {
			input = &authorityInput{Input: input, invocation: invocation}
		}
	}
	if e == nil {
		e = input.Validate(ctx)
	}
	if e == nil && (b.ProbeEvidence == "" || input.Evidence() != b.ProbeEvidence) {
		// A write: the reset must go through the writer gate (B80; a read snapshot
		// refused it, so the stale-probe reset never happened).
		gatedReset, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
		if err == nil {
			reset := gatedReset.Tx()
			_, err = s.bindingTx(ctx, reset, b.Request)
			if err == nil {
				_, err = reset.ExecContext(ctx, `UPDATE inventory_objects SET analysis_revision='',analysis_state='pending' WHERE id=? AND revision=?`, b.ObjectID, b.SourceRevision)
			}
			if err == nil {
				err = gatedReset.Commit()
			}
			gatedReset.Rollback()
		}
		e = ErrConflict
		if err != nil {
			e = err
		}
	}
	if e != nil {
		if input != nil {
			input.Close()
		}
		_, _, err := s.stageFailure(ctx, b, "source_verify", e)
		return nil, false, "", err
	}
	_, e = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE analysis_stage_runs SET state='complete',attempt=0,error_code='',next_ms=0,updated_ms=? WHERE job_id=? AND object_id=? AND source_revision=? AND policy_revision=? AND stage='source_verify' AND algorithm=?`, nowMS(), b.JobID, b.ObjectID, b.SourceRevision, b.PolicyRevision, Algorithm)
	if e != nil {
		input.Close()
		return nil, false, "", e
	}
	return input, true, "", nil
}
