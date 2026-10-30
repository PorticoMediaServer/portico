package remotemedia

import "context"

type sharedRangeKey struct {
	object, etag         string
	size, offset, length int64
}

// Only concurrent, exactly matching bounded reads on this physical endpoint
// share bytes. Completed data is never a cache or provider-version assertion.
// The admitted leader's real request context owns the transfer; its failure is
// returned to all joiners without retry, detached work or a replacement read.
type sharedRange struct {
	done  chan struct{}
	users int
	data  []byte
	err   error
}

func (s *ServeEndpoint) joinRange(key sharedRangeKey) *sharedRange {
	s.rangesMu.Lock()
	defer s.rangesMu.Unlock()
	shared := s.ranges[key]
	if shared != nil {
		shared.users++
		s.coalesced++
	}
	return shared
}

func (s *ServeEndpoint) startRange(key sharedRangeKey) (*sharedRange, bool) {
	s.rangesMu.Lock()
	defer s.rangesMu.Unlock()
	if shared := s.ranges[key]; shared != nil {
		shared.users++
		s.coalesced++
		return shared, false
	}
	if s.ranges == nil {
		s.ranges = make(map[sharedRangeKey]*sharedRange)
	}
	shared := &sharedRange{done: make(chan struct{}), users: 1}
	s.ranges[key] = shared
	return shared, true
}

// CoalescedRanges is an internal workload fact, not source or actor authority.
// It counts requests that joined an already admitted concurrent range read.
func (s *ServeEndpoint) CoalescedRanges() uint64 {
	s.rangesMu.Lock()
	defer s.rangesMu.Unlock()
	return s.coalesced
}

func (s *ServeEndpoint) finishRange(key sharedRangeKey, shared *sharedRange, data []byte, err error) {
	s.rangesMu.Lock()
	defer s.rangesMu.Unlock()
	delete(s.ranges, key)
	shared.data, shared.err = data, err
	close(shared.done)
}

func (s *ServeEndpoint) rangeResult(ctx context.Context, shared *sharedRange, check func(context.Context) error) ([]byte, error) {
	defer func() {
		s.rangesMu.Lock()
		shared.users--
		if shared.users == 0 {
			clear(shared.data)
			shared.data = nil
		}
		s.rangesMu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.budget.life.Done():
		return nil, s.budget.life.Err()
	case <-shared.done:
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if shared.err != nil {
		return nil, shared.err
	}
	if err := check(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Callers clear their own extent after use. Never expose the shared slice.
	s.rangesMu.Lock()
	data := append([]byte(nil), shared.data...)
	s.rangesMu.Unlock()
	return data, nil
}
