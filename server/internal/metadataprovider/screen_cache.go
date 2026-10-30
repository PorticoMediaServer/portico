package metadataprovider

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Small equivalent-request single-flight/cache. Immutable provider evidence is
// persisted by the caller. Only provider routes/queries appear in keys, never filesystem paths or credentials.
type screenCacheEntry struct {
	done chan struct{}
	data []byte
	err  error
	at   time.Time
}
type screenCache struct {
	mu      sync.Mutex
	entries map[string]*screenCacheEntry
	bytes   int
}

func (c *screenCache) get(ctx context.Context, key string, out any, fetch func() (any, error)) error {
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]*screenCacheEntry{}
	}
	if e := c.entries[key]; e != nil && (e.at.IsZero() || time.Since(e.at) < 5*time.Minute) {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.done:
		}
		if e.err != nil {
			return e.err
		}
		return json.Unmarshal(e.data, out)
	}
	// Evict oldest completed entries; never remove an in-flight call.
	for len(c.entries) >= 64 || c.bytes > 8<<20 {
		oldest := ""
		var at time.Time
		for k, e := range c.entries {
			if !e.at.IsZero() && (oldest == "" || e.at.Before(at)) {
				oldest = k
				at = e.at
			}
		}
		if oldest == "" {
			c.mu.Unlock()
			return &Error{Code: "request_capacity", Status: 503}
		}
		c.bytes -= len(c.entries[oldest].data)
		delete(c.entries, oldest)
	}
	e := &screenCacheEntry{done: make(chan struct{})}
	if prior := c.entries[key]; prior != nil {
		c.bytes -= len(prior.data)
	}
	c.entries[key] = e
	c.mu.Unlock()
	value, err := fetch()
	var raw []byte
	if err == nil {
		raw, err = json.Marshal(value)
		if len(raw) > 4<<20 {
			err = &Error{Code: "response_too_large"}
			raw = nil
		}
	}
	c.mu.Lock()
	e.data = raw
	e.err = err
	e.at = time.Now()
	if c.entries[key] == e {
		c.bytes += len(raw)
	}
	// The cache remains hard-bounded after insertion as well as before fetching.
	for c.bytes > 8<<20 {
		oldest := ""
		var at time.Time
		for k, v := range c.entries {
			if k != key && !v.at.IsZero() && (oldest == "" || v.at.Before(at)) {
				oldest = k
				at = v.at
			}
		}
		if oldest == "" {
			break
		}
		c.bytes -= len(c.entries[oldest].data)
		delete(c.entries, oldest)
	}
	if err != nil && c.entries[key] == e {
		delete(c.entries, key)
		c.bytes -= len(raw)
	}
	close(e.done)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (c *screenCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]*screenCacheEntry{}
	c.bytes = 0
}

type freshRequestKey struct{}

func freshRequest(ctx context.Context) context.Context {
	return context.WithValue(ctx, freshRequestKey{}, true)
}
func isFreshRequest(ctx context.Context) bool {
	fresh, _ := ctx.Value(freshRequestKey{}).(bool)
	return fresh
}
