package readprep

import "sync"

// TryColdQuantum lets an already-confirmed command's bounded read-only capture
// share the SAME cold-search lane as volatile preparation. The caller must hold
// a live delivery reservation from this store for all state/transient bytes.
// False is cooperative yield, never refusal. Release after the short snapshot;
// this is not a durable job, a query result, or a second admission queue.
func (s *Store) TryColdQuantum(reservation Result) (release func(), acquired bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, false, ErrClosed
	}
	d := reservation.delivery
	if d == nil || d.s != s || d.released {
		return nil, false, ErrStale
	}
	if s.coldOwner != nil {
		return nil, false, nil
	}
	// An external running owner cannot be selected by a worker. Its state and
	// memory belong to the caller's charged delivery, not an entry in the cache.
	owner := &entry{cold: true, running: true}
	s.coldOwner = owner
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.coldOwner == owner {
				s.coldOwner = nil
				s.cond.Broadcast()
			}
		})
	}, true, nil
}
