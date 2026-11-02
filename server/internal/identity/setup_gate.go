package identity

import (
	"sync"
	"sync/atomic"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// setupGate is the remote gate's live answer to "is this server still unset?" (G3). It
// must fail closed: the gate answers everything only while it holds, so it can't depend
// on every owner-creating path remembering to tell anyone. Every committed write goes
// through dbwork, which names the tables it touched (and wakes every subscriber when it
// can't tell), synchronously before the write returns. A write that touched accounts
// leaves a wake here, and the next decision rechecks once. So whichever path creates the
// owner (local setup, a Portico Account claim or owner exchange, the setup exchange, an
// owner reset file, or one added later), the very next gate decision sees it, without a
// policy reload and without a per-request count.
type setupGate struct {
	setUp atomic.Bool
	wake  *worker.Signal
	mu    sync.Mutex
	stop  func()
}

// initSetupGate subscribes before it counts, so no owner committed in between is missed.
func (s *Service) initSetupGate() error {
	s.gate = &setupGate{wake: worker.NewSignal()}
	s.gate.stop = dbwork.WakeOnTables(s.gate.wake, "accounts")
	var exists bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM accounts)`).Scan(&exists); err != nil {
		s.gate.stop()
		return err
	}
	if exists {
		s.markSetUp()
	}
	return nil
}

// markSetUp latches: once an owner exists the gate applies until the process restarts.
func (s *Service) markSetUp() {
	s.gate.setUp.Store(true)
	s.gate.mu.Lock()
	stop := s.gate.stop
	s.gate.stop = nil
	s.gate.mu.Unlock()
	if stop != nil {
		stop()
	}
}

// Unconfigured reports, cheaply enough for every connection, whether the server has no
// owner yet, so the remote gate answers everything (setup applies its own rule). It is
// one atomic load once set up; before that, a recheck only after a committed write
// touched accounts. A recheck that fails counts as set up for this decision and is
// tried again on the next one.
func (s *Service) Unconfigured() bool {
	if s.gate == nil {
		return false
	}
	if s.gate.setUp.Load() {
		return false
	}
	if !s.gate.wake.Take() {
		return true
	}
	var exists bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM accounts)`).Scan(&exists); err != nil {
		s.gate.wake.Wake()
		return false
	}
	if exists {
		s.markSetUp()
		return false
	}
	return true
}
