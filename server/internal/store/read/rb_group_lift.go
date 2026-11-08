package read

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Collection and playlist groupOrder lifting.
//
// The public groupOrder of a collection or playlist is NOT the raw authored
// membership window. A member is placed as the title that holds it: an
// episode's place is its show/season, a song's is its album, a book part's is
// its book, and a member of the requested kind is its own place. An entity's
// position is its FIRST authored occurrence, so repeated playlist entries and
// several members sharing one parent never duplicate the public row. This
// producer walks the complete authored source once through the bounded
// preparation lease and retains the lifted public order; only a finished,
// exactly-proved order reports SourceOrdered.
//
// Authored collections reuse the merged RS complete authored-collection root
// proof and member eligibility reader. Automatic collection membership (RS
// automatic) is not landed: an automatic collection is refused with an explicit
// named refusal, never as an invented empty set. Artist (5) and author (13)
// groupOrder need the separate relationship/author recipes (RB11/RB13); they
// are refused here rather than publishing an incomplete set.
type RBGroupLiftSource uint8

const (
	RBGroupLiftCollection RBGroupLiftSource = iota + 1
	RBGroupLiftPlaylist
)

// rbGroupLiftPolicy binds this private recipe to the exact lift source. It is
// stateful so a caller cannot select it with another preparation's state.
const rbGroupLiftPolicy readPolicyID = 32402

// ErrRBGroupLiftAutomatic is the explicit refusal for an automatic collection.
// RS automatic membership is not landed, so no authored-only walk may claim
// automatic membership and no empty result may stand in for it.
var ErrRBGroupLiftAutomatic = errors.New("automatic collection membership is not available")

func init() {
	registerReadPolicy(readPolicyRecipe{id: rbGroupLiftPolicy, domain: "rb-group-lift", kinds: []int64{1, 2, 3, 4, 6, 7, 8, 9, 10}, stateful: true, validate: rbGroupLiftEligibility})
}

type RBGroupLiftInput struct {
	// View carries the requested public kind, the viewer's library and the
	// immutable request anchor. Continuation is private preparation state.
	View    BrowseInput
	Source  RBGroupLiftSource
	Subject int64
}

// RBGroupLiftCursor is a values-only continuation. Its fields are private so a
// caller cannot fabricate another viewer/query/source anchor.
type RBGroupLiftCursor struct {
	key      rbPreparedKey
	fence    browseFence
	position int
}

type RBGroupLiftPage struct {
	// IDs is one page of the complete lifted public order. SourceOrdered is true
	// only because a page exists after the entire authored source was proved and
	// reduced; a raw edge window never reports an ordered public group.
	IDs           []int64
	Total         int64
	SourceOrdered bool
	Exhausted     bool
	Next          *RBGroupLiftCursor
	Statements    int
}

type RBGroupLiftAdvance struct {
	Ready bool
	// SourceOrdered is true only once the complete lifted public order is ready.
	SourceOrdered bool
	Restarts      int
	RawRows       int
	Statements    int
	Snapshot      governor.SnapshotResult
}

// RBGroupLiftReader owns the bounded live jobs for THIS source pair. It creates
// no goroutine and no second scheduler; it reuses the ONE governor preparation
// lease, restart budget and values-only cache purpose from the ordered-ID
// producer.
type RBGroupLiftReader struct {
	mu    sync.Mutex
	jobs  [governor.PreparationMaxActive]*RBGroupLiftPreparation
	pool  *governor.Snapshots
	a     *governor.Authority
	cache *rbPreparedCache
}

// RBGroupLiftPreparation is one opaque preparation job. Polls retain and reuse
// it; they never re-admit, re-size or re-deadline it. No snapshot, Rows,
// statement or SQL handle survives a quantum.
type RBGroupLiftPreparation struct {
	mu      sync.Mutex
	reader  *RBGroupLiftReader
	entry   *rbPreparedEntry
	view    BrowseInput
	source  RBGroupLiftSource
	subject int64
	query   rbPreparedKey
	started time.Time
	after   *rbOrderPosition
	root    []any
	// kept is the private first-occurrence index. Its capacity is funded from
	// the SAME preparation payload as the retained IDs; growth past it refuses
	// rather than truncating the public order.
	kept                           map[int64]struct{}
	capacity                       int64
	complete                       bool
	pinned                         browseFence
	pinnedSet                      bool
	copied, copyLimit, nativeLimit int64
}

// rbGroupLiftEntity is one complete entity point in the accepted parent chain.
// admission is nil when the current row has no accepted own-age fact.
type rbGroupLiftEntity struct {
	present   bool
	id        int64
	kind      int64
	library   int64
	retired   int64
	admission any
	parent    int64
	container int64
}

type rbGroupLiftMember struct {
	key    string
	cursor int64
	levels [3]rbGroupLiftEntity
}

func NewRBGroupLiftReader(pool *governor.Snapshots, a *governor.Authority) (*RBGroupLiftReader, error) {
	if pool == nil || a == nil || !pool.OwnedBy(a) {
		return nil, ErrInput
	}
	cache, err := newRBPreparedCache(a, time.Now)
	if err != nil {
		return nil, err
	}
	return &RBGroupLiftReader{pool: pool, a: a, cache: cache}, nil
}

// BeginRBGroupLift is an EXPLICIT preparation start. Polls retain the returned
// job; they must not Begin again with a fresh deadline. in.View.Now is trusted
// server request-entry time and the immutable query anchor.
func (r *RBGroupLiftReader) BeginRBGroupLift(ctx context.Context, owner governor.OwnerID, bytes int64, in RBGroupLiftInput) (*RBGroupLiftPreparation, error) {
	if r == nil || ctx == nil || owner == 0 || in.View.Now.IsZero() || in.View.Database == "" || len(in.View.Database) > 256 || in.View.TokenHash == "" || len(in.View.TokenHash) > 256 || in.View.Library <= 0 || in.View.After != nil || in.View.policy != (readPolicyBinding{}) || in.Subject <= 0 || (in.Source != RBGroupLiftCollection && in.Source != RBGroupLiftPlaylist) {
		return nil, ErrInput
	}
	// The lifted kinds this recipe can prove completely. Artist/author need their
	// own relationship/author sources; other kinds are not a browse entity kind.
	switch in.View.Kind {
	case 1, 2, 3, 4, 6, 7, 8, 9, 10:
	default:
		return nil, ErrUnqualified
	}
	// This bounded typed encoding is not a retained body or arbitrary query.
	canonical, err := json.Marshal(struct {
		Database, Token string
		Library, Kind   int64
		Source          RBGroupLiftSource
		Subject         int64
		Desc            bool
		Anchor          time.Time
	}{in.View.Database, in.View.TokenHash, in.View.Library, in.View.Kind, in.Source, in.Subject, in.View.Descending, in.View.Now})
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
	capacity := (entry.lease.PayloadBytes() - rbGroupLiftMetadataBytes) / rbGroupLiftKeepBytes
	if capacity < 1 {
		_ = entry.close()
		return nil, errRBPreparationCapacity
	}
	job := &RBGroupLiftPreparation{reader: r, entry: entry, view: in.View, source: in.Source, subject: in.Subject, query: key, started: time.Now(), capacity: capacity, kept: make(map[int64]struct{}, min(int(capacity), 4096))}
	r.jobs[free] = job
	return job, nil
}

func (*RBGroupLiftPreparation) readPolicyState() {}

// rbGroupLiftEligibility binds the private recipe to this job and its exact
// viewer/library/kind anchor. Restricted viewers are refused: this source does
// not prove a restricted container's permitted membership.
func rbGroupLiftEligibility(q browseQuery, in BrowseInput, f browseFence) (string, error) {
	p, ok := in.policy.state.(*RBGroupLiftPreparation)
	if !ok || p == nil || p.entry == nil || in.policy.subject != p.subject || in.Library != p.view.Library || in.Database != p.view.Database || in.TokenHash != p.view.TokenHash || in.Kind != p.view.Kind || in.Descending != p.view.Descending {
		return "", ErrInput
	}
	if f.maximumAge >= 0 || f.allowUnrated == 0 {
		return "", ErrUnqualified
	}
	// Before the first authorized handoff the governor lease is the holding
	// authority. A later current-security page is proved by the fresh whole
	// fence and the caller's own output grant, exactly like the prepared entry.
	if !p.entry.delivered {
		if err := p.entry.cache.a.CheckPreparation(p.entry.lease); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("rb-group-lift:%d:%d:%d", p.source, p.subject, p.view.Kind), nil
}

// Advance performs a bounded number of admitted raw quanta. Each quantum is a
// separate snapshot with genuine EOF and a private source cursor; no handle is
// retained across them. A content invalidation restarts the whole walk through
// task 01's wrapper (at most twice), keeping the owner, grant and deadline.
func (p *RBGroupLiftPreparation) Advance(ctx context.Context, request governor.Request, budget time.Duration) (result RBGroupLiftAdvance, err error) {
	if p == nil || p.reader == nil || p.entry == nil || ctx == nil || request.Owner != p.entry.key.owner || request.Class != governor.Foreground || request.Writer || request.Budget.RawRows < rbGroupLiftMaxQueries || budget <= 0 {
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
		result.Ready, result.SourceOrdered = err == nil, err == nil
		return result, err
	}
	if !p.pinnedSet {
		// The first complete fence must be observed before the restart wrapper
		// can compare a pinned authorization context. One bounded probe window
		// establishes it; nothing can restart inside a single snapshot.
		if _, err = p.window(ctx, request, budget, &result); err != nil {
			return result, err
		}
		result.Ready, result.SourceOrdered = p.complete, p.complete
		return result, nil
	}
	_, err = rbPrepareRestart(p.entry, p.pinned, func(attempt int) (browseFence, error) {
		if attempt > 0 {
			// The wrapper discarded every value from the invalidated attempt.
			// Restart from the beginning of the source; a stale mid-stream
			// cursor could publish a truncated or mixed public order.
			p.resetStream()
			result.Restarts++
		}
		return p.walk(ctx, request, budget, &result)
	})
	if err != nil {
		return result, err
	}
	result.Ready, result.SourceOrdered = p.complete, p.complete
	return result, nil
}

func (p *RBGroupLiftPreparation) resetStream() {
	p.after = nil
	p.root = nil
	p.complete = false
	clear(p.kept)
}

func (p *RBGroupLiftPreparation) walk(ctx context.Context, request governor.Request, budget time.Duration, result *RBGroupLiftAdvance) (browseFence, error) {
	deadline := time.Now().Add(budget)
	if request.Deadline.Before(deadline) {
		deadline = request.Deadline
	}
	if p.entry.lease.Deadline().Before(deadline) {
		deadline = p.entry.lease.Deadline()
	}
	var fresh browseFence
	for n := 0; n < rbGroupLiftQuantaPerAdvance; n++ {
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
// denial. All values for the window are typed and charged before the single
// retain call; a failed window leaves no partial public order.
func (p *RBGroupLiftPreparation) window(ctx context.Context, request governor.Request, budget time.Duration, result *RBGroupLiftAdvance) (fresh browseFence, err error) {
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
	result.Snapshot, err = p.reader.pool.ReadForecasted(parent, request, min(budget, time.Until(p.entry.lease.Deadline())), governor.SnapshotForecast{NativeRowBytes: p.reader.pool.NativeRowReservation(), MaxQueries: rbGroupLiftMaxQueries}, func(scope *governor.ReadScope) error {
		query := func(name string, args ...any) ([][]any, error) {
			result.Statements++
			return scope.Query(readStatementID(name), args...)
		}
		in := p.view
		in.Now = in.Now.Add(time.Since(p.started))
		in.policy = readPolicyBinding{recipe: rbGroupLiftPolicy, subject: p.subject, state: p}
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
		root, e := p.readRoot(query, in, f, false)
		if e != nil {
			return e
		}
		if e = p.acceptRoot(root); e != nil {
			return e
		}
		var raw [][]any
		var exhausted bool
		if p.source == RBGroupLiftCollection {
			raw, exhausted, e = p.readCollectionRows(query, in)
		} else {
			raw, exhausted, e = p.readPlaylistRows(query, in)
		}
		if e != nil {
			return e
		}
		result.RawRows += len(raw)
		if e = p.reduceWindow(query, raw, f); e != nil {
			return e
		}
		if exhausted {
			if e = p.finish(); e != nil {
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
		if errors.Is(err, ErrDenied) || errors.Is(err, ErrStale) || errors.Is(err, ErrUnqualified) || errors.Is(err, ErrRBGroupLiftAutomatic) || errors.Is(err, governor.ErrSnapshotBudget) || errors.Is(err, errRBPreparationCapacity) {
			_ = p.entry.close()
		}
		return fresh, err
	}
	return fresh, nil
}

// readRoot proves the authored source root in the current snapshot. Authored
// collections reuse the merged RS complete root proof and refuse automatic
// identity explicitly. A playlist is owner-only and never a shared or deleted
// list.
func (p *RBGroupLiftPreparation) readRoot(query browseQuery, in BrowseInput, f browseFence, delivery bool) ([]any, error) {
	if p.source == RBGroupLiftCollection {
		rows, err := query("rb.group_lift.collection_source", in.Library, p.subject)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 {
			return nil, ErrDenied
		}
		automatic, err := query("rb.group_lift.automatic", p.subject)
		if err != nil {
			return nil, err
		}
		if len(automatic) != 0 {
			return nil, ErrRBGroupLiftAutomatic
		}
		if _, err = rsCollectionRootProof(query, rows[0], f); err != nil {
			return nil, err
		}
		if err = p.chargeRoot(rows[0], delivery); err != nil {
			return nil, err
		}
		if delivery {
			if p.root == nil || !equalSource(p.root, rows[0]) {
				return nil, ErrStale
			}
		}
		return rows[0], nil
	}
	rows, err := query("rb.group_lift.playlist", p.subject)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, ErrDenied
	}
	row := rows[0]
	if len(row) != 7 || row[1] != "local" || row[2] != f.account || row[3] != f.profile || row[5] != int64(0) {
		return nil, ErrDenied
	}
	if err = p.chargeRoot(row, delivery); err != nil {
		return nil, err
	}
	if delivery {
		if p.root == nil || !equalSource(p.root, row) {
			return nil, ErrStale
		}
	}
	return row, nil
}

// chargeRoot keeps the whole-job root/cursor metadata funded from the SAME
// preparation payload. It is not a request output or a second reservation.
func (p *RBGroupLiftPreparation) chargeRoot(row []any, delivery bool) error {
	if delivery {
		return nil
	}
	n := int64(rbGroupLiftMetadataBytes)
	for _, v := range row {
		switch v := v.(type) {
		case string:
			n += 2 * int64(len(v))
		case []byte:
			n += 3 * int64(len(v))
		}
	}
	if n > p.copyLimit-p.copied {
		return governor.ErrSnapshotBudget
	}
	p.copied += n
	return nil
}

// acceptRoot classifies a changed root: identity/permission moves deny, content
// revisions restart from the beginning. The first attempt captures the root.
func (p *RBGroupLiftPreparation) acceptRoot(row []any) error {
	if p.root == nil {
		p.root = rbGroupLiftCloneRow(row)
		return nil
	}
	if equalSource(p.root, row) {
		return nil
	}
	switch p.source {
	case RBGroupLiftCollection:
		// id,retired,revision,collection_revision,metadata_incarnation,...
		if len(row) != 15 || row[0] != p.root[0] || row[1] != int64(0) {
			return ErrDenied
		}
		return errRBPrepareContent
	case RBGroupLiftPlaylist:
		// id,owner_authority,owner_account,owner_profile,revision,deleted,token
		if len(row) != 7 || row[0] != p.root[0] || row[1] != p.root[1] || row[2] != p.root[2] || row[3] != p.root[3] || row[5] != int64(0) || row[6] != p.root[6] {
			return ErrDenied
		}
		return errRBPrepareContent
	}
	return ErrDenied
}

func rbGroupLiftCloneRow(row []any) []any {
	out := make([]any, len(row))
	for i, v := range row {
		switch v := v.(type) {
		case string:
			out[i] = strings.Clone(v)
		case []byte:
			out[i] = slices.Clone(v)
		default:
			out[i] = v
		}
	}
	return out
}

// readCollectionRows streams complete authored (order_key,item_id) positions
// through the collection page index. The window binds cap+1 and treats a longer
// answer as a source that ignored its bound; a short window is the only EOF.
func (p *RBGroupLiftPreparation) readCollectionRows(query browseQuery, in BrowseInput) ([][]any, bool, error) {
	bound := int64(rbGroupLiftCollectionRaw + 1)
	var raw [][]any
	var err error
	if p.after == nil {
		raw, err = p.readLeg(query, "collection", "first", p.subject, bound)
	} else {
		raw, err = p.readLeg(query, "collection", "tail", p.subject, bound, p.after.key, p.after.id)
		if err == nil && int64(len(raw)) < bound {
			more, e := p.readLeg(query, "collection", "next", p.subject, bound-int64(len(raw)), p.after.key)
			if e != nil {
				return nil, false, e
			}
			raw = append(raw, more...)
		}
	}
	if err != nil {
		return nil, false, err
	}
	if int64(len(raw)) > bound {
		return nil, false, ErrUnqualified
	}
	if int64(len(raw)) == bound {
		return raw[:rbGroupLiftCollectionRaw], false, nil
	}
	return raw, true, nil
}

// readPlaylistRows streams complete authored (order_key,entry id) positions.
// A NULL playlist item consumes its raw position but contributes no member.
func (p *RBGroupLiftPreparation) readPlaylistRows(query browseQuery, in BrowseInput) ([][]any, bool, error) {
	bound := int64(rbGroupLiftPlaylistRaw + 1)
	var raw [][]any
	var err error
	if p.after == nil {
		raw, err = p.readLeg(query, "playlist", "first", p.subject, bound)
	} else {
		raw, err = p.readLeg(query, "playlist", "tail", p.subject, bound, p.after.key, p.after.id)
		if err == nil && int64(len(raw)) < bound {
			more, e := p.readLeg(query, "playlist", "next", p.subject, bound-int64(len(raw)), p.after.key)
			if e != nil {
				return nil, false, e
			}
			raw = append(raw, more...)
		}
	}
	if err != nil {
		return nil, false, err
	}
	if int64(len(raw)) > bound {
		return nil, false, ErrUnqualified
	}
	if int64(len(raw)) == bound {
		return raw[:rbGroupLiftPlaylistRaw], false, nil
	}
	return raw, true, nil
}

// readLeg measures the actual order-key bytes in this SAME snapshot, charges
// them, and then copies the payload with identical bindings. Every payload row
// must pair its measured identity and length exactly.
func (p *RBGroupLiftPreparation) readLeg(query browseQuery, source, part string, subject int64, bound int64, keys ...any) ([][]any, error) {
	prefix := "rb.group_lift." + source + "."
	args := append([]any{subject}, keys...)
	args = append(args, bound)
	sizes, err := query(prefix+part+"_octets", args...)
	if err != nil {
		return nil, err
	}
	if int64(len(sizes)) > bound {
		return nil, ErrUnqualified
	}
	total, err := rbGroupLiftKeyBytes(sizes)
	if err != nil {
		return nil, err
	}
	if total > p.nativeLimit ||
		int64(len(sizes))*rbGroupLiftFixedRowBytes+2*total > p.copyLimit-p.copied {
		return nil, governor.ErrSnapshotBudget
	}
	p.copied += int64(len(sizes))*rbGroupLiftFixedRowBytes + 2*total
	rows, err := query(prefix+part, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) != len(sizes) {
		return nil, ErrStale
	}
	for i, row := range rows {
		if len(row) < 2 || row[0] != sizes[i][0] {
			return nil, ErrStale
		}
		key, ok := row[rbGroupLiftKeyColumn(source)].(string)
		if !ok || int64(len(key)) != sizes[i][1].(int64) {
			return nil, ErrStale
		}
	}
	return rows, nil
}

func rbGroupLiftKeyColumn(source string) int {
	if source == "playlist" {
		return 2
	}
	return 1
}

// reduceWindow types one raw window, lifts each accepted member and retains the
// first occurrence of every public entity. The private cursor always advances
// through the last RAW row, including rejected and NULL entries.
func (p *RBGroupLiftPreparation) reduceWindow(query browseQuery, raw [][]any, f browseFence) error {
	windowIDs := make([]int64, 0, len(raw))
	seen := make(map[int64]struct{}, len(raw))
	var after *rbOrderPosition
	for _, row := range raw {
		member, err := p.parseMember(row)
		if err != nil {
			return err
		}
		after = &rbOrderPosition{key: member.key, id: member.cursor}
		if !member.levels[0].present {
			continue
		}
		visible, err := p.memberVisible(query, member, f)
		if err != nil {
			return err
		}
		if !visible {
			continue
		}
		id, ok := p.lift(member, f)
		if !ok {
			continue
		}
		if _, duplicate := p.kept[id]; duplicate {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		windowIDs = append(windowIDs, id)
	}
	if len(windowIDs) > 0 {
		if int64(len(p.kept))+int64(len(windowIDs)) > p.capacity {
			_ = p.entry.close()
			return errRBPreparationCapacity
		}
		if err := p.entry.retainIDs(windowIDs); err != nil {
			return err
		}
		for _, id := range windowIDs {
			p.kept[id] = struct{}{}
		}
	}
	if after != nil {
		p.after = after
	}
	return nil
}

// parseMember validates one raw row and its complete accepted parent chain.
func (p *RBGroupLiftPreparation) parseMember(row []any) (rbGroupLiftMember, error) {
	var m rbGroupLiftMember
	var base, containerAt int
	if p.source == RBGroupLiftCollection {
		if len(row) != 20 {
			return m, ErrUnqualified
		}
		cursor, ok := row[0].(int64)
		if !ok || cursor <= 0 {
			return m, ErrUnqualified
		}
		key, ok := row[1].(string)
		if !ok || len(key) > rbGroupLiftMaxKey {
			return m, ErrUnqualified
		}
		m.cursor, m.key, base, containerAt = cursor, key, 2, 19
	} else {
		if len(row) != 21 {
			return m, ErrUnqualified
		}
		cursor, ok := row[0].(int64)
		if !ok || cursor <= 0 {
			return m, ErrUnqualified
		}
		key, ok := row[2].(string)
		if !ok || len(key) > rbGroupLiftMaxKey {
			return m, ErrUnqualified
		}
		m.cursor, m.key, base, containerAt = cursor, key, 3, 20
	}
	for level := 0; level < 3; level++ {
		at := base + 6*level
		var e rbGroupLiftEntity
		if at >= len(row) || row[at] == nil {
			m.levels[level] = e
			break
		}
		id, ok := row[at].(int64)
		if !ok || id <= 0 {
			return m, ErrUnqualified
		}
		kind, ok := row[at+1].(int64)
		if !ok || kind < 1 || kind > 13 {
			return m, ErrUnqualified
		}
		library, ok := row[at+2].(int64)
		if !ok || library <= 0 {
			return m, ErrUnqualified
		}
		retired, ok := row[at+3].(int64)
		if !ok || retired < 0 || retired > 1 {
			return m, ErrUnqualified
		}
		e = rbGroupLiftEntity{present: true, id: id, kind: kind, library: library, retired: retired, admission: row[at+4]}
		if level < 2 {
			if row[at+5] != nil {
				parent, ok := row[at+5].(int64)
				if !ok || parent <= 0 {
					return m, ErrUnqualified
				}
				e.parent = parent
			}
		}
		m.levels[level] = e
	}
	if row[containerAt] != nil {
		container, ok := row[containerAt].(int64)
		if !ok || container < 0 || container > 1 {
			return m, ErrUnqualified
		}
		m.levels[0].container = container
	}
	return m, nil
}

// memberVisible applies the accepted member proof. Authored collections reuse
// the merged RS complete member tuple (own age/publication, no automatic
// inference); playlists are current same-library entities and never a
// cross-library reference.
func (p *RBGroupLiftPreparation) memberVisible(query browseQuery, m rbGroupLiftMember, f browseFence) (bool, error) {
	e := m.levels[0]
	if e.library != int64(p.view.Library) {
		return false, nil
	}
	if p.source == RBGroupLiftPlaylist {
		return rbGroupLiftAdmitted(e, f), nil
	}
	visible, err := rsCollectionMemberEligible(query, []any{e.id, e.retired, e.admission, e.kind, e.container}, p.view, f)
	if errors.Is(err, ErrDenied) {
		return false, nil
	}
	return err == nil && visible, err
}

// lift returns the first accepted entity of the requested kind on the member's
// authored parent chain. An unaccepted hop ends the chain rather than skipping
// over it; a foreign-library chain never lifts.
func (p *RBGroupLiftPreparation) lift(m rbGroupLiftMember, f browseFence) (int64, bool) {
	for level := 0; level < len(m.levels); level++ {
		e := m.levels[level]
		if !e.present {
			return 0, false
		}
		if e.library != int64(p.view.Library) {
			return 0, false
		}
		if level > 0 && !rbGroupLiftAdmitted(e, f) {
			return 0, false
		}
		if e.kind == p.view.Kind {
			return e.id, true
		}
	}
	return 0, false
}

func rbGroupLiftAdmitted(e rbGroupLiftEntity, f browseFence) bool {
	if !e.present || e.retired != 0 || e.kind < 1 || e.kind > 13 {
		return false
	}
	if e.admission == nil {
		return f.allowUnrated != 0
	}
	n, ok := e.admission.(int64)
	return ok && (f.maximumAge < 0 || n <= f.maximumAge)
}

// finish reverses a complete descending request in place, then publishes
// readiness through the SAME preparation lease. No new grant or allocation.
func (p *RBGroupLiftPreparation) finish() error {
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

// Page copies one page of the complete lifted order inside a fresh authorized
// response snapshot. The exact whole fence and the authored root must both
// match before any copy; the first delivery linearizes with the governor.
func (p *RBGroupLiftPreparation) Page(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput, after *RBGroupLiftCursor, limit int) (RBGroupLiftPage, BrowseReadResult, error) {
	if p == nil || p.reader == nil || p.entry == nil {
		return RBGroupLiftPage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, p.reader.pool, current.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (RBGroupLiftPage, BrowseReadResult, error) {
		current.Now = at
		return p.page(ctx, request, budget, output, current, after, limit)
	})
}

func (p *RBGroupLiftPreparation) page(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput, after *RBGroupLiftCursor, limit int) (page RBGroupLiftPage, result BrowseReadResult, err error) {
	started := time.Now()
	defer func() { result.Elapsed = time.Since(started) }()
	if ctx == nil || output.Authority == nil || output.Grant == nil || output.Request.Writer || output.Request.Owner != request.Owner || output.Request.Class != request.Class || len(current.Database) == 0 || len(current.Database) > 256 || len(current.TokenHash) == 0 || len(current.TokenHash) > 256 || current.Library <= 0 || current.Kind < 1 || current.Kind > 99 || budget <= 0 || limit < 1 || limit > rbGroupLiftMaxResults || request.Owner != p.entry.key.owner || request.Class != governor.Foreground || request.Writer || current.After != nil || current.Now.IsZero() {
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
	result.Publication, err = p.reader.pool.ReadForecasted(validation, request, time.Until(deadline), governor.SnapshotForecast{NativeRowBytes: p.reader.pool.NativeRowReservation(), MaxQueries: rbGroupLiftMaxQueries}, func(scope *governor.ReadScope) error {
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
		view.policy = readPolicyBinding{recipe: rbGroupLiftPolicy, subject: p.subject, state: p}
		var e error
		fresh, e = readBrowseFence(query, view)
		if e != nil {
			return e
		}
		if after != nil && (after.key != p.entry.key || after.fence != fresh) {
			return ErrStale
		}
		if _, e = p.readRoot(query, view, fresh, true); e != nil {
			return e
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
		if errors.Is(err, ErrDenied) || errors.Is(err, ErrStale) || errors.Is(err, ErrUnqualified) || errors.Is(err, ErrRBGroupLiftAutomatic) {
			_ = p.entry.close()
		}
		return RBGroupLiftPage{}, result, err
	}
	if err = ctx.Err(); err != nil {
		return RBGroupLiftPage{}, result, err
	}
	// Confirm the freshly observed token/horizon at actual publication, after
	// native cleanup; expiry must not leak a copied formerly-authorized page.
	at := current.Now.Add(time.Since(started))
	expires, e1 := time.Parse(time.RFC3339Nano, fresh.expiresAt)
	horizon, e2 := time.Parse(time.RFC3339Nano, fresh.horizon)
	if e1 != nil || e2 != nil || !at.Before(expires) || !at.Before(horizon) {
		_ = p.entry.close()
		return RBGroupLiftPage{}, result, ErrDenied
	}
	if !time.Now().Before(deadline) {
		return RBGroupLiftPage{}, result, context.DeadlineExceeded
	}
	if err = output.Authority.CheckContext(ctx, output.Grant, output.Request); err != nil {
		return RBGroupLiftPage{}, result, err
	}
	page = RBGroupLiftPage{IDs: prepared.ids, Total: prepared.total, SourceOrdered: true, Exhausted: prepared.exhausted, Statements: result.PublicationStatements}
	if !prepared.exhausted {
		page.Next = &RBGroupLiftCursor{key: p.entry.key, fence: fresh, position: prepared.next}
	}
	return page, result, nil
}

func (p *RBGroupLiftPreparation) Close() error {
	if p == nil || p.entry == nil {
		return ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.entry.close()
}
