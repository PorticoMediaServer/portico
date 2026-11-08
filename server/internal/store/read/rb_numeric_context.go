package read

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Filtered numeric context: a complete, bounded preparation of the year/decade
// facet values or the private exact eligible count for one current
// library/kind/viewer scope.
//
// It is the same preparation recipe as rb_facet_year/rb_exact_count, not a new
// scheduler or a new authority. One governor preparation lease, one values-only
// cache purpose (rbPreparedNumericFacets) and the task-01 content-restart wrapper
// carry the work. A caller never receives a SQL capability, a grant or a raw
// source handle, and no partial reduction can become ready.
//
// Two source shapes are composed:
//
//   - year/decade values always walk catalog_entities_year. Year order makes the
//     retained value counts contiguous, so no hash map or whole-library
//     aggregate is needed.
//   - the eligible count walks the most selective closed source order the
//     filter names (release/rating/critic) or catalog_entities_library when the
//     filter has no such key. Every source order is the complete library/kind
//     membership; order never changes eligibility.
//
// Every raw row is a thin bounded row. The exact scalar payload of the window
// anchor is re-read by complete entity PK through the scalar/scalar_octets pair,
// so the streamed facts are proved against the current row and the wide value
// bytes are charged from an actual same-snapshot octet preflight. The full
// detail row is never read.
const (
	// rbNumericContextRaw is the existing numeric 1,024-row advance. It is a
	// distinct declared recipe, not permission to enlarge other readers.
	rbNumericContextRaw = 1024
	// Two sentinels group admitted matches without a published year/decade
	// value. Year order puts zero/early years first and out-of-domain late
	// years last, so one sentinel could not keep the reduction contiguous.
	// Both are counted in the total and never published as values.
	rbNumericContextMissingLow  = math.MinInt64
	rbNumericContextMissingHigh = math.MaxInt64
	// rbNumericContextMaxQueries bounds one window: the common five fence
	// points, two walk legs and the scalar byte/payload pair. It is never
	// inferred from a request.
	rbNumericContextMaxQueries = 12
)

// rbNumericContextThreshold mirrors the published facet presentation rule: a
// value or total above it is published as a lower bound. The preparation itself
// is always complete; this adds no universal exact-total promise.
var rbNumericContextThreshold int64 = 10000

type RBNumericContextField uint8

const (
	RBNumericContextEligible RBNumericContextField = iota + 1
	RBNumericContextYear
	RBNumericContextDecade
)

type rbNumericContextSource uint8

const (
	rbNumericContextYearSource rbNumericContextSource = iota + 1
	rbNumericContextLibrarySource
	rbNumericContextReleaseSource
	rbNumericContextRatingSource
	rbNumericContextCriticSource
)

func (s rbNumericContextSource) name() string {
	switch s {
	case rbNumericContextYearSource:
		return ""
	case rbNumericContextLibrarySource:
		return "library."
	case rbNumericContextReleaseSource:
		return "release."
	case rbNumericContextRatingSource:
		return "rating."
	case rbNumericContextCriticSource:
		return "critic."
	}
	return ""
}

type RBNumericContextInput struct {
	// View carries the current library/kind/viewer anchor. After must be nil;
	// continuation is a private preparation cursor, never a caller anchor.
	View     BrowseInput
	Filter   json.RawMessage
	Personal json.RawMessage
	Field    RBNumericContextField
}

type RBNumericContextReader struct {
	mu    sync.Mutex
	jobs  [governor.PreparationMaxActive]*RBNumericContextPreparation
	pool  *governor.Snapshots
	a     *governor.Authority
	cache *rbPreparedCache
}

type RBNumericContextPreparation struct {
	mu       sync.Mutex
	reader   *RBNumericContextReader
	entry    *rbPreparedEntry
	view     BrowseInput
	field    RBNumericContextField
	source   rbNumericContextSource
	scalar   rbScalarCompleteFilter
	personal rbPersonalFilter
	started  time.Time
	after    *rbNumericContextPosition
	complete bool
	pinned   browseFence
	// pinnedSet records the first complete fence. A content restart keeps it;
	// every non-content field must still match exactly.
	pinnedSet bool
	// copy accounting is per window, never a renewed lease.
	copied, copyLimit int64
}

type rbNumericContextPosition struct {
	number float64
	text   string
	id     int64
}

type RBNumericContextAdvance struct {
	Ready      bool
	Restarts   int
	RawRows    int
	Statements int
	Snapshot   governor.SnapshotResult
}

type RBNumericContextValue struct {
	Value, Count      int64
	CountIsLowerBound bool
}

type RBNumericContextPage struct {
	Values []RBNumericContextValue
	// Total is the published count (capped by the public lower-bound rule).
	Total int64
	// ExactTotal is the complete preparation's count of every eligible match,
	// including matches without a published year/decade value.
	ExactTotal int64
	// Exact reports that the preparation walked to genuine raw EOF. The
	// lower-bound flags describe presentation only.
	Exact             bool
	TotalIsLowerBound bool
	After             *RBNumericContextCursor
	Exhausted         bool
	Statements        int
}

type RBNumericContextCursor struct {
	key      rbPreparedKey
	fence    browseFence
	position int
}

func NewRBNumericContextReader(pool *governor.Snapshots, a *governor.Authority) (*RBNumericContextReader, error) {
	if pool == nil || a == nil || !pool.OwnedBy(a) {
		return nil, ErrInput
	}
	cache, err := newRBPreparedCache(a, time.Now)
	if err != nil {
		return nil, err
	}
	return &RBNumericContextReader{pool: pool, a: a, cache: cache}, nil
}

// BeginNumericContext is an EXPLICIT preparation start. Polls retain the returned
// job; they must not Begin again with a fresh deadline. in.View.Now is trusted
// server request-entry time and the immutable relative-date anchor.
func (r *RBNumericContextReader) BeginNumericContext(ctx context.Context, owner governor.OwnerID, bytes int64, in RBNumericContextInput) (*RBNumericContextPreparation, error) {
	if r == nil || ctx == nil || owner == 0 || in.View.Now.IsZero() || in.View.Database == "" || len(in.View.Database) > 256 || in.View.TokenHash == "" || len(in.View.TokenHash) > 256 || in.View.Library <= 0 || in.View.After != nil || in.View.policy != (readPolicyBinding{}) || len(in.Filter) > 64<<10 || !utf8.Valid(in.Filter) || len(in.Personal) > 64<<10 || !utf8.Valid(in.Personal) {
		return nil, ErrInput
	}
	if in.Field != RBNumericContextEligible && in.Field != RBNumericContextYear && in.Field != RBNumericContextDecade {
		return nil, ErrInput
	}
	if !rbNumericContextKind(in.View.Kind) {
		return nil, ErrUnqualified
	}
	if _, err := resolveReadPolicy(in.View); err != nil {
		return nil, err
	}
	scalar, err := compileRBScalarCompleteFilter(in.Filter)
	if err != nil {
		return nil, err
	}
	personal, err := compileRBPersonalFilter(in.Personal)
	if err != nil {
		return nil, err
	}
	// The merged personal kernel is the entity's own sparse row. Public
	// container pivots also inherit member state; that OR belongs to the
	// membership recipes (tasks 04/06), so a container request refuses rather
	// than publishing an under-count as complete.
	if personal.node != nil && (in.View.Kind == 2 || in.View.Kind == 3 || in.View.Kind == 6 || in.View.Kind == 8) {
		return nil, ErrUnqualified
	}
	source := rbNumericContextSourceFor(in.Field, in.View.Kind, scalar.node)
	scalarJSON, err := json.Marshal(scalar.node)
	if err != nil {
		return nil, err
	}
	personalJSON, err := json.Marshal(personal.node)
	if err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(struct {
		Database, Token  string
		Library, Kind    int64
		Field            RBNumericContextField
		Source           rbNumericContextSource
		Scalar, Personal [32]byte
		Anchor           time.Time
	}{in.View.Database, in.View.TokenHash, in.View.Library, in.View.Kind, in.Field, source, sha256.Sum256(scalarJSON), sha256.Sum256(personalJSON), in.View.Now})
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
	entry, err := r.cache.begin(ctx, key, rbPreparedNumericFacets, deadline, bytes, governor.PublicationFootprint{{Library: in.View.Library}})
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
	job := &RBNumericContextPreparation{reader: r, entry: entry, view: in.View, field: in.Field, source: source, scalar: scalar, personal: personal, started: time.Now()}
	r.jobs[free] = job
	return job, nil
}

func rbNumericContextKind(kind int64) bool {
	switch kind {
	case 1, 2, 3, 4, 6, 7, 8, 9:
		return true
	}
	return false
}

func rbNumericContextSourceFor(field RBNumericContextField, kind int64, node *rbFilterNode) rbNumericContextSource {
	if field != RBNumericContextEligible {
		return rbNumericContextYearSource
	}
	// A complete source order is any covering library/kind index; these finite
	// preferences only avoid walking wide rows the filter would reject.
	if rbCompleteFilterHasField(node, "releaseDate") && rbNumericContextKindIn(kind, 1, 2, 3, 4, 6, 8) {
		return rbNumericContextReleaseSource
	}
	if rbCompleteFilterHasField(node, "communityRating") && rbNumericContextKindIn(kind, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 13) {
		return rbNumericContextRatingSource
	}
	if rbCompleteFilterHasField(node, "criticRating") && rbNumericContextKindIn(kind, 1, 2, 3, 4, 10) {
		return rbNumericContextCriticSource
	}
	return rbNumericContextLibrarySource
}

func rbNumericContextKindIn(kind int64, kinds ...int64) bool {
	for _, candidate := range kinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

func rbCompleteFilterHasField(n *rbFilterNode, field string) bool {
	if n == nil {
		return false
	}
	if n.Field == field {
		return true
	}
	for i := range n.All {
		if rbCompleteFilterHasField(&n.All[i], field) {
			return true
		}
	}
	for i := range n.Any {
		if rbCompleteFilterHasField(&n.Any[i], field) {
			return true
		}
	}
	return rbCompleteFilterHasField(n.Not, field)
}

// Advance performs ONE admitted 1,024-row raw quantum. No snapshot, rows or
// statement survives it; the preparation lease and retained values remain
// separately charged. A content invalidation restarts the whole walk through
// task 01's wrapper (at most twice) without replacing the owner, grant or
// original deadline.
func (p *RBNumericContextPreparation) Advance(ctx context.Context, request governor.Request, budget time.Duration) (result RBNumericContextAdvance, err error) {
	if p == nil || p.reader == nil || p.entry == nil || ctx == nil || request.Owner != p.entry.key.owner || request.Class != governor.Foreground || request.Writer || request.Budget.RawRows < rbNumericContextRaw+5 || budget <= 0 {
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
		if _, err = p.window(ctx, request, budget, &result); err != nil {
			return result, err
		}
		result.Ready = p.complete
		return result, nil
	}
	outcome, err := rbPrepareRestart(p.entry, p.pinned, func(attempt int) (browseFence, error) {
		if attempt > 0 {
			// Restart is from the beginning of the source. The wrapper already
			// discarded every retained value from the invalidated attempt.
			p.after = nil
			p.complete = false
			result.Restarts++
		}
		return p.window(ctx, request, budget, &result)
	})
	result.Restarts = outcome.Restarts
	if err != nil {
		return result, err
	}
	result.Ready = p.complete
	return result, nil
}

// window performs ONE admitted raw quantum and reduces it. The fence is compared
// before capture so a content move becomes a restart and every other move is a
// denial. All value groups for the window are reduced before the single retain
// pass; a failed window leaves no partial append.
func (p *RBNumericContextPreparation) window(ctx context.Context, request governor.Request, budget time.Duration, result *RBNumericContextAdvance) (fresh browseFence, err error) {
	p.copied = 0
	p.copyLimit = request.Need[governor.MemoryBytes] - p.reader.pool.NativeRowReservation()
	if p.copyLimit <= 0 {
		_ = p.entry.close()
		return fresh, governor.ErrSnapshotBudget
	}
	if request.Deadline.After(p.entry.lease.Deadline()) {
		request.Deadline = p.entry.lease.Deadline()
	}
	parent, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(p.entry.ctx, cancel)
	defer func() { stop(); cancel() }()
	result.Snapshot, err = p.reader.pool.ReadForecasted(parent, request, min(budget, time.Until(p.entry.lease.Deadline())), governor.SnapshotForecast{NativeRowBytes: p.reader.pool.NativeRowReservation(), MaxQueries: rbNumericContextMaxQueries}, func(scope *governor.ReadScope) error {
		query := func(name string, args ...any) ([][]any, error) {
			result.Statements++
			return scope.Query(readStatementID(name), args...)
		}
		in := p.view
		// The fence/horizon use the actual evaluation time; the relative-date
		// query anchor stays immutable for the whole preparation.
		in.Now = in.Now.Add(time.Since(p.started))
		f, e := readBrowseFence(query, in)
		if e != nil {
			return e
		}
		fresh = f
		if !p.pinnedSet {
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
		rows, e := p.readRows(query, in, f)
		if e != nil {
			return e
		}
		result.RawRows += len(rows)
		exhausted, e := p.reduce(scope, rows, f, result)
		if e != nil {
			return e
		}
		if exhausted {
			if e = p.entry.finish(true); e != nil {
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

func (p *RBNumericContextPreparation) readRows(query browseQuery, in BrowseInput, f browseFence) ([][]any, error) {
	name := "rb.numeric_context." + p.source.name()
	if p.after == nil {
		return query(name+"first", f.profile, in.Library, in.Kind, int64(rbNumericContextRaw))
	}
	if p.source == rbNumericContextLibrarySource {
		rows, err := query(name+"tail", f.profile, in.Library, in.Kind, p.after.id, int64(rbNumericContextRaw))
		if err != nil {
			return nil, err
		}
		if len(rows) > rbNumericContextRaw {
			return nil, ErrUnqualified
		}
		return rows, nil
	}
	if p.source == rbNumericContextReleaseSource {
		return p.readReleaseRows(query, name, in, f)
	}
	rows, err := query(name+"tail", f.profile, in.Library, in.Kind, p.numericKey(), p.after.id, int64(rbNumericContextRaw))
	if err != nil {
		return nil, err
	}
	if len(rows) < rbNumericContextRaw {
		// A full equal-key run may end before the window limit. The next-key seek
		// completes the window, so a short equality tail is never a false EOF.
		more, err := query(name+"next", f.profile, in.Library, in.Kind, p.numericKey(), int64(rbNumericContextRaw-len(rows)))
		if err != nil {
			return nil, err
		}
		rows = append(rows, more...)
	}
	if len(rows) > rbNumericContextRaw {
		return nil, ErrUnqualified
	}
	return rows, nil
}

// readReleaseRows uses the text release key for both the equality tail and the
// next-key completion.
func (p *RBNumericContextPreparation) readReleaseRows(query browseQuery, name string, in BrowseInput, f browseFence) ([][]any, error) {
	rows, err := query(name+"tail", f.profile, in.Library, in.Kind, p.after.text, p.after.id, int64(rbNumericContextRaw))
	if err != nil {
		return nil, err
	}
	if len(rows) < rbNumericContextRaw {
		more, err := query(name+"next", f.profile, in.Library, in.Kind, p.after.text, int64(rbNumericContextRaw-len(rows)))
		if err != nil {
			return nil, err
		}
		rows = append(rows, more...)
	}
	if len(rows) > rbNumericContextRaw {
		return nil, ErrUnqualified
	}
	return rows, nil
}

func (p *RBNumericContextPreparation) numericKey() any {
	switch p.source {
	case rbNumericContextYearSource, rbNumericContextLibrarySource:
		return int64(p.after.number)
	default:
		return p.after.number
	}
}

// reduce types, filters and reduces one raw window. The raw cursor always
// advances, including for rejected rows, so retirement cannot stall the walk.
func (p *RBNumericContextPreparation) reduce(scope *governor.ReadScope, raw [][]any, f browseFence, result *RBNumericContextAdvance) (bool, error) {
	if len(raw) > rbNumericContextRaw {
		return false, ErrUnqualified
	}
	var grouped [rbNumericContextRaw]rbNumericFacet
	used := 0
	var next *rbNumericContextPosition
	for _, row := range raw {
		if err := scope.Context().Err(); err != nil {
			return false, err
		}
		if len(row) != 17 {
			return false, ErrUnqualified
		}
		id, ok := row[0].(int64)
		if !ok || id <= 0 {
			return false, ErrUnqualified
		}
		year, ok := row[1].(int64)
		if !ok {
			return false, ErrUnqualified
		}
		position, e := p.position(row, id)
		if e != nil {
			return false, e
		}
		next = position
		item := [7]any{id, year, nil, nil, row[7], nil, row[8]}
		if !admittedItem(item[:], f) {
			continue
		}
		match, e := p.factsMatch(row, f)
		if e != nil {
			return false, e
		}
		if !match {
			continue
		}
		value := int64(rbNumericContextMissingLow)
		if p.field != RBNumericContextEligible {
			// Year/decade values share the published/parser domain. Matches
			// outside it still count in the total, never as a value. Two
			// sentinels keep the grouped reduction contiguous in year order:
			// low out-of-domain/zero years come first, high years last.
			switch {
			case year >= 1800 && year <= 2199:
				value = year
				if p.field == RBNumericContextDecade {
					value = year / 10 * 10
				}
			case year > 2199:
				value = rbNumericContextMissingHigh
			}
		}
		if used > 0 && grouped[used-1].Value == value {
			grouped[used-1].Count++
		} else {
			grouped[used] = rbNumericFacet{Value: value, Count: 1}
			used++
		}
	}
	if len(raw) > 0 {
		// The exact-byte pair is two registered queries; count them in the
		// window's statement evidence even though QueryExactBytes issues them.
		result.Statements += 2
		if err := p.anchorScalar(scope, raw[len(raw)-1]); err != nil {
			return false, err
		}
	}
	p.entry.cache.mu.Lock()
	priorLen, priorTotal := len(p.entry.facets), p.entry.total
	p.entry.cache.mu.Unlock()
	for _, group := range grouped[:used] {
		if err := p.entry.retainNumeric(group.Value, group.Count); err != nil {
			p.entry.cache.mu.Lock()
			if !p.entry.closed {
				p.entry.facets = p.entry.facets[:priorLen]
				p.entry.total = priorTotal
			}
			p.entry.cache.mu.Unlock()
			return false, err
		}
	}
	p.after = next
	return len(raw) < rbNumericContextRaw, nil
}

func (p *RBNumericContextPreparation) position(row []any, id int64) (*rbNumericContextPosition, error) {
	position := &rbNumericContextPosition{id: id}
	switch p.source {
	case rbNumericContextYearSource:
		position.number = float64(row[1].(int64))
	case rbNumericContextReleaseSource:
		key, ok := row[2].(string)
		if !ok || !utf8.ValidString(key) {
			return nil, ErrUnqualified
		}
		position.text = key
	case rbNumericContextRatingSource:
		key, ok := row[3].(float64)
		if !ok {
			return nil, ErrUnqualified
		}
		position.number = key
	case rbNumericContextCriticSource:
		key, ok := row[4].(float64)
		if !ok {
			return nil, ErrUnqualified
		}
		position.number = key
	}
	return position, nil
}

// factsMatch evaluates the complete scalar AST and then the existing personal
// AST. Personal facts are validated only when a personal node is present; an
// unrelated malformed row must not fail an ordinary scalar read. A malformed
// personal row that IS required is refused rather than silently dropped.
func (p *RBNumericContextPreparation) factsMatch(row []any, f browseFence) (bool, error) {
	title, ok := row[5].(string)
	if !ok || !utf8.ValidString(title) {
		return false, ErrUnqualified
	}
	release, err := rbNumericContextText(row[9])
	if err != nil {
		return false, err
	}
	added, err := rbNumericContextText(row[10])
	if err != nil {
		return false, err
	}
	community, err := rbNumericContextNumber(row[3])
	if err != nil {
		return false, err
	}
	critic, err := rbNumericContextNumber(row[4])
	if err != nil {
		return false, err
	}
	facts := rbScalarCompleteFacts{
		title:           title,
		library:         f.library,
		kind:            f.kind,
		year:            row[1].(int64),
		releaseDate:     release,
		dateAdded:       added,
		communityRating: community,
		criticRating:    critic,
		now:             p.view.Now,
	}
	if !p.scalar.matches(facts) {
		return false, nil
	}
	if p.personal.node == nil {
		return true, nil
	}
	var personal rbPersonalFilterFacts
	if row[11] != nil {
		public, ok := row[6].([]byte)
		item, valid := row[12].([]byte)
		if !ok || !valid || len(public) != 16 || len(item) != 16 || !reflect.DeepEqual(public, item) || row[13] != f.libraryID {
			return false, ErrUnqualified
		}
		favorite, okF := row[14].(int64)
		watchlisted, okW := row[15].(int64)
		if !okF || !okW || favorite < 0 || favorite > 1 || watchlisted < 0 || watchlisted > 1 {
			return false, ErrUnqualified
		}
		personal.present = true
		personal.favorite = favorite == 1
		personal.watchlisted = watchlisted == 1
		if row[16] != nil {
			rating, ok := row[16].(int64)
			if !ok || rating < 0 || rating > 10 {
				return false, ErrUnqualified
			}
			personal.rating = &rating
		}
	}
	return p.personal.matches(personal), nil
}

func rbNumericContextText(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok || !utf8.ValidString(text) {
		return "", ErrUnqualified
	}
	return text, nil
}

func rbNumericContextNumber(value any) (rbScalarNumber, error) {
	switch number := value.(type) {
	case nil:
		return rbScalarNumber{}, nil
	case float64:
		if number <= 0 {
			return rbScalarNumber{}, nil
		}
		return rbScalarNumber{value: number, present: true}, nil
	case int64:
		if number <= 0 {
			return rbScalarNumber{}, nil
		}
		return rbScalarNumber{value: float64(number), present: true}, nil
	}
	return rbScalarNumber{}, ErrUnqualified
}

// anchorScalar re-reads the window anchor through the complete entity PK. The
// octet pair charges the exact wide-value bytes before any copy; the payload
// must match every streamed scalar fact exactly.
func (p *RBNumericContextPreparation) anchorScalar(scope *governor.ReadScope, anchor []any) error {
	id := anchor[0].(int64)
	row, err := scope.QueryExactBytes(readStatementID("rb.numeric_context.scalar_octets"), readStatementID("rb.numeric_context.scalar"), []int{2, 3, 4}, id)
	if err != nil {
		return err
	}
	if len(row) != 7 || row[0] != id {
		return ErrStale
	}
	for _, pair := range []struct{ payload, streamed int }{{1, 1}, {2, 5}, {3, 9}, {4, 10}, {5, 3}, {6, 4}} {
		if !reflect.DeepEqual(row[pair.payload], anchor[pair.streamed]) {
			return ErrStale
		}
	}
	var bytes int64
	for _, value := range row[2:5] {
		if text, ok := value.(string); ok {
			bytes += int64(len(text))
		}
	}
	return p.reserve(64 + 4*32 + 2*bytes)
}

func (p *RBNumericContextPreparation) reserve(n int64) error {
	if n < 0 || n > p.copyLimit-p.copied {
		return governor.ErrSnapshotBudget
	}
	p.copied += n
	return nil
}

// Page copies one page of the complete reduction inside a fresh authorized
// response snapshot. The exact whole fence must match before any copy, and the
// first delivery linearizes with the governor like every other prepared cache
// purpose.
func (p *RBNumericContextPreparation) Page(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput, after *RBNumericContextCursor, limit int) (RBNumericContextPage, BrowseReadResult, error) {
	if p == nil || p.reader == nil || p.entry == nil {
		return RBNumericContextPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, p.reader.pool, current.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (RBNumericContextPage, BrowseReadResult, error) {
		current.Now = at
		return p.page(ctx, request, budget, output, current, after, limit)
	})
}

func (p *RBNumericContextPreparation) page(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput, after *RBNumericContextCursor, limit int) (page RBNumericContextPage, result BrowseReadResult, err error) {
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
	validation, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancel()
	result.Publication, err = p.reader.pool.ReadForecasted(validation, request, time.Until(deadline), governor.SnapshotForecast{NativeRowBytes: p.reader.pool.NativeRowReservation(), MaxQueries: rbNumericContextMaxQueries}, func(scope *governor.ReadScope) error {
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
		return RBNumericContextPage{}, result, err
	}
	if err = ctx.Err(); err != nil {
		return RBNumericContextPage{}, result, err
	}
	// Confirm the freshly observed token/horizon at actual publication, after
	// native cleanup; expiry must not leak a copied formerly-authorized page.
	at := current.Now.Add(time.Since(started))
	expires, e1 := time.Parse(time.RFC3339Nano, fresh.expiresAt)
	horizon, e2 := time.Parse(time.RFC3339Nano, fresh.horizon)
	if e1 != nil || e2 != nil || !at.Before(expires) || !at.Before(horizon) {
		_ = p.entry.close()
		return RBNumericContextPage{}, result, ErrDenied
	}
	if !time.Now().Before(deadline) {
		return RBNumericContextPage{}, result, context.DeadlineExceeded
	}
	if err = output.Authority.CheckContext(ctx, output.Grant, output.Request); err != nil {
		return RBNumericContextPage{}, result, err
	}
	page = RBNumericContextPage{ExactTotal: prepared.total, Exact: prepared.exhausted, Exhausted: prepared.exhausted, Statements: result.PublicationStatements}
	page.Total, page.TotalIsLowerBound = rbNumericContextCount(prepared.total)
	for _, value := range prepared.facets {
		if value.Value == rbNumericContextMissingLow || value.Value == rbNumericContextMissingHigh {
			continue
		}
		count, lower := rbNumericContextCount(value.Count)
		page.Values = append(page.Values, RBNumericContextValue{Value: value.Value, Count: count, CountIsLowerBound: lower})
	}
	if !prepared.exhausted {
		page.After = &RBNumericContextCursor{key: p.entry.key, fence: fresh, position: prepared.next}
	}
	return page, result, nil
}

// Count returns the complete prepared count of every eligible match. It is the
// same private exact count as rb_exact_count, not a universal Browse total; the
// lower-bound publication rule is applied only by Page.
func (p *RBNumericContextPreparation) Count(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput) (int64, BrowseReadResult, error) {
	page, result, err := p.Page(ctx, request, budget, output, current, nil, 1)
	if err != nil {
		return 0, result, err
	}
	if !page.Exact {
		return 0, result, ErrUnqualified
	}
	return page.ExactTotal, result, nil
}

func (p *RBNumericContextPreparation) Close() error {
	if p == nil || p.entry == nil {
		return ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.entry.close()
}

func rbNumericContextCount(count int64) (int64, bool) {
	if count > rbNumericContextThreshold {
		return rbNumericContextThreshold, true
	}
	return count, false
}
