package read

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

const rbFacetRaw = 1024

// ExactNumericField is a finite source recipe, not a SQL identifier.
type ExactNumericField uint8

const (
	ExactYear ExactNumericField = iota + 1
	ExactDecade
)

// ExactFacetReader is phase2 harness composition. It chooses no hardware
// profile and cannot turn on preparation conflict checking for phase1a.
type ExactFacetReader struct {
	mu    sync.Mutex
	jobs  [governor.PreparationMaxActive]*NumericFacetPreparation
	pool  *governor.Snapshots
	a     *governor.Authority
	cache *rbPreparedCache
}
type NumericFacetPreparation struct {
	mu       sync.Mutex
	reader   *ExactFacetReader
	entry    *rbPreparedEntry
	view     BrowseInput
	field    ExactNumericField
	started  time.Time
	after    *rbFacetPosition
	complete bool
}
type rbFacetPosition struct{ year, id int64 }
type FacetAdvance struct {
	Ready      bool
	RawRows    int
	Statements int
	Snapshot   governor.SnapshotResult
}

func NewExactFacetReader(pool *governor.Snapshots, a *governor.Authority) (*ExactFacetReader, error) {
	if pool == nil || a == nil || !pool.OwnedBy(a) {
		return nil, ErrInput
	}
	cache, err := newRBPreparedCache(a, time.Now)
	if err != nil {
		return nil, err
	}
	return &ExactFacetReader{pool: pool, a: a, cache: cache}, nil
}

// BeginNumericFacets is an EXPLICIT preparation start. Polls retain and use the
// returned opaque operation; they must not call Begin again with a fresh time.
// in.Now is trusted server request-entry time, also the immutable query anchor.
func (r *ExactFacetReader) BeginNumericFacets(ctx context.Context, owner governor.OwnerID, bytes int64, in BrowseInput, field ExactNumericField) (*NumericFacetPreparation, error) {
	if field != ExactYear && field != ExactDecade {
		return nil, ErrInput
	}
	return r.beginNumericPreparation(ctx, owner, bytes, in, field)
}

func (r *ExactFacetReader) beginNumericPreparation(ctx context.Context, owner governor.OwnerID, bytes int64, in BrowseInput, field ExactNumericField) (*NumericFacetPreparation, error) {
	if r == nil || ctx == nil || owner == 0 || in.Now.IsZero() || in.Database == "" || len(in.Database) > 256 || in.TokenHash == "" || len(in.TokenHash) > 256 || in.Library <= 0 || in.After != nil || in.policy != (readPolicyBinding{}) || (field != ExactYear && field != ExactDecade && field != rbExactEligibleCount) {
		return nil, ErrInput
	}
	switch in.Kind {
	case 1, 2, 3, 4, 6, 7, 8, 9:
	default:
		return nil, ErrUnqualified
	}
	if _, err := resolveReadPolicy(in); err != nil {
		return nil, err
	}
	// This bounded typed encoding is not a retained body or arbitrary query.
	canonical, err := json.Marshal(struct {
		Database, Token string
		Library, Kind   int64
		Field           ExactNumericField
		Anchor          time.Time
	}{in.Database, in.TokenHash, in.Library, in.Kind, field, in.Now})
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
	entry, err := r.cache.begin(ctx, key, rbPreparedNumericFacets, deadline, bytes, governor.PublicationFootprint{{Library: in.Library}})
	if err != nil {
		return nil, err
	}
	free := -1
	r.cache.mu.Lock()
	if err := entry.checkLocked(); err != nil {
		r.cache.mu.Unlock()
		return nil, err
	}
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
	job := &NumericFacetPreparation{reader: r, entry: entry, view: in, field: field, started: time.Now()}
	r.jobs[free] = job
	return job, nil
}

// Advance performs ONE admitted raw quantum. No snapshot, Rows or statement
// survives it; the publication lease/cache remain independently charged. The
// current accepting read fence is checked before retaining any source values.
func (p *NumericFacetPreparation) Advance(ctx context.Context, request governor.Request, budget time.Duration) (result FacetAdvance, err error) {
	if p == nil || p.reader == nil || p.entry == nil || ctx == nil || request.Owner != p.entry.key.owner || request.Class != governor.Foreground || request.Writer || request.Budget.RawRows < rbFacetRaw+5 || budget <= 0 {
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
	if err := p.reader.a.CheckPreparation(p.entry.lease); err != nil {
		_ = p.entry.close()
		return result, err
	}
	if request.Deadline.After(p.entry.lease.Deadline()) {
		request.Deadline = p.entry.lease.Deadline()
	}
	parent, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(p.entry.ctx, cancel)
	defer func() { stop(); cancel() }()
	// Retained prefix is rolled back if native cleanup refuses after the
	// callback. No advancing cursor or duplicate counts escape a failed window.
	p.entry.cache.mu.Lock()
	priorLen, priorTotal := len(p.entry.facets), p.entry.total
	var priorLast rbNumericFacet
	if priorLen > 0 {
		priorLast = p.entry.facets[priorLen-1]
	}
	p.entry.cache.mu.Unlock()
	var next *rbFacetPosition
	var exhausted bool
	result.Snapshot, err = p.reader.pool.Read(parent, request, min(budget, time.Until(p.entry.lease.Deadline())), func(scope *governor.ReadScope) error {
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
		if e = p.entry.capture(f); e != nil {
			return e
		}
		var rows [][]any
		if p.after == nil {
			rows, e = query("rb.prepare.year.first", in.Library, in.Kind, int64(rbFacetRaw))
		} else {
			rows, e = query("rb.prepare.year.tail", in.Library, in.Kind, p.after.year, p.after.id, int64(rbFacetRaw))
			if e == nil && len(rows) < rbFacetRaw {
				var rest [][]any
				rest, e = query("rb.prepare.year.next", in.Library, in.Kind, p.after.year, int64(rbFacetRaw-len(rows)))
				rows = append(rows, rest...)
			}
		}
		if e != nil {
			return e
		}
		result.RawRows = len(rows)
		// Values reduce in source order; no hash-map or set-sized uncharged copy.
		var grouped [rbFacetRaw]rbNumericFacet
		used := 0
		for _, row := range rows {
			if e = scope.Context().Err(); e != nil {
				return e
			}
			year, id := row[1].(int64), row[0].(int64)
			next = &rbFacetPosition{year: year, id: id}
			item := [7]any{row[0], row[1], nil, nil, row[2], nil, row[3]}
			if !admittedItem(item[:], f) {
				continue
			}
			value := int64(0)
			if p.field != rbExactEligibleCount {
				// Year/decade categories share the published/parser domain. A
				// whole eligible count also includes unknown/out-of-range years.
				if year < 1800 || year > 2199 {
					continue
				}
				value = year
				if p.field == ExactDecade {
					value = year / 10 * 10
				}
			}
			if used > 0 && grouped[used-1].Value == value {
				grouped[used-1].Count++
			} else {
				grouped[used] = rbNumericFacet{value, 1}
				used++
			}
		}
		for _, group := range grouped[:used] {
			if e = p.entry.retainNumeric(group.Value, group.Count); e != nil {
				return e
			}
		}
		exhausted = len(rows) < rbFacetRaw
		return nil
	})
	if result.Snapshot.Refusal != nil {
		err = governor.ErrBusy
	}
	if err != nil {
		p.entry.cache.mu.Lock()
		if !p.entry.closed {
			p.entry.facets = p.entry.facets[:priorLen]
			p.entry.total = priorTotal
			if priorLen > 0 {
				p.entry.facets[priorLen-1] = priorLast
			}
		}
		p.entry.cache.mu.Unlock()
		// Denial/stale/invalidated work cannot keep a ready-poll lease alive.
		if errors.Is(err, ErrDenied) || errors.Is(err, ErrStale) || errors.Is(err, ErrUnqualified) {
			_ = p.entry.close()
		}
		return result, err
	}
	p.after = next
	if exhausted {
		if err = p.entry.finish(true); err != nil {
			return result, err
		}
		p.complete = true
	}
	result.Ready = p.complete
	return result, nil
}
func (p *NumericFacetPreparation) Close() error {
	if p == nil || p.entry == nil {
		return ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.entry.close()
}

// NumericFacetValue is a value and its exact eligible-entity count. Zero/unknown
// years have no value, just as the existing year/decade field contract specifies.
type NumericFacetValue struct{ Value, Count int64 }
type NumericFacetPage struct {
	Values []NumericFacetValue
	// ValueMemberships counts eligible entities with a nonmissing facet value;
	// it is not an upgraded universal exact Browse total.
	ValueMemberships int64
	After            *NumericFacetCursor
	Exhausted        bool
	Statements       int
}
type NumericFacetCursor struct {
	key      rbPreparedKey
	fence    browseFence
	position int
}

// Page requires one original LIVE/output scope and a current trusted viewer
// input. It returns no partial reduction and holds the preparation lease until
// complete values are copied in the fresh authorized ready-response snapshot.
func (p *NumericFacetPreparation) Page(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput, after *NumericFacetCursor, limit int) (NumericFacetPage, BrowseReadResult, error) {
	if p == nil || p.reader == nil || p.entry == nil {
		return NumericFacetPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, p.reader.pool, current.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (NumericFacetPage, BrowseReadResult, error) {
		current.Now = at
		return p.page(ctx, request, budget, output, current, after, limit)
	})
}
func (p *NumericFacetPreparation) page(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput, after *NumericFacetCursor, limit int) (page NumericFacetPage, result BrowseReadResult, err error) {
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
	deadline := rbExactEarlier(request.Deadline, p.entry.lease.Deadline())
	deadline = rbExactEarlier(deadline, time.Now().Add(min(budget, time.Second)))
	request.Deadline = deadline
	var fresh browseFence
	var prepared rbPreparedPage
	validation, cancelValidation := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancelValidation()
	result.Publication, err = p.reader.pool.Read(validation, request, time.Until(deadline), func(scope *governor.ReadScope) error {
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
		return NumericFacetPage{}, result, err
	}
	if err = ctx.Err(); err != nil {
		return NumericFacetPage{}, result, err
	}
	// Confirm the freshly observed token/horizon at actual publication, after
	// native cleanup; expiry must not leak a copied formerly-authorized page.
	at := current.Now.Add(time.Since(started))
	expires, e1 := time.Parse(time.RFC3339Nano, fresh.expiresAt)
	horizon, e2 := time.Parse(time.RFC3339Nano, fresh.horizon)
	if e1 != nil || e2 != nil || !at.Before(expires) || !at.Before(horizon) {
		_ = p.entry.close()
		return NumericFacetPage{}, result, ErrDenied
	}
	if !time.Now().Before(deadline) {
		return NumericFacetPage{}, result, context.DeadlineExceeded
	}
	if err = output.Authority.CheckContext(ctx, output.Grant, output.Request); err != nil {
		return NumericFacetPage{}, result, err
	}
	page = NumericFacetPage{ValueMemberships: prepared.total, Exhausted: prepared.exhausted, Statements: result.PublicationStatements}
	for _, v := range prepared.facets {
		page.Values = append(page.Values, NumericFacetValue{v.Value, v.Count})
	}
	if !prepared.exhausted {
		page.After = &NumericFacetCursor{key: p.entry.key, fence: fresh, position: prepared.next}
	}
	return page, result, nil
}
func rbExactEarlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
