package httpapi

import (
	"sync"
	"time"
)

type personalLimiter struct {
	mu      sync.Mutex
	entries map[string]rateEntry
}

func (l *personalLimiter) allow(profile string) bool { return l.allowAt(profile, time.Now()) }
func (l *personalLimiter) allowAt(profile string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.entries == nil {
		l.entries = map[string]rateEntry{}
	}
	entry, exists := l.entries[profile]
	if !exists && len(l.entries) >= 1024 {
		for key, value := range l.entries {
			if now.Sub(value.window) >= time.Minute {
				delete(l.entries, key)
			}
		}
		if len(l.entries) >= 1024 {
			return false
		}
	}
	if now.Sub(entry.window) >= time.Minute {
		entry = rateEntry{window: now}
	}
	if entry.count >= 120 {
		return false
	}
	entry.count++
	l.entries[profile] = entry
	return true
}
