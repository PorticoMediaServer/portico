package governor

import (
	"context"
	"sync"
	"time"
)

// One ledger lock. A channel token permits bounded admission revalidation
// without polling, a goroutine per check or a second grant/queue authority.
// Admission and validation bound contention by original deadlines.
// Retirement and diagnostics use ordinary Lock.
// The zero value is usable, as with sync.Mutex.
type ledgerMutex struct {
	once  sync.Once
	token chan struct{}
	timer func(time.Duration) (<-chan time.Time, func()) // private deterministic test seam
}

func (m *ledgerMutex) init() {
	m.once.Do(func() {
		m.token = make(chan struct{}, 1)
		m.token <- struct{}{}
		if m.timer == nil {
			m.timer = func(d time.Duration) (<-chan time.Time, func()) {
				t := time.NewTimer(d)
				return t.C, func() { t.Stop() }
			}
		}
	})
}
func (m *ledgerMutex) Lock() { m.init(); <-m.token }
func (m *ledgerMutex) TryLock() bool {
	m.init()
	select {
	case <-m.token:
		return true
	default:
		return false
	}
}
func (m *ledgerMutex) Unlock() {
	select {
	case m.token <- struct{}{}:
	default:
		panic("unlock of unlocked governor ledger")
	}
}
func (m *ledgerMutex) lockWithin(remaining time.Duration, cancelled <-chan struct{}, ctx context.Context) error {
	if remaining <= 0 {
		return ErrGrant
	}
	m.init()
	deadline, stop := m.timer(remaining)
	defer stop()
	select {
	case <-deadline:
		return ErrGrant
	default:
	}
	select {
	case <-cancelled:
		return ErrGrant
	case <-ctx.Done():
		return ctx.Err()
	case <-m.token:
		return nil
	case <-deadline:
		return ErrGrant
	}
}
