package dbwork

import (
	"context"
	"database/sql"
	"time"
)

// Reader pressure is exceptional: ordinary reads are never gated. At the hard
// WAL threshold, a short pause in NEW reader admission creates the gap that
// overlapping healthy snapshots otherwise deny a resetting checkpoint. Already
// admitted transactions and new security-fence reads continue; writes retain
// their gate and priority. Sustained security reads may defer a reset. The
// drain happens before acquiring that gate, so a pinned reader cannot hold it.
const CheckpointReaderDrainDeadline = 250 * time.Millisecond

func checkpointReaderScope(conn *sql.Conn) *readerScope {
	var scope *readerScope
	_ = conn.Raw(func(raw any) error {
		if observed, ok := raw.(*observedConn); ok {
			scope = observed.readers
		}
		return nil
	})
	return scope
}

// pause returns an idempotent reopening function owned by this checkpoint.
// A concurrent checkpoint, or repeated failure on an external/pinned reader,
// gets a measured backoff instead of repeatedly delaying requests.
func (s *readerScope) pause() (reopen func(bool), drained <-chan struct{}, backoff time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pressure != nil {
		return nil, nil, CheckpointReaderDrainDeadline
	}
	if remaining := time.Until(s.nextPressure); remaining > 0 {
		return nil, nil, remaining
	}
	s.resume = make(chan struct{})
	s.pressure = s.resume
	s.drained = make(chan struct{})
	drained = s.drained
	if len(s.active) == 0 {
		close(s.drained)
		s.drained = nil
	}
	resume := s.resume
	return func(success bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.pressure != resume {
			return
		}
		if success {
			s.pressureFailures = 0
			s.nextPressure = time.Time{}
		} else {
			s.pressureFailures = min(4, s.pressureFailures+1)
			s.nextPressure = time.Now().Add(time.Second << (s.pressureFailures - 1))
		}
		if s.resume != nil {
			close(s.resume)
		}
		s.resume, s.pressure, s.drained = nil, nil, nil
	}, drained, 0
}

// Reopen requests as soon as the retained snapshots have drained. Copying the
// released tail may take time on slow storage; neither readers nor writers
// should wait for that I/O. The pressure owner remains reserved until its
// deferred finish records the reset outcome and backoff.
func (s *readerScope) resumePressureReaders() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resume != nil {
		close(s.resume)
		s.resume, s.drained = nil, nil
	}
}

// After out-of-gate copying, a short second reader gap lets TRUNCATE reset
// already copied frames. Reuse the pressure owner, not its old drain deadline;
// new requests were allowed to proceed for the whole copy pass.
func (s *readerScope) pausePressureReaders() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pressure == nil {
		return nil
	}
	s.resume = make(chan struct{})
	s.drained = make(chan struct{})
	drained := s.drained
	if len(s.active) == 0 {
		close(s.drained)
		s.drained = nil
	}
	return drained
}

func (s *readerScope) pressureBackoff() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return max(0, time.Until(s.nextPressure))
}

// waitReaderDrain keeps the waiting context separate from checkpoint copying:
// the driver busy callback has its own 50ms budget and cannot reliably obey
// context interruption. Reopening is the caller's defer, including on panic.
func waitReaderDrain(ctx context.Context, drained <-chan struct{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-drained:
		return nil
	}
}
