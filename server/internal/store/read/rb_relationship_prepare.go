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

// Complete album/artist relationship preparation for large libraries.
//
// This is the large-set counterpart of BrowseReader.RBRelationshipExact, not a
// new authority. It reuses the ONE governor preparation lease, byte reservation
// and values-only cache purpose (rbPreparedOrder) opened by task 01, plus task
// 02's bounded content-restart wrapper. A prepared list is never an eligibility
// proof and never an authorization grant: the fresh publication handoff below
// still matches the whole current fence before any ID is copied.
//
// Public semantics are rb_group_relationship.go's exactly:
//
//   - album order (songs only) groups songs by their current album's folded
//     sort key and ties equal keys by the song's output entity ID;
//   - artist order (albums or songs) groups by the current artist's folded sort
//     key reached through album -> artist and ties equal keys by output entity
//     ID;
//   - both article policies select sort_key or sort_key_keep_articles, and a
//     descending request is the full ascending order exactly reversed.
//
// The physical walk is hierarchical and complete: albums are streamed from the
// dual title points for album order, and artists are streamed from them for
// artist order. Songs follow catalog_entities_album; albums under an artist
// follow catalog_entities_artist. Every parent key group is merged by output
// entity ID before it is retained, so two parents with equal folded keys never
// concatenate their children. A parent group is retained in ascending output-ID
// order and group keys are checked for a strictly ascending merge; a
// non-monotone source is refused rather than published as a mixed order.
//
// Every source leg binds cap+1 and treats a longer answer as a source that
// ignored its bound. Rejected (retired, age-restricted, foreign-library or
// wrong-kind) rows still advance the private raw cursor; a short window is the
// only EOF. A complete set that does not fit the admitted payload refuses the
// whole preparation instead of publishing a truncated order: capacity falls
// back to the complete admitted walk, never to a top-K sample.
const (
	// rbRelationshipPrepareRaw bounds one raw source window exactly like the
	// common browse window. Rejected rows and lookahead count against it.
	rbRelationshipPrepareRaw = MaxRaw
	// rbRelationshipPrepareQuantaPerAdvance bounds one poll to a few snapshots.
	// The original 12s preparation deadline still bounds the whole job.
	rbRelationshipPrepareQuantaPerAdvance = 8
	// rbRelationshipPrepareMaxQueries is the finite proof recipe for one window:
	// the common fence, bounded title byte/payload pairs and the conjunctive
	// child legs (album, equal-track, later-track and later-disc).
	rbRelationshipPrepareMaxQueries = 48
	// rbRelationshipPrepareOuterBatch bounds how many title parents one window
	// may fetch. Half of the raw window stays available for their children.
	rbRelationshipPrepareOuterBatch = 8
)

// RBRelationshipPrepareInput names one complete compiled relationship order.
// After must be nil; continuation is a private preparation cursor, never a
// caller anchor.
type RBRelationshipPrepareInput struct {
	// View carries the current library/kind/viewer anchor. After must be nil;
	// continuation is a private preparation cursor, never a caller anchor.
	View BrowseInput
	Sort RBRelationshipSort
}

// RBRelationshipPrepareCursor is a values-only continuation. Its fields are
// private so a caller cannot fabricate another viewer/query/order/source anchor.
type RBRelationshipPrepareCursor struct {
	key      rbPreparedKey
	fence    browseFence
	position int
}

type RBRelationshipPreparePage struct {
	IDs       []int64
	Total     int64
	After     *RBRelationshipPrepareCursor
	Exhausted bool
	// Statements counts the fresh publication window only. Preparation cost is
	// reported by RBRelationshipPrepareAdvance.
	Statements int
}

type RBRelationshipPrepareAdvance struct {
	Ready      bool
	Restarts   int
	RawRows    int
	Statements int
	Snapshot   governor.SnapshotResult
}

type RBRelationshipPrepareReader struct {
	mu    sync.Mutex
	jobs  [governor.PreparationMaxActive]*RBRelationshipPreparation
	pool  *governor.Snapshots
	a     *governor.Authority
	cache *rbPreparedCache
}

// rbRelationshipParent is the current outer title row: one album (album order)
// or one artist (artist order). key is the raw title key last observed; folded
// is its ASCII-only NOCASE value. The latter is the public group key.
type rbRelationshipParent struct {
	id     int64
	key    string
	folded string
}

// rbRelationshipOuterPosition is the private title cursor. It always points at
// the last RAW parent row, including rejected parents, so retirement cannot
// stall the walk.
type rbRelationshipOuterPosition struct {
	key string
	id  int64
}

// rbRelationshipChildCursor is the private song cursor inside one album. It
// always points at the last RAW song row in catalog_entities_album order.
type rbRelationshipChildCursor struct {
	disc, track, id int64
	started         bool
	// leg is the current conjunctive keyset leg: the equal (disc,track) tail,
	// the later same-disc tracks, then the later discs.
	leg int
}

// RBRelationshipPreparation is one opaque preparation job. Polls retain and
// reuse it; they never re-admit, re-size or re-deadline it. No snapshot, Rows,
// statement or SQL handle survives a quantum.
type RBRelationshipPreparation struct {
	mu       sync.Mutex
	reader   *RBRelationshipPrepareReader
	entry    *rbPreparedEntry
	view     BrowseInput
	sort     RBRelationshipSort
	started  time.Time
	complete bool
	pinned   browseFence
	// pinnedSet records the first complete fence this preparation observed. A
	// content restart keeps it; every non-content field must still match.
	pinnedSet bool

	// outer is the last started title parent; parent is the parent whose
	// children are currently being enumerated across quanta.
	outer     *rbRelationshipOuterPosition
	outerDone bool
	parent    *rbRelationshipParent

	// album order enumerates songs directly under the outer album.
	songs rbRelationshipChildCursor
	// artist order enumerates albums (catalog_entities_artist) and, for song
	// output, the songs of the current album (catalog_entities_album).
	albumAfter  int64
	albumActive *rbRelationshipParent
	albumSongs  rbRelationshipChildCursor

	// The current equal-key group is already appended to entry.ids. Its IDs are
	// sorted in place when the group closes, so equal parent keys merge by
	// output ID instead of concatenating per-parent runs.
	groupKey   string
	groupStart int
	haveGroup  bool

	// copy accounting is per window (one bounded snapshot), never a renewed
	// lease: the governor funds each window and the cache funds retained values.
	copied, copyLimit, nativeLimit int64
}

func NewRBRelationshipPrepareReader(pool *governor.Snapshots, a *governor.Authority) (*RBRelationshipPrepareReader, error) {
	if pool == nil || a == nil || !pool.OwnedBy(a) {
		return nil, ErrInput
	}
	cache, err := newRBPreparedCache(a, time.Now)
	if err != nil {
		return nil, err
	}
	return &RBRelationshipPrepareReader{pool: pool, a: a, cache: cache}, nil
}

func rbRelationshipPrepareSort(in RBRelationshipPrepareInput) bool {
	if in.Sort != RBRelationshipAlbum && in.Sort != RBRelationshipArtist {
		return false
	}
	switch in.View.Kind {
	case 6:
		return in.Sort == RBRelationshipArtist
	case 7:
		return true
	}
	return false
}

// BeginRBRelationshipPrepare is an EXPLICIT preparation start. Polls retain the
// returned job; they must not Begin again with a fresh deadline. in.View.Now is
// trusted server request-entry time and the immutable query anchor.
func (r *RBRelationshipPrepareReader) BeginRBRelationshipPrepare(ctx context.Context, owner governor.OwnerID, bytes int64, in RBRelationshipPrepareInput) (*RBRelationshipPreparation, error) {
	if r == nil || ctx == nil || owner == 0 || in.View.Now.IsZero() || in.View.Database == "" || len(in.View.Database) > 256 || in.View.TokenHash == "" || len(in.View.TokenHash) > 256 || in.View.Library <= 0 || in.View.After != nil || in.View.policy != (readPolicyBinding{}) || !rbRelationshipPrepareSort(in) {
		return nil, ErrInput
	}
	if _, err := resolveReadPolicy(in.View); err != nil {
		return nil, err
	}
	// The relationship recipe is the same compiled domain policy the exact
	// fallback uses: unrestricted catalogue only, never a caller permission.
	in.View.policy = readPolicyBinding{recipe: rbRelationshipPolicy}
	if _, err := resolveReadPolicy(in.View); err != nil {
		return nil, err
	}
	// This bounded typed encoding is not a retained body or arbitrary query.
	canonical, err := json.Marshal(struct {
		Domain, Database, Token string
		Library, Kind           int64
		Sort                    RBRelationshipSort
		Keep, Desc              bool
		Anchor                  time.Time
	}{"rb.relationship_prepare", in.View.Database, in.View.TokenHash, in.View.Library, in.View.Kind, in.Sort, in.View.KeepArticles, in.View.Descending, in.View.Now})
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
	job := &RBRelationshipPreparation{reader: r, entry: entry, view: in.View, sort: in.Sort, started: time.Now()}
	r.jobs[free] = job
	return job, nil
}

// Advance performs a bounded number of admitted raw quanta. Each quantum is a
// separate snapshot with genuine EOF and a private hierarchical cursor; no
// handle is retained across them. A content invalidation restarts the whole
// walk through task 01's wrapper (at most twice), keeping the owner, grant and
// deadline.
func (p *RBRelationshipPreparation) Advance(ctx context.Context, request governor.Request, budget time.Duration) (result RBRelationshipPrepareAdvance, err error) {
	if p == nil || p.reader == nil || p.entry == nil || ctx == nil || request.Owner != p.entry.key.owner || request.Class != governor.Foreground || request.Writer || request.Budget.RawRows < rbRelationshipPrepareRaw+5 || budget <= 0 {
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
			p.restartAttempt()
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

// restartAttempt discards every reduced value and source cursor while keeping
// the retained vector capacity, original lease and physical grant.
func (p *RBRelationshipPreparation) restartAttempt() {
	p.outer, p.outerDone, p.parent = nil, false, nil
	p.songs = rbRelationshipChildCursor{}
	p.albumAfter, p.albumActive, p.albumSongs = 0, nil, rbRelationshipChildCursor{}
	p.groupKey, p.groupStart, p.haveGroup = "", 0, false
	p.complete = false
}

func (p *RBRelationshipPreparation) walk(ctx context.Context, request governor.Request, budget time.Duration, result *RBRelationshipPrepareAdvance) (browseFence, error) {
	deadline := time.Now().Add(budget)
	if request.Deadline.Before(deadline) {
		deadline = request.Deadline
	}
	if p.entry.lease.Deadline().Before(deadline) {
		deadline = p.entry.lease.Deadline()
	}
	var fresh browseFence
	for n := 0; n < rbRelationshipPrepareQuantaPerAdvance; n++ {
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
// All values for the window are typed and byte-charged before the single retain
// call; a failed window leaves no partial append.
func (p *RBRelationshipPreparation) window(ctx context.Context, request governor.Request, budget time.Duration, result *RBRelationshipPrepareAdvance) (fresh browseFence, err error) {
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
	result.Snapshot, err = p.reader.pool.ReadForecasted(parent, request, min(budget, time.Until(p.entry.lease.Deadline())), governor.SnapshotForecast{NativeRowBytes: p.reader.pool.NativeRowReservation(), MaxQueries: rbRelationshipPrepareMaxQueries}, func(scope *governor.ReadScope) error {
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
		return p.reduceWindow(scope, query, f, result)
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

// reduceWindow walks one bounded quantum of the hierarchy. The raw window is
// shared by the outer title batch and its children; rejected rows still advance
// their private cursor. Every group is closed before the next strictly larger
// folded key is admitted.
func (p *RBRelationshipPreparation) reduceWindow(scope *governor.ReadScope, query browseQuery, f browseFence, result *RBRelationshipPrepareAdvance) error {
	budget := rbRelationshipPrepareRaw
	for budget > 0 && !p.complete {
		if err := scope.Context().Err(); err != nil {
			return err
		}
		if p.parent != nil {
			n, done, err := p.advanceParent(scope, query, f, budget)
			budget -= n
			result.RawRows += n
			if err != nil {
				return err
			}
			if !done {
				return nil
			}
			p.parent = nil
			continue
		}
		if p.outerDone {
			return p.finishWalk()
		}
		// Keep at least half of the raw window for the first parent's children.
		outerCap := budget / 2
		if outerCap < 1 {
			outerCap = 1
		}
		if outerCap > rbRelationshipPrepareOuterBatch {
			outerCap = rbRelationshipPrepareOuterBatch
		}
		rows, exhausted, err := p.readOuter(query, f, outerCap)
		if err != nil {
			return err
		}
		result.RawRows += len(rows)
		budget -= len(rows)
		if len(rows) == 0 {
			if exhausted {
				return p.finishWalk()
			}
			return nil
		}
		for _, row := range rows {
			parent, valid, err := p.outerRow(row, f)
			if err != nil {
				return err
			}
			// Advance over every raw parent, including a rejected one, before
			// deciding whether its children can contribute.
			p.outer = &rbRelationshipOuterPosition{key: parent.key, id: parent.id}
			if !valid {
				continue
			}
			p.parent = parent
			p.songs = rbRelationshipChildCursor{}
			p.albumAfter, p.albumActive, p.albumSongs = 0, nil, rbRelationshipChildCursor{}
			n, done, err := p.advanceParent(scope, query, f, budget)
			budget -= n
			result.RawRows += n
			if err != nil {
				return err
			}
			if !done {
				return nil
			}
			p.parent = nil
			if budget <= 0 {
				return nil
			}
		}
		if exhausted {
			return p.finishWalk()
		}
	}
	return nil
}

// outerRow types one title row. The outer parent is only an ancestor/order key:
// retired, foreign-library or wrong-kind parents cannot contribute a key, but
// they still advance the raw cursor.
func (p *RBRelationshipPreparation) outerRow(row []any, f browseFence) (*rbRelationshipParent, bool, error) {
	if len(row) != 5 {
		return nil, false, ErrUnqualified
	}
	id, okID := row[0].(int64)
	key, okKey := row[1].(string)
	retired, okRetired := row[2].(int64)
	library, okLibrary := row[3].(int64)
	kind, okKind := row[4].(int64)
	if !okID || !okKey || !okRetired || !okLibrary || !okKind || id <= 0 || !utf8.ValidString(key) || len(key) > 1024 {
		return nil, false, ErrUnqualified
	}
	parent := &rbRelationshipParent{id: id, key: strings.Clone(key), folded: rbRelationshipFold(key)}
	return parent, retired == 0 && library == p.view.Library && kind == p.outerKind(), nil
}

func (p *RBRelationshipPreparation) outerKind() int64 {
	if p.sort == RBRelationshipArtist {
		return 5
	}
	return 6
}

// advanceParent enumerates one parent's children within the remaining raw
// budget. A partial return keeps p.parent so the next quantum resumes at the
// exact child cursor; no parent keys are concatenated across a group boundary.
func (p *RBRelationshipPreparation) advanceParent(scope *governor.ReadScope, query browseQuery, f browseFence, budget int) (n int, done bool, err error) {
	if budget <= 0 || p.parent == nil {
		return 0, false, nil
	}
	if p.sort == RBRelationshipAlbum {
		// Album order: the outer parent is the album; songs follow it directly.
		return p.advanceSongs(scope, query, f, p.parent.id, budget)
	}
	// Artist order: finish the active album's songs before advancing the album
	// cursor, so a partially enumerated album is never skipped.
	if p.albumActive != nil {
		used, songsDone, e := p.advanceSongs(scope, query, f, p.albumActive.id, budget)
		n += used
		budget -= used
		if e != nil {
			return n, false, e
		}
		if !songsDone {
			return n, false, nil
		}
		p.albumActive = nil
		if budget <= 0 {
			return n, false, nil
		}
	}
	rows, exhausted, err := p.readArtistAlbums(query, f, p.parent.id, p.albumAfter, budget)
	if err != nil {
		return n, false, err
	}
	for _, row := range rows {
		n++
		budget--
		id, ok := row[0].(int64)
		if !ok || id <= 0 {
			return n, false, ErrUnqualified
		}
		p.albumAfter = id
		if !rbRelationshipChildAdmitted(row, f, p.view.Library, 6) {
			continue
		}
		if p.view.Kind == 6 {
			// An album is itself the output entity for an album-kind artist list.
			if err := p.addOutput(id, p.parent); err != nil {
				return n, false, err
			}
			continue
		}
		// Song output: the current album is a required current ancestor.
		p.albumActive = &rbRelationshipParent{id: id}
		p.albumSongs = rbRelationshipChildCursor{}
		used, songsDone, e := p.advanceSongs(scope, query, f, id, budget)
		n += used
		budget -= used
		if e != nil {
			return n, false, e
		}
		if !songsDone {
			return n, false, nil
		}
		p.albumActive = nil
		if budget <= 0 {
			return n, false, nil
		}
	}
	if !exhausted {
		return n, false, nil
	}
	return n, true, nil
}

// advanceSongs enumerates one album's songs in catalog_entities_album order.
// The cursor always points at the last RAW song, so a rejected song cannot
// stall the walk. Every admitted song takes the CURRENT parent's folded key.
func (p *RBRelationshipPreparation) advanceSongs(scope *governor.ReadScope, query browseQuery, f browseFence, album int64, budget int) (n int, done bool, err error) {
	if budget <= 0 {
		return 0, false, nil
	}
	rows, exhausted, err := p.readSongs(query, f, album, budget)
	if err != nil {
		return 0, false, err
	}
	for _, row := range rows {
		if err := scope.Context().Err(); err != nil {
			return 0, false, err
		}
		n++
		if len(row) != 8 {
			return n, false, ErrUnqualified
		}
		id, ok := row[0].(int64)
		disc, okDisc := row[6].(int64)
		track, okTrack := row[7].(int64)
		if !ok || !okDisc || !okTrack || id <= 0 {
			return n, false, ErrUnqualified
		}
		cursor := &p.albumSongs
		if p.sort == RBRelationshipAlbum {
			cursor = &p.songs
		}
		cursor.disc, cursor.track, cursor.id, cursor.started = disc, track, id, true
		if !rbRelationshipChildAdmitted(row, f, p.view.Library, 7) {
			continue
		}
		if err := p.addOutput(id, p.parent); err != nil {
			return n, false, err
		}
	}
	if !exhausted {
		return n, false, nil
	}
	return n, true, nil
}

// addOutput closes an equal-key group before admitting a strictly larger folded
// parent key, then retains one output ID. The group's IDs are sorted in place at
// close, so equal parent keys merge by output entity ID globally. Bookkeeping
// and the append stay under the cache's physical-custody lock so a concurrent
// cache eviction cannot race the retained vector.
func (p *RBRelationshipPreparation) addOutput(id int64, parent *rbRelationshipParent) error {
	if id <= 0 || parent == nil {
		return ErrUnqualified
	}
	cache := p.entry.cache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if err := p.entry.checkLocked(); err != nil {
		return err
	}
	if !p.entry.captured || p.entry.complete {
		return ErrInput
	}
	if !p.haveGroup {
		p.haveGroup = true
		p.groupKey = parent.folded
		p.groupStart = len(p.entry.ids)
	} else if parent.folded != p.groupKey {
		if p.groupKey >= parent.folded {
			return ErrUnqualified
		}
		slices.Sort(p.entry.ids[p.groupStart:])
		p.groupKey = parent.folded
		p.groupStart = len(p.entry.ids)
	}
	if len(p.entry.ids) >= cap(p.entry.ids) {
		_ = p.entry.closeLocked()
		return errRBPreparationCapacity
	}
	p.entry.ids = append(p.entry.ids, id)
	p.entry.total++
	return nil
}

// publishOrder closes the last group, reverses a completed descending request
// in place and publishes readiness through the SAME preparation lease. No new
// grant or allocation is made.
func (p *RBRelationshipPreparation) publishOrder() error {
	cache := p.entry.cache
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if err := p.entry.checkLocked(); err != nil {
		return err
	}
	if !p.entry.captured || p.entry.complete || p.groupStart < 0 || p.groupStart > len(p.entry.ids) {
		return ErrInput
	}
	slices.Sort(p.entry.ids[p.groupStart:])
	p.groupStart = len(p.entry.ids)
	if p.view.Descending {
		slices.Reverse(p.entry.ids)
	}
	if err := cache.a.ReadyPreparation(p.entry.lease); err != nil {
		_ = p.entry.closeLocked()
		return err
	}
	p.entry.complete = true
	return nil
}

// finishWalk closes the last group and publishes the completed order.
func (p *RBRelationshipPreparation) finishWalk() error {
	if p.complete {
		return nil
	}
	p.outerDone = true
	if err := p.publishOrder(); err != nil {
		return err
	}
	p.complete = true
	return nil
}

// readOuter streams title parents through the existing dual title points. The
// byte leg is measured in this same snapshot before the payload is copied, and
// every payload key must match its measured length exactly. A short equal-key
// tail is completed by the next-key seek so it is never a false EOF.
func (p *RBRelationshipPreparation) readOuter(query browseQuery, f browseFence, cap int) ([][]any, bool, error) {
	if cap < 1 {
		return nil, false, nil
	}
	if cap > rbRelationshipPrepareOuterBatch {
		cap = rbRelationshipPrepareOuterBatch
	}
	remaining := cap + 1
	variant := "ignore"
	if p.view.KeepArticles {
		variant = "keep"
	}
	base := "rb.relationship_prepare.title." + variant + "."
	kind := p.outerKind()
	leg := func(part string, expected int, args ...any) ([][]any, error) {
		sizes, err := query(base+part+".bytes", args...)
		if err != nil {
			return nil, err
		}
		if len(sizes) > expected {
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
		if 8192+int64(len(sizes))*1024+2*total > p.copyLimit-p.copied {
			return nil, governor.ErrSnapshotBudget
		}
		rows, err := query(base+part, args...)
		if err != nil {
			return nil, err
		}
		if len(rows) != len(sizes) {
			return nil, ErrStale
		}
		for i, row := range rows {
			if len(row) != 5 || row[0] != sizes[i][0] {
				return nil, ErrStale
			}
			text, ok := row[1].(string)
			if !ok || int64(len(text)) != sizes[i][1] {
				return nil, ErrStale
			}
		}
		p.copied += 8192 + int64(len(rows))*1024 + 2*total
		return rows, nil
	}
	var rows [][]any
	var err error
	if p.outer == nil {
		rows, err = leg("first", remaining, p.view.Library, kind, int64(remaining))
	} else {
		rows, err = leg("tail", remaining, p.view.Library, kind, p.outer.key, p.outer.id, int64(remaining))
		if err == nil && len(rows) < remaining {
			var more [][]any
			more, err = leg("next", remaining-len(rows), p.view.Library, kind, p.outer.key, int64(remaining-len(rows)))
			rows = append(rows, more...)
		}
	}
	if err != nil {
		return nil, false, err
	}
	out, exhausted, err := rbRelationshipSlice(rows, cap)
	if err != nil {
		return nil, false, err
	}
	return out, exhausted, nil
}

// readArtistAlbums streams the current albums of one artist through
// catalog_entities_artist. It is the middle level of artist -> album -> song.
func (p *RBRelationshipPreparation) readArtistAlbums(query browseQuery, f browseFence, artist, after int64, cap int) ([][]any, bool, error) {
	bound := int64(cap + 1)
	var rows [][]any
	var err error
	if after == 0 {
		rows, err = query("rb.relationship_prepare.albums.first", artist, p.view.Library, bound)
	} else {
		rows, err = query("rb.relationship_prepare.albums.tail", artist, p.view.Library, after, bound)
	}
	if err != nil {
		return nil, false, err
	}
	out, exhausted, err := rbRelationshipSlice(rows, cap)
	if err != nil {
		return nil, false, err
	}
	if err := p.reserveRows(len(out)); err != nil {
		return nil, false, err
	}
	return out, exhausted, nil
}

// readSongs streams one album's songs through catalog_entities_album. The
// composite cursor keeps the maintained disc/track/id order without OFFSET.
func (p *RBRelationshipPreparation) readSongs(query browseQuery, f browseFence, album int64, cap int) ([][]any, bool, error) {
	var cursor *rbRelationshipChildCursor
	if p.albumActive != nil {
		cursor = &p.albumSongs
	} else {
		cursor = &p.songs
	}
	bound := int64(cap + 1)
	var rows [][]any
	var err error
	first := !cursor.started
	switch {
	case first:
		rows, err = query("rb.relationship_prepare.songs.first", album, p.view.Library, bound)
	case cursor.leg == 0:
		rows, err = query("rb.relationship_prepare.songs.tail", album, p.view.Library, cursor.disc, cursor.track, cursor.id, bound)
	case cursor.leg == 1:
		rows, err = query("rb.relationship_prepare.songs.next_track", album, p.view.Library, cursor.disc, cursor.track, bound)
	default:
		rows, err = query("rb.relationship_prepare.songs.next", album, p.view.Library, cursor.disc, bound)
	}
	if err != nil {
		return nil, false, err
	}
	out, exhausted, err := rbRelationshipSlice(rows, cap)
	if err != nil {
		return nil, false, err
	}
	if err := p.reserveRows(len(out)); err != nil {
		return nil, false, err
	}
	if !exhausted {
		// A full window can end inside an equal (disc,track) run; finish it
		// before the later-track or later-disc legs.
		cursor.leg = 0
		return out, false, nil
	}
	if first {
		return out, true, nil
	}
	if cursor.leg < 2 {
		cursor.leg++
		return out, false, nil
	}
	return out, true, nil
}

func (p *RBRelationshipPreparation) reserveRows(n int) error {
	cost := int64(n) * 96
	if cost < 0 || cost > p.copyLimit-p.copied {
		return governor.ErrSnapshotBudget
	}
	p.copied += cost
	return nil
}

// rbRelationshipSlice binds one source leg to cap+1 and refuses a longer answer
// as a source that ignored its bound. A full window is never EOF.
func rbRelationshipSlice(rows [][]any, cap int) ([][]any, bool, error) {
	if cap < 0 {
		return nil, false, ErrInput
	}
	if len(rows) > cap+1 {
		return nil, false, ErrUnqualified
	}
	if len(rows) > cap {
		return rows[:cap], false, nil
	}
	return rows, true, nil
}

// rbRelationshipChildAdmitted is the common item predicate plus this library's
// current kind for one child row shaped [id,retired,revision,own_age,library,
// kind,...]. Required ancestors are validated by the caller.
func rbRelationshipChildAdmitted(row []any, f browseFence, library, kind int64) bool {
	if len(row) < 6 {
		return false
	}
	retired, okRetired := row[1].(int64)
	revision, okRevision := row[2].(int64)
	ownAge := row[3]
	actualLibrary, okLibrary := row[4].(int64)
	actualKind, okKind := row[5].(int64)
	if !okRetired || !okRevision || !okLibrary || !okKind {
		return false
	}
	if retired != 0 || actualLibrary != library || actualKind != kind {
		return false
	}
	item := [7]any{row[0], "", nil, nil, retired, revision, ownAge}
	return admittedItem(item[:], f)
}

// Page copies one page of the complete ordered IDs inside a fresh authorized
// response snapshot. The exact whole fence must match before any copy, and the
// first delivery linearizes with the governor exactly like every other prepared
// cache purpose. A later page with a drifted fence is denied, never replayed.
func (p *RBRelationshipPreparation) Page(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput, after *RBRelationshipPrepareCursor, limit int) (RBRelationshipPreparePage, BrowseReadResult, error) {
	if p == nil || p.reader == nil || p.entry == nil {
		return RBRelationshipPreparePage{}, BrowseReadResult{}, ErrInput
	}
	// The publication fence must use the same compiled domain policy as the
	// preparation fence; a caller never supplies or widens it.
	current.policy = readPolicyBinding{recipe: rbRelationshipPolicy}
	return liveRead(ctx, request, budget, output, p.reader.pool, current.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (RBRelationshipPreparePage, BrowseReadResult, error) {
		current.Now = at
		return p.page(ctx, request, budget, output, current, after, limit)
	})
}

func (p *RBRelationshipPreparation) page(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput, after *RBRelationshipPrepareCursor, limit int) (page RBRelationshipPreparePage, result BrowseReadResult, err error) {
	started := time.Now()
	defer func() { result.Elapsed = time.Since(started) }()
	if ctx == nil || output.Authority == nil || output.Grant == nil || output.Request.Writer || output.Request.Owner != request.Owner || output.Request.Class != request.Class || len(current.Database) == 0 || len(current.Database) > 256 || len(current.TokenHash) == 0 || len(current.TokenHash) > 256 || current.Library <= 0 || current.Kind != p.view.Kind || budget <= 0 || limit < 1 || limit > 200 || request.Owner != p.entry.key.owner || request.Class != governor.Foreground || request.Writer || current.After != nil || current.Now.IsZero() {
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
	result.Publication, err = p.reader.pool.ReadForecasted(validation, request, time.Until(deadline), governor.SnapshotForecast{NativeRowBytes: p.reader.pool.NativeRowReservation(), MaxQueries: rbRelationshipPrepareMaxQueries}, func(scope *governor.ReadScope) error {
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
		return RBRelationshipPreparePage{}, result, err
	}
	if err = ctx.Err(); err != nil {
		return RBRelationshipPreparePage{}, result, err
	}
	// Confirm the freshly observed token/horizon at actual publication, after
	// native cleanup; expiry must not leak a copied formerly-authorized page.
	at := current.Now.Add(time.Since(started))
	expires, e1 := time.Parse(time.RFC3339Nano, fresh.expiresAt)
	horizon, e2 := time.Parse(time.RFC3339Nano, fresh.horizon)
	if e1 != nil || e2 != nil || !at.Before(expires) || !at.Before(horizon) {
		_ = p.entry.close()
		return RBRelationshipPreparePage{}, result, ErrDenied
	}
	if !time.Now().Before(deadline) {
		return RBRelationshipPreparePage{}, result, context.DeadlineExceeded
	}
	if err = output.Authority.CheckContext(ctx, output.Grant, output.Request); err != nil {
		return RBRelationshipPreparePage{}, result, err
	}
	page = RBRelationshipPreparePage{IDs: prepared.ids, Total: prepared.total, Exhausted: prepared.exhausted, Statements: result.PublicationStatements}
	if !prepared.exhausted {
		page.After = &RBRelationshipPrepareCursor{key: p.entry.key, fence: fresh, position: prepared.next}
	}
	return page, result, nil
}

func (p *RBRelationshipPreparation) Close() error {
	if p == nil || p.entry == nil {
		return ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.entry.close()
}
