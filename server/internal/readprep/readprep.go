// Package readprep shares incremental, read-only preparation in process memory.
// It stores no database rows, files, grants, or durable jobs. Construct one Store
// at the composition root and Close it at shutdown; there is no global store.
//
// A handle is not authorization. Adapters MUST authenticate and check live
// authority before AND after Submit/Open/Poll, including cached success. Bind
// Caller to the exact authenticated server/session/profile/authority context.
// Invalidate a content key when its basis can no longer be reconstructed or its
// policy/source authority changes. Content-only churn need not invalidate an
// independently reconstructible immutable basis. Restart and eviction are stale.
package readprep

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

var (
	ErrCapacity = errors.New("readprep: capacity exhausted")
	ErrStale    = errors.New("readprep: stale or unknown handle/content")
	ErrClosed   = errors.New("readprep: store closed")
	ErrInvalid  = errors.New("readprep: invalid argument or accounting")
	ErrFailed   = errors.New("readprep: computation failed")
	// ErrUncached explicitly hands delivery back to an adapter when an approved
	// exceptional legal scalar cannot fit the ordinary memory budget. Do not
	// retry the same preparation indefinitely or truncate/drop that row. Re-read
	// and deliver the exact single scalar on the adapter's transient uncached
	// path, checking live authority before/after. Its disclosed O(B) allocation,
	// normalization and transport are outside this store's ordinary cap; never
	// put that scalar or its query results in a file/database/side cache. This
	// signal does not authorize arbitrary scans or other cache-cap bypasses.
	ErrUncached = errors.New("readprep: exceptional scalar requires uncached delivery")
)

// Key uses full byte equality, never hash equality alone. Canonical must encode
// library lifetime, field, equivalent policy, full source witness, algorithm,
// folded query and window unambiguously (for example length-prefixed fields).
// Basis names immutable inputs, not a moving current head. Namespace separates
// producers. The store cannot validate a producer's semantic canonicalization.
type Key struct {
	Namespace string
	Basis     string
	Canonical string
}

// Caller is an exact canonical private request identity, separate from Key.
// Do not use only an account ID if sessions/profiles/authority differ.
type Caller string
type Handle string

type State uint8

const (
	Preparing State = iota
	Ready
)

// Result.Payload is a private copy. Modifying it cannot affect another reader.
// Adapters MUST defer result.Release() through serialization/transport; the copy
// stays charged until then. Do not use/retain Payload after Release. Copying a
// Result shares its idempotent release lease. Preparing has no payload; errors
// are returned separately, never empty success. Basis names immutable content.
type Result struct {
	State State
	Basis string
	// Key is the exact descriptor behind this caller's private handle. Domain
	// adapters use it to reauthorize cold-scope preparation; it is not a grant.
	Key            Key
	Payload        []byte
	OriginalExpiry time.Time
	// CreatedAt is this private handle's first admission time, unchanged by
	// polls or reuse of another caller's equivalent shared computation.
	CreatedAt time.Time
	delivery  *delivery
}

// Release returns the in-flight delivery reservation, including after Close or
// content/handle invalidation. It is concurrent-safe, idempotent and a no-op for
// zero results. Close never releases a caller's outstanding delivery.
func (r Result) Release() {
	if r.delivery == nil {
		return
	}
	d := r.delivery
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	if d.released {
		return
	}
	d.released = true
	d.s.used -= d.bytes
	d.s.deliveries--
	d.s.deliveryBytes -= d.bytes
	d.bytes = 0
}

// ResizeDelivery reserves the adapter's TOTAL additional live memory, excluding
// the original Result copy/lease which stays charged automatically. Call BEFORE
// allocating hydrated rows, labels, serialization buffers or transport copies;
// include backing capacities and overlapping buffers. Free buffers BEFORE
// shrinking. Copies of Result share this reservation. Release returns it all.
//
// Ordinary growth may evict completed cache entries and returns ErrCapacity if
// the aggregate cap still cannot fit it; failed growth changes no delivery charge.
// This never caches hydrated data or grants an allocation exception. If a single
// legal scalar requires approved exceptional O(B) work, the adapter must explicitly
// take the separate transient uncached path described by ErrUncached. Ordinary
// contention is not permission to bypass this cap or retry a giant row forever.
// Released/zero results return ErrStale; a closed store returns ErrClosed.
func (r Result) ResizeDelivery(bytes int64) error {
	if bytes < 0 {
		return ErrInvalid
	}
	if r.delivery == nil {
		return ErrStale
	}
	d := r.delivery
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	return d.resize(bytes)
}

// Reserve adds bytes to the shared adapter reservation atomically. Use this for
// independently allocated buffers; ResizeDelivery instead sets their total. A
// successful reservation must precede allocation and last through serialization.
func (r Result) Reserve(bytes int64) error {
	if bytes < 0 {
		return ErrInvalid
	}
	if r.delivery == nil {
		return ErrStale
	}
	d := r.delivery
	d.s.mu.Lock()
	defer d.s.mu.Unlock()
	if d.released {
		return ErrStale
	}
	if d.s.closed {
		return ErrClosed
	}
	if bytes > d.s.cfg.MaxBytes-d.bytes {
		return ErrCapacity
	}
	return d.resize(d.bytes - d.base + bytes)
}

type delivery struct {
	s           *Store
	bytes, base int64
	released    bool
}

// resize holds s.mu, including across the admission check and charge update.
func (d *delivery) resize(adapterBytes int64) error {
	if d.released {
		return ErrStale
	}
	if d.s.closed {
		return ErrClosed
	}
	if adapterBytes > d.s.cfg.MaxBytes-d.base {
		return ErrCapacity
	}
	target := d.base + adapterBytes
	delta := target - d.bytes
	if delta > 0 && !d.s.room(delta, nil) {
		return ErrCapacity
	}
	d.s.used += delta
	d.s.deliveryBytes += delta
	d.bytes = target
	return nil
}

// StepResult transfers exclusive ownership of Payload to the store when Done.
// Neither the producer nor any aliases may subsequently read/write that buffer.
// Its entire backing allocation (cap, not len) must be included in AccountedBytes.
// Payload must start at the beginning of its allocation; do not transfer a
// subslice that would retain an uncharged prefix of a larger allocation.
// Unfinished steps must return no Payload. Encode incrementally within the budget;
// do not construct an unbounded result and attempt admission afterward.
type StepResult struct {
	// RetryAfter yields a pending read without occupying a worker or spinning on
	// an unchanged readiness head. It is memory-only, at most one second, and
	// never renews the handle, poll deadline or original expiry.
	RetryAfter time.Duration
	Done       bool
	Payload    []byte
}

// Computation is used by only one worker at a time. Each Step must do a bounded
// quantum of read-only work, release snapshots before returning, and honor ctx.
// Return Done=false,nil to yield; any error (including deadline) is terminal.
// Reserve peak allocation through Budget BEFORE allocating, free before shrinking,
// and include capacities, cursor/reducer state, labels, buffers and retained seeds
// in AccountedBytes. No hidden all-results map or unaccounted external cache is
// permitted. AccountedBytes includes the returned payload until ownership passes.
// A computation must not start goroutines or retain the Budget after its call.
type Computation interface {
	AccountedBytes() int64
	Step(context.Context, *Budget) (StepResult, error)
}

// Plan defers construction until admission and a worker slot. InitialBytes must
// also cover captured factory descriptors/backing allocations; keep captures small.
// New has the same bounded/cancellable/allocation rules as Step. Shared content
// uses the first admitted plan; later identical submissions never invoke theirs.
// Callbacks must not call Close (which waits for the calling worker).
type Plan struct {
	// ColdSearch opts into the sole shared store's one-build cold-search lane.
	// Ownership spans every quantum until terminal/cancelled work has exited.
	// Only a successful Poll renews its 30s demand deadline; attaching handles
	// does not. Ordinary plans retain the existing independent worker admission.
	ColdSearch bool
	// ReuseHandle coalesces retries by the same exact private caller and key.
	// It never renews a lease or cold poll deadline; only successful Poll does.
	// Use for resumable command reads, not independent UI interests.
	ReuseHandle bool
	// ShouldYield is an optional cheap, nonblocking priority probe for ColdSearch
	// (e.g. dbwork.ColdReadShouldYield). Called before New/Step, outside the store
	// lock and before acquiring a snapshot. True retries after a short pause,
	// leaving workers available to ordinary plans. Include captures in InitialBytes.
	ShouldYield  func() bool
	InitialBytes int64
	New          func(context.Context, *Budget) (Computation, error)
}

type Config struct {
	MaxBytes    int64
	Workers     int
	MaxQueued   int
	MaxHandles  int
	OriginalTTL time.Duration
	IdleTTL     time.Duration
	CompleteTTL time.Duration
	// StepTimeout is disabled at zero. A positive value is an optional operator
	// watchdog whose expiry is terminal. The normal ~10ms quantum is cooperative:
	// producers check rows/bytes/time and yield; scheduler delay or an approved
	// O(B) scalar must not become a false terminal failure by default.
	StepTimeout time.Duration
	// Now must be concurrency safe and monotone. Ordinary expiry is lazy. Cold
	// demand expiry also wakes on a store-owned timer (at most 1s between checks).
	Now func() time.Time
}

// DefaultConfig returns starting limits, not a machine-capacity claim.
func DefaultConfig() Config {
	return Config{MaxBytes: 32 << 20, Workers: 2, MaxQueued: 16, MaxHandles: 512,
		OriginalTTL: 30 * time.Minute, IdleTTL: 5 * time.Minute, CompleteTTL: time.Minute,
		Now: time.Now}
}

// Limits above the starting worker/queue/handle bounds are rejected. Byte limits
// may be tuned; tests can use tiny limits. Zero values select defaults.
func New(cfg Config) (*Store, error) {
	d := DefaultConfig()
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = d.MaxBytes
	}
	if cfg.Workers == 0 {
		cfg.Workers = d.Workers
	}
	if cfg.MaxQueued == 0 {
		cfg.MaxQueued = d.MaxQueued
	}
	if cfg.MaxHandles == 0 {
		cfg.MaxHandles = d.MaxHandles
	}
	if cfg.OriginalTTL == 0 {
		cfg.OriginalTTL = d.OriginalTTL
	}
	if cfg.IdleTTL == 0 {
		cfg.IdleTTL = d.IdleTTL
	}
	if cfg.CompleteTTL == 0 {
		cfg.CompleteTTL = d.CompleteTTL
	}
	if cfg.Now == nil {
		cfg.Now = d.Now
	}
	if cfg.MaxBytes < 1 || cfg.Workers < 1 || cfg.Workers > 2 || cfg.MaxQueued < 1 || cfg.MaxQueued > 16 ||
		cfg.MaxHandles < 1 || cfg.MaxHandles > 512 || cfg.OriginalTTL < 0 || cfg.IdleTTL < 0 ||
		cfg.CompleteTTL < 0 || cfg.StepTimeout < 0 {
		return nil, ErrInvalid
	}
	s := &Store{cfg: cfg, entries: make(map[Key]*entry), handles: make(map[Handle]*lease)}
	s.cond = sync.NewCond(&s.mu)
	s.wg.Add(cfg.Workers)
	for i := 0; i < cfg.Workers; i++ {
		supervise.Go("readprep.worker", s.worker)
	}
	return s, nil
}

// Fixed conservative per-object charges cover bookkeeping/map/queue overhead in
// addition to actual key/caller byte lengths. MaxBytes is accounted live memory,
// not a promise about Go allocator/GC/RSS. Empty maps and worker stacks have fixed
// bounded overhead outside this budget; map high-water is bounded by these limits.
const entryOverhead int64 = 768
const handleOverhead int64 = 256
const deliveryOverhead int64 = 128

type entry struct {
	ready                      chan struct{}
	key                        Key
	meta, reserved             int64
	plan                       Plan
	work                       Computation
	ctx                        context.Context
	cancel                     context.CancelFunc
	refs                       int
	running, retired, complete bool
	payload                    []byte
	err                        error
	until, touched             time.Time
	cold                       bool // independent of plan, which is cleared at EOF
	pollUntil, retryAt         time.Time
}

type lease struct {
	waiting                 bool
	caller                  Caller
	e                       *entry
	original, idle, created time.Time
	bytes                   int64
}

type Store struct {
	retireChanged  chan struct{}
	initialWaiters int
	mu             sync.Mutex
	cond           *sync.Cond
	wg             sync.WaitGroup
	cfg            Config
	entries        map[Key]*entry
	handles        map[Handle]*lease
	queue          []*entry
	used           int64
	deliveries     int
	deliveryBytes  int64
	running        int
	closed         bool
	coldOwner      *entry // off queue between quanta; retained until worker exits
	wakeTimer      *time.Timer
}

// Budget charges all producer-owned live and temporary memory for one content
// computation. It is valid only during New/Step. Resize failure is terminal even
// if a producer ignores it; no weight update can bypass the store cap.
type Budget struct {
	s        *Store
	e        *entry
	err      error
	parent   *Budget
	overhead int64
}

// WithOverhead gives a composed reducer a view of this same reservation. Every
// child Resize retains the wrapper's still-live captures and buffers, including
// while other workers admit work. Errors remain sticky on the original budget.
// The view is valid only during this New/Step and must never be retained.
func (b *Budget) WithOverhead(bytes int64) (*Budget, error) {
	root := b
	if b.parent != nil {
		root = b.parent
	}
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	if root.err != nil {
		return nil, root.err
	}
	if bytes < 0 || b.overhead > math.MaxInt64-bytes {
		root.err = ErrInvalid
		return nil, ErrInvalid
	}
	return &Budget{s: b.s, e: b.e, parent: root, overhead: b.overhead + bytes}, nil
}

func (b *Budget) Resize(bytes int64) error {
	_, err := b.resize(bytes, false)
	return err
}

// TryResize admits an optional optimization before allocating it. Capacity
// returns false,nil without changing the reservation or poisoning the quantum;
// the computation may continue using its already-reserved exact algorithm.
// Invalid sizes, retirement and closure remain terminal, as with Resize.
func (b *Budget) TryResize(bytes int64) (bool, error) {
	return b.resize(bytes, true)
}

func (b *Budget) resize(bytes int64, optional bool) (bool, error) {
	root := b
	if b.parent != nil {
		root = b.parent
	}
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	if root.err != nil {
		return false, root.err
	}
	if b.e.retired || b.s.closed {
		root.err = ErrStale
		return false, root.err
	}
	if bytes < 0 || bytes > math.MaxInt64-b.overhead {
		root.err = ErrInvalid
		return false, root.err
	}
	bytes += b.overhead
	if bytes > b.e.reserved && !b.s.room(bytes-b.e.reserved, b.e) {
		if optional {
			return false, nil
		}
		root.err = ErrCapacity
		return false, root.err
	}
	b.s.used += bytes - b.e.reserved
	b.e.reserved = bytes
	return true, nil
}

// Submit opens a fresh private handle, sharing only exactly identical content.
// Request cancellation rejects the operation; it does not become the shared work
// context. Call Cancel to release demand after a request/session is retired.
func (s *Store) Submit(ctx context.Context, caller Caller, key Key, plan Plan) (Handle, error) {
	if plan.New == nil || plan.InitialBytes < 0 {
		return "", ErrInvalid
	}
	return s.open(ctx, caller, key, &plan, time.Time{})
}

// SubmitUntil is Submit with an inherited original expiry ceiling. Keyset
// continuation MUST carry the captured expiry forward, rather than acquire a
// fresh lifetime on each page. A zero ceiling uses the configured lifetime.
func (s *Store) SubmitUntil(ctx context.Context, caller Caller, key Key, plan Plan, until time.Time) (Handle, error) {
	if plan.New == nil || plan.InitialBytes < 0 {
		return "", ErrInvalid
	}
	return s.open(ctx, caller, key, &plan, until)
}

// Open attaches to existing content without submitting a plan. Missing content
// returns ErrStale. Every successful Open/Submit creates a distinct random handle.
func (s *Store) Open(ctx context.Context, caller Caller, key Key) (Handle, error) {
	return s.open(ctx, caller, key, nil, time.Time{})
}

// OpenUntil attaches a private handle with the same original expiry ceiling as
// SubmitUntil. Neither operation extends an earlier captured continuation lease.
func (s *Store) OpenUntil(ctx context.Context, caller Caller, key Key, until time.Time) (Handle, error) {
	return s.open(ctx, caller, key, nil, until)
}

func (s *Store) open(ctx context.Context, caller Caller, key Key, plan *Plan, until time.Time) (Handle, error) {
	if caller == "" || key.Namespace == "" || key.Basis == "" || key.Canonical == "" {
		return "", ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", ErrClosed
	}
	now := s.cfg.Now()
	s.prune(now)
	expires := now.Add(s.cfg.OriginalTTL)
	if !until.IsZero() && until.Before(expires) {
		expires = until
	}
	if !now.Before(expires) {
		return "", ErrStale
	}
	if plan != nil && plan.ReuseHandle {
		for h, l := range s.handles {
			if l.caller == caller && l.e.key == key {
				return h, nil
			}
		}
	}
	if len(s.handles) >= s.cfg.MaxHandles {
		return "", ErrCapacity
	}
	e := s.entries[key]
	if e == nil && plan == nil {
		return "", ErrStale
	}
	hbytes := handleOverhead + int64(len(caller))
	needed := hbytes
	var meta int64
	if e == nil {
		if len(s.queue) >= s.cfg.MaxQueued {
			return "", ErrCapacity
		}
		meta = entryOverhead + int64(len(key.Namespace)) + int64(len(key.Basis)) + int64(len(key.Canonical))
		// Subtraction-based checks also avoid overflow for malicious byte estimates.
		if meta > s.cfg.MaxBytes || plan.InitialBytes > s.cfg.MaxBytes-meta || hbytes > s.cfg.MaxBytes-meta-plan.InitialBytes {
			return "", ErrCapacity
		}
		needed += meta + plan.InitialBytes
	}
	if !s.room(needed, e) {
		return "", ErrCapacity
	}
	var raw [32]byte
	var h Handle
	for {
		if _, err := rand.Read(raw[:]); err != nil {
			return "", ErrFailed
		}
		h = Handle(base64.RawURLEncoding.EncodeToString(raw[:]))
		if _, exists := s.handles[h]; !exists {
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if e == nil {
		key = Key{strings.Clone(key.Namespace), strings.Clone(key.Basis), strings.Clone(key.Canonical)}
		workCtx, cancel := context.WithCancel(context.Background())
		e = &entry{key: key, meta: meta, reserved: plan.InitialBytes, plan: *plan, ctx: workCtx, cancel: cancel, touched: now, cold: plan.ColdSearch}
		if e.cold {
			e.pollUntil = now.Add(coldPollTTL)
		}
		s.entries[key] = e
		s.used += meta + plan.InitialBytes
		s.queue = append(s.queue, e)
		s.cond.Signal()
	}
	s.handles[h] = &lease{caller: Caller(strings.Clone(string(caller))), e: e,
		original: expires, idle: now.Add(s.cfg.IdleTTL), created: now, bytes: hbytes}
	s.used += hbytes
	e.refs++
	e.touched = now
	s.armWake(now)
	return h, nil
}

// LeaseExpiry returns the actual private lease's original expiry for signing
// anchors. It does not renew idle/original lifetime and is not authorization.
func (s *Store) LeaseExpiry(ctx context.Context, caller Caller, h Handle) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return time.Time{}, ErrClosed
	}
	s.prune(s.cfg.Now())
	l := s.handles[h]
	if l == nil || caller == "" || l.caller != caller {
		return time.Time{}, ErrStale
	}
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	return l.original, nil
}

func (s *Store) Poll(ctx context.Context, caller Caller, h Handle) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Result{}, ErrClosed
	}
	now := s.cfg.Now()
	s.prune(now)
	l := s.handles[h]
	if l == nil || caller == "" || l.caller != caller {
		return Result{}, ErrStale
	}
	e := l.e
	if e.err != nil {
		return Result{}, e.err
	}
	result := Result{State: Preparing, Basis: e.key.Basis, Key: e.key, OriginalExpiry: l.original, CreatedAt: l.created}
	// These immutable strings can outlive eviction of their original entry.
	n := deliveryOverhead + int64(len(e.key.Namespace)+len(e.key.Basis)+len(e.key.Canonical))
	if e.complete {
		n += int64(len(e.payload))
	}
	// Hold the copy, basis and lease through the caller's transport lifetime.
	if !s.room(n, e) {
		return Result{}, ErrCapacity
	}
	s.used += n
	s.deliveries++
	s.deliveryBytes += n
	result.delivery = &delivery{s: s, bytes: n, base: n}
	if e.complete {
		result.Payload = make([]byte, len(e.payload))
		copy(result.Payload, e.payload)
		result.State = Ready
	}
	if err := ctx.Err(); err != nil {
		if result.delivery != nil {
			s.used -= result.delivery.bytes
			s.deliveries--
			s.deliveryBytes -= result.delivery.bytes
		}
		return Result{}, err
	}
	if e.cold && !e.complete {
		e.pollUntil = now.Add(coldPollTTL)
		s.armWake(now)
	}
	l.idle = now.Add(s.cfg.IdleTTL)
	e.touched = now
	return result, nil
}

// Cancel is idempotent and reveals nothing about another caller's handle.
// Only this handle's demand is released. Unused unfinished work is cancelled;
// complete results may remain reusable until their fixed completion TTL/eviction.
func (s *Store) Cancel(ctx context.Context, caller Caller, h Handle) error {
	return s.cancel(ctx, caller, h, "")
}

// CancelNamespace releases only a demand in the adapter's own fixed namespace.
// A handle from another preparation family answers just like an absent handle
// and remains owned by its original adapter. Caller and namespace are checked
// together under the store lock, without a Poll allocation or check/use gap.
func (s *Store) CancelNamespace(ctx context.Context, caller Caller, h Handle, namespace string) error {
	if namespace == "" {
		return ErrStale
	}
	return s.cancel(ctx, caller, h, namespace)
}

func (s *Store) cancel(ctx context.Context, caller Caller, h Handle, namespace string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	s.prune(s.cfg.Now())
	if l := s.handles[h]; l != nil && caller != "" && l.caller == caller && (namespace == "" || l.e.key.Namespace == namespace) {
		s.release(h, l)
	}
	return nil
}

// Invalidate makes all handles for this exact key stale immediately. In-flight
// allocations remain charged until the cooperative worker actually returns.
func (s *Store) Invalidate(key Key) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.entries[key]; e != nil {
		s.retire(e)
	}
}

// Close is concurrent-safe and idempotent. It cancels and joins the fixed worker
// pool. Outstanding caller deliveries remain charged until Result.Release;
// Close does not wait for them. Producers must honor their context; Go cannot
// forcibly stop a callback.
func (s *Store) Close() error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.notifyRetirementLocked()
		if s.wakeTimer != nil {
			s.wakeTimer.Stop()
		}
		for _, e := range s.entries {
			s.retire(e)
		}
		s.cond.Broadcast()
	}
	s.mu.Unlock()
	s.wg.Wait()
	return nil
}

type Stats struct {
	Bytes                             int64
	Entries, Handles, Queued, Running int
	Deliveries                        int
	DeliveryBytes                     int64
}

// MaxBytes is the ordinary aggregate cache/computation/delivery budget. It is
// not a metadata cap or a promise that every legal scalar fits the cache.
func (s *Store) MaxBytes() int64 { return s.cfg.MaxBytes }

// Stats returns charged live bytes, including cancelled workers still unwinding.
func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(s.cfg.Now())
	return Stats{Bytes: s.used, Entries: len(s.entries), Handles: len(s.handles),
		Queued: len(s.queue), Running: s.running, Deliveries: s.deliveries, DeliveryBytes: s.deliveryBytes}
}

// All following bookkeeping runs with mu held.
func (s *Store) release(h Handle, l *lease) {
	s.notifyRetirementLocked()
	delete(s.handles, h)
	s.used -= l.bytes
	l.e.refs--
	if l.e.refs == 0 && !l.e.complete {
		s.retire(l.e)
	}
}

func (s *Store) prune(now time.Time) {
	for h, l := range s.handles {
		if !now.Before(l.original) || !now.Before(l.idle) {
			s.release(h, l)
		}
	}
	for _, e := range s.entries {
		if (e.complete && !now.Before(e.until)) || (e.cold && !e.complete && !now.Before(e.pollUntil)) {
			s.retire(e)
		}
	}
}

func (s *Store) retire(e *entry) {
	if e.retired {
		return
	}
	e.retired = true
	signalEntryReadyLocked(e)
	s.notifyRetirementLocked()
	e.cancel()
	delete(s.entries, e.key)
	for h, l := range s.handles {
		if l.e == e {
			delete(s.handles, h)
			s.used -= l.bytes
		}
	}
	e.refs = 0
	for i, queued := range s.queue {
		if queued == e {
			copy(s.queue[i:], s.queue[i+1:])
			s.queue[len(s.queue)-1] = nil
			s.queue = s.queue[:len(s.queue)-1]
			break
		}
	}
	if !e.running {
		s.dispose(e)
	}
}

func (s *Store) dispose(e *entry) {
	if s.coldOwner == e {
		s.coldOwner = nil
		s.cond.Broadcast()
	}
	s.used -= e.meta + e.reserved
	e.meta, e.reserved = 0, 0
	e.plan, e.work, e.payload = Plan{}, nil, nil
}

func (s *Store) room(n int64, protect *entry) bool {
	if n < 0 || n > s.cfg.MaxBytes {
		return false
	}
	for s.used > s.cfg.MaxBytes-n {
		var oldest *entry
		for _, e := range s.entries {
			if e != protect && e.complete && (oldest == nil || e.touched.Before(oldest.touched)) {
				oldest = e
			}
		}
		if oldest == nil {
			return false
		}
		s.retire(oldest)
	}
	return true
}

const coldPollTTL = 30 * time.Second
const coldRetryDelay = 10 * time.Millisecond
const coldPruneInterval = time.Second

var errPriorityYield = errors.New("readprep: priority yield")

// One timer belongs to the existing store lifecycle, not to individual handles
// or quanta. It cancels even a blocked cooperative callback with no API traffic.
// Capping the wait also makes a monotone injected clock observable while idle.
// All scheduling and the callback are under mu; Close stops further rearming.
func (s *Store) armWake(now time.Time) {
	if s.closed {
		return
	}
	delay := coldPruneInterval
	needed := false
	for _, e := range s.entries {
		if e.complete {
			continue
		}
		if e.cold {
			needed = true
			delay = min(delay, e.pollUntil.Sub(now))
		} else if !e.running && !e.retryAt.IsZero() {
			needed = true
		} else {
			continue
		}
		if !e.running && !e.retryAt.IsZero() {
			// A poll may acquire mu after the retry deadline but before its
			// timer callback. Keep an elapsed retry immediately runnable;
			// resetting it to the prune interval lets frequent polls postpone
			// all progress indefinitely.
			delay = min(delay, max(time.Nanosecond, e.retryAt.Sub(now)))
		}
	}
	if !needed {
		if s.wakeTimer != nil {
			s.wakeTimer.Stop()
		}
		return
	}
	delay = max(time.Nanosecond, delay)
	if s.wakeTimer == nil {
		s.wakeTimer = time.AfterFunc(delay, s.wake)
	} else {
		s.wakeTimer.Reset(delay)
	}
}

func (s *Store) wake() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	now := s.cfg.Now()
	s.prune(now)
	s.cond.Broadcast()
	s.armWake(now)
}

// The active cold owner lives outside the waiting queue across yields. Other
// cold plans still use the existing bounded queue; ordinary plans may bypass
// them and run on either fixed worker. A retired running owner keeps its slot
// and memory until the callback actually exits.
func (s *Store) next(now time.Time) *entry {
	for i, e := range s.queue {
		if now.Before(e.retryAt) {
			continue
		}
		e.retryAt = time.Time{}
		if e.cold && s.coldOwner != nil {
			continue
		}
		copy(s.queue[i:], s.queue[i+1:])
		s.queue[len(s.queue)-1] = nil
		s.queue = s.queue[:len(s.queue)-1]
		if e.cold {
			s.coldOwner = e
		}
		return e
	}
	e := s.coldOwner
	if e != nil && !e.running && !e.retired && !now.Before(e.retryAt) {
		e.retryAt = time.Time{}
		return e
	}
	return nil
}

func (s *Store) worker() {
	defer s.wg.Done()
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		now := s.cfg.Now()
		s.prune(now)
		if s.closed {
			return
		}
		e := s.next(now)
		if e == nil {
			s.armWake(now)
			s.cond.Wait()
			continue
		}
		e.running = true
		s.running++
		s.armWake(now)
		s.mu.Unlock()
		out, bytes, err := s.step(e)
		s.mu.Lock()
		// Expiry can win even if the callback finishes after its last deadline
		// but before the timer callback acquires mu.
		s.prune(s.cfg.Now())
		e.running = false
		s.running--
		if e.retired {
			s.dispose(e)
			continue
		}
		if err == errPriorityYield {
			e.retryAt = s.cfg.Now().Add(coldRetryDelay)
			continue
		}
		if err != nil || bytes < 0 || bytes > e.reserved || (!out.Done && out.Payload != nil) || (out.Done && int64(cap(out.Payload)) > bytes) || out.RetryAfter < 0 || out.RetryAfter > time.Second || (out.Done && out.RetryAfter != 0) {
			if err == nil {
				err = ErrInvalid
			}
			// Never retain arbitrary producer error objects/messages and their
			// potentially unbounded backing graphs in the cache.
			e.err = terminalError(err)
			out = StepResult{Done: true}
		}
		if out.Done {
			e.complete = true
			signalEntryReadyLocked(e)
			e.until = s.cfg.Now().Add(s.cfg.CompleteTTL)
			e.payload = out.Payload
			e.work, e.plan = nil, Plan{}
			s.used -= e.reserved - int64(cap(e.payload))
			e.reserved = int64(cap(e.payload))
			e.cancel()
			if s.coldOwner == e {
				s.coldOwner = nil
				s.cond.Broadcast()
			}
		} else {
			if out.RetryAfter > 0 {
				e.retryAt = s.cfg.Now().Add(out.RetryAfter)
			}
			if !e.cold {
				s.queue = append(s.queue, e)
			}
		}
		// Requeue ordinary work then select under the same lock, preserving
		// its existing queue bound and one-worker-at-a-time computation rule.
	}
}

func (s *Store) step(e *entry) (out StepResult, bytes int64, err error) {
	returned := false
	defer func() {
		if !returned {
			out = StepResult{}
			bytes = 0
			err = ErrFailed
		}
	}()
	defer supervise.Recover("readprep.step")
	out, bytes, err = s.doStep(e)
	returned = true
	return
}

func (s *Store) doStep(e *entry) (out StepResult, bytes int64, err error) {
	ctx := e.ctx
	if e.cold && e.plan.ShouldYield != nil && e.plan.ShouldYield() {
		return out, e.reserved, errPriorityYield
	}
	if s.cfg.StepTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.StepTimeout)
		defer cancel()
	}
	b := &Budget{s: s, e: e}
	if e.work == nil {
		e.work, err = e.plan.New(ctx, b)
		if err != nil {
			return out, 0, err
		}
		if e.work == nil {
			return out, 0, ErrInvalid
		}
		// Keep the factory captures charged until completion. Its producer
		// estimate must include them in subsequent AccountedBytes reports.
		if e.work.AccountedBytes() < 0 || e.work.AccountedBytes() > e.reserved {
			return out, 0, ErrInvalid
		}
	}
	if b.err != nil {
		return out, 0, b.err
	}
	out, err = e.work.Step(ctx, b)
	if b.err != nil {
		err = b.err
	}
	if err == nil {
		err = ctx.Err()
	}
	return out, e.work.AccountedBytes(), err
}

func terminalError(err error) error {
	if errors.Is(err, ErrStale) {
		return ErrStale
	}
	if errors.Is(err, ErrUncached) {
		return ErrUncached
	}
	if errors.Is(err, ErrCapacity) {
		return ErrCapacity
	}
	if errors.Is(err, ErrInvalid) {
		return ErrInvalid
	}
	return ErrFailed
}
