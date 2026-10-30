package httpapi

import "sync"

// Receiver waits retain their own bounded admission within the realtime lane.
type receiverStreamLimiter struct {
	mu    sync.Mutex
	total int
	held  map[string]int
}

func (l *receiverStreamLimiter) admit(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.total >= 512 || l.held[key] >= 4 {
		return false
	}
	l.total++
	l.held[key]++
	return true
}
func (l *receiverStreamLimiter) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.total--
	l.held[key]--
	if l.held[key] <= 0 {
		delete(l.held, key)
	}
}
