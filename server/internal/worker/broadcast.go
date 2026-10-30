package worker

import "sync"

// Broadcast gives each loop its own coalescing signal. A channel shared by
// multiple loops is a work queue, and cannot be used to notify all of them.
type Broadcast struct {
	mu          sync.Mutex
	subscribers map[*Signal]struct{}
}

func (b *Broadcast) Subscribe() (*Signal, func()) {
	s := NewSignal()
	b.mu.Lock()
	if b.subscribers == nil {
		b.subscribers = make(map[*Signal]struct{})
	}
	b.subscribers[s] = struct{}{}
	b.mu.Unlock()
	return s, func() { b.mu.Lock(); delete(b.subscribers, s); b.mu.Unlock() }
}

func (b *Broadcast) Wake() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subscribers {
		s.Wake()
	}
}
