package governor

import "time"

// One bounded metadata-only owed turn. It is not a native grant or permission
// to enter SQLite. New writers cannot jump it after the current physical writer
// returns. Cancellation/expiry drops only unused metadata, never an active CP.
type checkpointTicket struct {
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
	w := a.ledger.config.WAL
	now := a.ledger.config.Now()
	if w == nil || !until.After(now) || spacing <= 0 {
		return nil, ErrGrant
	}
	if !w.mu.TryLock() {
		return nil, ErrBusy
	}
	defer w.mu.Unlock()
	w.expireCheckpointTicket(now)
	if !recovery && now.Before(w.nextCheckpoint) {
		return nil, ErrCheckpointCadence
	}
	if w.pendingCheckpoint != nil || w.checkpoint {
		return nil, ErrBusy
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
	if t := w.pendingCheckpoint; t != nil && (!w.writer || w.policy.ConcurrentPassive) && !w.checkpoint && !t.signalled {
		t.signalled = true
		close(t.ready)
	}
}
func (w *WALGate) expireCheckpointTicket(now time.Time) {
	if t := w.pendingCheckpoint; t != nil && !now.Before(t.expires) {
		w.pendingCheckpoint = nil
	}
}
func (a *Authority) cancelCheckpointTicket(t *checkpointTicket) {
	w := a.ledger.config.WAL
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pendingCheckpoint == t && t != nil && t.owner == a.ledger {
		w.pendingCheckpoint = nil
	}
}
func (a *Authority) tryCheckpointTurn(r Request, t *checkpointTicket) Decision {
	return a.ledger.try(r, true, t)
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
