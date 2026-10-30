package linearinput

import (
	"context"
	"errors"
	"time"
)

type cachedResource struct {
	ready                  chan struct{}
	data                   []byte
	location, contentRange string
	expires                time.Time
	err                    error
}

// Acquisition is shared by probe, viewer and capture readers. Retention is
// bounded in both bytes and entries; each reader still has a private route.
func (g *Gateway) resourceBytes(ctx context.Context, res resource, rng string) (*cachedResource, error) {
	key := res.kind + "\n" + res.url + "\n" + rng
	g.mu.Lock()
	if g.done && errors.Is(g.failure, ErrRetryWindow) {
		g.mu.Unlock()
		return nil, ErrRetryWindow
	}
	if g.closing || g.ctx.Err() != nil {
		g.mu.Unlock()
		return nil, ErrUnavailable
	}
	now := time.Now()
	for k, v := range g.cache {
		if !v.expires.IsZero() && !v.expires.After(now) {
			g.cacheBytes -= int64(len(v.data))
			delete(g.cache, k)
		}
	}
	entry := g.cache[key]
	if entry != nil {
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-entry.ready:
			return entry, entry.err
		}
	}
	if len(g.cache) >= 512 {
		g.mu.Unlock()
		return nil, ErrUnavailable
	}
	entry = &cachedResource{ready: make(chan struct{})}
	g.cache[key] = entry
	g.mu.Unlock()
	select {
	case g.resourceSlots <- struct{}{}:
		defer func() { <-g.resourceSlots }()
	case <-g.ctx.Done():
		g.mu.Lock()
		entry.err = ErrUnavailable
		delete(g.cache, key)
		close(entry.ready)
		g.mu.Unlock()
		return nil, ErrUnavailable
	}
	var e error
	entry.data, entry.location, entry.contentRange, e = g.resourceData(res, rng)
	g.mu.Lock()
	if g.done && errors.Is(g.failure, ErrRetryWindow) {
		e = ErrRetryWindow
	}
	if !errors.Is(e, ErrRetryWindow) && (g.closing || g.ctx.Err() != nil) {
		e = ErrUnavailable
	}
	if errors.Is(e, ErrRetryWindow) {
		g.done, g.span.Ended, g.span.Reconnecting, g.failure = true, true, false, ErrRetryWindow
		g.signal()
	}
	for e == nil && g.cacheBytes+int64(len(entry.data)) > maxSegment {
		oldest := ""
		var until time.Time
		for key, value := range g.cache {
			if value == entry || value.expires.IsZero() {
				continue
			}
			if oldest == "" || value.expires.Before(until) {
				oldest, until = key, value.expires
			}
		}
		if oldest == "" {
			e = ErrUnavailable
			break
		}
		g.cacheBytes -= int64(len(g.cache[oldest].data))
		delete(g.cache, oldest)
	}
	if e != nil {
		entry.data = nil
		delete(g.cache, key)
	} else {
		g.cacheBytes += int64(len(entry.data))
	}
	entry.err = e
	ttl := time.Minute
	if res.kind == "manifest" {
		ttl = 500 * time.Millisecond
	}
	entry.expires = time.Now().Add(ttl)
	close(entry.ready)
	g.mu.Unlock()
	if errors.Is(e, ErrRetryWindow) {
		g.cancel()
	}
	return entry, e
}
