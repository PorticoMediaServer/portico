package read

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Complete ordered-ID preparation for later order/range consumers.
//
// This producer is a private preparation recipe, not a second scheduler or a
// new authority. It reuses the ONE governor preparation lease, byte reservation
// and values-only cache purpose (rbPreparedOrder) opened by task 01, plus its
// bounded content-restart wrapper. A prepared list is never an eligibility
// proof and never an authorization grant: the fresh publication handoff below
// still matches the whole current fence before any ID is copied.
//
// Two finite source recipes are supported:
//
//   - RBOrderByID streams the complete current library/kind entity IDs through
//     the covering catalog_entities_library seek (rb.order_prepare.first/tail).
//   - RBOrderByTitle streams the same complete set through the existing dual
//     title points (the ignore/keep catalog_entities_title indexes), so actual
//     title keys use those points rather than re-sorting hydrated IDs.
//
// Both streams are read ascending and a completed descending request is served
// by exactly reversing the finished list. Every window is reduced into typed
// keys by a fixed-capacity workspace that sorts one window and refuses an
// out-of-order merge. That is a bounded sort/merge proof for the appended
// stream, never a truncated top-K. A short raw window is the only EOF; rejected
// (retired/age-restricted) rows still advance the private raw cursor.
const (
	// rbOrderRaw bounds one raw source window exactly like the common browse
	// window. Rejected rows and lookahead count against it.
	rbOrderRaw = MaxRaw
	// rbOrderQuantaPerAdvance bounds one poll to a few snapshots. The original
	// 12s preparation deadline still bounds the whole job.
	rbOrderQuantaPerAdvance = 8
	// rbOrderMaxQueries is the finite proof recipe for one window: the common
	// fence plus the bounded source legs and (for title keys) one complete-PK
	// anchor byte pair. It is never inferred from a request.
	rbOrderMaxQueries = 16
)

// RBOrderKey names a finite, compiled order source. It is not a SQL identifier.
type RBOrderKey uint8

const (
	RBOrderByID RBOrderKey = iota + 1
	RBOrderByTitle
)

type RBOrderPrepareInput struct {
	// View carries the current library/kind/viewer anchor. After must be nil;
	// continuation is a private preparation cursor, never a caller anchor.
	View BrowseInput
	Key  RBOrderKey
}

// RBOrderPrepareCursor is a values-only continuation. Its fields are private so
// a caller cannot fabricate another viewer/query/order/source anchor.
type RBOrderPrepareCursor struct {
	key      rbPreparedKey
	fence    browseFence
	position int
}

type RBOrderPreparePage struct {
	IDs       []int64
	Total     int64
	After     *RBOrderPrepareCursor
	Exhausted bool
	// Statements counts the fresh publication window only. Preparation cost is
	// reported by RBOrderPrepareAdvance.
	Statements int
}

type RBOrderPrepareAdvance struct {
	Ready      bool
	Restarts   int
	RawRows    int
	Statements int
	Snapshot   governor.SnapshotResult
}

// RBOrderPrepareReader owns the bounded preparation jobs for this harness
// composition. It creates no goroutine and no second queue.
type RBOrderPrepareReader struct {
	mu    sync.Mutex
	jobs  [governor.PreparationMaxActive]*RBOrderPreparation
	pool  *governor.Snapshots
	a     *governor.Authority
	cache *rbPreparedCache
}

// RBOrderPreparation is one opaque preparation job. Polls retain and reuse it;
// they never re-admit, re-size or re-deadline it. No snapshot, Rows, statement
// or SQL handle survives a quantum.
type RBOrderPreparation struct {
	mu       sync.Mutex
	reader   *RBOrderPrepareReader
	entry    *rbPreparedEntry
	view     BrowseInput
	key      RBOrderKey
	started  time.Time
	after    *rbOrderPosition
	last     rbOrderSourceKey
	complete bool
	pinned   browseFence
	// pinnedSet records the first complete fence this preparation observed. A
	// content restart keeps it; every non-content field must still match.
	pinnedSet bool
	// copy accounting is per window (one bounded snapshot), never a renewed
	// lease: the governor funds each window and the cache funds retained values.
	copied, copyLimit, nativeLimit int64
}

// rbOrderPosition is the private source cursor. It always points at the last RAW
// row, including rejected rows, so retirement cannot stall the walk.
type rbOrderPosition struct {
	key string
	id  int64
}

// rbOrderSourceKey is the typed reducer value. Exactly one of id/key carries the
// order; id is the deterministic tie in both recipes.
type rbOrderSourceKey struct {
	id  int64
	key string
}

var errRBOrderWorkspace = errors.New("ordered preparation workspace capacity")

func NewRBOrderPrepareReader(pool *governor.Snapshots, a *governor.Authority) (*RBOrderPrepareReader, error) {
	if pool == nil || a == nil || !pool.OwnedBy(a) {
		return nil, ErrInput
	}
	cache, err := newRBPreparedCache(a, time.Now)
	if err != nil {
		return nil, err
	}
	return &RBOrderPrepareReader{pool: pool, a: a, cache: cache}, nil
}

// BeginRBOrderPrepare is an EXPLICIT preparation start. Polls retain the
// returned job; they must not Begin again with a fresh deadline. in.View.Now is
// trusted server request-entry time and the immutable query anchor.
func (r *RBOrderPrepareReader) BeginRBOrderPrepare(ctx context.Context, owner governor.OwnerID, bytes int64, in RBOrderPrepareInput) (*RBOrderPreparation, error) {
	if r == nil || ctx == nil || owner == 0 || in.View.Now.IsZero() || in.View.Database == "" || len(in.View.Database) > 256 || in.View.TokenHash == "" || len(in.View.TokenHash) > 256 || in.View.Library <= 0 || in.View.After != nil || in.View.policy != (readPolicyBinding{}) || (in.Key != RBOrderByID && in.Key != RBOrderByTitle) {
		return nil, ErrInput
	}
	switch in.View.Kind {
	case 1, 2, 3, 4, 6, 7, 8, 9:
	default:
		return nil, ErrUnqualified
	}
	if _, err := resolveReadPolicy(in.View); err != nil {
		return nil, err
	}
	// This bounded typed encoding is not a retained body or arbitrary query.
	canonical, err := json.Marshal(struct {
		Database, Token string
		Library, Kind   int64
		Key             RBOrderKey
		Keep, Desc      bool
		Anchor          time.Time
	}{in.View.Database, in.View.TokenHash, in.View.Library, in.View.Kind, in.Key, in.View.KeepArticles, in.View.Descending, in.View.Now})
	if err != nil {
		return nil, err
	}
	key := rbPreparedKey{owner: owner, query: sha256.Sum256(canonical)}
	deadline := time.Now().Add(governor.PreparationLifetime)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if !r.mu.TryLock() {
		return nil, governor.ErrBusy
	}
	defer r.mu.Unlock()
	entry, err := r.cache.begin(ctx, key, rbPreparedOrder, deadline, bytes, governor.PublicationFootprint{{Library: in.View.Library}})
	if err != nil {
		return nil, err
	}
	r.cache.mu.Lock()
	if err := entry.checkLocked(); err != nil {
		r.cache.mu.Unlock()
		return nil, err
	}
	free := -1
	for i, job := range r.jobs {
		if job != nil && job.entry == entry {
			r.cache.mu.Unlock()
			return job, nil
		}
		if job == nil || job.entry.closed {
			if free < 0 {
				free = i
			}
		}
	}
	r.cache.mu.Unlock()
	if free < 0 {
		_ = entry.close()
		return nil, governor.ErrBusy
	}
	job := &RBOrderPreparation{reader: r, entry: entry, view: in.View, key: in.Key, started: time.Now()}
	r.jobs[free] = job
	return job, nil
}

// Advance performs a bounded number of admitted raw quanta. Each quantum is a
// separate snapshot with genuine EOF and a private source cursor; no handle is
// retained across them. A content invalidation restarts the whole walk through
// task 01's wrapper (at most twice), keeping the owner, grant and deadline.
func (p *RBOrderPreparation) Advance(ctx context.Context, request governor.Request, budget time.Duration) (result RBOrderPrepareAdvance, err error) {
	if p == nil || p.reader == nil || p.entry == nil || ctx == nil || request.Owner != p.entry.key.owner || request.Class != governor.Foreground || request.Writer || request.Budget.RawRows < rbOrderRaw+5 || budget <= 0 {
		return result, ErrInput
	}
	if !p.mu.TryLock() {
		return result, governor.ErrBusy
	}
	defer p.mu.Unlock()
	if p.complete {
		p.entry.cache.mu.Lock()
		err = p.entry.checkLocked()
		p.entry.cache.mu.Unlock()
		result.Ready = err == nil
		return result, err
	}
	if !p.pinnedSet {
		// The first complete fence must be observed before the restart wrapper
		// can compare a pinned authorization context. One bounded probe window
		// establishes it; nothing can restart inside a single snapshot.
		if _, err = p.window(ctx, request, budget, &result); err != nil {
			return result, err
		}
		result.Ready = p.complete
		return result, nil
	}
	_, err = rbPrepareRestart(p.entry, p.pinned, func(attempt int) (browseFence, error) {
		if attempt > 0 {
			// The wrapper discarded every value from the invalidated attempt.
			// Restart is from the beginning of the source, never from a stale
			// mid-stream cursor that would publish a truncated set.
			p.after = nil
			p.last = rbOrderSourceKey{}
			p.complete = false
			result.Restarts++
		}
		return p.walk(ctx, request, budget, &result)
	})
	if err != nil {
		return result, err
	}
	result.Ready = p.complete
	return result, nil
}

func (p *RBOrderPreparation) walk(ctx context.Context, request governor.Request, budget time.Duration, result *RBOrderPrepareAdvance) (browseFence, error) {
	deadline := time.Now().Add(budget)
	if request.Deadline.Before(deadline) {
		deadline = request.Deadline
	}
	if p.entry.lease.Deadline().Before(deadline) {
		deadline = p.entry.lease.Deadline()
	}
	var fresh browseFence
	for n := 0; n < rbOrderQuantaPerAdvance; n++ {
		if !time.Now().Before(deadline) {
			return fresh, nil
		}
		var err error
		fresh, err = p.window(ctx, request, budget, result)
		if err != nil {
			return fresh, err
		}
		if p.complete || !time.Now().Before(deadline) {
			return fresh, nil
		}
	}
	return fresh, nil
}

// window performs ONE admitted raw quantum and reduces it. The fence is compared
// before capture so a content move becomes a restart and every other move is a
// denial. All values for the window are reduced, sorted, merged and byte-charged
// before the single retain call; a failed window leaves no partial append.
func (p *RBOrderPreparation) window(ctx context.Context, request governor.Request, budget time.Duration, result *RBOrderPrepareAdvance) (fresh browseFence, err error) {
	p.copied = 0
	p.copyLimit = request.Need[governor.MemoryBytes] - p.reader.pool.NativeRowReservation()
	p.nativeLimit = p.reader.pool.NativeRowReservation()
	if p.copyLimit <= 0 {
		return fresh, governor.ErrSnapshotBudget
	}
	if request.Deadline.After(p.entry.lease.Deadline()) {
		request.Deadline = p.entry.lease.Deadline()
	}
	parent, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(p.entry.ctx, cancel)
	defer func() { stop(); cancel() }()
	result.Snapshot, err = p.reader.pool.ReadForecasted(parent, request, min(budget, time.Until(p.entry.lease.Deadline())), governor.SnapshotForecast{NativeRowBytes: p.reader.pool.NativeRowReservation(), MaxQueries: rbOrderMaxQueries}, func(scope *governor.ReadScope) error {
		query := func(name string, args ...any) ([][]any, error) {
			result.Statements++
			return scope.Query(readStatementID(name), args...)
		}
		in := p.view
		in.Now = in.Now.Add(time.Since(p.started))
		f, e := readBrowseFence(query, in)
		if e != nil {
			return e
		}
		fresh = f
		if !p.pinnedSet {
			// Nothing has been retained yet; the first complete fence becomes the
			// pinned authorization context for every restart comparison.
			p.pinned, p.pinnedSet = f, true
			if e = p.entry.capture(f); e != nil {
				return e
			}
		} else if p.entry.fence != f {
			if !rbPreparePinnedFenceEqual(p.entry.fence, f) {
				return ErrDenied
			}
			return errRBPrepareContent
		}
		var raw [][]any
		if p.key == RBOrderByID {
			raw, e = p.readIDRows(query, in)
		} else {
			raw, e = p.readTitleRows(query, in)
		}
		if e != nil {
			return e
		}
		result.RawRows += len(raw)
		exhausted, e := p.reduceWindow(scope, raw, f)
		if e != nil {
			return e
		}
		if exhausted {
			if e = p.finishOrder(); e != nil {
				return e
			}
			p.complete = true
		}
		return nil
	})
	if result.Snapshot.Refusal != nil {
		err = governor.ErrBusy
	}
	if err != nil {
		// Denial/stale/invalidated work cannot keep a ready-poll lease alive.
		if errors.Is(err, ErrDenied) || errors.Is(err, ErrStale) || errors.Is(err, ErrUnqualified) || errors.Is(err, governor.ErrSnapshotBudget) {
			_ = p.entry.close()
		}
		return fresh, err
	}
	return fresh, nil
}

func (p *RBOrderPreparation) readIDRows(query browseQuery, in BrowseInput) ([][]any, error) {
	if p.after == nil {
		return query("rb.order_prepare.first", in.Library, in.Kind, int64(rbOrderRaw))
	}
	return query("rb.order_prepare.tail", in.Library, in.Kind, p.after.id, int64(rbOrderRaw))
}

// readTitleRows uses the existing dual title points. Their byte leg is measured
// in this same snapshot before the payload is copied, and the payload must match
// every measured length exactly.
func (p *RBOrderPreparation) readTitleRows(query browseQuery, in BrowseInput) ([][]any, error) {
	name := "rb.filter.personal.title.ignore.asc."
	if in.KeepArticles {
		name = "rb.filter.personal.title.keep.asc."
	}
	leg := func(part string, args ...any) ([][]any, error) {
		sizes, err := query(name+part+".bytes", args...)
		if err != nil {
			return nil, err
		}
		if len(sizes) > rbOrderRaw {
			return nil, ErrUnqualified
		}
		var total int64
		for _, row := range sizes {
			if len(row) != 3 {
				return nil, ErrUnqualified
			}
			for _, v := range row[1:] {
				n, ok := v.(int64)
				if !ok || n < 0 || n > p.nativeLimit-total {
					return nil, governor.ErrSnapshotBudget
				}
				total += n
			}
		}
		if 8192+int64(len(sizes))*1024+2*total > p.copyLimit-p.copied {
			return nil, governor.ErrSnapshotBudget
		}
		payloadArgs := append([]any{p.entry.fence.profile}, args...)
		rows, err := query(name+part, payloadArgs...)
		if err != nil {
			return nil, err
		}
		if len(rows) != len(sizes) {
			return nil, ErrStale
		}
		for i, row := range rows {
			if len(row) != 13 || row[0] != sizes[i][0] {
				return nil, ErrStale
			}
			for _, pair := range []struct {
				col    int
				length int
			}{{1, 1}, {3, 2}} {
				v, ok := row[pair.col].(string)
				if !ok || int64(len(v)) != sizes[i][pair.length] {
					return nil, ErrStale
				}
			}
		}
		p.copied += 8192 + int64(len(rows))*1024 + 2*total
		return rows, nil
	}
	if p.after == nil {
		return leg("first", in.Library, in.Kind, int64(rbOrderRaw))
	}
	rows, err := leg("tail", in.Library, in.Kind, p.after.key, p.after.id, int64(rbOrderRaw))
	if err != nil {
		return nil, err
	}
	// A full equal-key run may end before the window limit. The next-key seek
	// completes the window, so a short equality tail is never a false EOF.
	if len(rows) < rbOrderRaw {
		more, err := leg("next", in.Library, in.Kind, p.after.key, int64(rbOrderRaw-len(rows)))
		if err != nil {
			return nil, err
		}
		rows = append(rows, more...)
	}
	return rows, nil
}

// reduceWindow types, sorts and merges one raw window. The complete PK anchor
// pair proves the streamed title key against the current entity row and charges
// its exact bytes before any key is copied.
func (p *RBOrderPreparation) reduceWindow(scope *governor.ReadScope, raw [][]any, f browseFence) (bool, error) {
	if len(raw) > rbOrderRaw {
		return false, ErrUnqualified
	}
	workspace := rbOrderWorkspace{rows: make([]rbOrderSourceKey, 0, rbOrderRaw)}
	ids := make([]int64, 0, len(raw))
	var next *rbOrderPosition
	for _, row := range raw {
		if err := scope.Context().Err(); err != nil {
			return false, err
		}
		key, id, admitted, err := p.reduceRow(row, f)
		if err != nil {
			return false, err
		}
		next = &rbOrderPosition{id: id}
		if p.key == RBOrderByTitle {
			next.key = key.key
		}
		if !admitted {
			continue
		}
		if err := p.reserve(64 + 2*int64(len(key.key))); err != nil {
			return false, err
		}
		if err := workspace.add(key); err != nil {
			return false, err
		}
	}
	if p.key == RBOrderByTitle && len(raw) > 0 {
		anchor := raw[len(raw)-1]
		if err := p.anchorTitleKey(scope, anchor, f); err != nil {
			return false, err
		}
	}
	workspace.sort(p.key)
	if len(workspace.rows) > 0 && (p.last.id != 0 || p.last.key != "") {
		// Bounded merge proof: the window must start strictly after the retained
		// stream. A non-monotone source is refused instead of appended.
		if !rbOrderAfter(workspace.rows[0], p.last, p.key, false) {
			return false, ErrUnqualified
		}
	}
	ids = ids[:0]
	for _, key := range workspace.rows {
		ids = append(ids, key.id)
	}
	if err := p.entry.retainIDs(ids); err != nil {
		return false, err
	}
	if len(workspace.rows) > 0 {
		p.last = workspace.rows[len(workspace.rows)-1]
	}
	p.after = next
	return len(raw) < rbOrderRaw, nil
}

// reduceRow is the typed source-key/ID reducer. It never invents eligibility:
// retired and age-restricted rows still consume the raw window but contribute
// no ID.
func (p *RBOrderPreparation) reduceRow(row []any, f browseFence) (rbOrderSourceKey, int64, bool, error) {
	switch p.key {
	case RBOrderByID:
		if len(row) != 4 {
			return rbOrderSourceKey{}, 0, false, ErrUnqualified
		}
		id, ok := row[0].(int64)
		if !ok || id <= 0 {
			return rbOrderSourceKey{}, 0, false, ErrUnqualified
		}
		item := [7]any{id, "", nil, nil, row[1], row[2], row[3]}
		return rbOrderSourceKey{id: id}, id, admittedItem(item[:], f), nil
	case RBOrderByTitle:
		if len(row) != 13 {
			return rbOrderSourceKey{}, 0, false, ErrUnqualified
		}
		id, ok := row[0].(int64)
		key, okKey := row[1].(string)
		if !ok || id <= 0 || !okKey || !utf8.ValidString(key) {
			return rbOrderSourceKey{}, 0, false, ErrUnqualified
		}
		return rbOrderSourceKey{id: id, key: key}, id, admittedItem(row[:7], f), nil
	}
	return rbOrderSourceKey{}, 0, false, ErrInput
}

// anchorTitleKey re-reads the window anchor through the complete entity PK and
// requires the streamed title key to match the selected article policy. The
// exact octet pair charges the key bytes before the cursor copies them.
func (p *RBOrderPreparation) anchorTitleKey(scope *governor.ReadScope, anchor []any, f browseFence) error {
	id := anchor[0].(int64)
	key := anchor[1].(string)
	row, err := scope.QueryExactBytes(readStatementID("rb.order_prepare.point_octets"), readStatementID("rb.order_prepare.point"), []int{1, 2, 3}, id)
	if err != nil {
		return err
	}
	if len(row) != 7 || row[0] != id {
		return ErrStale
	}
	if f.keepArticles {
		if row[2] != key {
			return ErrStale
		}
	} else if row[1] != key {
		return ErrStale
	}
	var bytes int64
	for _, v := range row[1:4] {
		if s, ok := v.(string); ok {
			bytes += int64(len(s))
		}
	}
	return p.reserve(64 + 4*32 + 2*bytes)
}

func (p *RBOrderPreparation) reserve(n int64) error {
	if n < 0 || n > p.copyLimit-p.copied {
		return governor.ErrSnapshotBudget
	}
	p.copied += n
	return nil
}

// finishOrder reverses a completed descending request in place, then publishes
// readiness through the SAME preparation lease. No new grant or allocation.
func (p *RBOrderPreparation) finishOrder() error {
	p.entry.cache.mu.Lock()
	defer p.entry.cache.mu.Unlock()
	if err := p.entry.checkLocked(); err != nil {
		return err
	}
	if !p.entry.captured || p.entry.complete {
		return ErrInput
	}
	if p.view.Descending {
		slices.Reverse(p.entry.ids)
	}
	if err := p.entry.cache.a.ReadyPreparation(p.entry.lease); err != nil {
		_ = p.entry.closeLocked()
		return err
	}
	p.entry.complete = true
	return nil
}

// Page copies one page of the complete ordered IDs inside a fresh authorized
// response snapshot. The exact whole fence must match before any copy, and the
// first delivery linearizes with the governor exactly like every other prepared
// cache purpose. A later page with a drifted fence is denied, never replayed.
func (p *RBOrderPreparation) Page(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput, after *RBOrderPrepareCursor, limit int) (RBOrderPreparePage, BrowseReadResult, error) {
	if p == nil || p.reader == nil || p.entry == nil {
		return RBOrderPreparePage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, p.reader.pool, current.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (RBOrderPreparePage, BrowseReadResult, error) {
		current.Now = at
		return p.page(ctx, request, budget, output, current, after, limit)
	})
}

func (p *RBOrderPreparation) page(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput, after *RBOrderPrepareCursor, limit int) (page RBOrderPreparePage, result BrowseReadResult, err error) {
	started := time.Now()
	defer func() { result.Elapsed = time.Since(started) }()
	if ctx == nil || output.Authority == nil || output.Grant == nil || output.Request.Writer || output.Request.Owner != request.Owner || output.Request.Class != request.Class || len(current.Database) == 0 || len(current.Database) > 256 || len(current.TokenHash) == 0 || len(current.TokenHash) > 256 || current.Library <= 0 || current.Kind < 1 || current.Kind > 99 || budget <= 0 || limit < 1 || limit > 200 || request.Owner != p.entry.key.owner || request.Class != governor.Foreground || request.Writer || current.After != nil || current.Now.IsZero() {
		return page, result, ErrInput
	}
	if !p.mu.TryLock() {
		return page, result, governor.ErrBusy
	}
	defer p.mu.Unlock()
	if !p.complete {
		return page, result, governor.ErrBusy
	}
	deadline := earliest(request.Deadline, p.entry.lease.Deadline())
	deadline = earliest(deadline, time.Now().Add(min(budget, time.Second)))
	request.Deadline = deadline
	var fresh browseFence
	var prepared rbPreparedPage
	validation, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancel()
	result.Publication, err = p.reader.pool.ReadForecasted(validation, request, time.Until(deadline), governor.SnapshotForecast{NativeRowBytes: p.reader.pool.NativeRowReservation(), MaxQueries: rbOrderMaxQueries}, func(scope *governor.ReadScope) error {
		query := func(name string, args ...any) ([][]any, error) {
			result.PublicationStatements++
			if output.live != nil {
				if e := output.live.checkContext(validation); e != nil {
					return nil, e
				}
			}
			return scope.Query(readStatementID(name), args...)
		}
		view := current
		view.Now = current.Now.Add(time.Since(started))
		var e error
		fresh, e = readBrowseFence(query, view)
		if e != nil {
			return e
		}
		if after != nil && (after.key != p.entry.key || after.fence != fresh) {
			return ErrStale
		}
		if e = scope.Context().Err(); e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if 2*rbPreparedFenceBytes(fresh)+4096+int64(limit)*64 > output.Request.Need[governor.MemoryBytes]/2 {
			return governor.ErrSnapshotBudget
		}
		if e = output.Authority.CheckContext(validation, output.Grant, output.Request); e != nil {
			return e
		}
		offset := 0
		if after != nil {
			offset = after.position
		}
		prepared, e = p.entry.handoff(fresh, offset, limit)
		return e
	})
	result.Attempts = 1
	if !result.Publication.Started.IsZero() {
		result.PublicationWindows = append(result.PublicationWindows, result.Publication)
	}
	if result.Publication.Refusal != nil {
		result.Refusal = result.Publication.Refusal
		err = governor.ErrBusy
	}
	if err != nil {
		if errors.Is(err, ErrDenied) || errors.Is(err, ErrStale) || errors.Is(err, ErrUnqualified) {
			_ = p.entry.close()
		}
		return RBOrderPreparePage{}, result, err
	}
	if err = ctx.Err(); err != nil {
		return RBOrderPreparePage{}, result, err
	}
	// Confirm the freshly observed token/horizon at actual publication, after
	// native cleanup; expiry must not leak a copied formerly-authorized page.
	at := current.Now.Add(time.Since(started))
	expires, e1 := time.Parse(time.RFC3339Nano, fresh.expiresAt)
	horizon, e2 := time.Parse(time.RFC3339Nano, fresh.horizon)
	if e1 != nil || e2 != nil || !at.Before(expires) || !at.Before(horizon) {
		_ = p.entry.close()
		return RBOrderPreparePage{}, result, ErrDenied
	}
	if !time.Now().Before(deadline) {
		return RBOrderPreparePage{}, result, context.DeadlineExceeded
	}
	if err = output.Authority.CheckContext(ctx, output.Grant, output.Request); err != nil {
		return RBOrderPreparePage{}, result, err
	}
	page = RBOrderPreparePage{IDs: prepared.ids, Total: prepared.total, Exhausted: prepared.exhausted, Statements: result.PublicationStatements}
	if !prepared.exhausted {
		page.After = &RBOrderPrepareCursor{key: p.entry.key, fence: fresh, position: prepared.next}
	}
	return page, result, nil
}

func (p *RBOrderPreparation) Close() error {
	if p == nil || p.entry == nil {
		return ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.entry.close()
}

// rbOrderWorkspace is a fixed-capacity sort workspace for ONE raw window. It
// refuses capacity BEFORE append, so a failed window can never leave a partial
// ordered fragment behind.
type rbOrderWorkspace struct {
	rows []rbOrderSourceKey
}

func (w *rbOrderWorkspace) add(key rbOrderSourceKey) error {
	if w == nil || len(w.rows) >= cap(w.rows) {
		return errRBOrderWorkspace
	}
	w.rows = append(w.rows, key)
	return nil
}

func (w *rbOrderWorkspace) sort(key RBOrderKey) {
	slices.SortFunc(w.rows, func(a, b rbOrderSourceKey) int {
		return rbOrderCompare(a, b, key)
	})
}

// rbOrderCompare orders a before b in the ascending typed order.
func rbOrderCompare(a, b rbOrderSourceKey, key RBOrderKey) int {
	if key == RBOrderByTitle {
		if c := strings.Compare(rbPersonalASCIIFold(a.key), rbPersonalASCIIFold(b.key)); c != 0 {
			return c
		}
		return compareInt64(a.id, b.id)
	}
	return compareInt64(a.id, b.id)
}

// rbOrderAfter reports whether a comes strictly after b in the requested typed
// order and direction. Title keys use the same ASCII-only NOCASE fold as the
// dual title indexes; the entity ID is the deterministic tie.
func rbOrderAfter(a, b rbOrderSourceKey, key RBOrderKey, descending bool) bool {
	cmp := rbOrderCompare(a, b, key)
	if descending {
		return cmp < 0
	}
	return cmp > 0
}

func compareInt64(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
