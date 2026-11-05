// Package enrich prepares bounded, immutable inputs for the schema-v1 writer.
// It is not connected to the shipped server's runtime paths.
package enrich

import (
	"container/list"
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"
)

var (
	ErrBusy     = errors.New("enrichment transport capacity unavailable")
	ErrClosed   = errors.New("enrichment transport closed")
	ErrResponse = errors.New("enrichment response exceeds byte budget")
)

// RequestKey identifies a reusable provider document, never a local asset/version.
// Shape includes endpoint, append fields, filters and conditional request headers.
// Authority is an opaque non-secret credential/policy generation, not a token.
// One Transport belongs to one provider credential/rate bucket.
type RequestKey struct {
	Provider, Kind, ExternalID, Locale, Shape, Authority string
}

func (k RequestKey) valid() bool {
	for _, s := range []string{k.Provider, k.Kind, k.ExternalID, k.Shape, k.Authority} {
		if len(s) == 0 || len(s) > 1024 {
			return false
		}
	}
	return len(k.Locale) <= 128
}

// Response is an ephemeral acquisition result, not the persisted evidence model.
// Fetch must consume AND close the HTTP body before returning. Errors are never
// cached. RetryAfter on 429/5xx defers the entire bucket, not just one subject.
type Response struct {
	Body       []byte
	Status     int
	RetryAfter time.Duration
}

type ProviderError struct {
	Status     int
	RetryAfter time.Duration
}

func (e *ProviderError) Error() string { return "enrichment provider request rejected" }

// ParseRetryAfter accepts both provider delta-seconds and HTTP dates. Missing,
// malformed, past and extreme values receive a bounded, conservative delay.
func ParseRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds > 86400 {
			return 24 * time.Hour
		}
		if seconds < 1 {
			return time.Second
		}
		return time.Duration(seconds) * time.Second
	}
	if deadline, err := http.ParseTime(value); err == nil {
		return boundedRetry(deadline.Sub(now))
	}
	return time.Second
}

func boundedRetry(delay time.Duration) time.Duration {
	if delay < time.Second {
		return time.Second
	}
	if delay > 24*time.Hour {
		return 24 * time.Hour
	}
	return delay
}

// NetworkAdmission is the GV bridge. Try must refuse immediately and reserve
// only NetworkIO and bounded byte/memory custody, with no writer/CPU grant.
// Its returned retire function runs after Fetch has physically closed the body.
type NetworkAdmission interface {
	Try(context.Context, int64) (retire func(), err error)
}

type TransportConfig struct {
	Provider          string
	RequestsPerSecond int // 1..40; TMDB qualification targets 32..40
	MaxInFlight       int
	MaxPending        int // includes pacing and in-flight requests; no unbounded queue
	MaxWaiters        int // all callers, including coalesced requests
	MaxResponseBytes  int64
	CacheBytes        int64
	CacheEntries      int
	CacheTTL          time.Duration
	RequestTimeout    time.Duration
}

// TransportStats are cumulative, bucket-local counters. Requests counts logical
// document requests; Attempts counts actual Fetch calls, including retries made
// by durable jobs. Divide Attempts by accepted items in the harness, not here.
type TransportStats struct {
	Requests, Attempts, Reused, Deduplicated, Refused, Failures, Bytes uint64
	Pending, Waiters, InFlight, PeakInFlight                           int
	CacheBytes                                                         int64
}

type fetchCall struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	body    []byte
	err     error
}
type cachedResponse struct {
	key     RequestKey
	body    []byte
	expires time.Time
}

// Transport has no SQL handle, durable job lease or sleeping goroutine at idle.
// Retries belong to durable jobs; this broker never retries behind their backs.
type Transport struct {
	mu        sync.Mutex
	cfg       TransportConfig
	admission NetworkAdmission
	fetch     func(context.Context, RequestKey, int64) (Response, error)
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closed    bool
	flights   map[RequestKey]*fetchCall
	cache     map[RequestKey]*list.Element
	lru       *list.List
	stats     TransportStats
	next      time.Time
	pace      chan struct{}
	slots     chan struct{}
}

func NewTransport(cfg TransportConfig, admission NetworkAdmission, fetch func(context.Context, RequestKey, int64) (Response, error)) (*Transport, error) {
	if cfg.Provider == "" || len(cfg.Provider) > 1024 || cfg.RequestsPerSecond < 1 || cfg.RequestsPerSecond > 40 ||
		cfg.MaxInFlight < 1 || cfg.MaxInFlight > 64 || cfg.MaxPending < cfg.MaxInFlight || cfg.MaxPending > 256 ||
		cfg.MaxWaiters < cfg.MaxPending || cfg.MaxWaiters > 1024 || cfg.MaxResponseBytes < 1 || cfg.MaxResponseBytes > 4<<20 ||
		int64(cfg.MaxPending)*cfg.MaxResponseBytes > 64<<20 ||
		cfg.CacheBytes < 0 || cfg.CacheBytes > 64<<20 || cfg.CacheEntries < 0 || cfg.CacheEntries > 4096 ||
		cfg.CacheTTL < 0 || cfg.CacheTTL > 24*time.Hour || cfg.RequestTimeout <= 0 || cfg.RequestTimeout > time.Minute || admission == nil || fetch == nil {
		return nil, errors.New("invalid enrichment transport configuration")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Transport{cfg: cfg, admission: admission, fetch: fetch, ctx: ctx, cancel: cancel,
		flights: make(map[RequestKey]*fetchCall), cache: make(map[RequestKey]*list.Element), lru: list.New(),
		pace: make(chan struct{}, 1), slots: make(chan struct{}, cfg.MaxInFlight)}, nil
}

// Get joins a shared request or returns a private copy of a reusable response.
// Canceling one caller does not cancel other callers. The last departing caller
// cancels the acquisition; capacity stays charged until Fetch physically returns.
func (t *Transport) Get(ctx context.Context, key RequestKey) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !key.valid() || key.Provider != t.cfg.Provider {
		return nil, errors.New("invalid enrichment request key")
	}
	t.mu.Lock()
	t.stats.Requests++
	if t.closed {
		t.mu.Unlock()
		return nil, ErrClosed
	}
	if t.stats.Waiters >= t.cfg.MaxWaiters {
		t.stats.Refused++
		t.mu.Unlock()
		return nil, ErrBusy
	}
	if elem := t.cache[key]; elem != nil {
		v := elem.Value.(cachedResponse)
		if time.Now().Before(v.expires) {
			t.lru.MoveToFront(elem)
			t.stats.Reused++
			body := append([]byte(nil), v.body...)
			t.mu.Unlock()
			return body, nil
		}
		t.evict(elem)
	}
	c := t.flights[key]
	if c != nil {
		// A canceled request retains physical custody but cannot gain new waiters.
		if c.waiters == 0 {
			t.stats.Refused++
			t.mu.Unlock()
			return nil, ErrBusy
		}
		t.stats.Deduplicated++
	} else {
		if len(t.flights) >= t.cfg.MaxPending {
			t.stats.Refused++
			t.mu.Unlock()
			return nil, ErrBusy
		}
		workCtx, cancel := context.WithTimeout(t.ctx, t.cfg.RequestTimeout)
		c = &fetchCall{done: make(chan struct{}), cancel: cancel}
		t.flights[key] = c
		t.wg.Add(1)
		go t.run(workCtx, key, c)
	}
	c.waiters++
	t.stats.Waiters++
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		c.waiters--
		t.stats.Waiters--
		if c.waiters == 0 {
			c.cancel()
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return append([]byte(nil), c.body...), c.err
	}
}

func (t *Transport) run(ctx context.Context, key RequestKey, c *fetchCall) {
	defer t.wg.Done()
	defer c.cancel()
	body, err := t.acquire(ctx, key)
	t.mu.Lock()
	defer t.mu.Unlock()
	if err != nil {
		t.stats.Failures++
		if errors.Is(err, ErrBusy) {
			t.stats.Refused++
		}
	}
	if err == nil && !t.closed && c.waiters > 0 {
		t.remember(key, body)
	}
	c.body, c.err = body, err
	delete(t.flights, key)
	close(c.done)
}

func (t *Transport) acquire(ctx context.Context, key RequestKey) ([]byte, error) {
	select {
	case t.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-t.slots }()
	// Serialize rate admission through the actual Fetch start. Reserving future
	// slots in advance would allow overdue workers to burst after a slow grant.
	select {
	case t.pace <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	deferPace := true
	defer func() {
		if deferPace {
			<-t.pace
		}
	}()
	var retire func()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t.mu.Lock()
		delay := time.Until(t.next)
		t.mu.Unlock()
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			}
			continue
		}
		var err error
		retire, err = t.admission.Try(ctx, t.cfg.MaxResponseBytes)
		if err != nil {
			return nil, err
		}
		if retire == nil {
			return nil, errors.New("enrichment admission returned no retirement capability")
		}
		// A concurrent response may impose Retry-After during Try. Return the
		// unused network grant before sleeping and re-admit after the cooldown.
		t.mu.Lock()
		if time.Now().Before(t.next) {
			t.mu.Unlock()
			retire()
			continue
		}
		if err := ctx.Err(); err != nil {
			t.mu.Unlock()
			retire()
			return nil, err
		}
		t.next = time.Now().Add((time.Second + time.Duration(t.cfg.RequestsPerSecond) - 1) / time.Duration(t.cfg.RequestsPerSecond))
		t.stats.Attempts++
		t.stats.InFlight++
		if t.stats.InFlight > t.stats.PeakInFlight {
			t.stats.PeakInFlight = t.stats.InFlight
		}
		t.mu.Unlock()
		break
	}
	defer retire()
	<-t.pace
	deferPace = false
	r, err := t.fetch(ctx, key, t.cfg.MaxResponseBytes)
	t.mu.Lock()
	t.stats.InFlight--
	if err == nil && (r.Status == 429 || r.Status >= 500) {
		delay := boundedRetry(r.RetryAfter)
		if next := time.Now().Add(delay); next.After(t.next) {
			t.next = next
		}
		err = &ProviderError{Status: r.Status, RetryAfter: delay}
	} else if err == nil && r.Status != 200 {
		err = &ProviderError{Status: r.Status}
	}
	if err == nil && int64(len(r.Body)) > t.cfg.MaxResponseBytes {
		err = ErrResponse
	}
	if err == nil {
		t.stats.Bytes += uint64(len(r.Body))
	}
	t.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]byte(nil), r.Body...), nil
}

func (t *Transport) evict(e *list.Element) {
	v := e.Value.(cachedResponse)
	delete(t.cache, v.key)
	t.lru.Remove(e)
	t.stats.CacheBytes -= int64(len(v.body))
}

func (t *Transport) remember(key RequestKey, body []byte) {
	if t.cfg.CacheTTL == 0 || t.cfg.CacheEntries == 0 || int64(len(body)) > t.cfg.CacheBytes {
		return
	}
	for t.lru.Len() >= t.cfg.CacheEntries || t.stats.CacheBytes+int64(len(body)) > t.cfg.CacheBytes {
		t.evict(t.lru.Back())
	}
	e := t.lru.PushFront(cachedResponse{key: key, body: body, expires: time.Now().Add(t.cfg.CacheTTL)})
	t.cache[key] = e
	t.stats.CacheBytes += int64(len(body))
}

func (t *Transport) Stats() TransportStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.stats
	s.Pending = len(t.flights)
	return s
}

// Close cancels and joins every acquisition before returning. A Fetch adapter
// must honor cancellation and its bounded body deadline; no early IO retirement.
func (t *Transport) Close() {
	t.mu.Lock()
	t.closed = true
	t.cancel()
	t.mu.Unlock()
	t.wg.Wait()
	t.mu.Lock()
	t.cache = make(map[RequestKey]*list.Element)
	t.lru.Init()
	t.stats.CacheBytes = 0
	t.mu.Unlock()
}
