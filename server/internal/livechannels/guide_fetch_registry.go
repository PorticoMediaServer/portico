package livechannels

import (
	"context"
	"sync"
)

// Pause is source-wide for guide acquisition. A fetch registers the cancel
// handle for the source it is refreshing and, when it depends on a separate
// guide, for that guide source too. A committed pause cancels the registered
// handles; publication still rechecks the guide state and revision, so a
// cancelled or late result cannot publish.
const maxGuideFetchesPerSource = 64

type guideFetchRegistry struct {
	mu       sync.Mutex
	next     uint64
	bySource map[string]map[uint64]context.CancelFunc
}

// register returns an idempotent unregister. A source at the bounded maximum
// of in-flight fetches is refused rather than silently left untracked.
func (r *guideFetchRegistry) register(source string, cancel context.CancelFunc) (func(), error) {
	r.mu.Lock()
	if r.bySource == nil {
		r.bySource = map[string]map[uint64]context.CancelFunc{}
	}
	active := r.bySource[source]
	if len(active) >= maxGuideFetchesPerSource {
		r.mu.Unlock()
		return nil, ErrSourceUnavailable
	}
	r.next++
	id := r.next
	if active == nil {
		active = map[uint64]context.CancelFunc{}
		r.bySource[source] = active
	}
	active[id] = cancel
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if current := r.bySource[source]; current != nil {
			delete(current, id)
			if len(current) == 0 {
				delete(r.bySource, source)
			}
		}
	}, nil
}

// cancel is bounded in-memory work: no IO and no new jobs. The caller runs it
// only after the pause transaction commits.
func (r *guideFetchRegistry) cancel(source string) {
	r.mu.Lock()
	active := r.bySource[source]
	delete(r.bySource, source)
	r.mu.Unlock()
	for _, cancel := range active {
		cancel()
	}
}
