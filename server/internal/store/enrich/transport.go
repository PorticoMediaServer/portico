// Package enrich prepares bounded, immutable inputs for the schema-v1 writer.
// It is not connected to the shipped server's runtime paths.
package enrich

import (
	"container/list"
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

var (
	ErrBusy        = errors.New("enrichment transport capacity unavailable")
	ErrClosed      = errors.New("enrichment transport closed")
	ErrResponse    = errors.New("enrichment response exceeds byte budget")
	ErrWorkerPanic = errors.New("enrichment acquisition worker failed")
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
	Metadata   AcquisitionMetadata
}

// ProviderError carries the strongest retry floor alongside the original cause.
// Durable callers inspect it with errors.As on every Get error, and use
// errors.Is/Unwrap to distinguish cancellation, capacity and adapter failures.
type ProviderError struct {
	Status     int
	RetryAfter time.Duration
	Suspended  bool // no automatic retry, including across job restart
	cause      error
}

func (e *ProviderError) Error() string {
	if e != nil && e.Suspended {
		return "enrichment provider automatic retry suspended"
	}
	return "enrichment provider request rejected"
}

// Unwrap preserves local causes while ProviderError carries retry guidance.
func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
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
	MaxMetadataBytes  int64 // explicit allocation budget; zero disables typed acquisition (no production default)
	CacheBytes        int64 // conservative body+acquisition-metadata allocation bound; entry/key overhead is separate
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
	CacheBytes                                                         int64 // conservatively charged backing allocations, not payload len/cap
	CacheMetadataBytes                                                 int64 // subset of CacheBytes, not an additional pool
	KeyBytes                                                           int64 // owned key string payloads (config, pending and cached); body allocations separate
}

type fetchCall struct {
	done           chan struct{}
	cancel         context.CancelFunc
	waiters        int
	body           []byte
	metadata       AcquisitionMetadata
	metadataCharge int64
	err            error
}
type cachedResponse struct {
	key            RequestKey
	body           []byte
	metadata       AcquisitionMetadata
	metadataCharge int64
	expires        time.Duration
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
		cfg.MaxMetadataBytes < 0 || cfg.MaxMetadataBytes > 64<<20 ||
		int64(cfg.MaxPending)*(cfg.MaxResponseBytes+cfg.MaxMetadataBytes) > 64<<20 ||
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
func (t *Transport) Get(ctx context.Context, key RequestKey) (body []byte, err error) {
	// Apply the same public guidance to every error exit, including cancellation,
	// capacity refusal and errors selected after the provider response is handled.
	defer func() { err = t.withRetryFloor(err) }()
	document, err := t.get(ctx, key)
	return document.body, err
}

// GetDocument is the closed FIRST typed acquisition. Required original metadata
// must have an explicit trusted resource budget; missing policy refuses before
// dispatch. Generic body-only Get remains available for its existing callers.
func (t *Transport) GetDocument(ctx context.Context, key RequestKey) (AcquisitionDocument, error) {
	return t.getDocument(ctx, key, false)
}

// The governed producer cannot reuse its preparation slot until acquisition
// physically exits, including when its caller cancels. Ordinary GetDocument
// keeps its existing early-cancellation semantics.
func (t *Transport) getDocumentJoined(ctx context.Context, key RequestKey) (AcquisitionDocument, error) {
	return t.getDocument(ctx, key, true)
}

func (t *Transport) getDocument(ctx context.Context, key RequestKey, joinPhysical bool) (document AcquisitionDocument, err error) {
	defer func() {
		if ctx != nil {
			if cancelled := ctx.Err(); cancelled != nil {
				// Preserve the strongest direct provider guidance while adding
				// cancellation; a generic join must not hide it from the caller.
				if provider, ok := err.(*ProviderError); ok && provider != nil {
					copy := *provider
					copy.cause = errors.Join(err, cancelled)
					err = &copy
				} else {
					err = errors.Join(err, cancelled)
				}
			}
		}
		// Include a cancellation first observed at this return boundary, even
		// after a successful cache hit, before attaching the provider floor.
		err = t.withRetryFloor(err)
		if err != nil {
			document = AcquisitionDocument{}
		}
	}()
	if t.cfg.MaxMetadataBytes <= 0 {
		return AcquisitionDocument{}, ErrTMDBPolicy
	}
	if key.Provider != "tmdb" || key.Kind != "movie" || key.Shape != TMDBMovieRequestShape {
		return AcquisitionDocument{}, ErrAcquisitionMetadata
	}
	id, parseErr := strconv.ParseInt(key.ExternalID, 10, 64)
	if parseErr != nil || id <= 0 || strconv.FormatInt(id, 10) != key.ExternalID || key.Locale == "" || !utf8.ValidString(key.Locale) || strings.IndexByte(key.Locale, 0) >= 0 {
		return AcquisitionDocument{}, ErrAcquisitionMetadata
	}
	document, err = t.getJoined(ctx, key, joinPhysical)
	return document, err
}

func (t *Transport) get(ctx context.Context, key RequestKey) (AcquisitionDocument, error) {
	return t.getJoined(ctx, key, false)
}

func (t *Transport) getJoined(ctx context.Context, key RequestKey, joinPhysical bool) (AcquisitionDocument, error) {
	if ctx == nil {
		return AcquisitionDocument{}, errors.New("invalid enrichment request context")
	}
	if err := ctx.Err(); err != nil {
		return AcquisitionDocument{}, err
	}
	if !key.valid() || key.Provider != t.cfg.Provider {
		return AcquisitionDocument{}, errors.New("invalid enrichment request key")
	}
	key = key.owned()
	document, c, err := t.join(key, t.clock.Now())
	if err != nil || c == nil {
		return document, err
	}
	defer func() {
		t.mu.Lock()
		c.waiters--
		t.stats.Waiters--
		if c.waiters == 0 {
			c.cancel()
		}
		t.mu.Unlock()
		if joinPhysical {
			<-c.done
		}
	}()
	select {
	case <-ctx.Done():
		return AcquisitionDocument{}, ctx.Err()
	case <-c.done:
		if err := ctx.Err(); err != nil {
			return AcquisitionDocument{}, err
		}
		if c.err != nil {
			return AcquisitionDocument{}, c.err
		}
		return cloneAcquisitionDocument(key, c.body, c.metadata, c.metadataCharge), nil
	}
}

func (t *Transport) join(key RequestKey, now time.Duration) (AcquisitionDocument, *fetchCall, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stats.Requests++
	if t.closed {
		return AcquisitionDocument{}, nil, ErrClosed
	}
	if t.stats.Waiters >= t.cfg.MaxWaiters {
		t.stats.Refused++
		return AcquisitionDocument{}, nil, ErrBusy
	}
	if elem := t.cache[key]; elem != nil {
		v := elem.Value.(cachedResponse)
		if now < v.expires {
			t.lru.MoveToFront(elem)
			t.stats.Reused++
			return cloneAcquisitionDocument(key, v.body, v.metadata, v.metadataCharge), nil, nil
		}
		t.evict(elem)
	}
	c := t.flights[key]
	if c != nil {
		// A canceled request retains physical custody but cannot gain new waiters.
		if c.waiters == 0 {
			t.stats.Refused++
			return AcquisitionDocument{}, nil, ErrBusy
		}
		t.stats.Deduplicated++
	} else {
		if t.suspended {
			return AcquisitionDocument{}, nil, t.cooldownErrorLocked(now)
		}
		if len(t.flights) >= t.cfg.MaxPending {
			t.stats.Refused++
			return AcquisitionDocument{}, nil, ErrBusy
		}
		workCtx, cancel := context.WithTimeout(t.ctx, t.cfg.RequestTimeout)
		c = &fetchCall{done: make(chan struct{}), cancel: cancel}
		t.flights[key] = c
		t.stats.KeyBytes += key.bytes()
		t.wg.Add(1)
		supervise.Go("store.enrich.acquire", func() { t.run(workCtx, key, c) })
	}
	c.waiters++
	t.stats.Waiters++
	return AcquisitionDocument{}, c, nil
}

func (t *Transport) run(ctx context.Context, key RequestKey, c *fetchCall) {
	defer t.wg.Done()
	defer c.cancel()
	var document AcquisitionDocument
	var err error
	var refused bool
	var expires time.Duration
	completed := false
	defer func() {
		failure := recover()
		if !completed {
			document = AcquisitionDocument{}
			err = ErrWorkerPanic
		}
		func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			if !completed {
				t.suspended = true
			}
			if err != nil {
				t.stats.Failures++
				if refused {
					t.stats.Refused++
				}
			}
			if err == nil && !t.closed && c.waiters > 0 {
				t.remember(key, document.body, document.metadata, document.metadataCharge, expires)
			}
			c.body, c.metadata, c.metadataCharge, c.err = document.body, document.metadata, document.metadataCharge, err
			delete(t.flights, key)
			t.stats.KeyBytes -= key.bytes()
			close(c.done)
		}()
		// The shared supervisor logs/counts the panic after every waiter and custody
		// counter has a terminal outcome. Never restart in-memory acquisition state.
		if failure != nil {
			panic(failure)
		}
	}()
	document, err = t.acquire(ctx, key)
	// Error hooks are adapter-defined code. Run them inside worker containment,
	// before terminal finalization, with no mutex held. A typed nil is not guidance.
	var provider *ProviderError
	if errors.As(err, &provider) && provider != nil {
		copy := *provider
		copy.cause = err
		err = &copy
	}
	refused = errors.Is(err, ErrBusy)
	if err == nil {
		expires = later(t.clock.Now(), t.cfg.CacheTTL)
	}
	completed = true
}

// cooldownErrorLocked exposes the remaining provider floor without a shorter
// automatic retry horizon. Long/overflow floors latch suspension until the
// durable provider/job owner explicitly authorizes another broker lifecycle.
func (t *Transport) cooldownErrorLocked(now time.Duration) *ProviderError {
	return &ProviderError{Status: t.cooldownStatus, RetryAfter: max(time.Duration(0), t.next-now), Suspended: t.suspended}
}

// withRetryFloor never invokes error hooks. The returned snapshot wraps the
// original cause, so durable callers can use both errors.Is and errors.As.
func (t *Transport) withRetryFloor(err error) error {
	if err == nil {
		return nil
	}
	var guidance ProviderError
	if provider, ok := err.(*ProviderError); ok && provider != nil {
		guidance = *provider
	}
	now := t.clock.Now()
	func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.suspended || (t.cooldownStatus != 0 && t.next > now) {
			if guidance.Status == 0 {
				guidance.Status = t.cooldownStatus
			}
			guidance.RetryAfter = max(guidance.RetryAfter, t.next-now)
			guidance.Suspended = guidance.Suspended || t.suspended
		}
	}()
	if guidance.RetryAfter <= 0 && !guidance.Suspended {
		return err
	}
	guidance.cause = err
	return &guidance
}
func (t *Transport) acquire(ctx context.Context, key RequestKey) (AcquisitionDocument, error) {
	select {
	case t.slots <- struct{}{}:
	case <-ctx.Done():
		return AcquisitionDocument{}, ctx.Err()
	}
	defer func() { <-t.slots }()
	select {
	case t.pace <- struct{}{}:
	case <-ctx.Done():
		return AcquisitionDocument{}, ctx.Err()
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
		// Read the clock outside the mutex, while still guaranteeing custody
		// release if that read itself panics during worker unwinding.
		now := maxDuration
		defer func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			finished = true
			if started {
				t.stats.InFlight--
			} else {
				t.next = max(t.next, later(now, (time.Second+time.Duration(t.cfg.RequestsPerSecond)-1)/time.Duration(t.cfg.RequestsPerSecond)))
			}
			releaseLocked()
		}()
		now = t.clock.Now()
	}()
	var retire func()
	defer func() {
		if retire != nil {
			retire()
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return AcquisitionDocument{}, err
		}
		now := t.clock.Now()
		var delay time.Duration
		var cooldown error
		func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			if t.suspended {
				cooldown = t.cooldownErrorLocked(now)
			}
			delay = t.next - now
		}()
		if cooldown != nil {
			return AcquisitionDocument{}, cooldown
		}
		if delay > 0 {
			if err := t.clock.Wait(ctx, delay); err != nil {
				return AcquisitionDocument{}, err
			}
			continue
		}
		var err error
		retire, err = t.admission.Try(ctx, t.cfg.MaxResponseBytes+t.cfg.MaxMetadataBytes)
		if err != nil {
			return AcquisitionDocument{}, err
		}
		if retire == nil {
			return AcquisitionDocument{}, errors.New("enrichment admission returned no retirement capability")
		}
		now = t.clock.Now()
		stale := func() bool {
			t.mu.Lock()
			defer t.mu.Unlock()
			return t.suspended || now < t.next
		}()
		if stale {
			staleRetirement := retire
			retire = nil
			staleRetirement()
			continue
		}
		break
	}
	dispatch := Dispatch{
		check: func() error {
			now, contextErr := t.clock.Now(), ctx.Err()
			t.mu.Lock()
			defer t.mu.Unlock()
			if finished || started {
				if !finished {
					dispatchErr = ErrDispatch
				}
				return ErrDispatch
			}
			if contextErr != nil {
				return contextErr
			}
			if t.suspended || now < t.next {
				return t.cooldownErrorLocked(now)
			}
			checked = true
			return nil
		},
		started: func() error {
			now, contextErr := t.clock.Now(), ctx.Err()
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
			if contextErr != nil {
				boundaryErr = contextErr
			} else if t.suspended || now < t.next {
				boundaryErr = t.cooldownErrorLocked(now)
			}
			t.next = max(t.next, later(now, (time.Second+time.Duration(t.cfg.RequestsPerSecond)-1)/time.Duration(t.cfg.RequestsPerSecond)))
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
		return AcquisitionDocument{}, err
	}
	r, err := t.fetch(ctx, key, t.cfg.MaxResponseBytes, dispatch)
	now := t.clock.Now()
	func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		finished = true
		if !started && err == nil {
			err = ErrDispatch
		}
		if err == nil && dispatchErr != nil {
			err = dispatchErr
		}
		if r.Status == 429 || r.Status >= 500 {
			delay := retryFloor(r.RetryAfter)
			t.next = max(t.next, later(now, delay))
			t.cooldownStatus = r.Status
			t.suspended = t.suspended || delay > automaticRetryHorizon
			if err == nil {
				err = t.cooldownErrorLocked(now)
			}
		} else if err == nil && r.Status != 200 {
			err = &ProviderError{Status: r.Status}
		}
		if err == nil && int64(len(r.Body)) > t.cfg.MaxResponseBytes {
			err = &ResponseSizeError{LimitBytes: t.cfg.MaxResponseBytes, ObservedBytes: int64(len(r.Body))}
		}
	}()
	if err != nil {
		return AcquisitionDocument{}, err
	}
	if err := ctx.Err(); err != nil {
		return AcquisitionDocument{}, err
	}
	var metadata AcquisitionMetadata
	var metadataCharge int64
	if t.cfg.MaxMetadataBytes > 0 {
		var err error
		metadata, metadataCharge, err = freezeAcquisitionMetadata(key, r.Metadata, t.cfg.MaxMetadataBytes)
		if err != nil {
			return AcquisitionDocument{}, err
		}
	}
	// Retain an independently owned body. Metadata was bounded first. Cache
	// accounting includes allocator rounding for BOTH body and metadata strings.
	owned := make([]byte, len(r.Body))
	copy(owned, r.Body)
	if err := ctx.Err(); err != nil {
		return AcquisitionDocument{}, err
	}
	func() { t.mu.Lock(); defer t.mu.Unlock(); t.stats.Bytes += uint64(len(r.Body)) }()
	return AcquisitionDocument{body: owned, key: key, metadata: metadata, metadataCharge: metadataCharge}, nil
}

func (t *Transport) evict(e *list.Element) {
	v := e.Value.(cachedResponse)
	delete(t.cache, v.key)
	t.lru.Remove(e)
	t.stats.CacheBytes -= cacheBodyCharge(len(v.body)) + v.metadataCharge
	t.stats.CacheMetadataBytes -= v.metadataCharge
	t.stats.KeyBytes -= v.key.bytes()
}

func (t *Transport) remember(key RequestKey, body []byte, metadata AcquisitionMetadata, metadataCharge int64, expires time.Duration) {
	charge := cacheBodyCharge(len(body)) + metadataCharge
	if t.cfg.CacheTTL == 0 || t.cfg.CacheEntries == 0 || charge > t.cfg.CacheBytes {
		return
	}
	for t.lru.Len() >= t.cfg.CacheEntries || t.stats.CacheBytes+charge > t.cfg.CacheBytes {
		t.evict(t.lru.Back())
	}
	e := t.lru.PushFront(cachedResponse{key: key, body: body, metadata: metadata, metadataCharge: metadataCharge, expires: expires})
	t.cache[key] = e
	t.stats.CacheBytes += charge
	t.stats.CacheMetadataBytes += metadataCharge
	t.stats.KeyBytes += key.bytes()
}

// Go's noscan []byte objects use size classes through 32KiB, then 8KiB pages.
// Next-power-of-two (at least a 16B tiny block) conservatively bounds the small
// classes without depending on a private runtime table. Saturation keeps a
// future oversized input from wrapping into a small cache debit. Cache entry/
// key metadata and runtime/span overhead have their separate reservation; this
// bound concerns retained body objects, not whole-process RSS qualification.
func cacheBodyCharge(n int) int64 {
	if n == 0 {
		return 0
	}
	if n <= 32<<10 {
		charge := int64(16)
		for charge < int64(n) {
			charge *= 2
		}
		return charge
	}
	if int64(n) > math.MaxInt64-8191 {
		return math.MaxInt64
	}
	return (int64(n) + 8191) &^ 8191
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
	func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.closed = true
	}()
	t.cancel()
	t.wg.Wait()
	func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.cache = make(map[RequestKey]*list.Element)
		t.lru.Init()
		t.stats.CacheBytes = 0
		t.stats.CacheMetadataBytes = 0
		t.stats.KeyBytes = int64(len(t.cfg.Provider))
	}()
}
