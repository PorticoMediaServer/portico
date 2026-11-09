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
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Complete personal-order preparation for large sets.
//
// This is the large-library counterpart of rb_personal_order.go, not a new
// authority. It reuses the ONE governor preparation lease, byte reservation and
// values-only cache purpose (rbPreparedOrder) opened by task 01, plus task 02's
// bounded content-restart wrapper. A prepared list is never an eligibility proof
// and never a grant: the fresh publication handoff below still matches the whole
// current fence before any ID is copied.
//
// Public semantics are rb_personal_order.go's exactly:
//
//   - a valued run (personalRating > 0, playCount > 0, or lastPlayed <> ”)
//     ordered by value and then entity ID in the requested direction;
//   - the title-default remainder in title NOCASE ASC, entity ID ASC order in
//     BOTH directions, under the requested article policy.
//
// A descending request is valued-then-remainder; an ascending request is
// remainder-then-valued. The remainder is streamed through the existing dual
// title points. The valued run cannot use one ordered index because its
// maintained indexes tie equal values by item_public_id, not entity ID, so the
// complete valued set is reduced into a charged, fixed-capacity workspace and
// sorted once. Every raw window binds cap+1 and treats len > cap as a full
// window; a short window is the only EOF. Rejected rows still advance the raw
// cursor. A set that does not fit the admitted payload refuses instead of
// publishing a truncated order.
//
// MEMORY SPLIT. The cache kind is rbPreparedPersonalOrder, whose fixed split is
// decided once when the first fence is captured and is never resized:
//
//	P = lease.PayloadBytes()
//	W = ceil(P/2)                      // staged valued workspace
//	cap_ids = (P - W - 2*fenceBytes)/8 // retained ID vector
//
// The staged region W holds the fixed key-header array, the text appended to
// those keys and the reusable batch of IDs handed back to the cache. Both
// allocations happen once; the remainder phase appends into cap_ids, the valued
// phase appends into W, and a content restart only resets both lengths to zero
// inside the same allocations. The physical peak is therefore
// ids(cap_ids*8) + staged(W) + fences <= P at every point, with no shrink copy,
// capacity restore or retain-time growth copy. Refusals keep the existing named
// errors: IDs above cap_ids are errRBPreparationCapacity, a valued run above W
// is errRBPersonalPrepareWorkspace, and neither publishes a truncated order.
// For the default 16 MiB grant cap_ids is roughly 1.0 million IDs minus fences.
// The shared rbPreparedOrder kind used by every other producer keeps its previous
// grow-to-need behaviour.
const (
	// rbPersonalPrepareRaw bounds one raw source window exactly like the common
	// browse window. Rejected rows and lookahead count against it.
	rbPersonalPrepareRaw = MaxRaw
	// rbPersonalPrepareQuantaPerAdvance bounds one poll to a few snapshots. The
	// original 12s preparation deadline still bounds the whole job.
	rbPersonalPrepareQuantaPerAdvance = 8
	// rbPersonalPrepareMaxQueries is the finite proof recipe for one window: the
	// common fence plus at most one byte preflight and its payload leg.
	rbPersonalPrepareMaxQueries = 16

	// rbPersonalValuedKeyBytes is the inline cost of one staged key (number,
	// string header, public identity and entity ID).
	rbPersonalValuedKeyBytes = int64(unsafe.Sizeof(rbPersonalValuedKey{}))
	// rbPersonalStagedTextProvision is the per-slot text allowance used to
	// preallocate the fixed key-header array out of the workspace W. Actual text
	// is charged separately before each append; a value with longer text simply
	// fits fewer keys, never more bytes than W.
	rbPersonalStagedTextProvision = 32
)

type RBPersonalPrepareInput struct {
	// View carries the current library/kind/viewer anchor. After must be nil;
	// continuation is a private preparation cursor, never a caller anchor.
	View  BrowseInput
	Order PersonalOrder
}

// RBPersonalPrepareCursor is a values-only continuation. Its fields are private
// so a caller cannot fabricate another viewer/query/order/source anchor.
type RBPersonalPrepareCursor struct {
	key      rbPreparedKey
	fence    browseFence
	position int
}

type RBPersonalPreparePage struct {
	IDs       []int64
	Total     int64
	After     *RBPersonalPrepareCursor
	Exhausted bool
	// Statements counts the fresh publication window only. Preparation cost is
	// reported by RBPersonalPrepareAdvance.
	Statements int
}

type RBPersonalPrepareAdvance struct {
	Ready      bool
	Restarts   int
	RawRows    int
	Statements int
	Snapshot   governor.SnapshotResult
}

// RBPersonalPrepareReader owns the bounded preparation jobs for this harness
// composition. It creates no goroutine and no second queue.
type RBPersonalPrepareReader struct {
	mu    sync.Mutex
	jobs  [governor.PreparationMaxActive]*RBPersonalPreparation
	pool  *governor.Snapshots
	a     *governor.Authority
	cache *rbPreparedCache
}

// RBPersonalPreparation is one opaque preparation job. Polls retain and reuse
// it; they never re-admit, re-size or re-deadline it. No snapshot, Rows,
// statement or SQL handle survives a quantum.
type RBPersonalPreparation struct {
	mu       sync.Mutex
	reader   *RBPersonalPrepareReader
	entry    *rbPreparedEntry
	view     BrowseInput
	order    PersonalOrder
	started  time.Time
	complete bool
	pinned   browseFence
	// pinnedSet records the first complete fence this preparation observed. A
	// content restart keeps it; every non-content field must still match.
	pinnedSet bool

	// Valued and remainder phases. A direction chooses which runs first; the
	// remainder is always title ASC and the valued ties are always entity ID in
	// the requested direction.
	valuedExhausted, restExhausted bool
	valuedAfter                    *rbPersonalValuedPosition
	valuedLeg                      int
	restAfter                      *rbPersonalRestPosition
	staged                         []rbPersonalValuedKey
	stagedBytes, stagedCap         int64
	stageIDs                       []int64

	// copy accounting is per window (one bounded snapshot), never a renewed
	// lease: the governor funds each window and the cache funds retained values.
	copied, copyLimit, nativeLimit int64
}

// The valued source does not admit an OR keyset: the closed SQL grammar and
// plan model carry only conjunctive predicates. A complete order therefore
// walks consecutive bounded legs from the private cursor.
const (
	rbValuedLegFirst = iota
	rbValuedLegTail
	rbValuedLegTailEqual
	rbValuedLegNext
)

// rbPersonalValuedPosition is the private valued-source cursor. It always points
// at the last RAW row, including rejected rows, so retirement cannot stall it.
type rbPersonalValuedPosition struct {
	number   int64
	text     string
	activity int64
	public   [16]byte
}

// rbPersonalRestPosition is the private title-remainder cursor.
type rbPersonalRestPosition struct {
	key string
	id  int64
}

// rbPersonalValuedKey is the complete valued reducer value. Exactly one of
// number/text carries the order; id is the public tie in both directions.
type rbPersonalValuedKey struct {
	number int64
	text   string
	public [16]byte
	id     int64
}

var errRBPersonalPrepareWorkspace = errors.New("personal order preparation workspace capacity")

func NewRBPersonalPrepareReader(pool *governor.Snapshots, a *governor.Authority) (*RBPersonalPrepareReader, error) {
	if pool == nil || a == nil || !pool.OwnedBy(a) {
		return nil, ErrInput
	}
	cache, err := newRBPreparedCache(a, time.Now)
	if err != nil {
		return nil, err
	}
	return &RBPersonalPrepareReader{pool: pool, a: a, cache: cache}, nil
}

func rbPersonalPrepareKind(kind int64) bool {
	switch kind {
	case 1, 2, 3, 4, 5, 6, 7, 8, 9:
		return true
	}
	// Collection/container orders need task 09's permitted-member aggregate.
	return false
}

func rbPersonalPrepareOrder(order PersonalOrder) bool {
	return order == PersonalRatingOrder || order == PersonalPlayedOrder || order == PersonalPlayCountOrder
}

// BeginRBPersonalPrepare is an EXPLICIT preparation start. Polls retain the
// returned job; they must not Begin again with a fresh deadline. in.View.Now is
// trusted server request-entry time and the immutable query anchor.
func (r *RBPersonalPrepareReader) BeginRBPersonalPrepare(ctx context.Context, owner governor.OwnerID, bytes int64, in RBPersonalPrepareInput) (*RBPersonalPreparation, error) {
	if r == nil || ctx == nil || owner == 0 || in.View.Now.IsZero() || in.View.Database == "" || len(in.View.Database) > 256 || in.View.TokenHash == "" || len(in.View.TokenHash) > 256 || in.View.Library <= 0 || in.View.After != nil || in.View.policy != (readPolicyBinding{}) || !rbPersonalPrepareOrder(in.Order) || !rbPersonalPrepareKind(in.View.Kind) {
		return nil, ErrInput
	}
	if _, err := resolveReadPolicy(in.View); err != nil {
		return nil, err
	}
	// This bounded typed encoding is not a retained body or arbitrary query.
	canonical, err := json.Marshal(struct {
		Domain, Database, Token string
		Library, Kind           int64
		Order                   PersonalOrder
		Keep, Desc              bool
		Anchor                  time.Time
	}{"rb.personal_prepare", in.View.Database, in.View.TokenHash, in.View.Library, in.View.Kind, in.Order, in.View.KeepArticles, in.View.Descending, in.View.Now})
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
	// The opt-in personal kind gets the fixed ID/staged split at capture; the
	// shared cache default used by every other producer is unchanged.
	entry, err := r.cache.begin(ctx, key, rbPreparedPersonalOrder, deadline, bytes, governor.PublicationFootprint{{Library: in.View.Library}})
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
	job := &RBPersonalPreparation{reader: r, entry: entry, view: in.View, order: in.Order, started: time.Now()}
	r.jobs[free] = job
	return job, nil
}

// Advance performs a bounded number of admitted raw quanta. Each quantum is a
// separate snapshot with genuine EOF and a private source cursor; no handle is
// retained across them. A content invalidation restarts the whole walk through
// task 01's wrapper (at most twice), keeping the owner, grant and deadline.
func (p *RBPersonalPreparation) Advance(ctx context.Context, request governor.Request, budget time.Duration) (result RBPersonalPrepareAdvance, err error) {
	if p == nil || p.reader == nil || p.entry == nil || ctx == nil || request.Owner != p.entry.key.owner || request.Class != governor.Foreground || request.Writer || request.Budget.RawRows < rbPersonalPrepareRaw+5 || budget <= 0 {
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
		if err != nil {
			p.retireClosed()
		}
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
			// Restart is from the beginning of both sources, never from a stale
			// mid-stream cursor that would publish a truncated set.
			p.restartAttempt()
			result.Restarts++
		}
		return p.walk(ctx, request, budget, &result)
	})
	if err != nil {
		p.retireClosed()
		return result, err
	}
	result.Ready = p.complete
	return result, nil
}

// restartAttempt discards every reduced value and source cursor while keeping
// the fixed allocations, original lease and physical grant. Both lengths - the
// staged keys and the retained IDs - are reset inside the SAME cap_ids and W
// allocations, so attempt two cannot inherit attempt one's sizing.
func (p *RBPersonalPreparation) restartAttempt() {
	p.valuedExhausted, p.restExhausted = false, false
	p.valuedAfter, p.restAfter = nil, nil
	p.valuedLeg = rbValuedLegFirst
	p.staged = p.staged[:0]
	p.stagedBytes = p.stagedFixedBytes()
	p.complete = false
	if p.entry.cache != nil {
		p.entry.cache.mu.Lock()
		p.entry.resetRetainedIDsLocked()
		p.entry.cache.mu.Unlock()
	}
}

func (p *RBPersonalPreparation) walk(ctx context.Context, request governor.Request, budget time.Duration, result *RBPersonalPrepareAdvance) (browseFence, error) {
	deadline := time.Now().Add(budget)
	if request.Deadline.Before(deadline) {
		deadline = request.Deadline
	}
	if p.entry.lease.Deadline().Before(deadline) {
		deadline = p.entry.lease.Deadline()
	}
	var fresh browseFence
	for n := 0; n < rbPersonalPrepareQuantaPerAdvance; n++ {
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

// window performs ONE admitted raw quantum. The fence is compared before
// capture so a content move becomes a restart and every other move is a denial.
// All values for a window are reduced, byte-charged and capacity-checked before
// the single retain call; a failed window leaves no partial append.
func (p *RBPersonalPreparation) window(ctx context.Context, request governor.Request, budget time.Duration, result *RBPersonalPrepareAdvance) (fresh browseFence, err error) {
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
	result.Snapshot, err = p.reader.pool.ReadForecasted(parent, request, min(budget, time.Until(p.entry.lease.Deadline())), governor.SnapshotForecast{NativeRowBytes: p.reader.pool.NativeRowReservation(), MaxQueries: rbPersonalPrepareMaxQueries}, func(scope *governor.ReadScope) error {
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
			// A personal-value edit moves viewer_revision, which is part of the
			// pinned authorization context. It denies (ErrDenied) instead of
			// content-restarting: a restart would publish values reduced under
			// the old personal facts. Packet task 10 requires other-profile
			// isolation, and the general RB rule keeps viewer/personal drift a
			// denial while only data/order/metadata revisions restart.
			if !rbPreparePinnedFenceEqual(p.entry.fence, f) {
				return ErrDenied
			}
			return errRBPrepareContent
		}
		valued, done := p.nextPhase()
		if done {
			return p.finishPersonal()
		}
		if valued {
			raw, e := p.readValuedRows(query, in, f.profile)
			if e != nil {
				return e
			}
			result.RawRows += len(raw)
			e = p.reduceValuedWindow(scope.Context(), raw)
			if e != nil {
				return e
			}
			return nil
		}
		raw, e := p.readRestRows(query, in, f.profile)
		if e != nil {
			return e
		}
		result.RawRows += len(raw)
		if e = p.reduceRestWindow(scope.Context(), raw); e != nil {
			return e
		}
		return nil
	})
	if result.Snapshot.Refusal != nil {
		err = governor.ErrBusy
	}
	if err != nil {
		// Denial/stale/invalidated work cannot keep a ready-poll lease alive.
		if errors.Is(err, ErrDenied) || errors.Is(err, ErrStale) || errors.Is(err, ErrUnqualified) || errors.Is(err, governor.ErrSnapshotBudget) || errors.Is(err, errRBPersonalPrepareWorkspace) {
			p.discard()
		} else {
			// checkLocked may already have retired the lease (expiry/cancel).
			p.retireClosed()
		}
		return fresh, err
	}
	return fresh, nil
}

// nextPhase reports which run produces the next window. Descending is
// valued-then-remainder; ascending is remainder-then-valued.
func (p *RBPersonalPreparation) nextPhase() (valued bool, done bool) {
	if p.valuedExhausted && p.restExhausted {
		return false, true
	}
	if p.view.Descending {
		if !p.valuedExhausted {
			return true, false
		}
		return false, false
	}
	if !p.restExhausted {
		return false, false
	}
	return true, false
}

func (p *RBPersonalPreparation) readValuedRows(query browseQuery, in BrowseInput, profile string) ([][]any, error) {
	base := "rb.personal_prepare." + rbPersonalPrepareValueRecipe(p.order)
	limit := int64(rbPersonalPrepareRaw + 1)
	if p.valuedAfter == nil {
		name := base + ".first"
		if p.order == PersonalPlayedOrder {
			return p.readRecentRows(query, in, profile, name)
		}
		return query(name, profile, in.Library, in.Kind, limit)
	}
	// A full first window leaves the leg at First; its continuation is the
	// equal-value run, never the later-values leg.
	leg := p.valuedLeg
	if leg == rbValuedLegFirst {
		leg = rbValuedLegTail
	}
	if p.order == PersonalPlayedOrder {
		name := base + ".next"
		if leg == rbValuedLegTail {
			name = base + ".tail"
		} else if leg == rbValuedLegTailEqual {
			name = base + ".tail_equal"
		}
		return p.readRecentRows(query, in, profile, name)
	}
	if leg == rbValuedLegTail {
		return query(base+".tail", profile, p.valuedAfter.number, p.valuedAfter.public[:], in.Library, in.Kind, limit)
	}
	return query(base+".next", profile, p.valuedAfter.number, in.Library, in.Kind, limit)
}

func rbPersonalPrepareValueRecipe(order PersonalOrder) string {
	switch order {
	case PersonalPlayedOrder:
		return "recent"
	case PersonalPlayCountOrder:
		return "plays"
	}
	return "rating"
}

// readRecentRows preflights the exact last-played octets in this same snapshot,
// then requires the payload to match every measured identity and length.
func (p *RBPersonalPreparation) readRecentRows(query browseQuery, in BrowseInput, profile, name string) ([][]any, error) {
	var args []any
	switch {
	case p.valuedAfter == nil:
		args = []any{profile, in.Library, in.Kind, int64(rbPersonalPrepareRaw + 1)}
	case p.valuedLeg == rbValuedLegTail:
		args = []any{profile, in.Library, in.Kind, p.valuedAfter.activity, p.valuedAfter.text, int64(rbPersonalPrepareRaw + 1)}
	case p.valuedLeg == rbValuedLegTailEqual:
		args = []any{profile, in.Library, in.Kind, p.valuedAfter.activity, p.valuedAfter.text, p.valuedAfter.public[:], int64(rbPersonalPrepareRaw + 1)}
	default:
		args = []any{profile, in.Library, in.Kind, p.valuedAfter.activity, int64(rbPersonalPrepareRaw + 1)}
	}
	sizes, err := query(name+".bytes", args...)
	if err != nil {
		return nil, err
	}
	if len(sizes) > rbPersonalPrepareRaw+1 {
		return nil, ErrUnqualified
	}
	var total int64
	for _, row := range sizes {
		if len(row) != 2 {
			return nil, ErrUnqualified
		}
		if _, ok := row[0].(int64); !ok {
			return nil, ErrUnqualified
		}
		n, ok := row[1].(int64)
		if !ok || n < 0 || n > p.nativeLimit-total {
			return nil, governor.ErrSnapshotBudget
		}
		total += n
	}
	if 8192+int64(len(sizes))*1024+total > p.copyLimit-p.copied {
		return nil, governor.ErrSnapshotBudget
	}
	rows, err := query(name, args...)
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
		text, ok := row[1].(string)
		if !ok || int64(len(text)) != sizes[i][1] {
			return nil, ErrStale
		}
	}
	p.copied += 8192 + int64(len(rows))*1024 + total
	return rows, nil
}

// readRestRows streams the complete title order. The existing dual title byte
// points preflight exact key/title octets in this same snapshot, and the payload
// must match every measured identity and length.
func (p *RBPersonalPreparation) readRestRows(query browseQuery, in BrowseInput, profile string) ([][]any, error) {
	variant := "ignore."
	if in.KeepArticles {
		variant = "keep."
	}
	bytesName := "rb.filter.personal.title." + variant + "asc."
	payloadName := "rb.personal_prepare.title." + variant
	leg := func(part string, args []any, payloadArgs []any) ([][]any, error) {
		sizes, err := query(bytesName+part+".bytes", args...)
		if err != nil {
			return nil, err
		}
		if len(sizes) > rbPersonalPrepareRaw+1 {
			return nil, ErrUnqualified
		}
		var total int64
		for _, row := range sizes {
			if len(row) != 3 {
				return nil, ErrUnqualified
			}
			if _, ok := row[0].(int64); !ok {
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
		rows, err := query(payloadName+part, payloadArgs...)
		if err != nil {
			return nil, err
		}
		if len(rows) != len(sizes) {
			return nil, ErrStale
		}
		for i, row := range rows {
			if len(row) != 10 || row[0] != sizes[i][0] {
				return nil, ErrStale
			}
			key, ok := row[1].(string)
			if !ok || int64(len(key)) != sizes[i][1] {
				return nil, ErrStale
			}
			title, ok := row[3].(string)
			if !ok || int64(len(title)) != sizes[i][2] {
				return nil, ErrStale
			}
		}
		p.copied += 8192 + int64(len(rows))*1024 + 2*total
		return rows, nil
	}
	if p.restAfter == nil {
		return leg("first", []any{in.Library, in.Kind, int64(rbPersonalPrepareRaw + 1)}, []any{profile, in.Library, in.Kind, int64(rbPersonalPrepareRaw + 1)})
	}
	rows, err := leg("tail", []any{in.Library, in.Kind, p.restAfter.key, p.restAfter.id, int64(rbPersonalPrepareRaw + 1)}, []any{profile, in.Library, in.Kind, p.restAfter.key, p.restAfter.id, int64(rbPersonalPrepareRaw + 1)})
	if err != nil {
		return nil, err
	}
	// A full equal-key run may end before the window limit. The next-key seek
	// completes the window, so a short equality tail is never a false EOF.
	if len(rows) < rbPersonalPrepareRaw+1 {
		more, err := leg("next", []any{in.Library, in.Kind, p.restAfter.key, int64(rbPersonalPrepareRaw + 1 - len(rows))}, []any{profile, in.Library, in.Kind, p.restAfter.key, int64(rbPersonalPrepareRaw + 1 - len(rows))})
		if err != nil {
			return nil, err
		}
		rows = append(rows, more...)
	}
	return rows, nil
}

// windowTail trims the cap+1 EOF probe and reports whether the stream ended.
func windowTail(raw [][]any) ([][]any, bool) {
	if len(raw) > rbPersonalPrepareRaw {
		return raw[:rbPersonalPrepareRaw], false
	}
	return raw, true
}

// reduceValuedWindow types and stages the complete valued set. Rejected rows
// still advance the raw cursor and count against the window. Staging is atomic
// per window: if the window fails part way (for example a context cancellation
// that does not close the entry), every key appended for this window is rolled
// back. The raw cursor is only advanced after the whole window succeeds, so a
// retry re-reads the same rows and cannot retain a duplicate valued ID.
func (p *RBPersonalPreparation) reduceValuedWindow(ctx context.Context, raw [][]any) (err error) {
	raw, exhausted := windowTail(raw)
	stagedStart := len(p.staged)
	committed := false
	defer func() {
		if committed {
			return
		}
		for _, key := range p.staged[stagedStart:] {
			p.stagedBytes -= int64(len(key.text))
		}
		p.staged = p.staged[:stagedStart]
	}()
	var next *rbPersonalValuedPosition
	for _, row := range raw {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, position, admitted, err := p.reduceValuedRow(row)
		if err != nil {
			return err
		}
		next = position
		if !admitted {
			continue
		}
		if err := p.appendStaged(key); err != nil {
			return err
		}
	}
	if len(raw) > 0 {
		p.valuedAfter = next
	}
	if !exhausted {
		// A full window ends inside the value (or activity/last pair) it was
		// ordered by, so the continuation must finish that equal run first.
		if p.order == PersonalPlayedOrder {
			p.valuedLeg = rbValuedLegTailEqual
		} else {
			p.valuedLeg = rbValuedLegTail
		}
	}
	if exhausted {
		p.advanceValuedLeg()
		if p.valuedExhausted {
			if err := p.retainStaged(); err != nil {
				return err
			}
		}
	}
	committed = true
	return nil
}

// advanceValuedLeg moves to the next bounded, conjunctive keyset leg once the
// current one is short. Rating/play-count orders have the equal-value run then
// later values; last-played also has the same-value later-public run.
func (p *RBPersonalPreparation) advanceValuedLeg() {
	switch p.valuedLeg {
	case rbValuedLegFirst:
		// The first statement starts at the beginning of the order, so a short
		// answer means the whole valued set fits this one window.
		p.valuedExhausted = true
	case rbValuedLegTail:
		if p.order == PersonalPlayedOrder {
			p.valuedLeg = rbValuedLegNext
			return
		}
		p.valuedLeg = rbValuedLegNext
		return
	case rbValuedLegTailEqual:
		p.valuedLeg = rbValuedLegTail
		return
	}
	p.valuedExhausted = true
}

func (p *RBPersonalPreparation) reduceValuedRow(row []any) (rbPersonalValuedKey, *rbPersonalValuedPosition, bool, error) {
	position := &rbPersonalValuedPosition{}
	fail := func() (rbPersonalValuedKey, *rbPersonalValuedPosition, bool, error) {
		return rbPersonalValuedKey{}, nil, false, ErrUnqualified
	}
	switch p.order {
	case PersonalRatingOrder, PersonalPlayCountOrder:
		if len(row) != 6 {
			return fail()
		}
		id, ok := row[0].(int64)
		value, okValue := row[1].(int64)
		if !ok || id <= 0 || !okValue || value < 0 {
			return fail()
		}
		public, okPublic := rbPersonalPreparePublic(row[2])
		if !okPublic {
			return fail()
		}
		position.number, position.public = value, public
		key := rbPersonalValuedKey{number: value, public: public, id: id}
		item := [7]any{id, "", nil, nil, row[3], row[4], row[5]}
		return key, position, admittedItem(item[:], p.entry.fence), nil
	case PersonalPlayedOrder:
		if len(row) != 7 {
			return fail()
		}
		id, ok := row[0].(int64)
		text, okText := row[1].(string)
		activity, okActivity := row[2].(int64)
		if !ok || id <= 0 || !okText || !utf8.ValidString(text) || !okActivity {
			return fail()
		}
		public, okPublic := rbPersonalPreparePublic(row[3])
		if !okPublic {
			return fail()
		}
		position.text, position.activity, position.public = text, activity, public
		key := rbPersonalValuedKey{text: text, public: public, id: id}
		item := [7]any{id, "", nil, nil, row[4], row[5], row[6]}
		return key, position, admittedItem(item[:], p.entry.fence), nil
	}
	return fail()
}

// reduceRestWindow keeps the title-remainder stream monotone and admits only
// eligible, unvalued rows. Valued rows still consume the raw window. IDs are
// retained only after the whole window succeeds, so a failed window leaves no
// partial remainder.
func (p *RBPersonalPreparation) reduceRestWindow(ctx context.Context, raw [][]any) error {
	raw, exhausted := windowTail(raw)
	if len(raw) > 0 {
		key, ok := raw[0][1].(string)
		id, okID := raw[0][0].(int64)
		if !ok || !okID || !rbPersonalRestAfter(key, id, p.restAfter) {
			return ErrUnqualified
		}
	}
	ids := make([]int64, 0, len(raw))
	var last *rbPersonalRestPosition
	for _, row := range raw {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(row) != 10 {
			return ErrUnqualified
		}
		id, ok := row[0].(int64)
		key, okKey := row[1].(string)
		if !ok || id <= 0 || !okKey || !utf8.ValidString(key) {
			return ErrUnqualified
		}
		last = &rbPersonalRestPosition{key: key, id: id}
		if !admittedItem(row[:7], p.entry.fence) {
			continue
		}
		if p.personalValuePresent(row) {
			continue
		}
		ids = append(ids, id)
	}
	if last != nil {
		p.restAfter = last
	}
	if exhausted {
		p.restExhausted = true
	}
	return p.entry.retainIDs(ids)
}

func (p *RBPersonalPreparation) personalValuePresent(row []any) bool {
	switch p.order {
	case PersonalRatingOrder:
		v, ok := row[7].(int64)
		return ok && v > 0
	case PersonalPlayCountOrder:
		v, ok := row[8].(int64)
		return ok && v > 0
	case PersonalPlayedOrder:
		v, ok := row[9].(string)
		return ok && v != ""
	}
	return false
}

func rbPersonalRestAfter(key string, id int64, after *rbPersonalRestPosition) bool {
	if after == nil {
		return true
	}
	if c := strings.Compare(rbPersonalASCIIFold(key), rbPersonalASCIIFold(after.key)); c != 0 {
		return c > 0
	}
	return id > after.id
}

func rbPersonalPreparePublic(v any) ([16]byte, bool) {
	b, ok := v.([]byte)
	if !ok || len(b) != 16 {
		return [16]byte{}, false
	}
	var public [16]byte
	copy(public[:], b)
	return public, true
}

// initStaged allocates the complete valued workspace once out of the fixed
// split's W. It never shrinks or restores the ID vector.
func (p *RBPersonalPreparation) initStaged() error {
	if p.staged != nil {
		return nil
	}
	p.entry.cache.mu.Lock()
	if err := p.entry.checkLocked(); err != nil {
		p.entry.cache.mu.Unlock()
		return err
	}
	workspace := p.entry.workspace
	p.entry.cache.mu.Unlock()
	if p.entry.purpose != rbPreparedPersonalOrder || workspace <= 0 {
		return errRBPersonalPrepareWorkspace
	}
	// W covers the fixed key-header array, the text appended to those keys and
	// the reusable batch of IDs handed to the cache. The header array is sized
	// once from a small per-key text provision; every append then charges its
	// actual text before writing, and refuses if that would cross W.
	batchBytes := int64(rbPersonalPrepareRaw) * 8
	keyCap := int((workspace - batchBytes) / (rbPersonalValuedKeyBytes + rbPersonalStagedTextProvision))
	if keyCap < 1 {
		return errRBPersonalPrepareWorkspace
	}
	p.stageIDs = make([]int64, 0, rbPersonalPrepareRaw)
	p.staged = make([]rbPersonalValuedKey, 0, keyCap)
	p.stagedCap = workspace
	p.stagedBytes = p.stagedFixedBytes()
	return nil
}

// stagedFixedBytes is the already-committed part of W: the preallocated key
// headers plus the reusable batch. Text bytes are added per append.
func (p *RBPersonalPreparation) stagedFixedBytes() int64 {
	if p.staged == nil {
		return 0
	}
	return int64(cap(p.staged))*rbPersonalValuedKeyBytes + int64(cap(p.stageIDs))*8
}

func (p *RBPersonalPreparation) appendStaged(key rbPersonalValuedKey) error {
	if err := p.initStaged(); err != nil {
		return err
	}
	textBytes := int64(len(key.text))
	if len(p.staged) >= cap(p.staged) || textBytes > p.stagedCap-p.stagedBytes {
		return errRBPersonalPrepareWorkspace
	}
	p.stagedBytes += textBytes
	p.staged = append(p.staged, key)
	return nil
}

func (p *RBPersonalPreparation) retainStaged() error {
	if len(p.staged) == 0 {
		return nil
	}
	slices.SortFunc(p.staged, func(a, b rbPersonalValuedKey) int {
		return rbComparePersonalPrepareValued(a, b, p.order, p.view.Descending)
	})
	// IDs are appended into the cap_ids vector allocated at capture. The batch
	// is the fixed rbPersonalPrepareRaw buffer already inside W, so this loop
	// never allocates or grows a second vector.
	for start := 0; start < len(p.staged); start += rbPersonalPrepareRaw {
		end := min(len(p.staged), start+rbPersonalPrepareRaw)
		p.stageIDs = p.stageIDs[:0]
		for _, key := range p.staged[start:end] {
			p.stageIDs = append(p.stageIDs, key.id)
		}
		if err := p.entry.retainIDs(p.stageIDs); err != nil {
			return err
		}
	}
	// The staged workspace must not survive readiness (a parked Ready job would
	// hold it for the whole lease). The ID vector keeps its fixed cap_ids; only
	// the W region is dropped.
	p.dropWorkspace()
	return nil
}

// dropWorkspace releases the fixed staged region. It is used after a
// successful retain, on readiness, on every close/denial/workspace error and on
// a content restart that has no more work, so a retired lease never keeps W.
func (p *RBPersonalPreparation) dropWorkspace() {
	p.staged = nil
	p.stagedBytes = 0
	p.stagedCap = 0
	p.stageIDs = nil
}

func rbComparePersonalPrepareValued(a, b rbPersonalValuedKey, order PersonalOrder, descending bool) int {
	c := 0
	if order == PersonalPlayedOrder {
		c = strings.Compare(a.text, b.text)
	} else {
		c = compareInt64(a.number, b.number)
	}
	if c == 0 {
		c = compareInt64(a.id, b.id)
	}
	if descending {
		return -c
	}
	return c
}

// finishPersonal publishes readiness through the SAME preparation lease. The
// IDs were retained in final order, so no reversal is needed after a restart.
func (p *RBPersonalPreparation) finishPersonal() error {
	// A parked Ready job must not keep the temporary sort workspace. Only the
	// fixed cap_ids vector remains retained after readiness.
	p.dropWorkspace()
	p.entry.cache.mu.Lock()
	err := p.entry.checkLocked()
	if err == nil && (!p.entry.captured || p.entry.complete) {
		err = ErrInput
	}
	if err == nil {
		if err = p.entry.cache.a.ReadyPreparation(p.entry.lease); err != nil {
			_ = p.entry.closeLocked()
		} else {
			p.entry.complete = true
			p.complete = true
		}
	}
	p.entry.cache.mu.Unlock()
	if err != nil {
		p.releaseSlot()
	}
	return err
}

// Page copies one page of the complete ordered IDs inside a fresh authorized
// response snapshot. The exact whole fence must match before any copy, and the
// first delivery linearizes with the governor exactly like every other prepared
// cache purpose. A later page with a drifted fence is denied, never replayed.
//
// Non-blocking review note kept as documentation: once a job is Ready, a Page
// that observes a viewer_revision drift returns ErrStale (the cache handoff
// compares the whole fence) rather than ErrDenied. Preparation-time drift still
// denies before readiness; the difference is only which fence comparison sees it.
func (p *RBPersonalPreparation) Page(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput, after *RBPersonalPrepareCursor, limit int) (RBPersonalPreparePage, BrowseReadResult, error) {
	if p == nil || p.reader == nil || p.entry == nil {
		return RBPersonalPreparePage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, p.reader.pool, current.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (RBPersonalPreparePage, BrowseReadResult, error) {
		current.Now = at
		return p.page(ctx, request, budget, output, current, after, limit)
	})
}

func (p *RBPersonalPreparation) page(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput, after *RBPersonalPrepareCursor, limit int) (page RBPersonalPreparePage, result BrowseReadResult, err error) {
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
	result.Publication, err = p.reader.pool.ReadForecasted(validation, request, time.Until(deadline), governor.SnapshotForecast{NativeRowBytes: p.reader.pool.NativeRowReservation(), MaxQueries: rbPersonalPrepareMaxQueries}, func(scope *governor.ReadScope) error {
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
			p.discard()
		} else {
			p.retireClosed()
		}
		return RBPersonalPreparePage{}, result, err
	}
	if err = ctx.Err(); err != nil {
		return RBPersonalPreparePage{}, result, err
	}
	// Confirm the freshly observed token/horizon at actual publication, after
	// native cleanup; expiry must not leak a copied formerly-authorized page.
	at := current.Now.Add(time.Since(started))
	expires, e1 := time.Parse(time.RFC3339Nano, fresh.expiresAt)
	horizon, e2 := time.Parse(time.RFC3339Nano, fresh.horizon)
	if e1 != nil || e2 != nil || !at.Before(expires) || !at.Before(horizon) {
		p.discard()
		return RBPersonalPreparePage{}, result, ErrDenied
	}
	if !time.Now().Before(deadline) {
		return RBPersonalPreparePage{}, result, context.DeadlineExceeded
	}
	if err = output.Authority.CheckContext(ctx, output.Grant, output.Request); err != nil {
		return RBPersonalPreparePage{}, result, err
	}
	page = RBPersonalPreparePage{IDs: prepared.ids, Total: prepared.total, Exhausted: prepared.exhausted, Statements: result.PublicationStatements}
	if !prepared.exhausted {
		page.After = &RBPersonalPrepareCursor{key: p.entry.key, fence: fresh, position: prepared.next}
	}
	return page, result, nil
}

func (p *RBPersonalPreparation) Close() error {
	if p == nil || p.entry == nil {
		return ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dropWorkspace()
	err := p.entry.close()
	p.releaseSlot()
	return err
}

// discard retires a job after a denial, stale fence, workspace refusal or
// publication error: the fixed W region, the job's staged fields and the
// reader's slot reference all go before the physical lease is attested.
func (p *RBPersonalPreparation) discard() {
	p.dropWorkspace()
	_ = p.entry.close()
	p.releaseSlot()
}

// retireClosed releases the job's staged region and reader slot when a lease was
// already retired elsewhere (expiry, cancellation, pinned-fence denial or the
// restart limit). It never closes anything twice and never runs under cache.mu.
func (p *RBPersonalPreparation) retireClosed() {
	if p == nil || p.entry == nil {
		return
	}
	p.entry.cache.mu.Lock()
	closed := p.entry.closed
	p.entry.cache.mu.Unlock()
	if closed {
		p.dropWorkspace()
		p.releaseSlot()
	}
}

// releaseSlot drops the reader's reference to this job once its lease retires.
// It never runs while the cache mutex is held, so it cannot invert Begin's
// reader-then-cache order.
func (p *RBPersonalPreparation) releaseSlot() {
	r := p.reader
	if r == nil {
		return
	}
	r.mu.Lock()
	for i, job := range r.jobs {
		if job == p {
			r.jobs[i] = nil
		}
	}
	r.mu.Unlock()
}
