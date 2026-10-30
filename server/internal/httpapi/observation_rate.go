package httpapi

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

const (
	observationsPerSecond  = 10
	observationBurst       = 20
	observationRateEntries = 4096
	observationRateTTL     = time.Minute
)

type observationRateEntry struct {
	key         string
	tokens      float64
	at, expires time.Time
}
type observationLimiter struct {
	mu      sync.Mutex
	order   *list.List
	entries map[string]*list.Element
}

func newObservationLimiter() *observationLimiter {
	return &observationLimiter{order: list.New(), entries: make(map[string]*list.Element)}
}

// Applied only after the API's existing authentication and device binding.
// A playing device observes once a second; the headroom covers a handover or a
// second session on the same device while bounding what one device can send.
// Profiles, token rotation and session IDs do not mint new buckets.
func (l *observationLimiter) allow(c v1Caller, now time.Time) bool {
	if c.DeviceID == "" {
		return false
	}
	raw, _ := json.Marshal([]string{c.ServerID, c.Authority, c.AccountID, c.DeviceID})
	sum := sha256.Sum256(raw)
	key := hex.EncodeToString(sum[:])
	l.mu.Lock()
	defer l.mu.Unlock()
	element, found := l.entries[key]
	value := observationRateEntry{key: key, tokens: observationBurst, at: now}
	if found {
		old := element.Value.(observationRateEntry)
		if now.Before(old.expires) {
			value = old
			if now.After(value.at) {
				value.tokens += now.Sub(value.at).Seconds() * observationsPerSecond
				if value.tokens > observationBurst {
					value.tokens = observationBurst
				}
				value.at = now
			}
		}
	}
	allowed := value.tokens >= 1
	if allowed {
		value.tokens--
	}
	value.expires = now.Add(observationRateTTL)
	if found {
		element.Value = value
		l.order.MoveToFront(element)
	} else {
		l.entries[key] = l.order.PushFront(value)
	}
	for l.order.Len() > observationRateEntries {
		old := l.order.Back()
		delete(l.entries, old.Value.(observationRateEntry).key)
		l.order.Remove(old)
	}
	return allowed
}
