// Package stateownership retains the server's one native state-directory lock.
// It supplies no database handle, principal, command, lease or write authority.
package stateownership

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	ErrHeld        = errors.New("another Portico server is already using this state directory")
	ErrUnsupported = errors.New("this filesystem does not support advisory locking")
	ErrInvalid     = errors.New("invalid state ownership")
	ErrClosing     = errors.New("state ownership is closing")
	ErrBusy        = errors.New("state ownership still has physical borrowers")
	ErrChanged     = errors.New("state ownership lock file changed")
)

// Lock is issued only by an actual native acquisition. Zero and copied values
// refuse. Close revokes new borrows immediately, retaining the descriptor until
// every borrower has physically finished. This is lifetime custody, not a
// scheduler or a resource grant.
type Lock struct {
	self  *Lock
	state *lockState
}

type lockState struct {
	mu       sync.Mutex
	file     *os.File
	path     string
	closing  bool
	borrowed uint64
	closeErr error
	fault    error // permanent: restoring the pathname cannot restore authority
}

// Lease retains the native lock through a physical owner. Copies share the
// SAME one-shot release; copying a value never creates another holding.
// A caller's cancellation cannot release a lease: its actual owner calls Close
// only after all native/child work and borrowed outputs have joined.
type Lease struct{ holding *holding }

type holding struct {
	state    *lockState
	released bool // protected by state.mu
}

// Acquire preserves the server's native lock and second-description probe.
// A PID file is not an ownership proof. The lock file is deliberately retained
// on disk after release; a leftover pathname never proves an owner is alive.
func Acquire(path string) (*Lock, error) { return acquire(path, lockInstanceFile) }

// Fault injection is private and is used only by this package's native tests.
func acquire(path string, lock func(*os.File) error) (*Lock, error) {
	if path == "" {
		return nil, ErrInvalid
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// Go opens close-on-exec: helpers must not inherit ownership after exit.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lock(file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	// A successful syscall alone cannot detect filesystems silently ignoring it.
	probe, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	firstInfo, firstErr := file.Stat()
	probeInfo, probeErr := probe.Stat()
	if firstErr != nil || probeErr != nil || !os.SameFile(firstInfo, probeInfo) {
		return nil, errors.Join(fmt.Errorf("%w: lock file changed while checking exclusivity", ErrUnsupported), firstErr, probeErr, probe.Close(), file.Close())
	}
	err = lock(probe)
	closeErr := probe.Close()
	if !errors.Is(err, ErrHeld) || closeErr != nil {
		if err == nil {
			err = fmt.Errorf("%w: a second handle acquired the exclusive lock", ErrUnsupported)
		} else {
			err = fmt.Errorf("cannot verify exclusive state locking: %w", errors.Join(err, closeErr))
		}
		return nil, errors.Join(err, closeErr, file.Close())
	}
	out := &Lock{state: &lockState{file: file, path: path}}
	out.self = out
	return out, nil
}

func (l *Lock) valid() bool { return l != nil && l.self == l && l.state != nil }

// Borrow retains actual ownership. A closing lock cannot issue new borrowers.
// No lease can be minted from a path, boolean, interface or caller-supplied FD.
func (l *Lock) Borrow() (*Lease, error) {
	if !l.valid() {
		return nil, ErrInvalid
	}
	s := l.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(); err != nil {
		return nil, err
	}
	if s.borrowed == ^uint64(0) {
		return nil, ErrBusy
	}
	s.borrowed++
	return &Lease{holding: &holding{state: s}}, nil
}

// Check refuses a revoked, released or zero lease and a replaced lock pathname.
// It is not proof of the database identity or any current native row. Trusted
// writer opening must bind those independently before minting ingress.
func (l *Lease) Check() error {
	if l == nil || l.holding == nil || l.holding.state == nil {
		return ErrInvalid
	}
	h := l.holding
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	if h.released {
		return ErrClosing
	}
	return h.state.check()
}

// LockPath reports only the verified owned lock pathname, never a DB handle.
func (l *Lease) LockPath() (string, error) {
	if l == nil || l.holding == nil || l.holding.state == nil {
		return "", ErrInvalid
	}
	h := l.holding
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	if h.released {
		return "", ErrClosing
	}
	if err := h.state.check(); err != nil {
		return "", err
	}
	return h.state.path, nil
}

// check is called with mu held. Native state storage must still obey the
// existing rule that multiple hosts never share one state folder: a local
// second-FD probe cannot establish cross-host NFS locking semantics.
func (s *lockState) check() error {
	if s.closing || s.file == nil {
		return ErrClosing
	}
	if s.fault != nil {
		return s.fault
	}
	held, err := s.file.Stat()
	if err != nil {
		s.fault = errors.Join(ErrChanged, err)
		return s.fault
	}
	current, err := os.Stat(s.path)
	if err != nil || !os.SameFile(held, current) {
		s.fault = errors.Join(ErrChanged, err)
		return s.fault
	}
	return nil
}

// Close revokes ingress before returning. ErrBusy means the native lock is
// still retained; the last physical borrow releases it without an early unlock.
func (l *Lock) Close() error {
	if l == nil {
		return nil
	}
	if !l.valid() {
		return ErrInvalid
	}
	s := l.state
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closing = true
	if s.borrowed != 0 {
		return ErrBusy
	}
	return s.close()
}

// Close is idempotent across copies of a lease. Calling it is a physical-owner
// obligation; a deadline or Cancel does not authorize an early release.
func (l *Lease) Close() error {
	if l == nil {
		return nil
	}
	if l.holding == nil || l.holding.state == nil {
		return ErrInvalid
	}
	h := l.holding
	s := h.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if h.released {
		return s.closeErr
	}
	h.released = true
	s.borrowed--
	if s.closing && s.borrowed == 0 {
		return s.close()
	}
	return nil
}

func (s *lockState) close() error {
	if s.file != nil {
		unlockInstanceFile(s.file)
		s.closeErr = s.file.Close()
		s.file = nil
	}
	return s.closeErr
}
