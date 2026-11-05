// Package enrich prepares bounded, immutable inputs for the schema-v1 writer.
// It is not connected to the shipped server's runtime paths.
package enrich

import (
	"container/list"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
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
	Suspended  bool // no automatic retry, including across job restart
}

func (e *ProviderError) Error() string {
	if e.Suspended {
		return "enrichment provider automatic retry suspended"
	}
	return "enrichment provider request rejected"
}

// ParseRetryAfter preserves every representable provider floor. Positive numeric
// overflow saturates to maxDuration, which suspends automatic retry rather than
// replacing an unrepresentable delay with a short retry. Malformed/past values
// alone use the one-second fallback. HTTP dates are converted once at receipt.
func ParseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	decimal := value != ""
	for _, r := range value {
		if r < '0' || r > '9' {
			decimal = false
			break
		}
	}
	if decimal {
		seconds, err := strconv.ParseUint(value, 10, 64)
		if err != nil || seconds > uint64(maxDuration/time.Second) {
			return maxDuration
		}
		return retryFloor(time.Duration(seconds) * time.Second)
	}
	if deadline, err := http.ParseTime(value); err == nil {
		return retryFloor(deadline.Sub(now))
	}
	return time.Second
}
func retryFloor(delay time.Duration) time.Duration {
	if delay < time.Second {
		return time.Second
	}
	return delay
}

func (k RequestKey) owned() RequestKey {
	return RequestKey{strings.Clone(k.Provider), strings.Clone(k.Kind), strings.Clone(k.ExternalID), strings.Clone(k.Locale), strings.Clone(k.Shape), strings.Clone(k.Authority)}
}
func (k RequestKey) bytes() int64 {
	return int64(len(k.Provider) + len(k.Kind) + len(k.ExternalID) + len(k.Locale) + len(k.Shape) + len(k.Authority))
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
// document requests; Attempts counts acknowledged dispatches, including retries made
// by durable jobs. Divide Attempts by accepted items in the harness, not here.
type TransportStats struct {
	Requests, Attempts, Reused, Deduplicated, Refused, Failures, Bytes uint64
	Pending, Waiters, InFlight, PeakInFlight                           int
	CacheBytes                                                         int64
	KeyBytes                                                           int64 // owned key string payloads (config, pending and cached); body bytes separate
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
	expires time.Duration
}

// Transport has no SQL handle, durable job lease or sleeping goroutine at idle.
// Retries belong to durable jobs; this broker never retries behind their backs.
type Transport struct {
	mu             sync.Mutex
	cfg            TransportConfig
	admission      NetworkAdmission
	fetch          Fetch
	clock          transportClock
	suspended      bool
	cooldownStatus int
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	closed         bool
	flights        map[RequestKey]*fetchCall
	cache          map[RequestKey]*list.Element
	lru            *list.List
	stats          TransportStats
	next           time.Duration
	pace           chan struct{}
	slots          chan struct{}
}

func NewTransport(cfg TransportConfig, admission NetworkAdmission, fetch Fetch) (*Transport, error) {
	return newTransport(cfg, admission, fetch, realTransportClock{origin: time.Now()})
}
func newTransport(cfg TransportConfig, admission NetworkAdmission, fetch Fetch, clock transportClock) (*Transport, error) {
	if cfg.Provider == "" || len(cfg.Provider) > 1024 || cfg.RequestsPerSecond < 1 || cfg.RequestsPerSecond > 40 ||
		cfg.MaxInFlight < 1 || cfg.MaxInFlight > 64 || cfg.MaxPending < cfg.MaxInFlight || cfg.MaxPending > 256 ||
		cfg.MaxWaiters < cfg.MaxPending || cfg.MaxWaiters > 1024 || cfg.MaxResponseBytes < 1 || cfg.MaxResponseBytes > 4<<20 ||
		int64(cfg.MaxPending)*cfg.MaxResponseBytes > 64<<20 ||
		cfg.CacheBytes < 0 || cfg.CacheBytes > 64<<20 || cfg.CacheEntries < 0 || cfg.CacheEntries > 4096 ||
		cfg.CacheTTL < 0 || cfg.CacheTTL > 24*time.Hour || cfg.RequestTimeout <= 0 || cfg.RequestTimeout > time.Minute || admission == nil || fetch == nil || clock == nil {
		return nil, errors.New("invalid enrichment transport configuration")
	}
	cfg.Provider = strings.Clone(cfg.Provider)
	ctx, cancel := context.WithCancel(context.Background())
	return &Transport{cfg: cfg, admission: admission, fetch: fetch, clock: clock, stats: TransportStats{KeyBytes: int64(len(cfg.Provider))}, ctx: ctx, cancel: cancel,
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
	key = key.owned()
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
		if t.clock.Now() < v.expires {
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
		if t.suspended {
			err := t.cooldownErrorLocked()
			t.mu.Unlock()
			return nil, err
		}
		if len(t.flights) >= t.cfg.MaxPending {
			t.stats.Refused++
			t.mu.Unlock()
			return nil, ErrBusy
		}
		workCtx, cancel := context.WithTimeout(t.ctx, t.cfg.RequestTimeout)
		c = &fetchCall{done: make(chan struct{}), cancel: cancel}
		t.flights[key] = c
		t.stats.KeyBytes += key.bytes()
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
	t.stats.KeyBytes -= key.bytes()
	close(c.done)
}

// cooldownErrorLocked exposes the remaining provider floor without a shorter
// automatic retry horizon. Long/overflow floors latch suspension until the
// durable provider/job owner explicitly authorizes another broker lifecycle.
func (t *Transport) cooldownErrorLocked() *ProviderError {
	return &ProviderError{Status: t.cooldownStatus, RetryAfter: max(time.Duration(0), t.next-t.clock.Now()), Suspended: t.suspended}
}
func (t *Transport) acquire(ctx context.Context, key RequestKey) ([]byte, error) {
	select {
	case t.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-t.slots }()
	select {
	case t.pace <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// State and release are guarded by mu, including acknowledgment from an HTTP
	// write callback on another goroutine. A return without ack stays conservative.
	released, finished, checked, started := false, false, false, false
	var dispatchErr error
	releaseLocked := func() {
		if !released {
			<-t.pace
			released = true
		}
	}
	defer func() {
		t.mu.Lock()
		finished = true
		if !started {
			t.next = max(t.next, later(t.clock.Now(), (time.Second+time.Duration(t.cfg.RequestsPerSecond)-1)/time.Duration(t.cfg.RequestsPerSecond)))
		}
		releaseLocked()
		t.mu.Unlock()
	}()
	var retire func()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t.mu.Lock()
		if t.suspended {
			err := t.cooldownErrorLocked()
			t.mu.Unlock()
			return nil, err
		}
		delay := t.next - t.clock.Now()
		t.mu.Unlock()
		if delay > 0 {
			if err := t.clock.Wait(ctx, delay); err != nil {
				return nil, err
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
		t.mu.Lock()
		stale := t.suspended || t.clock.Now() < t.next
		t.mu.Unlock()
		if stale {
			retire()
			continue
		}
		break
	}
	defer retire()
	dispatch := Dispatch{
		check: func() error {
			t.mu.Lock()
			defer t.mu.Unlock()
			if finished || started {
				if !finished {
					dispatchErr = ErrDispatch
				}
				return ErrDispatch
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if t.suspended || t.clock.Now() < t.next {
				return t.cooldownErrorLocked()
			}
			checked = true
			return nil
		},
		started: func() error {
			t.mu.Lock()
			defer t.mu.Unlock()
			if finished || started || !checked {
				if !finished {
					dispatchErr = ErrDispatch
				}
				return ErrDispatch
			}
			started = true
			// Acknowledgment follows the actual send. Extend from this fresh monotonic
			// instant, never from an overdue reservation or the provider's wall clock.
			var boundaryErr error
			if err := ctx.Err(); err != nil {
				boundaryErr = err
			} else if t.suspended || t.clock.Now() < t.next {
				boundaryErr = t.cooldownErrorLocked()
			}
			t.next = max(t.next, later(t.clock.Now(), (time.Second+time.Duration(t.cfg.RequestsPerSecond)-1)/time.Duration(t.cfg.RequestsPerSecond)))
			t.stats.Attempts++
			t.stats.InFlight++
			if t.stats.InFlight > t.stats.PeakInFlight {
				t.stats.PeakInFlight = t.stats.InFlight
			}
			dispatchErr = boundaryErr
			releaseLocked()
			return boundaryErr
		},
	}
	if err := dispatch.Check(); err != nil {
		return nil, err
	}
	r, err := t.fetch(ctx, key, t.cfg.MaxResponseBytes, dispatch)
	t.mu.Lock()
	finished = true
	if started {
		t.stats.InFlight--
	} else if err == nil {
		err = ErrDispatch
	}
	if err == nil && dispatchErr != nil {
		err = dispatchErr
	}
	if err == nil && (r.Status == 429 || r.Status >= 500) {
		delay := retryFloor(r.RetryAfter)
		now := t.clock.Now()
		t.next = max(t.next, later(now, delay))
		t.cooldownStatus = r.Status
		t.suspended = t.suspended || delay > automaticRetryHorizon
		// Every durable-job error carries the bucket's strongest remaining
		// floor, including a longer cooldown from another in-flight response.
		err = &ProviderError{Status: r.Status, RetryAfter: max(delay, t.next-now), Suspended: t.suspended}
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
	t.stats.KeyBytes -= v.key.bytes()
}

func (t *Transport) remember(key RequestKey, body []byte) {
	if t.cfg.CacheTTL == 0 || t.cfg.CacheEntries == 0 || int64(len(body)) > t.cfg.CacheBytes {
		return
	}
	for t.lru.Len() >= t.cfg.CacheEntries || t.stats.CacheBytes+int64(len(body)) > t.cfg.CacheBytes {
		t.evict(t.lru.Back())
	}
	e := t.lru.PushFront(cachedResponse{key: key, body: body, expires: later(t.clock.Now(), t.cfg.CacheTTL)})
	t.cache[key] = e
	t.stats.CacheBytes += int64(len(body))
	t.stats.KeyBytes += key.bytes()
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
	t.stats.KeyBytes = int64(len(t.cfg.Provider))
	t.mu.Unlock()
}
