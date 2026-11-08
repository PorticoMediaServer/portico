package governor

import "context"

// PrepareWriter is trusted composition maintenance BEFORE admission, not a
// writer grant or a retry. The already bound service supplies the exact keeper,
// issuer and WAL. The closed writer supplies its full prospective request.
// Nothing renews the original deadline; cleanup retains its existing custody.
func (s *CheckpointWriterService) PrepareWriter(ctx context.Context, maintenance, next Request) (CheckpointResult, error) {
	if s == nil || s.keeper == nil || s.keeper.service.Load() != s || s.stopped.Load() || s.retired.Load() {
		return CheckpointResult{}, ErrCheckpointKeeper
	}
	c := s.keeper.cp
	if !validRequest(next) || !next.Writer || next.Need[WALBytes] <= 0 {
		return CheckpointResult{}, ErrGrant
	}
	maintenance.Deadline = minTime(maintenance.Deadline, next.Deadline)
	now := c.authority.ledger.config.Now()
	if err := c.wal.mu.lockUntil(ctx, maintenance.Deadline, now); err != nil {
		return CheckpointResult{}, admissionError(admissionWaitCause(err), err)
	}
	needed, err := func() (bool, error) {
		defer c.wal.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return false, err
		}
		now = c.authority.ledger.config.Now()
		if !maintenance.Deadline.After(now) {
			return false, context.DeadlineExceeded
		}
		if c.wal.writer || !c.wal.fresh(now) || next.RestartRisk != c.wal.restartRisk() {
			return false, ErrWALObservation
		}
		return c.wal.writerPressure(c.wal.state, next.Need[WALBytes]), nil
	}()
	if err != nil || !needed {
		return CheckpointResult{}, err
	}
	return c.step(ctx, maintenance, true, &next)
}

// The threshold is unchanged. Subtraction handles the prospective whole-command
// append without overflow. An empty WAL needs no reset even for a large command;
// ordinary admission still enforces the hard cap and its exact forecast.
func (w *WALGate) writerPressure(s WALState, appendBytes int64) bool {
	return s.ActiveBytes > 0 && s.PhysicalBytes <= w.policy.HardBytes && appendBytes > 0 &&
		(s.ActiveBytes >= w.policy.ActivePressureBytes || appendBytes > w.policy.ActivePressureBytes-s.ActiveBytes)
}
