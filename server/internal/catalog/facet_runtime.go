package catalog

// Process-owned composition of maintained BASE facts and the ONE shared,
// memory-only read-preparation store. No request starts a worker or persists a
// query, result, handle, timer or caller identity.

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
	"github.com/PorticoMediaServer/portico/server/internal/sorttext"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// Fallback is explicitly partial, retains its preparation handle and cannot
// issue keyset anchors over a mutable staging image.
type SimpleFacetFallbackPage struct {
	Kind string `json:"kind"`
	FacetPage
	Preparation SimpleFacetPreparing `json:"preparation"`
}

// FacetBaseCapture is installed once for an additional fixed source family.
// It captures BASE source facts only, never request authorization or query q.
type FacetBaseCapture func(context.Context, compactcatalog.ReadQuery, FacetBaseScope) (FacetBasePlan, error)

type facetRuntimeDemand struct {
	scope           FacetBaseScope
	touched         time.Time
	build, retiring int64
	ready           int64
	err             error // only fixed, normalized errors; no retained arbitrary error graph
	gap             time.Duration
	next            time.Time
	changed         chan struct{}
	charge          int64
	turn            uint64
	waitingSince    time.Time
	ingestWindow    uint64
	ingestSteps     uint64
	ingestStarted   time.Time
	ingestReported  bool
	// verify re-arms ONE audit of an already-ready basis at a producer/policy
	// state it has not been re-verified against. It is set only by a changed
	// fence and only on a non-terminal demand, never by a bare wake, and cleared
	// by the audit that proves the witness and authority still match, by the
	// build that replaces it, or when the demand takes a fixed terminal result.
	// A terminal demand therefore never carries it: due() refuses a terminal
	// demand (except for retirement), so nothing can spin on an undischargeable
	// requirement.
	verify bool
}

// facetRuntimeFence is the exact producer/policy identity a ready basis is
// re-verified against. The producer fence alone is a content barrier: it
// deliberately excludes per-scope and live AUTHORITY, whose policy/genre
// generations the base binds through CaptureFacetAuthority. Both are captured
// from ONE snapshot, so a policy-only change still reopens the sweep instead of
// inheriting the producer's "nothing changed" answer.
type facetRuntimeFence struct {
	producer compactcatalog.FacetProducerFence
	policy   int64
	genre    int64
}

type FacetRuntime struct {
	db                               *sql.DB
	windows                          *FacetWindows
	store                            *readprep.Store
	check                            FacetWindowCheck
	baseLimit, registryLimit         int64
	ingestWindow                     uint64
	ingestDemand                     *facetRuntimeDemand
	starveAfter                      time.Duration
	ingestHold                       time.Duration
	mu                               sync.Mutex
	demands                          map[FacetBaseScope]*facetRuntimeDemand
	sources                          map[string]FacetBaseCapture
	registryBytes                    int64
	wake, shared, dirty              *worker.Signal
	running                          atomic.Bool
	stopped                          bool
	now                              func() time.Time
	turn                             uint64
	gcComplete                       uint8
	gcPending                        bool
	gcPhase                          int
	gcBase, gcText, gcValue, gcClass int64
	started                          time.Time

	// fence is the producer/policy state read at the top of the current turn and
	// swept is the state every retained ready basis was last re-verified against.
	// known means fence holds a readable state (not pending/failed), sweptSet
	// means swept has been initialised, and moved records a family change
	// observed but not yet swept.
	fence    facetRuntimeFence
	swept    facetRuntimeFence
	known    bool
	sweptSet bool
	moved    bool
}

func NewFacetRuntime(db *sql.DB, shared *readprep.Store, check FacetWindowCheck, baseByteLimit int64) (*FacetRuntime, error) {
	if baseByteLimit == 0 {
		baseByteLimit = 128 << 20
	}
	if baseByteLimit < 0 {
		return nil, ErrFacetBaseInput
	}
	w, err := NewFacetWindows(db, shared, check)
	if err != nil {
		return nil, err
	}
	// A fixed reserved slice covers the bounded registry, keys, channels, maps,
	// provider callbacks and worker descriptors. Persistent current/staging/retired
	// bytes all share the remaining ledger allowance; no per-class reservation.
	reserve := min(baseByteLimit/8, int64(1<<20))
	if reserve < 4096 {
		return nil, ErrFacetBaseCapacity
	}
	return &FacetRuntime{db: db, windows: w, store: shared, check: check,
		baseLimit: baseByteLimit - reserve, registryLimit: reserve, registryBytes: 4096,
		demands: make(map[FacetBaseScope]*facetRuntimeDemand), sources: make(map[string]FacetBaseCapture),
		wake: worker.NewSignal(), shared: worker.NewSignal(), dirty: worker.NewSignal(), now: time.Now, gcPending: true}, nil
}

func (r *FacetRuntime) RegisterSource(source string, capture FacetBaseCapture) error {
	if r == nil || source == "" || len(source) > 256 || strings.Contains(source, ":") || capture == nil {
		return ErrFacetBaseInput
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return readprep.ErrClosed
	}
	if _, ok := r.sources[source]; ok || source == UnrestrictedFacetBaseSource || source == RestrictedMemberFacetBaseSource || source == RestrictedShowFacetBaseSource || source == LinkedDecadeFacetBaseSource {
		return ErrFacetBaseInput
	}
	n := int64(256 + 2*len(source))
	if len(r.sources) >= 8 || n > r.registryLimit-r.registryBytes {
		return ErrFacetBaseCapacity
	}
	r.sources[source] = capture
	r.registryBytes += n
	return nil
}

func (r *FacetRuntime) capture(ctx context.Context, q compactcatalog.ReadQuery, scope FacetBaseScope) (FacetBasePlan, error) {
	r.mu.Lock()
	capture := r.sources[scope.Source]
	if capture == nil {
		for prefix, candidate := range r.sources {
			if strings.HasPrefix(scope.Source, prefix+":") {
				capture = candidate
				break
			}
		}
	}
	r.mu.Unlock()
	if capture != nil {
		return capture(ctx, q, scope)
	}
	switch scope.Source {
	case UnrestrictedFacetBaseSource, RestrictedMemberFacetBaseSource, RestrictedShowFacetBaseSource, LinkedDecadeFacetBaseSource:
		policy, err := parseFacetBasePolicy(scope.Policy)
		if err != nil {
			return FacetBasePlan{}, err
		}
		return CaptureSimpleFacetBasePlan(ctx, q, scope.LibraryID, scope.Library, scope.Field, policy)
	default:
		return FacetBasePlan{}, ErrFacetBaseInput
	}
}

func (r *FacetRuntime) demand(scope FacetBaseScope) (*facetRuntimeDemand, error) {
	if r == nil || !scope.valid() {
		return nil, ErrFacetBaseInput
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return nil, readprep.ErrClosed
	}
	if d := r.demands[scope]; d != nil {
		// Repeated failed requests cannot pin a terminal BASE demand forever.
		if d.err == nil {
			d.touched = r.now()
		}
		return d, nil
	}
	n := int64(1024) + 4*int64(len(scope.Library)+len(scope.Field)+len(scope.Policy)+len(scope.Source))
	if len(r.demands) >= 64 || n > r.registryLimit-r.registryBytes {
		return nil, ErrFacetBaseCapacity
	}
	d := &facetRuntimeDemand{scope: scope, touched: r.now(), waitingSince: r.now(), charge: n, changed: make(chan struct{})}
	r.demands[scope] = d
	r.registryBytes += n
	r.wake.Wake()
	return d, nil
}

func (r *FacetRuntime) active(ctx context.Context, scope FacetBaseScope) (FacetBaseSnapshot, error) {
	var s FacetBaseSnapshot
	err := dbwork.WithReadSnapshot(ctx, r.db, func(ctx context.Context) error {
		q := dbwork.Snapshot(ctx)
		var id int64
		if err := q.QueryRowContext(ctx, `SELECT COALESCE(active_base,0) FROM catalog_facet_base_classes WHERE library_id=? AND library=? AND field=? AND policy=? AND source=?`, scope.LibraryID, scope.Library, scope.Field, scope.Policy, scope.Source).Scan(&id); err != nil {
			return err
		}
		var err error
		s, err = facetBaseSnapshot(ctx, q, id, true)
		if err != nil {
			return err
		}
		// No content-source epoch or current-witness equality check on COMPLETE.
		authority, err := CaptureFacetAuthority(ctx, q, scope)
		if err != nil {
			return err
		}
		if s.Authority != authority {
			return compactcatalog.ErrBuilding
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrFacetBaseUnavailable) {
		err = compactcatalog.ErrBuilding
	}
	return s, err
}

// Demand is an internal BASE provider, not a grant or a query preparation. Its
// caller (including GenreRuntime) must authorize every frontend composition.
func (r *FacetRuntime) Demand(ctx context.Context, scope FacetBaseScope) (FacetBaseSnapshot, error) {
	if r == nil || !scope.valid() {
		return FacetBaseSnapshot{}, ErrFacetBaseInput
	}
	d, admitErr := r.demand(scope)
	s, err := r.active(ctx, scope)
	if err == nil {
		return s, nil
	} // accepted old B remains useful even if demand is full
	if !errors.Is(err, compactcatalog.ErrBuilding) {
		return s, err
	}
	if admitErr != nil {
		return s, admitErr
	}
	r.mu.Lock()
	d.ready = 0
	terminal := d.err
	r.mu.Unlock()
	if terminal != nil {
		return s, terminal
	}
	return s, compactcatalog.ErrBuilding
}

type facetScopeDescriptor struct {
	Scope     FacetBaseScope  `json:"scope"`
	Spec      facetWindowSpec `json:"window"`
	Authority string          `json:"authority"`
	Witness   string          `json:"witness"`
}
type facetScopeSummary struct {
	Base   int64              `json:"base"`
	Window facetWindowSummary `json:"window"`
}

func runtimeFacetRequest(caller readprep.Caller, v Viewer, req FacetRequest) error {
	if caller == "" || req.Library == "" || req.Field == "" || !v.AllowsLibrary(req.Library) {
		return ErrFacetBaseInput
	}
	if req.Query != nil || req.Pivot != "" || req.Group != nil || req.OffsetPresent || req.Offset != 0 || len(req.Q) > 100 {
		return ErrFacetBaseInput
	}
	if req.Field == "actor" || req.Field == "director" || req.Field == "writer" || req.Field == "library" {
		return ErrFacetBaseInput
	}
	return nil
}

func (r *FacetRuntime) Initial(ctx context.Context, caller readprep.Caller, v Viewer, req FacetRequest) (SimpleFacetResult, error) {
	if r == nil {
		return SimpleFacetResult{}, ErrFacetBaseUnavailable
	}
	if err := runtimeFacetRequest(caller, v, req); err != nil {
		return SimpleFacetResult{}, err
	}
	if req.After != "" || req.Before != "" || req.At != "" {
		return SimpleFacetResult{}, ErrFacetBaseInput
	}
	limit := req.Limit
	if limit == 0 && !req.LimitPresent {
		limit = 200
	}
	if limit < 1 || limit > 200 {
		return SimpleFacetResult{}, ErrFacetBaseInput
	}
	var plan FacetBasePlan
	var authority string
	var captureErr error
	err := dbwork.WithReadSnapshot(ctx, r.db, func(ctx context.Context) error {
		q := dbwork.Snapshot(ctx)
		var libraryID int64
		var kind string
		if err := q.QueryRowContext(ctx, `SELECT c.id,l.kind FROM catalog_libraries c JOIN libraries l ON l.id=c.library_id WHERE c.library_id=? AND c.retired=0`, req.Library).Scan(&libraryID, &kind); err != nil {
			return err
		}
		if isReadingLibrary(kind) {
			return ErrFacetBaseInput
		}
		plan, captureErr = CaptureSimpleFacetBasePlan(ctx, q, libraryID, req.Library, req.Field, v.EffectiveRestrictions())
		if !plan.Scope.valid() {
			if captureErr != nil {
				return captureErr
			}
			return ErrFacetBaseInput
		}
		var err error
		authority, err = CaptureFacetAuthority(ctx, q, plan.Scope)
		return err
	})
	if err != nil {
		return SimpleFacetResult{}, err
	}
	pendingSnap := FacetBaseSnapshot{Scope: plan.Scope}
	if err = r.check(ctx, caller, pendingSnap); err != nil {
		return SimpleFacetResult{}, err
	}
	snap, err := r.Demand(ctx, plan.Scope)
	if err == nil {
		if err = r.check(ctx, caller, snap); err != nil {
			return SimpleFacetResult{}, err
		}
		return r.windows.submit(ctx, caller, snap, facetWindowSpec{Needle: sorttext.Fold(req.Q), Limit: limit}, 0)
	}
	if !errors.Is(err, compactcatalog.ErrBuilding) {
		return SimpleFacetResult{}, err
	}
	if captureErr != nil && !errors.Is(captureErr, compactcatalog.ErrBuilding) {
		return SimpleFacetResult{}, captureErr
	}
	descriptor := facetScopeDescriptor{Scope: plan.Scope, Spec: facetWindowSpec{Needle: sorttext.Fold(req.Q), Limit: limit}, Authority: authority, Witness: plan.Witness}
	canonical, err := json.Marshal(descriptor)
	if err != nil {
		return SimpleFacetResult{}, err
	}
	initial := int64(16384 + 4*len(canonical) + len(caller))
	expiryNanos := &atomic.Int64{}
	key := readprep.Key{Namespace: "facet-scope", Basis: "awaiting-base", Canonical: string(canonical)}
	h, err := r.store.Submit(ctx, caller, key, readprep.Plan{InitialBytes: initial, New: func(_ context.Context, b *readprep.Budget) (readprep.Computation, error) {
		if err := b.Resize(initial); err != nil {
			return nil, err
		}
		return &facetScopeComputation{runtime: r, descriptor: descriptor, bytes: initial, caller: caller, expiry: expiryNanos}, nil
	}})
	if err != nil {
		return SimpleFacetResult{}, err
	}
	expiry, err := r.store.LeaseExpiry(ctx, caller, h)
	if err != nil {
		_ = r.store.Cancel(ctx, caller, h)
		return SimpleFacetResult{}, err
	}
	expiryNanos.Store(expiry.UnixNano())
	out, err := r.Poll(ctx, caller, "C"+string(h))
	if err != nil {
		_ = r.store.Cancel(ctx, caller, h)
	}
	return out, err
}

func scopeHandle(id string) (readprep.Handle, bool) {
	if len(id) != 44 || id[0] != 'C' {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(id[1:])
	return readprep.Handle(id[1:]), err == nil && len(raw) == 32
}

func (r *FacetRuntime) Poll(ctx context.Context, caller readprep.Caller, id string) (out SimpleFacetResult, err error) {
	if r == nil {
		return out, ErrFacetBaseUnavailable
	}
	h, cold := scopeHandle(id)
	if !cold {
		return r.windows.Poll(ctx, caller, id)
	}
	result, err := r.store.Poll(ctx, caller, h)
	if err != nil {
		return out, err
	}
	transferred := false
	defer func() {
		if !transferred {
			result.Release()
		}
	}()
	if result.Key.Namespace != "facet-scope" {
		return out, readprep.ErrStale
	}
	if err = result.ResizeDelivery(int64(int(facetWindowDecodeReservation(result.Payload)) + 4*len(result.Key.Canonical))); err != nil {
		return out, err
	}
	var descriptor facetScopeDescriptor
	if json.Unmarshal([]byte(result.Key.Canonical), &descriptor) != nil || !descriptor.Scope.valid() {
		return out, readprep.ErrStale
	}
	snap := FacetBaseSnapshot{Scope: descriptor.Scope}
	if err = r.check(ctx, caller, snap); err != nil {
		_ = r.store.Cancel(ctx, caller, h)
		return out, err
	}
	preparation := SimpleFacetPreparing{Kind: "facet-preparing", State: "preparing", QueryID: id, RetryAfterMs: 1000}
	if result.State == readprep.Preparing {
		if r.now().Sub(result.CreatedAt) >= 10*time.Second {
			fallback, e := r.fallback(ctx, caller, descriptor, result, preparation)
			if e != nil {
				return out, e
			}
			out.Fallback = &fallback
		} else {
			out.Preparing = &preparation
		}
	} else {
		var summary facetScopeSummary
		if json.Unmarshal(result.Payload, &summary) != nil || summary.Base <= 0 {
			return out, readprep.ErrStale
		}
		snap, err = r.windows.authorize(ctx, caller, summary.Base)
		if err != nil {
			_ = r.store.Cancel(ctx, caller, h)
			return out, err
		}
		if snap.Scope != descriptor.Scope || summary.Window.Spec != descriptor.Spec {
			return out, readprep.ErrStale
		}
		token := facetWindowToken{base: snap.ID, handle: h, expires: result.OriginalExpiry.Unix()}
		summary.Window.CompleteMatchIDs = nil
		page, e := r.windows.hydrate(ctx, snap, summary.Window, token, result)
		if e != nil {
			return out, e
		}
		page.Window.QueryID = id
		out.Page = &page
	}
	if err = r.check(ctx, caller, snap); err != nil {
		_ = r.store.Cancel(ctx, caller, h)
		return SimpleFacetResult{}, err
	}
	out.release = result.Release
	transferred = true
	return out, nil
}

func (r *FacetRuntime) Continue(ctx context.Context, caller readprep.Caller, v Viewer, req FacetRequest, token string) (SimpleFacetResult, error) {
	if r == nil {
		return SimpleFacetResult{}, ErrFacetBaseUnavailable
	}
	if err := runtimeFacetRequest(caller, v, req); err != nil {
		return SimpleFacetResult{}, err
	}
	t, err := r.windows.decode(token)
	if err != nil || t.row <= 0 || t.direction < 1 || t.direction > 3 {
		return SimpleFacetResult{}, readprep.ErrStale
	}
	n := 0
	for i, value := range []string{req.After, req.Before, req.At} {
		if value != "" {
			n++
			if value != token || byte(i+1) != t.direction {
				return SimpleFacetResult{}, ErrFacetBaseInput
			}
		}
	}
	if n > 1 {
		return SimpleFacetResult{}, ErrFacetBaseInput
	}
	result, err := r.store.Poll(ctx, caller, t.handle)
	if err != nil {
		return SimpleFacetResult{}, err
	}
	defer result.Release()
	if result.State != readprep.Ready {
		return SimpleFacetResult{}, readprep.ErrStale
	}
	if err = result.ResizeDelivery(int64(int(facetWindowDecodeReservation(result.Payload)) + 4*len(result.Key.Canonical))); err != nil {
		return SimpleFacetResult{}, err
	}
	var scope FacetBaseScope
	var spec facetWindowSpec
	switch result.Key.Namespace {
	case "facet-scope":
		var d facetScopeDescriptor
		var s facetScopeSummary
		if json.Unmarshal([]byte(result.Key.Canonical), &d) != nil || json.Unmarshal(result.Payload, &s) != nil || s.Base != t.base || s.Window.Spec != d.Spec {
			return SimpleFacetResult{}, readprep.ErrStale
		}
		scope, spec = d.Scope, d.Spec
	case "simple-facets-v1":
		var d struct {
			Scope  FacetBaseScope
			Window facetWindowSpec
		}
		if json.Unmarshal([]byte(result.Key.Canonical), &d) != nil || result.Basis != strconv.FormatInt(t.base, 10) {
			return SimpleFacetResult{}, readprep.ErrStale
		}
		scope, spec = d.Scope, d.Window
	default:
		return SimpleFacetResult{}, readprep.ErrStale
	}
	if scope.Library != req.Library || scope.Field != req.Field || scope.Policy != FacetBasePolicy(v.EffectiveRestrictions()) || spec.Needle != sorttext.Fold(req.Q) || req.LimitPresent && req.Limit != spec.Limit {
		return SimpleFacetResult{}, readprep.ErrStale
	}
	snap, err := r.windows.authorize(ctx, caller, t.base)
	if err != nil {
		return SimpleFacetResult{}, err
	}
	if snap.Scope != scope {
		return SimpleFacetResult{}, readprep.ErrStale
	}
	var present bool
	if err = dbwork.QueryRow(ctx, r.db, `SELECT EXISTS(SELECT 1 FROM catalog_facet_base_rows WHERE base_id=? AND value_id=?)`, t.base, t.row).Scan(&present); err != nil {
		return SimpleFacetResult{}, err
	}
	if !present {
		return SimpleFacetResult{}, readprep.ErrStale
	}
	spec.Anchor, spec.Direction = t.row, t.direction
	return r.windows.submit(ctx, caller, snap, spec, t.expires)
}

func (r *FacetRuntime) Cancel(ctx context.Context, caller readprep.Caller, id string) error {
	if r == nil {
		return nil
	}
	if h, ok := scopeHandle(id); ok {
		return r.store.Cancel(ctx, caller, h)
	}
	return r.windows.Cancel(ctx, caller, id)
}

type facetScopeComputation struct {
	runtime    *FacetRuntime
	descriptor facetScopeDescriptor
	scan       *facetWindowScan
	caller     readprep.Caller
	expiry     *atomic.Int64
	bytes      int64
}

func (c *facetScopeComputation) AccountedBytes() int64 { return c.bytes }
func (c *facetScopeComputation) Step(ctx context.Context, budget *readprep.Budget) (readprep.StepResult, error) {
	if err := budget.Resize(c.bytes); err != nil {
		return readprep.StepResult{}, err
	}
	if c.scan == nil {
		d, err := c.runtime.demand(c.descriptor.Scope)
		if err != nil {
			return readprep.StepResult{}, runtimePreparationError(err)
		}
		c.runtime.mu.Lock()
		id, terminal, changed := d.ready, d.err, d.changed
		c.runtime.mu.Unlock()
		if terminal != nil {
			return readprep.StepResult{}, runtimePreparationError(terminal)
		}
		if id == 0 {
			// A bounded cooperative wait uses the existing store worker. No new query
			// scheduler/goroutine, no held snapshot and no busy database polling.
			timer := time.NewTimer(8 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return readprep.StepResult{}, ctx.Err()
			case <-changed:
			case <-timer.C:
			}
			return readprep.StepResult{}, nil
		}
		snap, err := facetBaseSnapshot(ctx, dbwork.ReadHandle(ctx, c.runtime.db), id, true)
		if err != nil {
			return readprep.StepResult{}, readprep.ErrStale
		}
		c.scan = newFacetWindowScan(c.runtime.windows, c.caller, snap, c.descriptor.Spec, c.bytes, c.expiry)
	}
	result, err := c.scan.Step(ctx, budget)
	c.bytes = c.scan.bytes
	if err != nil || !result.Done {
		return result, err
	}
	if err = budget.Resize(c.bytes + facetWindowDecodeReservation(result.Payload) + facetWindowPrefixEncodeBytes); err != nil {
		return readprep.StepResult{}, err
	}
	c.bytes += facetWindowDecodeReservation(result.Payload) + facetWindowPrefixEncodeBytes
	var summary facetWindowSummary
	if err = json.Unmarshal(result.Payload, &summary); err != nil {
		return readprep.StepResult{}, err
	}
	payload, err := json.Marshal(facetScopeSummary{Base: c.scan.base, Window: summary})
	if err != nil {
		return readprep.StepResult{}, err
	}
	return readprep.StepResult{Done: true, Payload: payload}, nil
}
func runtimePreparationError(err error) error {
	if errors.Is(err, ErrFacetBaseCapacity) {
		return readprep.ErrCapacity
	}
	return err
}

// Run is supervised by the composition root. There is one BASE worker and the
// existing global writer gate; readprep retains its own shared read-only workers.
func (r *FacetRuntime) Run(ctx context.Context) error {
	if r == nil || !r.running.CompareAndSwap(false, true) {
		return ErrFacetBaseInput
	}
	defer r.stop()
	r.mu.Lock()
	r.started = r.now()
	r.mu.Unlock()
	off := dbwork.WakeOnTables(r.shared, facetRuntimeWakeTables...)
	offDirty := dbwork.WakeOnTables(r.dirty, facetRuntimeWakeTables...)
	defer off()
	defer offDirty()
	r.dirty.Wake()
	runctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var fatal error
	worker.RunWith(runctx, "facet-runtime", r.wake, r.shared, func(ctx context.Context) time.Duration {
		started := time.Now()
		r.cooperateWithColdBuild(ctx)
		if err := r.writerReady(ctx); err != nil {
			if errors.Is(err, compactcatalog.ErrFacetProducerFailed) {
				r.failUnpreparedScopes()
			}
			if errors.Is(err, errFacetRuntimeDeferred) {
				return 2 * time.Second
			}
			if ctx.Err() != nil {
				return 0
			}
			if facetRuntimeTemporary(err) {
				return time.Second
			}
			fatal = err
			cancel()
			return 0
		}
		d, busy := r.schedule(r.now())
		wait := time.Duration(0)
		if d != nil {
			if dbwork.CatalogIngestWindowActive(r.db) {
				wait = r.maintainColdBurst(ctx, d)
			} else {
				wait = r.maintain(ctx, d)
			}
		}
		if err := r.collect(ctx); err != nil {
			if ctx.Err() != nil {
				return 0
			}
			if errors.Is(err, errFacetRuntimeDeferred) {
				return 2 * time.Second
			}
			if facetRuntimeTemporary(err) {
				return time.Second
			}
			fatal = err
			cancel()
			return 0
		}
		if dbwork.CatalogIngestWindowActive(r.db) {
			// Every slice releases the writer gate. Skip quiet-time sleeps during the
			// finite window, but return immediately when a user is waiting.
			if dbwork.WriteGate().Admission().UserQueued > 0 {
				return time.Millisecond
			}
			if wait > time.Millisecond {
				return wait
			}
			return time.Nanosecond
		}
		if !dbwork.PaceBackground(ctx, started) {
			return 0
		}
		if busy {
			return time.Millisecond
		}
		if r.gcPending && (wait == 0 || wait > 10*time.Millisecond) {
			wait = 10 * time.Millisecond
		}
		r.mu.Lock()
		for _, pending := range r.demands {
			if pending.retiring > 0 || pending.err == nil && (pending.build > 0 || pending.ready == 0 || !pending.next.IsZero()) {
				due := max(time.Millisecond, pending.next.Sub(r.now()))
				if wait == 0 || due < wait {
					wait = due
				}
			}
		}
		r.mu.Unlock()
		return wait
	})
	if fatal != nil {
		return fatal
	}
	return ctx.Err()
}

// facetRuntimeWakeTables is the producer-family table set this worker observes.
// WakeOnTables matches every table a committed statement NAMED, so this list also
// names the roots of the runtime's own reads: a committed BASE quantum wakes the
// very same dirty signal it later consumes. The wake is therefore "look again",
// never "the source changed" — see schedule.
var facetRuntimeWakeTables = []string{"jobs", "catalog_dirty", "catalog_facet_counts", "catalog_facet_members", "catalog_show_facet_values", "catalog_facet_source_state", "catalog_generations", "catalog_derivations", "catalog_derivation_failures", "catalog_item_attribute_edges", "catalog_attribute_terms", "content_rating_ages", "catalog_upstream_sources"}

// schedule performs one bounded scheduling step: the readable producer/policy
// fence is compared with the state the retained ready bases were last verified
// against, then exactly one due demand is chosen. A wake is only a nudge — it
// carries no change of its own, because WakeOnTables matches the READ roots of
// this runtime's own quanta — so an unchanged fence re-arms nothing and a
// self-woken quantum proceeds straight to its next quantum instead of spending
// the following len(demands)-1 turns re-auditing ready bases. A changed fence
// (or a family observed mid-change) re-verifies every ready idle basis once.
//
// Invariant: swept advances ONLY here. writerReady may refresh r.fence again
// later in the same turn (maintain/collect/writeBase each run the guard), but a
// fence that moved after this comparison still differs from swept at the next
// turn, so the change is re-armed then. An audit can clear only its own demand's
// verify — it can never clear the next turn's requirement, because that
// requirement is derived from the fence and not from a per-demand stamp.
func (r *FacetRuntime) schedule(now time.Time) (*facetRuntimeDemand, bool) {
	r.mu.Lock()
	// Consume the pending wake for its own sake; the comparison below, not the
	// wake, decides whether anything must be re-verified. A change that landed
	// between the turn-start read and this take is therefore still queued.
	r.dirty.Take()
	if !r.known {
		// The turn-start guard could not read the producer/policy state. Keep the
		// requirement so the first readable turn still re-verifies every ready
		// basis.
		r.moved = true
	} else if r.moved || !r.sweptSet || r.fence != r.swept {
		r.sweptSet, r.swept, r.moved = true, r.fence, false
		for _, d := range r.demands {
			// A terminal demand is never re-armed: its fixed error is the whole
			// answer, no audit can change it, and arming it would keep a 1 ms
			// queue alive forever. Only retirement still has work to do.
			if d.err == nil && d.ready != 0 && d.build == 0 && d.retiring == 0 && d.next.IsZero() {
				d.verify = true
			}
		}
	}
	busy := false
	for _, d := range r.demands {
		if d.due(now) {
			busy = true
			break
		}
	}
	r.mu.Unlock()
	return r.nextDemand(!busy), busy
}

// due reports whether the next turn can make progress on this demand now. A
// future quiet gap, retry or fixed terminal result is not progress: it must keep
// the worker asleep for exactly the wait the turn computes instead of spinning.
// Retirement is checked FIRST: a terminal demand that is still reclaiming its
// retired image must keep progressing, but it must never be due for anything
// else — in particular never for a re-verification it can no longer use.
func (d *facetRuntimeDemand) due(now time.Time) bool {
	if d.retiring > 0 {
		return true
	}
	if d.err != nil {
		return false
	}
	if !d.next.IsZero() && now.Before(d.next) {
		return false
	}
	return d.verify || d.build > 0 || d.ready == 0 || !d.next.IsZero()
}

func (r *FacetRuntime) nextDemand(pendingOnly ...bool) *facetRuntimeDemand {
	r.mu.Lock()
	defer r.mu.Unlock()
	if focus := r.ingestDemand; focus != nil && focus.ready == 0 && focus.err == nil && dbwork.CatalogIngestWindowActive(r.db) {
		r.turn++
		focus.turn = r.turn
		return focus
	}
	now := r.now()
	var selected *facetRuntimeDemand
	for scope, d := range r.demands {
		if now.Sub(d.touched) >= 5*time.Minute {
			delete(r.demands, scope)
			r.registryBytes -= d.charge
			r.restartGC()
			close(d.changed)
			continue
		}
		if d.ready != 0 && d.build == 0 && d.retiring == 0 && d.next.IsZero() && !d.verify {
			// A ready, idle basis already verified at the current producer/policy
			// state has nothing to do. Skipping it here — not only on a quiet turn —
			// is what stops a build quantum from being followed by len(demands)-1
			// no-op audits before the next quantum.
			continue
		}
		if len(pendingOnly) > 0 && pendingOnly[0] && d.err != nil && d.retiring == 0 {
			continue
		}
		if selected == nil || d.turn < selected.turn {
			selected = d
		}
	}
	if selected != nil {
		r.turn++
		selected.turn = r.turn
	}
	return selected
}

// A bounded read snapshot may expire while the process context is still live.
// Retry that quantum after releasing it; it must not stop the process worker or
// discard a durable build. Corruption/input/capacity errors keep their existing
// terminal handling, and process cancellation still exits Run directly.
func facetRuntimeTemporary(err error) bool {
	return dbwork.Overloaded(err) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, dbwork.ErrReadPressure)
}

func runtimeTerminal(err error) error {
	switch {
	case errors.Is(err, ErrFacetBaseCapacity):
		return ErrFacetBaseCapacity
	case errors.Is(err, ErrFacetBaseInput):
		return ErrFacetBaseInput
	case errors.Is(err, readprep.ErrClosed):
		return readprep.ErrClosed
	default:
		return ErrFacetBaseUnavailable
	}
}
func (r *FacetRuntime) notify(d *facetRuntimeDemand) {
	close(d.changed)
	d.changed = make(chan struct{})
}

func (r *FacetRuntime) maintain(ctx context.Context, d *facetRuntimeDemand) time.Duration {
	r.cooperateWithColdBuild(ctx)
	if err := r.writerReady(ctx); err != nil {
		if errors.Is(err, compactcatalog.ErrFacetProducerFailed) {
			r.failUnpreparedScopes()
		}
		return 2 * time.Second
	}
	r.mu.Lock()
	build, retiring, next, terminal := d.build, d.retiring, d.next, d.err
	r.mu.Unlock()
	if retiring > 0 {
		done := false
		err := r.writeBase(ctx, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			done, err = r.retire(ctx, tx, retiring)
			return err
		})
		if err != nil {
			return time.Second
		}
		if done {
			r.mu.Lock()
			if d.retiring == retiring {
				d.retiring = 0
			}
			r.notify(d)
			r.mu.Unlock()
		}
		return time.Millisecond
	}
	if terminal != nil {
		return 0
	}
	if r.now().Before(next) {
		return max(time.Millisecond, next.Sub(r.now()))
	}
	var plan FacetBasePlan
	var authority string
	err := dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), r.db, func(ctx context.Context) error {
		var err error
		q := dbwork.Snapshot(ctx)
		plan, err = r.capture(ctx, q, d.scope)
		if err != nil {
			return err
		}
		if plan.Scope != d.scope || plan.Producer == nil || plan.Witness == "" {
			return ErrFacetBaseInput
		}
		authority, err = CaptureFacetAuthority(ctx, q, plan.Scope)
		return err
	})
	if err != nil {
		return r.maintenanceFailure(ctx, d, build, err)
	}
	if build == 0 {
		active, activeErr := r.active(ctx, d.scope)
		if activeErr == nil && active.Witness == plan.Witness && active.Authority == authority {
			r.mu.Lock()
			d.ready = active.ID
			d.gap = 0
			d.next = time.Time{}
			// The audit proved the retained basis is current at this producer AND
			// authority state, so it is verified; a later change re-arms it.
			d.verify = false
			r.notify(d)
			r.mu.Unlock()
			return 0
		}
		if activeErr != nil && !errors.Is(activeErr, compactcatalog.ErrBuilding) {
			return r.maintenanceFailure(ctx, d, 0, activeErr)
		}
		// No read snapshot survives writer admission. Revalidate source and narrow
		// authority inside this same gated transaction before installing BASE work.
		var id int64
		var accepted bool
		err = r.writeBase(ctx, func(ctx context.Context, tx *sql.Tx) error {
			current, err := r.capture(ctx, tx, d.scope)
			if err != nil {
				return err
			}
			currentAuthority, err := CaptureFacetAuthority(ctx, tx, d.scope)
			if err != nil {
				return err
			}
			if current.Scope != plan.Scope || current.Witness != plan.Witness || currentAuthority != authority {
				return compactcatalog.ErrFacetProducerChanged
			}
			b := FacetBases{DB: r.db, Producer: plan.Producer, MaxBytes: r.baseLimit}
			id, err = b.Begin(ctx, tx, d.scope, plan.Witness, authority)
			if err != nil {
				return err
			}
			var complete bool
			if err = tx.QueryRowContext(ctx, `SELECT complete FROM catalog_facet_bases WHERE id=?`, id).Scan(&complete); err != nil {
				return err
			}
			if complete {
				_, err = tx.ExecContext(ctx, `UPDATE catalog_facet_base_classes SET active_base=? WHERE id=(SELECT class_id FROM catalog_facet_bases WHERE id=?)`, id, id)
				accepted = err == nil
			}
			return err
		})
		if err != nil {
			return r.maintenanceFailure(ctx, d, 0, err)
		}
		r.mu.Lock()
		// A replacement build supersedes any pending re-verification of the basis
		// it replaces.
		d.verify = false
		if accepted {
			d.ready = id
			d.gap = 0
			d.next = time.Time{}
		} else {
			d.build = id
		}
		r.notify(d)
		r.mu.Unlock()
		if accepted {
			return 0
		}
		return time.Millisecond
	}
	done := false
	advanced := uint64(0)
	err = r.writeBase(ctx, func(ctx context.Context, tx *sql.Tx) error {
		snap, err := facetBaseSnapshot(ctx, tx, build, false)
		if err != nil {
			return err
		}
		if snap.Scope != plan.Scope || snap.Witness != plan.Witness || snap.Authority != authority {
			return compactcatalog.ErrFacetProducerChanged
		}
		currentAuthority, err := CaptureFacetAuthority(ctx, tx, d.scope)
		if err != nil {
			return err
		}
		if currentAuthority != snap.Authority {
			return compactcatalog.ErrFacetProducerChanged
		}
		b := FacetBases{DB: r.db, Producer: plan.Producer, MaxBytes: r.baseLimit}
		if d.scope.Source == RestrictedMemberFacetBaseSource && dbwork.CatalogIngestWindowActive(r.db) {
			var witness restrictedFacetWitness
			if json.Unmarshal([]byte(snap.Witness), &witness) != nil {
				return ErrFacetBaseInput
			}
			if err = compactcatalog.ValidateFacetProducerFence(ctx, tx, witness.Fence); err != nil {
				return err
			}
			ctx = context.WithValue(ctx, facetColdSourceKey{}, &facetColdSourceBudget{raw: 512, bytes: 256 << 10, started: time.Now(), tx: tx, fence: witness.Fence})
		}
		done, err = b.Step(ctx, tx, build)
		if err != nil {
			return err
		}
		advanced++
		if budget := facetColdSource(ctx); budget != nil {
			advanced = uint64(max(1, budget.used))
		}

		// Additional source rows (for example genre candidate-only final phases)
		// also share the ledger, including a Done batch with no counted facts.
		var used int64
		if err = tx.QueryRowContext(ctx, `SELECT used_bytes FROM catalog_facet_base_usage WHERE id=1`).Scan(&used); err != nil {
			return err
		}
		if used > r.baseLimit {
			return ErrFacetBaseCapacity
		}
		return nil
	})
	if err != nil {
		return r.maintenanceFailure(ctx, d, build, err)
	}
	r.mu.Lock()
	token := r.ingestWindow
	if token != 0 && d.ingestWindow == token {
		d.ingestSteps += advanced
	}
	r.mu.Unlock()
	dbwork.ProgressCatalogIngestWindow(r.db, token)
	if done {
		r.mu.Lock()
		d.ready = build
		d.build = 0
		d.gap = 0
		d.next = time.Time{}
		// The completed basis was validated against the producer/authority state
		// inside its own writer transaction.
		d.verify = false
		r.notify(d)
		r.mu.Unlock()
		return 0
	}
	return time.Millisecond
}

func (r *FacetRuntime) maintenanceFailure(ctx context.Context, d *facetRuntimeDemand, build int64, err error) time.Duration {
	if errors.Is(err, errFacetRuntimeDeferred) {
		return 2 * time.Second
	}
	if ctx.Err() != nil || facetRuntimeTemporary(err) {
		return time.Second
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if build > 0 {
		d.build = 0
		d.retiring = build
	}
	if errors.Is(err, compactcatalog.ErrBuilding) || errors.Is(err, compactcatalog.ErrFacetProducerChanged) {
		// A source change after work started restarts only after a quiet gap. An
		// already-building upstream source before Begin merely waits for readiness.
		if build > 0 || errors.Is(err, compactcatalog.ErrFacetProducerChanged) {
			if d.gap == 0 {
				d.gap = 2 * time.Second
			} else {
				d.gap = min(2*d.gap, 2*time.Minute)
			}
			d.next = r.now().Add(d.gap)
		} else if d.gap > 0 {
			d.next = r.now().Add(d.gap)
		} else {
			d.next = r.now().Add(100 * time.Millisecond)
		}
		r.notify(d)
		return max(time.Millisecond, d.next.Sub(r.now()))
	}
	d.err = runtimeTerminal(err)
	// A fixed terminal result supersedes any outstanding re-verification; leaving
	// d.verify set would be a requirement nothing can ever discharge.
	d.verify = false
	if errors.Is(err, ErrFacetBaseCapacity) {
		r.restartGC()
	}
	r.notify(d)
	return time.Millisecond
}

// collect walks raw identities and performs at most ONE small GC quantum per
// pass. It never uses DELETE cascades or a filtered LIMIT scanning for orphans.
// COMPLETE old bases are retained while a class is demanded unless space is
// under pressure. Capacity eviction is explicit stale, never a mixed basis.
func (r *FacetRuntime) collect(ctx context.Context) error {
	// No GC phase can write unless a phase is pending, so the quiet path keeps
	// its old cost: no writer guard, no producer fence and no read snapshot on
	// every idle turn. The turn-start guard still owns admission errors.
	if !r.gcPending {
		return nil
	}
	if err := r.writerReady(ctx); err != nil {
		if errors.Is(err, errFacetRuntimeDeferred) {
			return nil
		}
		return err
	}
	phase := r.gcPhase
	r.gcPhase = (r.gcPhase + 1) % 4
	if r.gcComplete&(1<<phase) != 0 {
		return nil
	}
	b := FacetBases{DB: r.db, MaxBytes: r.baseLimit}
	if phase == 1 || phase == 2 {
		var next int64
		var done bool
		err := r.writeBase(ctx, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			if phase == 1 {
				next, done, err = b.CollectTextStep(ctx, tx, r.gcText)
			} else {
				next, done, err = b.CollectValueStep(ctx, tx, r.gcValue)
			}
			return err
		})
		if err == nil {
			if done {
				next = 0
			}
			if phase == 1 {
				r.gcText = next
			} else {
				r.gcValue = next
			}
		}
		if err == nil && done {
			r.finishGC(phase)
		}
		return err
	}
	if phase == 3 {
		var id int64
		var scope FacetBaseScope
		err := dbwork.QueryRow(ctx, r.db, `SELECT id,library_id,library,field,policy,source FROM catalog_facet_base_classes WHERE id>? ORDER BY id LIMIT 1`, r.gcClass).Scan(&id, &scope.LibraryID, &scope.Library, &scope.Field, &scope.Policy, &scope.Source)
		if errors.Is(err, sql.ErrNoRows) {
			r.gcClass = 0
			r.finishGC(phase)
			return nil
		}
		if err != nil {
			return err
		}
		r.gcClass = id
		r.mu.Lock()
		wanted := r.demands[scope] != nil
		r.mu.Unlock()
		if wanted {
			return nil
		}
		return r.writeBase(ctx, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM catalog_facet_base_classes WHERE id=? AND active_base IS NULL AND NOT EXISTS(SELECT 1 FROM catalog_facet_bases WHERE class_id=?)`, id, id)
			return err
		})
	}
	var id, active, used int64
	var complete bool
	var scope FacetBaseScope
	err := dbwork.QueryRow(ctx, r.db, `SELECT b.id,COALESCE(c.active_base,0),b.complete,c.library_id,c.library,c.field,c.policy,c.source,u.used_bytes FROM catalog_facet_bases b JOIN catalog_facet_base_classes c ON c.id=b.class_id CROSS JOIN catalog_facet_base_usage u WHERE b.id>? AND u.id=1 ORDER BY b.id LIMIT 1`, r.gcBase).Scan(&id, &active, &complete, &scope.LibraryID, &scope.Library, &scope.Field, &scope.Policy, &scope.Source, &used)
	if errors.Is(err, sql.ErrNoRows) {
		r.gcBase = 0
		r.finishGC(phase)
		return nil
	}
	if err != nil {
		return err
	}
	r.mu.Lock()
	d := r.demands[scope]
	wanted := d != nil && r.now().Sub(d.touched) < 5*time.Minute
	working := d != nil && (d.build == id || d.retiring == id)
	grace := r.now().Sub(r.started) < 5*time.Minute
	r.mu.Unlock()
	if working || active == id && (wanted || grace) || complete && wanted && used < r.baseLimit*4/5 {
		r.gcBase = id
		return nil
	}
	done := false
	err = r.writeBase(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if active == id {
			if _, err := tx.ExecContext(ctx, `UPDATE catalog_facet_base_classes SET active_base=NULL WHERE active_base=?`, id); err != nil {
				return err
			}
		}
		var err error
		done, err = r.retire(ctx, tx, id)
		return err
	})
	if err == nil && done {
		r.gcBase = id
		// A wanted basis can be reclaimed under capacity pressure. Re-arm exactly
		// that demand's audit so the next turn repairs it in the normal
		// audit → ErrBuilding → rebuild path, instead of leaving a dead id ready.
		r.mu.Lock()
		if d := r.demands[scope]; d != nil && d.err == nil && (d.ready == id || d.build == id) {
			d.verify = true
		}
		r.mu.Unlock()
	}
	return err
}

func (r *FacetRuntime) fallback(ctx context.Context, caller readprep.Caller, d facetScopeDescriptor, result readprep.Result, preparation SimpleFacetPreparing) (SimpleFacetFallbackPage, error) {
	empty := SimpleFacetFallbackPage{Kind: "facet-fallback", FacetPage: FacetPage{Field: d.Scope.Field, TotalIsLowerBound: true, Values: []FacetValue{}}, Preparation: preparation}
	r.mu.Lock()
	demand := r.demands[d.Scope]
	var id int64
	if demand != nil {
		id = demand.build
		if id == 0 {
			id = demand.ready
		}
	}
	r.mu.Unlock()
	if id == 0 {
		return empty, nil
	}
	out := empty
	var actual FacetBaseSnapshot
	// The width read, reservation and hydration share ONE short bounded snapshot.
	// Retiring/rewriting staging rows cannot swap a larger string after admission.
	err := dbwork.WithReadSnapshot(ctx, r.db, func(ctx context.Context) error {
		q := dbwork.Snapshot(ctx)
		var err error
		actual, err = facetBaseSnapshot(ctx, q, id, false)
		if errors.Is(err, ErrFacetBaseUnavailable) {
			actual, err = facetBaseSnapshot(ctx, q, id, true)
		}
		if err != nil {
			return err
		}
		if actual.Scope != d.Scope {
			return readprep.ErrStale
		}
		if err = r.check(ctx, caller, actual); err != nil {
			return err
		}
		rows, err := q.QueryContext(ctx, `SELECT b.value_id,6*(length(CAST(v.value AS BLOB))+length(CAST(x.label AS BLOB)))+128,instr(CAST(x.folded AS BLOB),CAST(? AS BLOB))>0 FROM catalog_facet_base_rows b INDEXED BY catalog_facet_base_order JOIN catalog_facet_base_values v ON v.id=b.value_id JOIN catalog_facet_base_texts x ON x.id=b.text_id WHERE b.base_id=? ORDER BY `+facetWindowOrder+` LIMIT ?`, d.Spec.Needle, id, d.Spec.Limit)
		if err != nil {
			return err
		}
		ids := make([]int64, 0, d.Spec.Limit)
		used := int64(0)
		for rows.Next() {
			var value, width int64
			var match bool
			if err = rows.Scan(&value, &width, &match); err != nil {
				rows.Close()
				return err
			}
			if !match {
				continue
			}
			if len(ids) > 0 && used+width > 256<<10 {
				break
			}
			ids = append(ids, value)
			used += width
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if err = result.Reserve(32768 + 5*used); err != nil {
			if !(errors.Is(err, readprep.ErrCapacity) && len(ids) == 1 && (used-128)/6 > 256<<10) {
				return err
			}
		}
		out.Revision = strconv.FormatInt(id, 10)
		for _, value := range ids {
			var f FacetValue
			var count int64
			if err = q.QueryRowContext(ctx, `SELECT v.value,x.label,-b.negative_count FROM catalog_facet_base_rows b JOIN catalog_facet_base_values v ON v.id=b.value_id JOIN catalog_facet_base_texts x ON x.id=b.text_id WHERE b.base_id=? AND b.value_id=?`, id, value).Scan(&f.Value, &f.Label, &count); err != nil {
				return err
			}
			if count < 1 || count > 9007199254740991 || int64(int(count)) != count {
				return ErrFacetBaseInput
			}
			f.Count = int(count)
			f.CountIsLowerBound = true
			out.Values = append(out.Values, f)
		}
		out.Total = len(out.Values)
		if d.Spec.Needle == "" {
			if actual.Total > 9007199254740991 || int64(int(actual.Total)) != actual.Total {
				return ErrFacetBaseInput
			}
			out.Total = int(actual.Total)
		}
		return nil
	})
	if errors.Is(err, ErrFacetBaseUnavailable) || errors.Is(err, sql.ErrNoRows) {
		return empty, nil
	}
	if err != nil {
		return empty, err
	}
	// Fresh post-snapshot authority catches revocation during hydration.
	if err = r.check(ctx, caller, actual); err != nil {
		return empty, err
	}
	return out, nil
}

// Additional source rows use the same writer/ledger and must retire before
// their parent base. Implemented by sources with bounded candidate tables.
type FacetBaseRetirer interface {
	RetireFacetBaseStep(context.Context, *sql.Tx, FacetBaseSnapshot) (bool, error)
}

func (r *FacetRuntime) retire(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	if _, err := facetRuntimeWriterReadyDuring(ctx, tx, dbwork.CatalogIngestWindowActive(r.db)); err != nil {
		return false, err
	}
	r.gcPending = true
	r.gcComplete &^= 14
	r.gcText, r.gcValue, r.gcClass = 0, 0, 0
	snap, err := facetBaseSnapshot(ctx, tx, id, false)
	if errors.Is(err, ErrFacetBaseUnavailable) {
		snap, err = facetBaseSnapshot(ctx, tx, id, true)
	}
	if errors.Is(err, ErrFacetBaseUnavailable) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	plan, _ := r.capture(ctx, tx, snap.Scope)
	if source, ok := plan.Producer.(FacetBaseRetirer); ok {
		var active bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_facet_base_classes WHERE active_base=?)`, id).Scan(&active); err != nil {
			return false, err
		}
		if active {
			return false, ErrFacetBaseInput
		}
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_facet_bases SET complete=0 WHERE id=?`, id); err != nil {
			return false, err
		}
		done, err := source.RetireFacetBaseStep(ctx, tx, snap)
		if err != nil || !done {
			return false, err
		}
	}
	b := FacetBases{DB: r.db, MaxBytes: r.baseLimit}
	return b.RetireStep(ctx, tx, id)
}

func (r *FacetRuntime) restartGC() {
	r.gcPending = true
	r.gcComplete = 0
	r.gcBase = 0
	r.gcText = 0
	r.gcValue = 0
	r.gcClass = 0
}
func (r *FacetRuntime) finishGC(phase int) {
	r.gcComplete |= 1 << phase
	if r.gcComplete == 15 {
		r.gcPending = false
	}
}

// A source-idle predicate on the existing scheduler/gate, not another admission
// system. Check before admission and again in the acquired transaction. A scan
// admitted in the interval can therefore cause no derived mutation.
var errFacetRuntimeDeferred = errors.New("facet BASE writes deferred for scan/source work")

func facetRuntimeWriterReady(ctx context.Context, q compactcatalog.ReadQuery) error {
	_, err := facetRuntimeWriterReadyDuring(ctx, q, false)
	return err
}

// facetRuntimeWriterReadyDuring is the ONE writer-activation guard: a scan/ingest
// state plus the real producer-family fence. It returns the fence it captured so
// the turn scheduler can key its sweep to the same snapshot instead of repeating
// the query. The fence alone is a CONTENT barrier; live per-scope authority is
// bound separately (see writerReady).
func facetRuntimeWriterReadyDuring(ctx context.Context, q compactcatalog.ReadQuery, ingestHeld bool) (compactcatalog.FacetProducerFence, error) {
	var active bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jobs INDEXED BY catalog_facet_active_scans WHERE status IN('queued','running'))`).Scan(&active); err != nil {
		return compactcatalog.FacetProducerFence{}, err
	}
	if active && !ingestHeld {
		return compactcatalog.FacetProducerFence{}, errFacetRuntimeDeferred
	}
	fence, err := compactcatalog.CaptureFacetProducerFence(ctx, q)
	if errors.Is(err, compactcatalog.ErrBuilding) || errors.Is(err, compactcatalog.ErrFacetProducerFailed) {
		return fence, errors.Join(errFacetRuntimeDeferred, err)
	}
	return fence, err
}

// writerReady records the exact producer fence AND the authority generations
// captured in the same turn-start snapshot. CaptureFacetProducerFence excludes
// per-scope/live authority by design, and CaptureFacetAuthority binds
// policy_epoch/genre_epoch (plus library kind/retirement, which the source-epoch
// triggers also move), so a policy-only change must reopen the sweep on its own.
// A guard that could not be read is never treated as "nothing changed".
func (r *FacetRuntime) writerReady(ctx context.Context) error {
	var key facetRuntimeFence
	err := dbwork.WithReadSnapshot(dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia), r.db, func(ctx context.Context) error {
		q := dbwork.Snapshot(ctx)
		fence, err := facetRuntimeWriterReadyDuring(ctx, q, dbwork.CatalogIngestWindowActive(r.db))
		if err != nil {
			return err
		}
		if err = q.QueryRowContext(ctx, `SELECT policy_epoch,genre_epoch FROM catalog_facet_source_state WHERE singleton=1`).Scan(&key.policy, &key.genre); err != nil {
			return err
		}
		key.producer = fence
		return nil
	})
	r.mu.Lock()
	if err != nil {
		// A family observed mid-change (pending stage, failure, scan hold or a
		// transient read error) cannot be compared yet: the next readable turn
		// re-verifies every ready basis even if the counters come back unchanged.
		r.known = false
		r.moved = true
	} else {
		r.fence, r.known = key, true
	}
	r.mu.Unlock()
	return err
}
func (r *FacetRuntime) writeBase(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
	if err := r.writerReady(ctx); err != nil {
		return err
	}
	return dbwork.WithWriteTxContext(ctx, r.db, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := facetRuntimeWriterReadyDuring(ctx, tx, dbwork.CatalogIngestWindowActive(r.db)); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}
func (r *FacetRuntime) failUnpreparedScopes() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range r.demands {
		if d.ready == 0 && d.err == nil {
			d.err = ErrFacetBaseUnavailable
			d.verify = false
			r.notify(d)
		}
	}
}

// This defer also runs before the composition root's supervisor recovers a
// producer panic. The runtime cannot be restarted with partial in-memory state.
func (r *FacetRuntime) stop() {
	dbwork.ForgetCatalogIngestWindow(r.db, r.ingestWindow)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	for _, d := range r.demands {
		d.err = readprep.ErrClosed
		d.verify = false
		r.notify(d)
	}
}

// The quiet-gap protocol remains the common path. Only a cold maintained class
// still waiting after three minutes asks the existing writer scheduler for a
// progress-renewed source-ingest window. Live users, playback and revocations stay live.
func (r *FacetRuntime) cooperateWithColdBuild(ctx context.Context) {
	threshold := r.starveAfter
	if threshold <= 0 {
		threshold = 3 * time.Minute
	}
	hold := r.ingestHold
	if hold <= 0 {
		hold = dbwork.CatalogIngestWindowLimit
	}
	now := r.now()
	r.mu.Lock()
	var cold *facetRuntimeDemand
	pending := false
	for _, d := range r.demands {
		if d.ready == 0 && d.err == nil && now.Sub(d.touched) <= 30*time.Second {
			pending = true
			if !d.waitingSince.IsZero() && now.Sub(d.waitingSince) >= threshold && (cold == nil || d.waitingSince.Before(cold.waitingSince)) {
				cold = d
			}
		}
	}
	token := r.ingestWindow
	active := dbwork.CatalogIngestWindowActive(r.db)
	for _, d := range r.demands {
		if d.ingestWindow != 0 && !d.ingestReported && (d.ready != 0 || d.err != nil || !active) {
			elapsed := time.Since(d.ingestStarted)
			log.Printf("facet cold-build window library=%d field=%s source=%s source_visits=%d elapsed=%s visits_per_second=%.1f exact=%t", d.scope.LibraryID, d.scope.Field, d.scope.Source, d.ingestSteps, elapsed.Round(time.Millisecond), float64(d.ingestSteps)/max(elapsed.Seconds(), 0.001), d.ready != 0)
			d.ingestReported = true
		}
	}
	r.mu.Unlock()
	if !pending || cold == nil {
		if token != 0 {
			dbwork.ReleaseCatalogIngestWindow(r.db, token)
		}
		r.mu.Lock()
		r.ingestDemand = nil
		r.mu.Unlock()
		return
	}
	if active {
		r.mu.Lock()
		focused := r.ingestDemand == cold
		r.mu.Unlock()
		if focused {
			return
		}
	}
	// A retained COMPLETE basis makes this a refresh, which never holds ingest.
	if active, err := r.active(ctx, cold.scope); err == nil && active.ID > 0 {
		r.mu.Lock()
		cold.ready = active.ID
		r.notify(cold)
		r.mu.Unlock()
		return
	}
	id, _ := dbwork.RequestCatalogIngestWindow(r.db, hold)
	if id == 0 {
		return
	}
	r.mu.Lock()
	r.ingestWindow = id
	r.ingestDemand = cold
	// Complete one cold class before spreading a finite window across others.
	// Once it is ready, the next oldest class uses the remaining opportunity.
	if cold.ingestWindow != id {
		cold.ingestWindow = id
		cold.ingestSteps = 0
		cold.ingestStarted = time.Now()
		cold.ingestReported = false
		cold.next = time.Time{}
		cold.gap = 0
	}
	r.mu.Unlock()
}

// A hold turn adapts to thousands of source/policy steps, using separate bounded
// writer slices rather than growing a non-preemptible transaction. Existing row,
// VM and byte budgets apply independently to every slice. No source cursor or
// scalar buffer survives a slice. A user queued between slices gets the gate.
func (r *FacetRuntime) maintainColdBurst(ctx context.Context, d *facetRuntimeDemand) time.Duration {
	started := time.Now()
	limit := 16
	for step := 0; step < limit; step++ {
		if ctx.Err() != nil || dbwork.WriteGate().Admission().UserQueued > 0 || !dbwork.CatalogIngestWindowActive(r.db) {
			return time.Millisecond
		}
		r.mu.Lock()
		before := d.ingestSteps
		r.mu.Unlock()
		wait := r.maintain(ctx, d)
		r.mu.Lock()
		progressed := d.ingestSteps > before
		complete := d.ready != 0 || d.err != nil
		r.mu.Unlock()
		if complete {
			r.cooperateWithColdBuild(ctx)
			return 0
		}
		if !progressed {
			return wait
		}
		elapsed := time.Since(started)
		if elapsed >= 25*time.Millisecond {
			return time.Nanosecond
		}
		// Aim for <=25ms of worker CPU per turn, still capped at4096 steps.
		limit = min(4096, max(16, int(25*time.Millisecond/max(elapsed/time.Duration(step+1), time.Nanosecond))))
	}
	return time.Nanosecond
}
