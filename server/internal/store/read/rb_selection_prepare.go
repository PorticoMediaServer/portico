package read

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Complete selection/rank/range preparation.
//
// This producer is a private preparation recipe, not a second scheduler or a
// new authority. It uses the SAME one governor preparation lease and bounded
// content-restart rules as task 02 (rb_order_prepare.go), but retains one
// complete typed query/order result instead of IDs alone: every admitted row
// keeps its order key and its maintained duration metric. Rank, selection
// membership, an arbitrary range and the duration aggregate are then answered
// from that ONE completed walk. There is no OFFSET, no second COUNT scan and no
// hidden per-page re-walk.
//
// Legacy semantics copied from catalog/browse_range.go, browse_anchors.go and
// browse_selection_*.go:
//
//   - a range start is a 0-based position in the requested order, and a
//     descending order mirrors ascending positions rather than re-reading;
//   - an anchor names an entity's exact position, or the beginning of a key
//     run (the first equal/next key in the requested direction);
//   - the duration aggregate sums each included title once and is an explicit
//     lower bound while any included title has no usable duration;
//   - selection is a bounded set of displayed entities, deduplicated by
//     identity, with the same exact rank positions and duration aggregate.
//
// The preparation is never an eligibility proof and never an authorization
// grant: the fresh publication handoff still matches the whole current fence
// before any ID is copied.
const (
	// rbSelectionRaw bounds one raw source window exactly like the common browse
	// window. Rejected rows and lookahead count against it.
	rbSelectionRaw = MaxRaw
	// rbSelectionQuantaPerAdvance bounds one poll to a few snapshots. The
	// original 12s preparation deadline still bounds the whole job.
	rbSelectionQuantaPerAdvance = 8
	// rbSelectionMaxQueries is the finite proof recipe for a publication window:
	// the common fence plus bounded source legs.
	rbSelectionMaxQueries = 24
	// rbSelectionMaxIDs bounds one selection request. A larger set is refused
	// rather than silently truncated: a partial selection would be a false
	// "select all" descriptor.
	rbSelectionMaxIDs = 4096
	// rbSelectionMaxResults is the published page bound, matching the existing
	// browse window contract.
	rbSelectionMaxResults = 200
	// rbSelectionJobDomain keeps this preparation's governor job identity
	// disjoint from the ordered-ID cache purpose.
	rbSelectionJobDomain = 398
)

// RBSelectionInput names one finite compiled source order. After must be nil;
// continuation is a private preparation cursor, never a caller anchor.
type RBSelectionInput struct {
	View BrowseInput
	Key  RBOrderKey
}

// RBSelectionQuery is one lookup over the completed result. Start is a 0-based
// position in the request's direction. AnchorID and AnchorKey are optional and
// mutually exclusive; AnchorKey is only defined for a title order. SelectedIDs
// is an unordered set of displayed entity identities.
type RBSelectionQuery struct {
	Start        int
	Limit        int
	AnchorID     int64
	AnchorKey    string
	AnchorKeySet bool
	SelectedIDs  []int64
}

// RBSelectionEntry is one displayed entity in a range or selection result.
// Rank is its exact 0-based position in the requested direction.
type RBSelectionEntry struct {
	ID            int64
	Rank          int64
	DurationMs    int64
	DurationKnown bool
}

// RBSelectionResult is the complete answer of one lookup. Total is the exact
// admitted membership of the finished preparation, never a capped count.
// DurationIsLowerBound is set while any included title has no usable duration.
type RBSelectionResult struct {
	Entries                       []RBSelectionEntry
	Start                         int
	Total                         int64
	Exhausted                     bool
	AnchorRank                    int64
	AnchorFound                   bool
	DurationMs                    int64
	DurationIsLowerBound          bool
	DurationMissing               int64
	Selection                     []RBSelectionEntry
	SelectionMissing              []int64
	SelectionDurationMs           int64
	SelectionDurationIsLowerBound bool
	Statements                    int
}

type RBSelectionPrepareAdvance struct {
	Ready      bool
	Restarts   int
	RawRows    int
	Statements int
	Snapshot   governor.SnapshotResult
}

// RBSelectionPrepareReader owns the bounded preparation jobs for this harness
// composition. It creates no goroutine and no second queue.
type RBSelectionPrepareReader struct {
	mu   sync.Mutex
	jobs [governor.PreparationMaxActive]*RBSelectionPreparation
	pool *governor.Snapshots
	a    *governor.Authority
}

// RBSelectionPreparation is one opaque preparation job. Polls retain and reuse
// it; they never re-admit, re-size or re-deadline it. No snapshot, Rows,
// statement or SQL handle survives a quantum.
type RBSelectionPreparation struct {
	mu       sync.Mutex
	reader   *RBSelectionPrepareReader
	entry    *rbSelectionEntry
	view     BrowseInput
	key      RBOrderKey
	query    rbPreparedKey
	started  time.Time
	after    *rbOrderPosition
	complete bool
	pinned   browseFence
	// pinnedSet records the first complete fence this preparation observed. A
	// content restart keeps it; every non-content field must still match.
	pinnedSet bool
	// copy accounting is per window (one bounded snapshot), never a renewed
	// lease: the governor funds each window and the entry funds retained rows.
	copied, copyLimit, nativeLimit int64
}

// rbSelectionRow is one admitted retained row. The key is the typed order
// value; durationMs is the maintained metric converted to integer
// milliseconds and durationKnown is false when the metric is absent or zero.
type rbSelectionRow struct {
	key           rbOrderSourceKey
	durationMs    int64
	durationKnown bool
}

var errRBSelectionCapacity = errors.New("selection preparation exceeds admitted retained bytes")

func NewRBSelectionPrepareReader(pool *governor.Snapshots, a *governor.Authority) (*RBSelectionPrepareReader, error) {
	if pool == nil || a == nil || !pool.OwnedBy(a) {
		return nil, ErrInput
	}
	return &RBSelectionPrepareReader{pool: pool, a: a}, nil
}

// BeginRBSelectionPrepare is an EXPLICIT preparation start. Polls retain the
// returned job; they must not Begin again with a fresh deadline. in.View.Now is
// trusted server request-entry time and the immutable query anchor.
func (r *RBSelectionPrepareReader) BeginRBSelectionPrepare(ctx context.Context, owner governor.OwnerID, bytes int64, in RBSelectionInput) (*RBSelectionPreparation, error) {
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
	// An identical live job attaches to the SAME lease; a poll never renews it.
	free := -1
	for i, job := range r.jobs {
		if job != nil && !job.entry.closed {
			if job.query == key {
				if err := job.entry.check(); err != nil {
					return nil, err
				}
				return job, nil
			}
			continue
		}
		if free < 0 {
			free = i
		}
	}
	if free < 0 {
		return nil, governor.ErrBusy
	}
	decision := r.a.BeginPreparation(ctx, governor.PreparationRequest{Key: governor.JobKey{Domain: rbSelectionJobDomain, ID: hex.EncodeToString(key.query[:])}, Owner: owner, Deadline: deadline, ReservedBytes: bytes, Dependencies: governor.PublicationFootprint{{Library: in.View.Library}}})
	if decision.Lease == nil {
		return nil, governor.ErrBusy
	}
	entry := &rbSelectionEntry{ctx: ctx, authority: r.a, lease: decision.Lease, title: in.Key == RBOrderByTitle, descending: in.View.Descending}
	job := &RBSelectionPreparation{reader: r, entry: entry, view: in.View, key: in.Key, query: key, started: time.Now()}
	r.jobs[free] = job
	return job, nil
}

// Advance performs a bounded number of admitted raw quanta. Each quantum is a
// separate snapshot with genuine EOF and a private source cursor; no handle is
// retained across them. A content invalidation restarts the whole walk through
// task 01's rule (at most twice), keeping the owner, grant and deadline.
func (p *RBSelectionPreparation) Advance(ctx context.Context, request governor.Request, budget time.Duration) (result RBSelectionPrepareAdvance, err error) {
	if p == nil || p.reader == nil || p.entry == nil || ctx == nil || request.Owner != p.query.owner || request.Class != governor.Foreground || request.Writer || request.Budget.RawRows < rbSelectionRaw+5 || budget <= 0 {
		return result, ErrInput
	}
	if !p.mu.TryLock() {
		return result, governor.ErrBusy
	}
	defer p.mu.Unlock()
	if p.complete {
		err = p.entry.check()
		result.Ready = err == nil
		return result, err
	}
	if !p.pinnedSet {
		// The first complete fence must be observed before the restart rule can
		// compare a pinned authorization context. One bounded probe window
		// establishes it; nothing can restart inside a single snapshot.
		if _, err = p.window(ctx, request, budget, &result); err != nil {
			return result, err
		}
		result.Ready = p.complete
		return result, nil
	}
	for {
		if err = p.entry.check(); err != nil {
			return result, err
		}
		fresh, attemptErr := p.walk(ctx, request, budget, &result)
		if attemptErr != nil && !errors.Is(attemptErr, errRBPrepareContent) {
			return result, attemptErr
		}
		if !rbPreparePinnedFenceEqual(p.pinned, fresh) {
			// Viewer/policy/lifetime/source structure changed. A content restart
			// is never an authorization refresh.
			_ = p.entry.close()
			return result, ErrDenied
		}
		if attemptErr == nil {
			result.Ready = p.complete
			return result, nil
		}
		if result.Restarts >= rbPrepareRestartLimit {
			_ = p.entry.close()
			return result, attemptErr
		}
		if err = p.entry.reset(fresh); err != nil {
			return result, err
		}
		// Restart is from the beginning of the source, never from a stale
		// mid-stream cursor that would publish a truncated set.
		p.after = nil
		p.complete = false
		result.Restarts++
	}
}

func (p *RBSelectionPreparation) walk(ctx context.Context, request governor.Request, budget time.Duration, result *RBSelectionPrepareAdvance) (browseFence, error) {
	deadline := time.Now().Add(budget)
	if request.Deadline.Before(deadline) {
		deadline = request.Deadline
	}
	if p.entry.lease.Deadline().Before(deadline) {
		deadline = p.entry.lease.Deadline()
	}
	var fresh browseFence
	for n := 0; n < rbSelectionQuantaPerAdvance; n++ {
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
// denial. All values for the window are typed, byte-charged and retained before
// the next quantum; a failed window leaves no partial append.
func (p *RBSelectionPreparation) window(ctx context.Context, request governor.Request, budget time.Duration, result *RBSelectionPrepareAdvance) (fresh browseFence, err error) {
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
	result.Snapshot, err = p.reader.pool.ReadForecasted(parent, request, min(budget, time.Until(p.entry.lease.Deadline())), governor.SnapshotForecast{NativeRowBytes: p.reader.pool.NativeRowReservation(), MaxQueries: rbSelectionMaxQueries}, func(scope *governor.ReadScope) error {
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
		var exhausted bool
		if p.key == RBOrderByID {
			raw, exhausted, e = p.readIDRows(query, in)
		} else {
			raw, exhausted, e = p.readTitleRows(query, in)
		}
		if e != nil {
			return e
		}
		result.RawRows += len(raw)
		if e = p.reduceRows(scope, raw, f); e != nil {
			return e
		}
		if exhausted {
			if e = p.entry.finish(p.key == RBOrderByTitle); e != nil {
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

// readIDRows streams the complete current library/kind entity IDs through the
// covering catalog_entities_library seek. The window binds cap+1 and treats a
// longer answer as a source that ignored its bound; a short window is the only
// EOF.
func (p *RBSelectionPreparation) readIDRows(query browseQuery, in BrowseInput) ([][]any, bool, error) {
	bound := int64(rbSelectionRaw + 1)
	var raw [][]any
	var err error
	if p.after == nil {
		raw, err = query("rb.selection_prepare.first", in.Library, in.Kind, bound)
	} else {
		raw, err = query("rb.selection_prepare.tail", in.Library, in.Kind, p.after.id, bound)
	}
	if err != nil {
		return nil, false, err
	}
	if len(raw) > rbSelectionRaw+1 {
		return nil, false, ErrUnqualified
	}
	if len(raw) > rbSelectionRaw {
		return raw[:rbSelectionRaw], false, nil
	}
	return raw, true, nil
}

// readTitleRows streams the same complete set through the existing dual title
// points, so actual title keys use those indexes rather than re-sorting
// hydrated IDs. The byte leg is measured in this same snapshot before the
// payload is copied, and the payload must match every measured length exactly.
func (p *RBSelectionPreparation) readTitleRows(query browseQuery, in BrowseInput) ([][]any, bool, error) {
	name := "rb.selection_prepare.title.ignore."
	if in.KeepArticles {
		name = "rb.selection_prepare.title.keep."
	}
	leg := func(part string, expected int, args ...any) ([][]any, error) {
		sizes, err := query(name+part+".bytes", args...)
		if err != nil {
			return nil, err
		}
		if len(sizes) > expected || len(sizes) > rbSelectionRaw+1 {
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
		rows, err := query(name+part, args...)
		if err != nil {
			return nil, err
		}
		if len(rows) != len(sizes) {
			return nil, ErrStale
		}
		for i, row := range rows {
			if len(row) != 7 || row[0] != sizes[i][0] {
				return nil, ErrStale
			}
			for _, pair := range []struct {
				col    int
				length int
			}{{1, 1}, {2, 2}} {
				v, ok := row[pair.col].(string)
				if !ok || int64(len(v)) != sizes[i][pair.length] {
					return nil, ErrStale
				}
			}
		}
		p.copied += 8192 + int64(len(rows))*1024 + 2*total
		return rows, nil
	}
	var raw [][]any
	var err error
	remaining := rbSelectionRaw + 1
	if p.after == nil {
		raw, err = leg("first", remaining, in.Library, in.Kind, int64(remaining))
	} else {
		raw, err = leg("tail", remaining, in.Library, in.Kind, p.after.key, p.after.id, int64(remaining))
		if err == nil && len(raw) < remaining {
			// A full equal-key run may end before the window limit. The next-key
			// seek completes the window, so a short equality tail is never a
			// false EOF.
			var more [][]any
			more, err = leg("next", remaining-len(raw), in.Library, in.Kind, p.after.key, int64(remaining-len(raw)))
			raw = append(raw, more...)
		}
	}
	if err != nil {
		return nil, false, err
	}
	if len(raw) > rbSelectionRaw+1 {
		return nil, false, ErrUnqualified
	}
	if len(raw) > rbSelectionRaw {
		return raw[:rbSelectionRaw], false, nil
	}
	return raw, true, nil
}

// reduceRows types and validates one raw window. Every raw row, including a
// rejected one, must advance the private source order; a non-monotone source is
// refused rather than retained as a mixed stream.
func (p *RBSelectionPreparation) reduceRows(scope *governor.ReadScope, raw [][]any, f browseFence) error {
	if len(raw) > rbSelectionRaw {
		return ErrUnqualified
	}
	prev := rbOrderSourceKey{}
	if p.after != nil {
		prev = rbOrderSourceKey{key: p.after.key, id: p.after.id}
	}
	for _, row := range raw {
		if err := scope.Context().Err(); err != nil {
			return err
		}
		key, durationMs, durationKnown, admitted, err := p.reduceRow(row, f)
		if err != nil {
			return err
		}
		if (prev.id != 0 || prev.key != "") && !rbOrderAfter(key, prev, p.key, false) {
			return ErrUnqualified
		}
		prev = key
		if !admitted {
			continue
		}
		if err := p.reserve(48 + 2*int64(len(key.key))); err != nil {
			return err
		}
		if err = p.entry.retain(rbSelectionRow{key: key, durationMs: durationMs, durationKnown: durationKnown}); err != nil {
			return err
		}
	}
	if len(raw) > 0 {
		p.after = &rbOrderPosition{key: prev.key, id: prev.id}
	}
	return nil
}

// reduceRow is the typed source-key/ID/duration reducer. It never invents
// eligibility: retired and age-restricted rows still consume the raw window but
// contribute no retained row. Duration is the maintained longest-file metric in
// seconds; zero or absent is an explicit missing fact, not zero milliseconds.
func (p *RBSelectionPreparation) reduceRow(row []any, f browseFence) (rbOrderSourceKey, int64, bool, bool, error) {
	if p.key == RBOrderByID {
		if len(row) != 5 {
			return rbOrderSourceKey{}, 0, false, false, ErrUnqualified
		}
		id, ok := row[0].(int64)
		if !ok || id <= 0 {
			return rbOrderSourceKey{}, 0, false, false, ErrUnqualified
		}
		item := [7]any{id, "", nil, nil, row[1], row[2], row[3]}
		durationMs, durationKnown, err := rbSelectionDurationMs(row[4])
		if err != nil {
			return rbOrderSourceKey{}, 0, false, false, err
		}
		return rbOrderSourceKey{id: id}, durationMs, durationKnown, admittedItem(item[:], f), nil
	}
	if len(row) != 7 {
		return rbOrderSourceKey{}, 0, false, false, ErrUnqualified
	}
	id, ok := row[0].(int64)
	key, okKey := row[1].(string)
	title, okTitle := row[2].(string)
	if !ok || !okKey || !okTitle || id <= 0 || !utf8.ValidString(key) || !utf8.ValidString(title) {
		return rbOrderSourceKey{}, 0, false, false, ErrUnqualified
	}
	item := [7]any{id, key, nil, title, row[3], row[4], row[5]}
	durationMs, durationKnown, err := rbSelectionDurationMs(row[6])
	if err != nil {
		return rbOrderSourceKey{}, 0, false, false, err
	}
	return rbOrderSourceKey{id: id, key: key}, durationMs, durationKnown, admittedItem(item[:], f), nil
}

func (p *RBSelectionPreparation) reserve(n int64) error {
	if n < 0 || n > p.copyLimit-p.copied {
		return governor.ErrSnapshotBudget
	}
	p.copied += n
	return nil
}

// Lookup answers rank, range, selection and duration from the ONE finished
// walk inside a fresh authorized response snapshot. The exact whole fence must
// match before any copy, and the first delivery linearizes with the governor
// exactly like every other preparation.
func (p *RBSelectionPreparation) Lookup(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput, query RBSelectionQuery) (RBSelectionResult, BrowseReadResult, error) {
	if p == nil || p.reader == nil || p.entry == nil {
		return RBSelectionResult{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, p.reader.pool, current.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (RBSelectionResult, BrowseReadResult, error) {
		current.Now = at
		return p.lookup(ctx, request, budget, output, current, query)
	})
}

func (p *RBSelectionPreparation) lookup(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput, q RBSelectionQuery) (result RBSelectionResult, cost BrowseReadResult, err error) {
	started := time.Now()
	defer func() { cost.Elapsed = time.Since(started) }()
	if ctx == nil || output.Authority == nil || output.Grant == nil || output.Request.Writer || output.Request.Owner != request.Owner || output.Request.Class != request.Class || len(current.Database) == 0 || len(current.Database) > 256 || len(current.TokenHash) == 0 || len(current.TokenHash) > 256 || current.Library <= 0 || current.Kind < 1 || current.Kind > 99 || budget <= 0 || current.After != nil || current.Now.IsZero() || !validRBSelectionQuery(q) || request.Owner != p.query.owner || request.Class != governor.Foreground || request.Writer {
		return result, cost, ErrInput
	}
	if !p.mu.TryLock() {
		return result, cost, governor.ErrBusy
	}
	defer p.mu.Unlock()
	if !p.complete {
		return result, cost, governor.ErrBusy
	}
	deadline := earliest(request.Deadline, p.entry.lease.Deadline())
	deadline = earliest(deadline, time.Now().Add(min(budget, time.Second)))
	request.Deadline = deadline
	var fresh browseFence
	validation, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancel()
	cost.Publication, err = p.reader.pool.ReadForecasted(validation, request, time.Until(deadline), governor.SnapshotForecast{NativeRowBytes: p.reader.pool.NativeRowReservation(), MaxQueries: rbSelectionMaxQueries}, func(scope *governor.ReadScope) error {
		run := func(name string, args ...any) ([][]any, error) {
			cost.PublicationStatements++
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
		fresh, e = readBrowseFence(run, view)
		if e != nil {
			return e
		}
		if e = scope.Context().Err(); e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if 2*rbPreparedFenceBytes(fresh)+4096+int64(q.Limit)*64+64*int64(len(q.SelectedIDs)) > output.Request.Need[governor.MemoryBytes]/2 {
			return governor.ErrSnapshotBudget
		}
		if e = output.Authority.CheckContext(validation, output.Grant, output.Request); e != nil {
			return e
		}
		result, e = p.entry.handoff(fresh, q)
		return e
	})
	cost.Attempts = 1
	if !cost.Publication.Started.IsZero() {
		cost.PublicationWindows = append(cost.PublicationWindows, cost.Publication)
	}
	if cost.Publication.Refusal != nil {
		cost.Refusal = cost.Publication.Refusal
		err = governor.ErrBusy
	}
	if err != nil {
		if errors.Is(err, ErrDenied) || errors.Is(err, ErrStale) || errors.Is(err, ErrUnqualified) {
			_ = p.entry.close()
		}
		return RBSelectionResult{}, cost, err
	}
	if err = ctx.Err(); err != nil {
		return RBSelectionResult{}, cost, err
	}
	// Confirm the freshly observed token/horizon at actual publication, after
	// native cleanup; expiry must not leak a copied formerly-authorized result.
	at := current.Now.Add(time.Since(started))
	expires, e1 := time.Parse(time.RFC3339Nano, fresh.expiresAt)
	horizon, e2 := time.Parse(time.RFC3339Nano, fresh.horizon)
	if e1 != nil || e2 != nil || !at.Before(expires) || !at.Before(horizon) {
		_ = p.entry.close()
		return RBSelectionResult{}, cost, ErrDenied
	}
	if !time.Now().Before(deadline) {
		return RBSelectionResult{}, cost, context.DeadlineExceeded
	}
	if err = output.Authority.CheckContext(ctx, output.Grant, output.Request); err != nil {
		return RBSelectionResult{}, cost, err
	}
	result.Statements = cost.PublicationStatements
	return result, cost, nil
}

func validRBSelectionQuery(q RBSelectionQuery) bool {
	if q.Start < 0 || q.Limit < 1 || q.Limit > rbSelectionMaxResults || len(q.SelectedIDs) > rbSelectionMaxIDs || q.AnchorID < 0 || (q.AnchorKeySet && (q.AnchorKey == "" || q.AnchorID != 0 || len(q.AnchorKey) > 1024)) {
		return false
	}
	for _, id := range q.SelectedIDs {
		if id <= 0 {
			return false
		}
	}
	return true
}

func (p *RBSelectionPreparation) Close() error {
	if p == nil || p.entry == nil {
		return ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.entry.close()
}

// rbSelectionEntry is ONE values-only preparation holding. It owns no SQL
// capability; the governor lease is the single admission and physical-custody
// authority.
type rbSelectionEntry struct {
	mu         sync.Mutex
	ctx        context.Context
	authority  *governor.Authority
	lease      *governor.PreparationLease
	fence      browseFence
	title      bool
	descending bool
	rows       []rbSelectionRow
	byID       []int32
	payload    int64
	retained   int64
	captured   bool
	complete   bool
	delivered  bool
	closed     bool
}

func (e *rbSelectionEntry) check() error {
	if e == nil {
		return ErrInput
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.checkLocked()
}

func (e *rbSelectionEntry) checkLocked() error {
	if e == nil || e.closed {
		return ErrInput
	}
	if err := e.ctx.Err(); err != nil {
		_ = e.closeLocked()
		return err
	}
	if !time.Now().Before(e.lease.Deadline()) {
		_ = e.closeLocked()
		return governor.ErrPreparationExpired
	}
	if !e.delivered {
		if err := e.authority.CheckPreparation(e.lease); err != nil {
			_ = e.closeLocked()
			return err
		}
	}
	return nil
}

// capture pins the WHOLE first complete fence and allocates the retained vector
// from the separately funded preparation payload, never from source columns
// times a maximum length.
func (e *rbSelectionEntry) capture(f browseFence) error {
	if e == nil {
		return ErrInput
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkLocked(); err != nil {
		return err
	}
	if e.captured && e.fence != f {
		_ = e.closeLocked()
		return ErrStale
	}
	if !e.captured {
		e.payload = e.lease.PayloadBytes()
		if e.payload < 8192 {
			_ = e.closeLocked()
			return errRBSelectionCapacity
		}
		e.rows = make([]rbSelectionRow, 0, int(e.payload/64))
	}
	e.fence = f
	e.captured = true
	return nil
}

// reset discards every value from an invalidated content attempt while keeping
// the physical governor grant, lease and deadline. A delivered entry is never
// restartable.
func (e *rbSelectionEntry) reset(f browseFence) error {
	if e == nil {
		return ErrInput
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkLocked(); err != nil {
		return err
	}
	if e.delivered {
		return ErrStale
	}
	e.rows = e.rows[:0]
	e.byID = nil
	e.retained = 0
	e.complete = false
	if e.captured {
		e.fence = f
	} else {
		e.fence = browseFence{}
	}
	return nil
}

// retain appends one admitted row, checking capacity BEFORE append so a failed
// window can never leave a partial ordered fragment behind.
func (e *rbSelectionEntry) retain(row rbSelectionRow) error {
	if e == nil {
		return ErrInput
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkLocked(); err != nil {
		return err
	}
	if !e.captured || e.complete || e.closed || row.key.id <= 0 || len(row.key.key) > 1<<20 {
		return ErrInput
	}
	cost := 48 + 2*int64(len(row.key.key))
	if e.retained+cost > e.payload || len(e.rows) >= cap(e.rows) {
		_ = e.closeLocked()
		return errRBSelectionCapacity
	}
	e.rows = append(e.rows, row)
	e.retained += cost
	return nil
}

// finish publishes readiness through the SAME lease. A title order builds one
// charged identity index so selection and anchor lookups are exact binary
// searches, never a per-query scan.
func (e *rbSelectionEntry) finish(title bool) error {
	if e == nil {
		return ErrInput
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkLocked(); err != nil {
		return err
	}
	if !e.captured || e.complete {
		return ErrInput
	}
	if title {
		cost := 4 * int64(len(e.rows))
		if cost > e.payload-e.retained {
			_ = e.closeLocked()
			return errRBSelectionCapacity
		}
		index := make([]int32, len(e.rows))
		for i := range index {
			index[i] = int32(i)
		}
		slices.SortFunc(index, func(a, b int32) int {
			return compareInt64(e.rows[a].key.id, e.rows[b].key.id)
		})
		e.byID = index
		e.retained += cost
	}
	if err := e.authority.ReadyPreparation(e.lease); err != nil {
		_ = e.closeLocked()
		return err
	}
	e.complete = true
	return nil
}

// handoff is invoked ONLY inside the fresh authorized response snapshot, AFTER
// its caller has acquired independent output custody. The full fence matches
// before ANY value is copied, then the governor linearizes the first delivery.
func (e *rbSelectionEntry) handoff(f browseFence, q RBSelectionQuery) (RBSelectionResult, error) {
	if e == nil {
		return RBSelectionResult{}, ErrInput
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkLocked(); err != nil {
		return RBSelectionResult{}, err
	}
	if !e.complete || !e.captured {
		return RBSelectionResult{}, ErrInput
	}
	if e.fence != f {
		_ = e.closeLocked()
		return RBSelectionResult{}, ErrStale
	}
	if !validRBSelectionQuery(q) || (q.AnchorKeySet && !e.title) {
		return RBSelectionResult{}, ErrInput
	}
	n := len(e.rows)
	if q.Start > n {
		return RBSelectionResult{}, ErrInput
	}
	out := RBSelectionResult{Start: q.Start, Total: int64(n)}
	// Exact range: positions are sliced from the finished order, never sought
	// with OFFSET and never re-counted.
	count := min(q.Limit, n-q.Start)
	for k := 0; k < count; k++ {
		rank := int64(q.Start + k)
		row := &e.rows[e.ascIndexForRank(rank, n)]
		out.Entries = append(out.Entries, RBSelectionEntry{ID: row.key.id, Rank: rank, DurationMs: row.durationMs, DurationKnown: row.durationKnown})
		if err := rbSelectionAddDuration(&out.DurationMs, &out.DurationMissing, *row); err != nil {
			return RBSelectionResult{}, err
		}
	}
	out.Exhausted = q.Start+count >= n
	out.DurationIsLowerBound = out.DurationMissing > 0
	if q.AnchorID > 0 {
		if i, ok := e.ascIndexForID(q.AnchorID); ok {
			out.AnchorRank, out.AnchorFound = e.rankForAsc(i, n), true
		}
	}
	if q.AnchorKeySet {
		if rank, ok := e.anchorKeyRank(q.AnchorKey, n); ok {
			out.AnchorRank, out.AnchorFound = rank, true
		}
	}
	if len(q.SelectedIDs) > 0 {
		var selectionMissing int64
		seen := make(map[int64]struct{}, len(q.SelectedIDs))
		for _, id := range q.SelectedIDs {
			if _, duplicate := seen[id]; duplicate {
				continue
			}
			seen[id] = struct{}{}
			i, ok := e.ascIndexForID(id)
			if !ok {
				out.SelectionMissing = append(out.SelectionMissing, id)
				continue
			}
			row := &e.rows[i]
			rank := e.rankForAsc(i, n)
			out.Selection = append(out.Selection, RBSelectionEntry{ID: id, Rank: rank, DurationMs: row.durationMs, DurationKnown: row.durationKnown})
			if err := rbSelectionAddDuration(&out.SelectionDurationMs, &selectionMissing, *row); err != nil {
				return RBSelectionResult{}, err
			}
		}
		out.SelectionDurationIsLowerBound = selectionMissing > 0
	}
	if !e.delivered {
		if err := e.authority.HandoffPreparation(e.lease); err != nil {
			_ = e.closeLocked()
			return RBSelectionResult{}, err
		}
		e.delivered = true
	}
	return out, nil
}

func (e *rbSelectionEntry) ascIndexForRank(rank int64, n int) int {
	if e.descending {
		return n - 1 - int(rank)
	}
	return int(rank)
}

func (e *rbSelectionEntry) rankForAsc(i, n int) int64 {
	if e.descending {
		return int64(n - 1 - i)
	}
	return int64(i)
}

// ascIndexForID finds an entity in the finished ascending result. The ID recipe
// is already id-ascending; a title order uses the charged identity index.
func (e *rbSelectionEntry) ascIndexForID(id int64) (int, bool) {
	if e.byID != nil {
		i, ok := slices.BinarySearchFunc(e.byID, id, func(v int32, target int64) int {
			return compareInt64(e.rows[v].key.id, target)
		})
		if !ok {
			return 0, false
		}
		return int(e.byID[i]), true
	}
	lo, hi := 0, len(e.rows)
	for lo < hi {
		mid := (lo + hi) / 2
		if e.rows[mid].key.id < id {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(e.rows) && e.rows[lo].key.id == id {
		return lo, true
	}
	return 0, false
}

// anchorKeyRank resolves a key run in the requested direction: ascending
// returns the first position whose folded key is at or after the anchor;
// descending returns the first position of the equal run as it appears in the
// mirrored order. A key beyond the result is not found.
func (e *rbSelectionEntry) anchorKeyRank(anchor string, n int) (int64, bool) {
	if !e.title || anchor == "" {
		return 0, false
	}
	target := rbPersonalASCIIFold(anchor)
	if !e.descending {
		lo, hi := 0, n
		for lo < hi {
			mid := (lo + hi) / 2
			if rbPersonalASCIIFold(e.rows[mid].key.key) < target {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		if lo >= n {
			return 0, false
		}
		return int64(lo), true
	}
	lo, hi := 0, n
	for lo < hi {
		mid := (lo + hi) / 2
		if rbPersonalASCIIFold(e.rows[mid].key.key) <= target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return 0, false
	}
	return int64(n - lo), true
}

func rbSelectionAddDuration(total *int64, missing *int64, row rbSelectionRow) error {
	if !row.durationKnown {
		*missing = *missing + 1
		return nil
	}
	if row.durationMs <= 0 || *total > math.MaxInt64-row.durationMs {
		return ErrUnqualified
	}
	*total += row.durationMs
	return nil
}

// rbSelectionDurationMs converts the maintained seconds metric to integer
// milliseconds. Absent, zero and non-positive values are an explicit missing
// fact (the aggregate is then a lower bound); NaN/Inf/overflow is refused.
func rbSelectionDurationMs(value any) (int64, bool, error) {
	var seconds float64
	switch v := value.(type) {
	case nil:
		return 0, false, nil
	case float64:
		seconds = v
	case int64:
		seconds = float64(v)
	default:
		return 0, false, ErrUnqualified
	}
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 {
		return 0, false, nil
	}
	if seconds > float64(math.MaxInt64/1000) {
		return 0, false, ErrUnqualified
	}
	ms := int64(math.Round(seconds * 1000))
	if ms <= 0 {
		return 0, false, nil
	}
	return ms, true, nil
}

func (e *rbSelectionEntry) close() error {
	if e == nil {
		return ErrInput
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.closeLocked()
}

func (e *rbSelectionEntry) closeLocked() error {
	if e == nil || e.closed {
		return nil
	}
	// Drop every owned buffer before attesting physical retirement.
	e.rows = nil
	e.byID = nil
	e.fence = browseFence{}
	e.closed = true
	if err := e.authority.CancelPreparation(e.lease); err != nil {
		return err
	}
	return e.authority.RetirePreparation(e.lease)
}
