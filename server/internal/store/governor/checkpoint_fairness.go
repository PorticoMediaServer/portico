package governor

import (
	"context"
	"sync/atomic"
	"time"
)

// One bounded metadata-only owed turn. It is not a native grant or permission
// to enter SQLite. New writers cannot jump it after the current physical writer
// returns. Cancellation/expiry drops only unused metadata, never an active CP.
type checkpointTicket struct {
	cancelled atomic.Bool // unused metadata only; never releases an active CP
	gate      *WALGate
	owner     *ledger
	expires   time.Time
	ready     chan struct{}
	recovery  bool
	signalled bool // protected by gate.mu
}

func (a *Authority) queueCheckpoint(until time.Time, spacing time.Duration) (*checkpointTicket, error) {
	return a.queueCheckpointMode(until, spacing, false)
}
func (a *Authority) queueCheckpointMode(until time.Time, spacing time.Duration, recovery bool) (*checkpointTicket, error) {
	return a.queueCheckpointContext(context.Background(), until, spacing, recovery)
}
func (a *Authority) queueCheckpointContext(ctx context.Context, until time.Time, spacing time.Duration, recovery bool) (*checkpointTicket, error) {
	w := a.ledger.config.WAL
	now := a.ledger.config.Now()
	if w == nil || spacing <= 0 {
		return nil, admissionError(CauseInvalidRequest, ErrGrant)
	}
	if !until.After(now) {
		return nil, admissionError(CauseDeadline, context.DeadlineExceeded)
	}
	if err := w.mu.lockUntil(ctx, until, now); err != nil {
		return nil, admissionError(admissionWaitCause(err), err)
	}
	defer w.mu.Unlock()
	now = a.ledger.config.Now()
	if !until.After(now) {
		return nil, admissionError(CauseDeadline, context.DeadlineExceeded)
	}
	w.expireCheckpointTicket(now)
	if !recovery && now.Before(w.nextCheckpoint) {
		return nil, admissionError(CauseCheckpointCadence, ErrCheckpointCadence)
	}
	if w.pendingCheckpoint != nil || w.checkpoint {
		return nil, admissionError(CauseCheckpointActive, ErrBusy)
	}
	ticket := &checkpointTicket{gate: w, owner: a.ledger, expires: until, ready: make(chan struct{}), recovery: recovery}
	w.pendingCheckpoint = ticket
	// Charge the WHOLE attempted exclusion allowance, even if it is cancelled or
	// cannot acquire resources. Reconstructing a driver cannot reset this budget.
	w.nextCheckpoint = now.Add(spacing)
	w.signalCheckpointTicket()
	return ticket, nil
}
func (w *WALGate) signalCheckpointTicket() {
	if t := w.pendingCheckpoint; t != nil && !t.cancelled.Load() && (!w.writer || w.policy.ConcurrentPassive) && !w.checkpoint && !t.signalled {
		t.signalled = true
		close(t.ready)
	}
}
func (w *WALGate) expireCheckpointTicket(now time.Time) {
	if t := w.pendingCheckpoint; t != nil && (t.cancelled.Load() || !now.Before(t.expires)) {
		w.pendingCheckpoint = nil
	}
}

// Cancellation is an issuer-checked, lock-free metadata tombstone. The next
// gate owner prunes the one pending pointer; no caller waits for the gate merely
// to abandon an unused offer. Active CP custody lives elsewhere and is untouched.
func (a *Authority) cancelCheckpointTicket(t *checkpointTicket) {
	if a != nil && a.ledger != nil && t != nil && t.owner == a.ledger && t.gate == a.ledger.config.WAL {
		t.cancelled.Store(true)
	}
}

func (a *Authority) tryCheckpointTurn(r Request, t *checkpointTicket) Decision {
	return a.ledger.try(r, true, t)
}
func (a *Authority) tryCheckpointTurnContext(ctx context.Context, r Request, t *checkpointTicket) Decision {
	return a.ledger.tryReservedContext(ctx, r.Deadline, r, true, t, nil, nil, nil)
}

// Cap attempted CP exclusion to 5% over repeated periods, plus one bounded
// burst. MaxWait(<=75ms)+MaxHold(<=25ms) bounds that burst by100ms, preserving
// >=90% writer availability in every rolling second under qualified exits.
// The paired FG/scan dispatcher must still prove its own service split.
// MaxWait includes the old writer/wakeup allowance; MaxHold includes the whole
// physical turn. Native/scheduler misses fail, never preempt or refund early.
func checkpointSpacing(policy CheckpointPolicy) time.Duration {
	return max(policy.Interval, 20*(policy.MaxWait+policy.MaxHold))
}
