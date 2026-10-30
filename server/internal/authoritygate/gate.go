// Package authoritygate is the process-wide server-authority fence: one
// incarnation, admission leases, and a quiesce that drains them. Networking's
// claim and certificate work runs under it.
package authoritygate

import (
	"context"
	"errors"
	"regexp"
	"sync"
	"time"
)

var digest = regexp.MustCompile(`^[a-f0-9]{64}$`)

var ErrFenced = errors.New("server authority is fenced")

// AuthorityGate is the single process-wide incarnation/admission owner. Only the
// root supervisor may create/rotate/activate it. Domain adapters acquire leases.
type AuthorityGate struct {
	mu          sync.Mutex
	incarnation string
	denied      bool
	leases      map[*AuthorityLease]struct{}
	drained     chan struct{}
}
type AuthorityLease struct {
	gate        *AuthorityGate
	ctx         context.Context
	cancel      context.CancelFunc
	incarnation string
	closed      bool
	committing  bool
}

func NewAuthorityGate(incarnation string, denied bool) (*AuthorityGate, error) {
	if !digest.MatchString(incarnation) {
		return nil, ErrFenced
	}
	drained := make(chan struct{})
	close(drained)
	return &AuthorityGate{incarnation: incarnation, denied: denied, leases: make(map[*AuthorityLease]struct{}), drained: drained}, nil
}
func (g *AuthorityGate) Acquire(ctx context.Context) (*AuthorityLease, error) {
	began := time.Now()
	g.mu.Lock()
	waited := time.Since(began)
	defer warnAuthorityLockWait(waited)
	defer g.mu.Unlock()
	if g.denied || ctx.Err() != nil {
		return nil, ErrFenced
	}
	if len(g.leases) == 0 {
		g.drained = make(chan struct{})
	}
	leaseCtx, cancel := context.WithCancel(ctx)
	lease := &AuthorityLease{gate: g, ctx: leaseCtx, cancel: cancel, incarnation: g.incarnation}
	g.leases[lease] = struct{}{}
	return lease, nil
}
func (l *AuthorityLease) Context() context.Context { return l.ctx }
func (l *AuthorityLease) Incarnation() string      { return l.incarnation }
func (l *AuthorityLease) check() error {
	if l.closed || l.gate.denied || l.gate.incarnation != l.incarnation || l.ctx.Err() != nil {
		return ErrFenced
	}
	return nil
}
func (l *AuthorityLease) Check() error {
	began := time.Now()
	l.gate.mu.Lock()
	waited := time.Since(began)
	defer warnAuthorityLockWait(waited)
	defer l.gate.mu.Unlock()
	return l.check()
}

// Commit linearizes admission under a short lock, then runs the durable effect
// as counted in-flight work. Quiesce immediately denies/cancels even if the
// callback stalls; WaitDrained cannot succeed until the callback AND lease end.
// A commit admitted before quiesce may finish during drain, never during install.
func (l *AuthorityLease) Commit(commit func() error) error {
	g := l.gate
	began := time.Now()
	g.mu.Lock()
	waited := time.Since(began)
	if err := l.check(); err != nil {
		g.mu.Unlock()
		warnAuthorityLockWait(waited)
		return err
	}
	if commit == nil || l.committing {
		g.mu.Unlock()
		warnAuthorityLockWait(waited)
		return ErrFenced
	}
	l.committing = true
	g.mu.Unlock()
	warnAuthorityLockWait(waited)
	defer func() {
		began := time.Now()
		g.mu.Lock()
		waited := time.Since(began)
		defer warnAuthorityLockWait(waited)
		defer g.mu.Unlock()
		l.committing = false
		if l.closed {
			delete(g.leases, l)
			if len(g.leases) == 0 {
				close(g.drained)
			}
		}
	}()
	return commit()
}
func (l *AuthorityLease) Close() {
	g := l.gate
	began := time.Now()
	g.mu.Lock()
	waited := time.Since(began)
	defer warnAuthorityLockWait(waited)
	defer g.mu.Unlock()
	if l.closed {
		return
	}
	l.closed = true
	l.cancel()
	if l.committing {
		return
	}
	delete(g.leases, l)
	if len(g.leases) == 0 {
		close(g.drained)
	}
}
func (g *AuthorityGate) BeginQuiesce() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.denied = true
	for lease := range g.leases {
		lease.cancel()
	}
}
func (g *AuthorityGate) WaitDrained(ctx context.Context) error {
	g.mu.Lock()
	if !g.denied {
		g.mu.Unlock()
		return ErrFenced
	}
	drained := g.drained
	g.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-drained:
		return nil
	}
}
func (g *AuthorityGate) Rotate(incarnation string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.denied || len(g.leases) != 0 || !digest.MatchString(incarnation) {
		return ErrFenced
	}
	g.incarnation = incarnation
	return nil
}

// Activate reopens a quiesced gate. The incarnation must equal the current
// one; no consumer may bypass the shared gate.
func (g *AuthorityGate) Activate(incarnation string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.denied || len(g.leases) != 0 || incarnation != g.incarnation {
		return ErrFenced
	}
	g.denied = false
	return nil
}
