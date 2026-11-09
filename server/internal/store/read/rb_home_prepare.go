package read

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Large exact fixed Home rows and chronology.
//
// This private preparation producer replaces the small fixed family's
// four-library / thirty-two-entity completion boundary. It keeps the merged
// fixed composition (rb_home_fixed*.go), chronology (rb_chronology.go) and
// whole-playable binding (rb_home_binding*.go) semantics unchanged and only
// replaces how the complete input frame is discovered:
//
//   - new_on_server prepares the complete newest-works set from catalogue
//     chronology frontiers (catalog_entities_added_v1 for leaves;
//     catalog_entities_content_added for a show's maintained chronology) and
//     then completes every selected work's children through the hierarchy
//     indexes. The last100 works, the one-hour delivery group and the
//     episode/season/show target are composed by the unchanged helpers.
//   - continue, listen_next and continue_books walk the profile's complete
//     current personal rows, so an activity row is bounded by the owner's own
//     activity instead of the library's size. listen_next's recent fallback
//     additionally prepares each library's newest albums/books from the same
//     chronology frontiers.
//
// It creates no command ID, no durable receipt and no second scheduler: one
// governor preparation lease, the F-04 original deadline/restart budget and
// the first-delivery linearization are reused exactly. A source that has not
// landed (UO container watermarks, MP reading resume, provider edition
// equivalence) is never invented; the merged fixed composition keeps its named
// pending refusals and this producer preserves them.

var errRBHomePrepareCapacity = errors.New("home preparation exceeds admitted retained bytes")

// RBHomePrepareInput names one published fixed row. View carries the current
// viewer/database anchor; After must be nil and the policy is installed by the
// reader, never by a caller.
type RBHomePrepareInput struct {
	View  BrowseInput
	RowID string
}

// RBHomePreparePage is the prepared shelf. It uses the same harness card shape
// as the small fixed family, so the composed semantics are comparable.
type RBHomePreparePage struct {
	Items                     []HomeFixedCard
	Exact, Exhausted, Pending bool
	PendingReason             string
	RawPositions, RawBindings int
	Statements                int
}

type RBHomePrepareAdvance struct {
	Ready      bool
	Restarts   int
	RawRows    int
	Statements int
	Snapshot   governor.SnapshotResult
}

// RBHomePrepareReader owns the bounded preparation jobs for this composition.
// It creates no goroutine and no second queue.
type RBHomePrepareReader struct {
	mu    sync.Mutex
	jobs  [governor.PreparationMaxActive]*RBHomePreparePreparation
	fixed *HomeFixedReader
	a     *governor.Authority
}

// RBHomePreparePreparation is one opaque preparation job. Polls retain and
// reuse it; they never re-admit, re-size or re-deadline it. No snapshot, Rows,
// statement or SQL handle survives a quantum: every window is reduced into the
// typed values below before the window closes.
type RBHomePreparePreparation struct {
	mu      sync.Mutex
	reader  *RBHomePrepareReader
	entry   *rbHomePrepareEntry
	view    BrowseInput
	row     string
	started time.Time
	phase   int

	streamsBuilt  bool
	streams       []*rbHomePrepareStream
	candidates    map[int64]*rbHomePrepareCandidate
	candidateList []int64
	active        map[int64]int64
	personalAfter int64
	personalDone  bool
	considered    map[int64]bool
	ordered       []int64
	orderedDirty  bool
	cut           map[int64]string
	resumeStreams bool
	works         []int64
	workAt        int
	completion    map[int64]*rbHomePrepareWork
	entities      map[int64]*rbHomePrepareEntity
	hydrated      map[int64]bool
	childrenOf    map[int64][]int64
	seasons       map[int64]bool
	pending       string
	nextScope     *rbHomePrepareScope
	rawRows       int
	rawPositions  int
	rawBindings   int
	statements    int
	copied        int64
	copyLimit     int64
	nativeLimit   int64
}

type rbHomePrepareStream struct {
	library int64
	kind    int64
	clock   Chronology
	fence   browseFence
	started bool
	values  bool
	date    string
	id      int64
	done    bool
}

type rbHomePrepareCandidate struct {
	id, library, kind int64
	date              string
	public            [16]byte
}

type rbHomePrepareWork struct {
	id, library, kind int64
	stage             int // 0 work row, 1 children, 2 seasons, 3 complete
	pending           []int64
	leafAt            int
	children          int
	seasons           []int64
	seasonAt          int
}

// rbHomePrepareScope is the compact pinned authorization/scope witness. It keeps
// only values, never manifest rows: the full source frame is re-read and
// compared at every window and at handoff.
type rbHomePrepareScope struct {
	Principal browseFence
	Libraries []rbHomePrepareLibrary
}

type rbHomePrepareLibrary struct {
	ID                               string
	NativeID                         int64
	Kind, CatalogueState             string
	Position                         int64
	DataRevision, OrderRevision      int64
	MetadataRevision, ViewerRevision int64
	OwnerPolicy                      bool
	Fence                            browseFence
}

// rbHomePrepareEntry is the values-only governor holding. It never escapes the
// reader and carries no raw row, statement or transaction.
type rbHomePrepareEntry struct {
	ctx                context.Context
	owner              governor.OwnerID
	key                [32]byte
	lease              *governor.PreparationLease
	scope              rbHomePrepareScope
	captured, complete bool
	delivered, closed  bool
	// restarts is the job-wide F-04 content-restart budget. It survives across
	// Advance polls; a fresh per-call counter would let one job restart on every
	// poll and exceed the limit.
	restarts                  int
	cards                     []HomeFixedCard
	pending                   string
	rawPositions, rawBindings int
	retained, payload         int64
}

func NewRBHomePrepareReader(pool *governor.Snapshots, a *governor.Authority) (*RBHomePrepareReader, error) {
	if pool == nil || a == nil || !pool.OwnedBy(a) {
		return nil, ErrInput
	}
	fixed, err := NewHomeFixedReader(pool)
	if err != nil {
		return nil, err
	}
	return &RBHomePrepareReader{fixed: fixed, a: a}, nil
}

func rbHomePrepareRowValid(row string) bool {
	return row == "new_on_server" || row == "continue" || row == "listen_next" || row == "continue_books"
}

// Begin is an EXPLICIT preparation start. Polls retain the returned job; they
// must not Begin again with a fresh deadline. in.View.Now is trusted server
// request-entry time and the immutable query anchor.
func (r *RBHomePrepareReader) Begin(ctx context.Context, owner governor.OwnerID, bytes int64, in RBHomePrepareInput) (*RBHomePreparePreparation, error) {
	if r == nil || r.fixed == nil || ctx == nil || owner == 0 || bytes < 8192 || bytes%4096 != 0 || !rbHomePrepareRowValid(in.RowID) ||
		in.View.Now.IsZero() || in.View.Database == "" || len(in.View.Database) > 256 || in.View.TokenHash == "" || len(in.View.TokenHash) > 256 ||
		in.View.After != nil || in.View.Descending || in.View.KeepArticles || in.View.policy != (readPolicyBinding{}) || in.View.Limit < 1 || in.View.Limit > MaxPage {
		return nil, ErrInput
	}
	canonical, err := json.Marshal(struct {
		Database, Token, Row string
		Limit                int
		Anchor               time.Time
	}{in.View.Database, in.View.TokenHash, in.RowID, in.View.Limit, in.View.Now})
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256(canonical)
	deadline := time.Now().Add(governor.PreparationLifetime)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if !r.mu.TryLock() {
		return nil, governor.ErrBusy
	}
	defer r.mu.Unlock()
	free := -1
	for i, job := range r.jobs {
		if job != nil && job.entry != nil && !job.entry.closed && job.entry.key == key {
			if job.entry.lease.Deadline().After(time.Now()) {
				return job, nil
			}
			free = i
			continue
		}
		if free < 0 && (job == nil || job.entry.closed) {
			free = i
		}
	}
	if free < 0 {
		return nil, governor.ErrBusy
	}
	// Home spans every visible library, so the dependency names the
	// all-library wildcard conservatively. It can over-conflict with a
	// publication but never under-cover one.
	d := r.a.BeginPreparation(ctx, governor.PreparationRequest{Key: governor.JobKey{Domain: 395, ID: strings.Join([]string{in.View.Database, in.View.TokenHash, in.RowID}, "\x00")}, Owner: owner, Deadline: deadline, ReservedBytes: bytes, Dependencies: governor.PublicationFootprint{{Library: 0}}})
	if d.Lease == nil {
		return nil, governor.ErrBusy
	}
	entry := &rbHomePrepareEntry{ctx: ctx, owner: owner, key: key, lease: d.Lease}
	p := &RBHomePreparePreparation{
		reader: r, entry: entry, view: in.View, row: in.RowID, started: time.Now(),
		candidates: map[int64]*rbHomePrepareCandidate{}, active: map[int64]int64{}, considered: map[int64]bool{},
		completion: map[int64]*rbHomePrepareWork{}, entities: map[int64]*rbHomePrepareEntity{}, seasons: map[int64]bool{}, childrenOf: map[int64][]int64{}, hydrated: map[int64]bool{},
		personalDone: in.RowID == "new_on_server",
	}
	r.jobs[free] = p
	return p, nil
}

// Advance performs a bounded number of admitted raw quanta. A content
// invalidation restarts the whole walk (at most twice) with the SAME owner,
// grant, retained byte ceiling and original absolute deadline. No prior
// attempt's values can be published after a restart.
func (p *RBHomePreparePreparation) Advance(ctx context.Context, request governor.Request, budget time.Duration) (result RBHomePrepareAdvance, err error) {
	if p == nil || p.reader == nil || p.entry == nil || ctx == nil || request.Owner != p.entry.owner || request.Class != governor.Foreground || request.Writer || request.Budget.RawRows < uint32(rbHomePrepareMaxQueries) || budget <= 0 {
		return result, ErrInput
	}
	if !p.mu.TryLock() {
		return result, governor.ErrBusy
	}
	defer p.mu.Unlock()
	if p.entry.complete {
		err = p.check()
		result.Ready = err == nil
		return result, err
	}
	if err = p.check(); err != nil {
		return result, err
	}
	for {
		err = p.walk(ctx, request, budget, &result)
		if err == nil {
			result.Ready = p.entry.complete
			return result, nil
		}
		if !errors.Is(err, errRBPrepareContent) {
			return result, err
		}
		if p.entry.restarts >= rbHomePrepareRestartLimit || p.entry.delivered {
			p.clear()
			_ = p.entry.close(p.reader)
			return result, ErrStale
		}
		if err = p.reset(); err != nil {
			return result, err
		}
		if p.nextScope != nil {
			if err = p.entry.adoptScope(*p.nextScope); err != nil {
				return result, err
			}
			p.nextScope = nil
		}
		p.entry.restarts++
		result.Restarts++
	}
}

func (p *RBHomePreparePreparation) walk(ctx context.Context, request governor.Request, budget time.Duration, result *RBHomePrepareAdvance) error {
	deadline := time.Now().Add(budget)
	if request.Deadline.Before(deadline) {
		deadline = request.Deadline
	}
	if p.entry.lease.Deadline().Before(deadline) {
		deadline = p.entry.lease.Deadline()
	}
	for n := 0; n < rbHomePrepareQuantaPerAdvance; n++ {
		if !time.Now().Before(deadline) || p.entry.complete {
			return nil
		}
		err := p.window(ctx, request, budget, result)
		if errors.Is(err, errRBPrepareContent) {
			return err
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// window performs ONE fence snapshot and one bounded unit of preparation. The
// whole manifest/principal/library scope is established before any value is
// reduced, so a moved authorization context is a denial and a moved content
// revision is a restart.
func (p *RBHomePreparePreparation) window(ctx context.Context, request governor.Request, budget time.Duration, result *RBHomePrepareAdvance) (err error) {
	p.copied = 0
	p.copyLimit = request.Need[governor.MemoryBytes] - p.reader.fixed.native.pool.NativeRowReservation()
	p.nativeLimit = p.reader.fixed.native.pool.NativeRowReservation()
	if p.copyLimit <= 0 {
		return governor.ErrSnapshotBudget
	}
	if request.Deadline.After(p.entry.lease.Deadline()) {
		request.Deadline = p.entry.lease.Deadline()
	}
	parent, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(p.entry.ctx, cancel)
	defer func() { stop(); cancel() }()
	rawBefore := p.rawRows
	result.Snapshot, err = p.reader.fixed.native.pool.ReadForecasted(parent, request, min(budget, time.Until(p.entry.lease.Deadline())), governor.SnapshotForecast{NativeRowBytes: p.reader.fixed.native.pool.NativeRowReservation(), MaxQueries: rbHomePrepareMaxQueries}, func(scope *governor.ReadScope) error {
		s := &savedViewSession{r: p.reader.fixed.native, ctx: scope.Context(), request: request, started: time.Now(), clock: p.view.Now, copyLimit: p.copyLimit, nativeLimit: p.nativeLimit}
		query := func(name string, args ...any) ([][]any, error) {
			p.statements++
			rows, e := scope.Query(readStatementID(name), args...)
			if e != nil {
				return nil, e
			}
			if e = s.charge(rows); e != nil {
				return nil, e
			}
			return rows, nil
		}
		fresh, frame, e := p.readScope(s, query)
		if e != nil {
			return e
		}
		if !p.entry.captured {
			if e = p.entry.capture(fresh); e != nil {
				return e
			}
		} else if !rbHomePrepareScopeEqual(p.entry.scope, fresh) {
			if !rbHomePrepareScopeAuthEqual(p.entry.scope, fresh) {
				return ErrDenied
			}
			p.nextScope = &fresh
			return errRBPrepareContent
		}
		if p.pending == "" {
			doc := homeManifestSourceProject(frame, s.input(p.view).Now).Manifest
			visible := false
			for _, row := range doc.Rows {
				if row.ID == p.row {
					visible = true
					break
				}
			}
			if !visible {
				return ErrDenied
			}
		}
		e = p.reduce(s, query)
		p.copied = s.copied
		return e
	})
	result.Statements += p.statements
	if delta := p.rawRows - rawBefore; delta > 0 {
		result.RawRows += delta
	}
	p.statements = 0
	if result.Snapshot.Refusal != nil {
		err = governor.ErrBusy
	}
	if err != nil {
		if errors.Is(err, errRBHomePrepareCapacity) && p.entry.captured {
			// The admitted retained-byte ceiling cannot hold this complete
			// source. Publish an explicit pending page, never a truncation.
			p.pending = "home_prepare_capacity"
			p.phase = 3
			return nil
		}
		if errors.Is(err, ErrDenied) || errors.Is(err, ErrStale) || errors.Is(err, ErrUnqualified) || errors.Is(err, governor.ErrSnapshotBudget) || errors.Is(err, errRBHomePrepareCapacity) {
			p.clear()
			_ = p.entry.close(p.reader)
		}
		return err
	}
	return nil
}

// readScope re-reads the complete committed manifest, the pinned principal and
// every visible library's native fence, then reduces them to the compact scope.
func (p *RBHomePreparePreparation) readScope(s *savedViewSession, query browseQuery) (rbHomePrepareScope, homeManifestSourceFrame, error) {
	var principal publicationPrincipalPin
	frame, err := homeManifestSourcePinnedFrameRead(s, query, p.view, &principal)
	if err != nil {
		return rbHomePrepareScope{}, frame, err
	}
	scope := rbHomePrepareScope{Principal: frame.Manifest.Principal}
	if frame.Manifest.Pending {
		p.pending = "library_membership_preparation"
		return scope, frame, nil
	}
	for _, lib := range frame.Manifest.Libraries {
		// The server-wide newest shelf is video-only, exactly like the public
		// row; other libraries neither contribute cards nor consume this
		// preparation's scope. Activity rows keep every library so the merged
		// composition's own kind and policy boundaries still apply.
		if p.row == "new_on_server" && !homeFixedVideoLibrary(lib.Kind) {
			continue
		}
		if len(scope.Libraries) == rbHomePrepareLibraryCap {
			p.pending = "library_scope_preparation"
			return scope, frame, nil
		}
		item := rbHomePrepareLibrary{ID: lib.ID, NativeID: lib.NativeID, Kind: lib.Kind, CatalogueState: lib.CatalogueState, Position: lib.Position, DataRevision: lib.DataRevision, OrderRevision: lib.OrderRevision, MetadataRevision: lib.MetadataRevision, ViewerRevision: lib.ViewerRevision}
		if lib.NativeID <= 0 || lib.CatalogueState != "current" {
			p.pending = "library_catalogue_unprepared"
			return scope, frame, nil
		}
		fence, err := readHomeFixedLibraryFence(query, p.view, lib.NativeID)
		if err != nil {
			return scope, frame, err
		}
		item.Fence = fence
		policy, err := query("rb.home.fixed.owner.policy", "library:"+lib.ID)
		if err != nil {
			return scope, frame, err
		}
		item.OwnerPolicy = len(policy) != 0
		scope.Libraries = append(scope.Libraries, item)
	}
	return scope, frame, nil
}

// reduce advances the requested row's bounded state machine by one unit.
func (p *RBHomePreparePreparation) reduce(s *savedViewSession, query browseQuery) error {
	if p.entry.complete {
		return nil
	}
	if p.pending != "" && p.phase < 3 {
		p.phase = 3
	}
	switch p.phase {
	case 0:
		if !p.streamsBuilt {
			p.buildStreams()
			p.streamsBuilt = true
		}
		budget := rbHomePrepareStreamsPerQuantum
		if err := p.advanceStreams(query, &budget); err != nil {
			return err
		}
		if err := p.advancePersonal(s, query, &budget); err != nil {
			return err
		}
		if p.pending != "" {
			return nil
		}
		if p.resumeStreams {
			// A later round found that the composed-date cut is not yet proved.
			// Take the newly read candidates into selection; completion and the
			// frontier decide again whether the shelf is closed.
			p.resumeStreams = false
			p.phase = 1
			return nil
		}
		if p.indexReady() {
			p.phase = 1
		}
	case 1:
		return p.advanceSelection(s, query)
	case 2:
		budget := 1
		return p.advanceCompletion(s, query, &budget)
	case 3:
		return p.compose(s)
	}
	return nil
}

// buildStreams derives the finite set of source frontiers for this row. It is
// compiled policy: a request cannot name a library, kind or clock.
func (p *RBHomePreparePreparation) buildStreams() {
	add := func(library, kind int64, clock Chronology, fence browseFence) {
		p.streams = append(p.streams, &rbHomePrepareStream{library: library, kind: kind, clock: clock, fence: fence, values: true})
	}
	for _, lib := range p.entry.scope.Libraries {
		switch p.row {
		case "new_on_server":
			switch lib.Kind {
			case "movie":
				add(lib.NativeID, 1, DateAdded, lib.Fence)
			case "tv", "anime":
				if rbHomePrepareUnrestricted(lib.Fence) {
					add(lib.NativeID, 2, ContentAdded, lib.Fence)
				} else {
					add(lib.NativeID, 4, DateAdded, lib.Fence)
				}
			}
		case "listen_next":
			switch lib.Kind {
			case "music":
				add(lib.NativeID, 7, DateAdded, lib.Fence)
			case "audiobook":
				add(lib.NativeID, 9, DateAdded, lib.Fence)
			}
		}
	}
}

// indexReady reports whether the frontier proof has closed: every source is
// either exhausted or cannot contribute a work above the current threshold.
func (p *RBHomePreparePreparation) indexReady() bool {
	if p.row != "new_on_server" && !p.personalDone {
		return false
	}
	for _, stream := range p.streams {
		if stream.done {
			continue
		}
		threshold, ok := p.candidateThreshold(stream.library)
		if !ok {
			return false
		}
		if stream.values && !(stream.date < threshold) {
			return false
		}
		// The undated bucket sorts after every dated row. Once a dated
		// threshold exists, unseen null rows cannot enter the prepared set.
	}
	return true
}

// candidateThreshold returns the newest-works cut over candidate LEAF dates for
// one library (or globally for the server-wide shelf). It is only the initial
// input-batch gate: leaf dates are an upper bound for composed dates, so it must
// never be mistaken for the published cut. frontier proves that cut.
func (p *RBHomePreparePreparation) candidateThreshold(library int64) (string, bool) {
	var dates []string
	for _, c := range p.candidates {
		if c.date == "" {
			continue
		}
		if p.row != "new_on_server" && c.library != library {
			continue
		}
		dates = append(dates, c.date)
	}
	if len(dates) < rbHomePrepareRowLimit {
		return "", false
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))
	return dates[rbHomePrepareRowLimit-1], true
}

// advanceStreams advances up to budget chronology frontiers, one window each.
func (p *RBHomePreparePreparation) advanceStreams(query browseQuery, budget *int) error {
	for _, stream := range p.streams {
		if *budget <= 0 {
			return nil
		}
		if stream.done {
			continue
		}
		*budget--
		if err := p.readStream(query, stream); err != nil {
			return err
		}
		if p.pending != "" {
			return nil
		}
	}
	return nil
}

func (p *RBHomePreparePreparation) readStream(query browseQuery, stream *rbHomePrepareStream) error {
	var rows [][]any
	var err error
	if !stream.started {
		stream.started, stream.values = true, true
		rows, err = query("rb.home_prepare."+string(stream.clock)+".first", stream.library, stream.kind, int64(rbHomePrepareRaw+1))
	} else if stream.values {
		rows, err = query("rb.home_prepare."+string(stream.clock)+".equal", stream.library, stream.kind, stream.date, stream.id, int64(rbHomePrepareRaw+1))
		if err == nil && len(rows) < rbHomePrepareRaw+1 {
			var more [][]any
			more, err = query("rb.home_prepare."+string(stream.clock)+".next", stream.library, stream.kind, stream.date, int64(rbHomePrepareRaw+1-len(rows)))
			rows = append(rows, more...)
		}
	} else {
		rows, err = query("rb.home_prepare."+string(stream.clock)+".nulls", stream.library, stream.kind, stream.id, int64(rbHomePrepareRaw+1))
	}
	if err != nil {
		return err
	}
	if err = rbHomePrepareChronologyOctets(rows, rbHomePrepareRaw+1); err != nil {
		return err
	}
	full := len(rows) > rbHomePrepareRaw
	if full {
		rows = rows[:rbHomePrepareRaw]
	}
	if len(rows) == 0 {
		if stream.values {
			stream.values, stream.id = false, int64(1<<62)
			return nil
		}
		stream.done = true
		return nil
	}
	for _, row := range rows {
		date, _ := row[1].(string)
		id := row[0].(int64)
		p.rawRows++
		p.rawPositions++
		stream.date, stream.id = date, id
		work, kind := int64(0), int64(0)
		if stream.clock == ContentAdded {
			work, kind = id, 2
		} else {
			switch stream.kind {
			case 1:
				work, kind = id, 1
			case 4:
				work, kind = rbHomePrepareNullableInt(row[5]), 2
			case 7:
				work, kind = rbHomePrepareNullableInt(row[6]), 6
			case 9:
				work, kind = rbHomePrepareNullableInt(row[7]), 8
			}
		}
		if work <= 0 {
			continue
		}
		if !rbHomePrepareAdmitted(row[3].(int64), row[4], stream.fence) {
			continue
		}
		public, _ := row[2].([]byte)
		if err := p.addCandidate(work, stream.library, kind, date, public); err != nil {
			return err
		}
		if p.pending != "" {
			return nil
		}
	}
	if !full {
		if stream.values {
			stream.values, stream.id = false, int64(1<<62)
		} else {
			stream.done = true
		}
	}
	return nil
}

// addCandidate keeps one work candidate. The retained candidate map, its pointer
// and the parallel ordering slice are charged before the value is stored, so the
// admitted retained-byte ceiling covers the workspace and never truncates it.
func (p *RBHomePreparePreparation) addCandidate(id, library, kind int64, date string, public []byte) error {
	c := p.candidates[id]
	if c == nil {
		if len(p.candidateList) >= rbHomePrepareSelectionCap {
			p.pending = "home_prepare_selection_cap"
			return nil
		}
		if err := p.entry.retainValue(rbHomePrepareCandidateCost + int64(len(date))); err != nil {
			return err
		}
		c = &rbHomePrepareCandidate{id: id, library: library, kind: kind}
		p.candidates[id] = c
		p.candidateList = append(p.candidateList, id)
		p.orderedDirty = true
	}
	if date != "" && (c.date == "" || date > c.date) {
		c.date = date
	}
	if kind == 1 && id == c.id && len(public) == 16 {
		copy(c.public[:], public)
	}
	return nil
}

// keepEntity adds one typed entity value and charges its actual retained bytes
// against the preparation lease before it is stored.
func (p *RBHomePreparePreparation) keepEntity(id int64, ent *rbHomePrepareEntity) error {
	if _, ok := p.entities[id]; ok {
		return nil
	}
	size := int64(256)
	for _, value := range []string{ent.Added, ent.ContentAdded, ent.Personal.ActivityAt, ent.Personal.ContainerWatermark, ent.Personal.ContainerClearedThrough, ent.Personal.ResumePlaybackID, ent.Personal.DismissedActivityAt, ent.Personal.DismissedPlaybackID} {
		size += 2 * int64(len(value))
	}
	if err := p.entry.retainValue(size); err != nil {
		return err
	}
	p.entities[id] = ent
	return nil
}

func (p *RBHomePreparePreparation) libraryFence(library int64) (browseFence, bool) {
	for _, lib := range p.entry.scope.Libraries {
		if lib.NativeID == library {
			return lib.Fence, true
		}
	}
	return browseFence{}, false
}

// advancePersonal walks the profile's complete current personal set. It is the
// activity-row source; only rows the owner actually has are read.
func (p *RBHomePreparePreparation) advancePersonal(s *savedViewSession, query browseQuery, budget *int) error {
	if p.personalDone || *budget <= 0 || p.row == "new_on_server" {
		return nil
	}
	*budget--
	profile := p.entry.scope.Principal.profile
	rows, err := query("rb.home_prepare.personal.first", profile, p.personalAfter, int64(rbHomePreparePersonalWindow+1))
	if err != nil {
		return err
	}
	if err = rbHomePreparePersonalOctets(rows, rbHomePreparePersonalWindow+1); err != nil {
		return err
	}
	full := len(rows) > rbHomePreparePersonalWindow
	if full {
		rows = rows[:rbHomePreparePersonalWindow]
	}
	for _, row := range rows {
		id := row[0].(int64)
		p.personalAfter = id
		p.rawRows++
		p.rawPositions++
		if row[1] == nil {
			continue
		}
		library := row[1].(int64)
		fence, ok := p.libraryFence(library)
		if !ok {
			// Legacy Home starts from the visible libraries' membership, so a
			// personal row whose library has left the viewer's scope is simply
			// not part of the shelf. Ignoring it matches that source; denying the
			// whole row would publish less than the legacy composition.
			continue
		}
		ent, err := p.hydrateEntity(s, query, id, library, fence, true)
		if err != nil {
			return err
		}
		if ent == nil {
			// A personal row that no longer names a visible entity is not a
			// member of any in-scope library. Legacy iterates library membership,
			// never the personal set, so this row is ignored rather than stale.
			continue
		}
		if err := p.keepEntity(id, ent); err != nil {
			return err
		}
		if p.pending != "" {
			return nil
		}
		work := int64(0)
		switch ent.Kind {
		case 4:
			if ent.HasShow {
				work = ent.Show
			}
		case 7:
			if ent.HasAlbum {
				work = ent.Album
			}
		case 9:
			if ent.HasBook {
				work = ent.Book
			}
		default:
			if ent.Kind == 1 || ent.Kind == 2 || ent.Kind == 6 || ent.Kind == 8 {
				work = id
			}
		}
		if work <= 0 {
			continue
		}
		if _, ok := p.entities[work]; !ok {
			parent, err := p.hydrateEntity(s, query, work, library, fence, true)
			if err != nil {
				return err
			}
			if parent == nil {
				return ErrStale
			}
			if err := p.keepEntity(work, parent); err != nil {
				return err
			}
			if p.pending != "" {
				return nil
			}
		}
		p.active[work] = library
	}
	if !full {
		p.personalDone = true
	}
	return nil
}

// advanceSelection considers one candidate work in descending leaf-date order.
// Hydration is what proves whether a candidate can yield a card, so the loop
// keeps taking candidates until every candidate that could reach the current
// composed-date cut has been hydrated. Completion and frontier then decide
// whether the shelf is exact; unplayable works are completed (so their absence
// is proved) but never counted.
func (p *RBHomePreparePreparation) advanceSelection(s *savedViewSession, query browseQuery) error {
	if err := p.selectActive(); err != nil {
		return err
	}
	ordered := p.orderedCandidates()
	for _, id := range ordered {
		if p.considered[id] {
			continue
		}
		c := p.candidates[id]
		p.considered[id] = true
		if p.belowCut(c) {
			continue
		}
		if _, ok := p.entities[id]; !ok {
			fence, ok := p.libraryFence(c.library)
			if !ok {
				return ErrDenied
			}
			ent, err := p.hydrateEntity(s, query, id, c.library, fence, true)
			if err != nil {
				return err
			}
			if ent == nil {
				return ErrStale
			}
			if err := p.keepEntity(id, ent); err != nil {
				return err
			}
		}
		ent := p.entities[id]
		c.kind = ent.Kind
		c.public = ent.Public
		if err := p.addWork(id, ent.Library, ent.Kind); err != nil {
			return err
		}
		return nil
	}
	p.phase = 2
	return nil
}

// selectActive adds the profile's own activity works. They are a complete
// personal source, not part of a chronology frontier, so every one is selected.
func (p *RBHomePreparePreparation) selectActive() error {
	for id := range p.active {
		ent := p.entities[id]
		if ent == nil {
			continue
		}
		if err := p.addWork(id, ent.Library, ent.Kind); err != nil {
			return err
		}
	}
	return nil
}

// addWork retains one selected work and charges its workspace before growth.
func (p *RBHomePreparePreparation) addWork(id, library, kind int64) error {
	if _, ok := p.completion[id]; ok {
		return nil
	}
	if len(p.works) >= rbHomePrepareSelectionCap {
		p.pending = "home_prepare_selection_cap"
		return nil
	}
	if err := p.entry.retainValue(rbHomePrepareWorkCost); err != nil {
		return err
	}
	p.works = append(p.works, id)
	p.completion[id] = &rbHomePrepareWork{id: id, library: library, kind: kind}
	return nil
}

// orderedCandidates returns the candidate ids in descending leaf-date order.
// Leaf dates are only the discovery order (and an upper bound for composed
// dates); the published order is proved later from actualDate.
func (p *RBHomePreparePreparation) orderedCandidates() []int64 {
	if !p.orderedDirty && len(p.ordered) == len(p.candidateList) {
		return p.ordered
	}
	p.ordered = append(p.ordered[:0], p.candidateList...)
	slices.SortFunc(p.ordered, func(a, b int64) int {
		ca, cb := p.candidates[a], p.candidates[b]
		if ca.date != cb.date {
			if ca.date > cb.date {
				return -1
			}
			return 1
		}
		if ca.id > cb.id {
			return -1
		}
		if ca.id < cb.id {
			return 1
		}
		return 0
	})
	p.orderedDirty = false
	return p.ordered
}

// belowCut reports that a candidate's leaf date is older than the proved
// composed-date cut for its scope. Every unread or unselected work's composed
// date is at most its leaf date, so such a candidate can never enter the
// published row and need not be hydrated. The cut only advances, so using it
// here never skips a work that the final proof still needs.
func (p *RBHomePreparePreparation) belowCut(c *rbHomePrepareCandidate) bool {
	scope := int64(0)
	if p.row == "listen_next" {
		scope = c.library
	}
	cut, ok := p.cut[scope]
	if !ok {
		return false
	}
	return c.date == "" || c.date < cut
}

func (p *RBHomePreparePreparation) advanceCompletion(s *savedViewSession, query browseQuery, budget *int) error {
	for *budget > 0 && p.workAt < len(p.works) && p.pending == "" {
		*budget--
		w := p.completion[p.works[p.workAt]]
		if w == nil {
			p.workAt++
			continue
		}
		if err := p.readWork(s, query, w); err != nil {
			return err
		}
		if w.stage >= 3 {
			p.workAt++
		}
	}
	if p.pending != "" {
		return nil
	}
	if p.workAt >= len(p.works) {
		// Availability can move a work's composed date below its newest admitted
		// leaf date. The shelf is exact only once the composed-date cut is proved
		// against the next unread leaf-date upper bound; otherwise keep reading.
		needSelection, needStreams := p.frontier()
		switch {
		case needSelection:
			p.phase = 1
		case needStreams:
			p.resumeStreams = true
			p.phase = 0
		default:
			p.phase = 3
		}
	}
	return nil
}

// actualDate is the date the unchanged composition will use for one work: the
// newest available child (or the work's own date), and the maintained content
// chronology for an unrestricted show. A work with no available leaf yields no
// card and therefore no date.
func (p *RBHomePreparePreparation) actualDate(id int64) string {
	ent := p.entities[id]
	if ent == nil {
		return ""
	}
	if ent.Retired != 0 {
		return ""
	}
	if ent.Kind == 1 {
		if ent.Available {
			if ent.HasAdded {
				return ent.Added
			}
		}
		return ""
	}
	if ent.Kind != 2 && ent.Kind != 6 && ent.Kind != 8 {
		return ""
	}
	available, best := false, ""
	for _, child := range p.childrenOf[id] {
		leaf := p.entities[child]
		if leaf == nil || !leaf.Available || !leaf.HasAdded {
			continue
		}
		available = true
		if leaf.Added > best {
			best = leaf.Added
		}
	}
	if !available {
		return ""
	}
	if ent.Kind == 2 {
		if fence, ok := p.libraryFence(ent.Library); ok && rbHomePrepareUnrestricted(fence) && ent.HasContentAdded {
			return ent.ContentAdded
		}
	}
	return best
}

// rbHomePreparePublished is one card-producing work's composed date and public
// id, in the same tie order as homeFixedRecent/sortHomeCards.
type rbHomePreparePublished struct {
	date   string
	public [16]byte
}

// publishable returns the works that actually yield a card for one scope (zero =
// the whole shelf), ordered exactly like the published row. A work whose newest
// admitted leaf has no resolvable binding is absent: it is skipped, never
// counted toward the row size.
func (p *RBHomePreparePreparation) publishable(scope int64) []rbHomePreparePublished {
	out := make([]rbHomePreparePublished, 0, len(p.works))
	for _, id := range p.works {
		ent := p.entities[id]
		if ent == nil {
			continue
		}
		if scope != 0 && ent.Library != scope {
			continue
		}
		date := p.actualDate(id)
		if date == "" {
			continue
		}
		out = append(out, rbHomePreparePublished{date: date, public: ent.Public})
	}
	slices.SortFunc(out, func(a, b rbHomePreparePublished) int {
		if a.date != b.date {
			if a.date > b.date {
				return -1
			}
			return 1
		}
		return -strings.Compare(entityid.Encode(a.public[:]), entityid.Encode(b.public[:]))
	})
	return out
}

// frontier proves whether the published composed-date cut is final. A
// candidate's composed date is never newer than its newest leaf date, so the
// next unread leaf date is an upper bound for every unread work's composed date.
// The shelf is exact only when the published row size of card-producing works is
// known and the limit-th composed date is strictly newer than that upper bound
// (an equal instant could still outrank by public id), or the scope's source is
// genuinely exhausted. It reports whether more candidates must be hydrated or
// more source rows read; both are bounded by the named capacity refusals.
func (p *RBHomePreparePreparation) frontier() (needSelection, needStreams bool) {
	if p.row != "new_on_server" && p.row != "listen_next" {
		return false, false
	}
	if p.cut == nil {
		p.cut = map[int64]string{}
	}
	for _, scope := range p.scopes() {
		published := p.publishable(scope)
		if len(published) >= rbHomePrepareRowLimit {
			cut := published[rbHomePrepareRowLimit-1].date
			p.cut[scope] = cut
			if p.hasUnconsidered(scope, cut, true) {
				needSelection = true
				continue
			}
			if p.scopeStreamsDone(scope) {
				continue
			}
			upper, known := p.scopeUpperBound(scope)
			if !known || !(cut > upper) {
				needStreams = true
			}
			continue
		}
		// Fewer card-producing works than the published row are known. Any
		// remaining candidate could still add one, so none may be skipped and the
		// source must be read to its end before the shelf is called exhausted.
		if p.hasUnconsidered(scope, "", false) {
			needSelection = true
		}
		if !p.scopeStreamsDone(scope) {
			needStreams = true
		}
	}
	return needSelection, needStreams
}

// scopes names the independent published rows: the whole shelf for
// new_on_server, or each configured listening library for listen_next.
func (p *RBHomePreparePreparation) scopes() []int64 {
	if p.row == "new_on_server" {
		return []int64{0}
	}
	if p.row != "listen_next" {
		return nil
	}
	seen := map[int64]bool{}
	scopes := make([]int64, 0, len(p.streams))
	for _, stream := range p.streams {
		if seen[stream.library] {
			continue
		}
		seen[stream.library] = true
		scopes = append(scopes, stream.library)
	}
	slices.Sort(scopes)
	return scopes
}

// hasUnconsidered reports whether a candidate at or above the scope's cut is
// still unselected. When no cut exists every remaining candidate of the scope
// could produce a card, so any unconsidered candidate needs selection.
func (p *RBHomePreparePreparation) hasUnconsidered(scope int64, cut string, hasCut bool) bool {
	for _, id := range p.orderedCandidates() {
		if p.considered[id] {
			continue
		}
		c := p.candidates[id]
		if scope != 0 && c.library != scope {
			continue
		}
		if !hasCut {
			return true
		}
		if c.date != "" && c.date >= cut {
			return true
		}
	}
	return false
}

func (p *RBHomePreparePreparation) scopeStreamsDone(scope int64) bool {
	for _, stream := range p.streams {
		if scope != 0 && stream.library != scope {
			continue
		}
		if !stream.done {
			return false
		}
	}
	return true
}

// scopeUpperBound is the newest leaf date that any unread candidate of the scope
// could still have. An unstarted stream is unknown, not empty.
func (p *RBHomePreparePreparation) scopeUpperBound(scope int64) (string, bool) {
	upper := ""
	for _, stream := range p.streams {
		if scope != 0 && stream.library != scope {
			continue
		}
		if stream.done {
			continue
		}
		if !stream.started {
			return "", false
		}
		// The undated bucket sorts after every dated row, so it contributes no
		// dated upper bound.
		if stream.values && stream.date > upper {
			upper = stream.date
		}
	}
	return upper, true
}

func (p *RBHomePreparePreparation) readWork(s *savedViewSession, query browseQuery, w *rbHomePrepareWork) error {
	fence, ok := p.libraryFence(w.library)
	if !ok {
		return ErrDenied
	}
	switch w.stage {
	case 0:
		if _, ok := p.entities[w.id]; !ok {
			ent, err := p.hydrateEntity(s, query, w.id, w.library, fence, true)
			if err != nil {
				return err
			}
			if ent == nil {
				return ErrStale
			}
			if err := p.keepEntity(w.id, ent); err != nil {
				return err
			}
			w.kind = ent.Kind
		}
		if rbHomePrepareChildSourceForKind(w.kind).name == "" {
			w.stage = 3
		} else {
			w.stage = 1
		}
	case 1:
		return p.readChildren(s, query, w, fence)
	case 2:
		return p.readSeasons(s, query, w, fence)
	case 3:
		return nil
	}
	return nil
}

// readChildren reads one work's complete bounded child window, then hydrates a
// bounded number of those children (personal text and exact binding bytes) per
// snapshot. A child set larger than the declared window is a named pending
// boundary, never a truncated set.
func (p *RBHomePreparePreparation) readChildren(s *savedViewSession, query browseQuery, w *rbHomePrepareWork, fence browseFence) error {
	source := rbHomePrepareChildSourceForKind(w.kind)
	if source.name == "" {
		w.stage = 2
		return nil
	}
	if w.pending == nil {
		rows, err := query("rb.home_prepare.children."+source.name+".all", w.id, w.library, int64(rbHomePrepareChildWindow+1))
		if err != nil {
			return err
		}
		if err = rbHomePrepareChildOctets(rows, rbHomePrepareChildWindow+1); err != nil {
			return err
		}
		if len(rows) > rbHomePrepareChildWindow {
			p.pending = "home_prepare_child_window"
			return nil
		}
		w.pending = make([]int64, 0, len(rows))
		for _, row := range rows {
			ent, err := rbHomePrepareEntityFrom(row)
			if err != nil {
				return err
			}
			ent.Library = w.library
			w.children++
			p.rawRows++
			p.rawPositions++
			if err := p.entry.retainValue(rbHomePrepareChildRefCost); err != nil {
				return err
			}
			p.childrenOf[w.id] = append(p.childrenOf[w.id], ent.ID)
			if _, ok := p.entities[ent.ID]; !ok {
				if err := p.keepEntity(ent.ID, ent); err != nil {
					return err
				}
			}
			if ent.Kind == 4 && ent.HasSeason && !p.seasons[ent.Season] {
				p.seasons[ent.Season] = true
				w.seasons = append(w.seasons, ent.Season)
			}
			w.pending = append(w.pending, ent.ID)
		}
	}
	for n := 0; n < rbHomePrepareLeafWindow && w.leafAt < len(w.pending); n++ {
		id := w.pending[w.leafAt]
		w.leafAt++
		if p.hydrated[id] {
			continue
		}
		ent := p.entities[id]
		if ent == nil {
			return ErrStale
		}
		if err := p.hydratePersonal(s, query, ent, fence); err != nil {
			return err
		}
		if err := p.hydrateBinding(s, query, ent, fence); err != nil {
			return err
		}
		if p.pending != "" {
			return nil
		}
		p.hydrated[id] = true
	}
	if w.leafAt >= len(w.pending) {
		w.stage = 2
	}
	return nil
}

func (p *RBHomePreparePreparation) readSeasons(s *savedViewSession, query browseQuery, w *rbHomePrepareWork, fence browseFence) error {
	for w.seasonAt < len(w.seasons) {
		id := w.seasons[w.seasonAt]
		w.seasonAt++
		if _, ok := p.entities[id]; ok {
			continue
		}
		ent, err := p.hydrateEntity(s, query, id, w.library, fence, false)
		if err != nil {
			return err
		}
		if ent == nil {
			continue
		}
		if err := p.keepEntity(id, ent); err != nil {
			return err
		}
		return nil
	}
	w.stage = 3
	return nil
}

func rbHomePrepareChildSourceForKind(kind int64) rbHomePrepareChildSource {
	for _, source := range rbHomePrepareChildSources() {
		if source.name == "show" && kind == 2 || source.name == "album" && kind == 6 || source.name == "book" && kind == 8 {
			return source
		}
	}
	return rbHomePrepareChildSource{}
}

// hydrateEntity reads one complete entity row and, when asked, its current
// personal state. It returns nil for a missing row so callers can preserve the
// merged composition's "not a member" behaviour.
func (p *RBHomePreparePreparation) hydrateEntity(s *savedViewSession, query browseQuery, id, library int64, fence browseFence, personal bool) (*rbHomePrepareEntity, error) {
	rows, err := query("rb.home.fixed.entity", id, library)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if len(rows) != 1 {
		return nil, ErrUnqualified
	}
	ent, err := rbHomePrepareEntityFrom(rows[0])
	if err != nil {
		return nil, err
	}
	ent.Library = library
	if err = p.hydrateExternal(query, ent); err != nil {
		return nil, err
	}
	if personal {
		if err = p.hydratePersonal(s, query, ent, fence); err != nil {
			return nil, err
		}
	}
	if err = p.hydrateBinding(s, query, ent, fence); err != nil {
		return nil, err
	}
	return ent, nil
}

// hydrateExternal preserves the provider-edition dependency: an actual provider
// identity is pending, never two editions asserted as two works.
func (p *RBHomePreparePreparation) hydrateExternal(query browseQuery, ent *rbHomePrepareEntity) error {
	if ent.Kind != 1 {
		return nil
	}
	rows, err := query("rb.home.fixed.external", ent.ID)
	if err != nil {
		return err
	}
	if len(rows) != 0 {
		p.pending = "provider_edition_grouping_preparation"
	}
	return nil
}

func (p *RBHomePreparePreparation) hydratePersonal(s *savedViewSession, query browseQuery, ent *rbHomePrepareEntity, fence browseFence) error {
	profile := p.entry.scope.Principal.profile
	sizes, err := query("rb.home.fixed.personal.bytes", profile, ent.ID)
	if err != nil {
		return err
	}
	var total int64
	for _, row := range sizes {
		for _, v := range row {
			if v == nil {
				continue
			}
			n, ok := v.(int64)
			if !ok || n < 0 || n > s.nativeLimit-total {
				return governor.ErrSnapshotBudget
			}
			total += n
		}
	}
	if 1024+3*total > s.copyLimit-s.copied {
		return governor.ErrSnapshotBudget
	}
	rows, err := query("rb.home.fixed.personal", profile, ent.ID)
	if err != nil {
		return err
	}
	if len(rows) > 1 {
		return ErrUnqualified
	}
	if len(rows) != len(sizes) {
		return ErrStale
	}
	for n, row := range rows {
		for k, pos := range []int{11, 16, 17, 21, 14, 22} {
			if !homeFixedByteMatches(sizes[n][k], row[pos]) {
				return ErrStale
			}
		}
	}
	if len(rows) == 0 {
		return nil
	}
	personal, err := rbHomePreparePersonalFrom(rows[0])
	if err != nil {
		return err
	}
	if personal.ItemPublic != ent.Public {
		return ErrUnqualified
	}
	ent.Personal = personal
	if personal.HasActivityAt && !personal.HasActivityStateLength {
		p.pending = "activity_state_unresolved"
	}
	if personal.HasActivityAt && len(personal.ActivityAt) > 64 {
		p.pending = "invalid_activity_domain"
	}
	if len(personal.ContainerWatermark) > 64 || len(personal.ContainerClearedThrough) > 64 {
		p.pending = "invalid_activity_domain"
	}
	_ = fence
	return nil
}

func (p *RBHomePreparePreparation) hydrateBinding(s *savedViewSession, query browseQuery, ent *rbHomePrepareEntity, fence browseFence) error {
	if !rbHomePrepareAdmitted(ent.Retired, rbHomePrepareMaybeInt(ent.AdmissionAge, ent.HasAdmissionAge), fence) {
		return nil
	}
	switch ent.Kind {
	case 1, 4, 7, 9:
	default:
		return nil
	}
	var trace [][][]any
	tr := func(name string, args ...any) ([][]any, error) {
		rows, err := homeFixedBindingRows(s, query, name, args...)
		trace = append(trace, rows)
		return rows, err
	}
	binding, err := readHomeWholePlayableBinding(tr, ent.Library, ent.ID)
	p.rawBindings += binding.Progress.ItemRows + binding.Progress.LinkRows + binding.Progress.ObjectRows
	if err == nil {
		ent.Binding, ent.HasBinding, ent.Available = binding, true, true
		return nil
	}
	if errors.Is(err, errHomeBindingUnresolved) {
		unavailable := len(trace) >= 2 && len(trace[1]) == 0
		if len(trace) >= 2 && len(trace[1]) == 1 {
			link := trace[1][0]
			unavailable = link[4] == nil && link[5] == int64(0) && link[15] == ent.Library && (link[6] == int64(0) || link[16] == int64(0))
		}
		if !unavailable {
			p.pending = "whole_playable_binding_unresolved"
		}
		return nil
	}
	if errors.Is(err, errHomeBindingBudget) {
		p.pending = "whole_playable_binding_budget"
		return nil
	}
	return err
}

// compose builds the merged fixed frame from the typed values and runs the
// unchanged composition, then retains the values-only page under the lease.
func (p *RBHomePreparePreparation) compose(s *savedViewSession) error {
	frame := homeFixedFrame{RawPositions: p.rawPositions, RawBindings: p.rawBindings, Pending: p.pending}
	for _, lib := range p.entry.scope.Libraries {
		one := homeFixedLibrary{Library: HomeManifestLibrary{ID: lib.ID, NativeID: lib.NativeID, Kind: lib.Kind, CatalogueState: lib.CatalogueState, Position: lib.Position}, Fence: lib.Fence}
		if lib.OwnerPolicy {
			one.OwnerPolicy = [][]any{{int64(1)}}
		}
		for _, ent := range p.entities {
			if ent.Library == lib.NativeID {
				one.Entities = append(one.Entities, ent.fixed())
			}
		}
		slices.SortFunc(one.Entities, func(a, b homeFixedEntity) int { return compareInt64(a.Identity.ID, b.Identity.ID) })
		frame.Libraries = append(frame.Libraries, one)
	}
	at := s.clock.Add(time.Since(p.started))
	page, err := composeHomeFixed(frame, p.row, at)
	if err != nil {
		return err
	}
	if err = p.entry.retainPage(page); err != nil {
		if !errors.Is(err, errRBHomePrepareCapacity) {
			return err
		}
		page = HomeFixedPage{Pending: true, PendingReason: "home_prepare_capacity", RawPositions: frame.RawPositions, RawBindings: frame.RawBindings}
		if err = p.entry.retainPage(page); err != nil {
			return err
		}
	}
	if err = p.entry.finish(p.reader); err != nil {
		return err
	}
	p.entry.complete = true
	return nil
}

// Row hands off the prepared shelf inside a fresh authorized response snapshot.
// The exact whole scope must match before any card is copied, and the first
// delivery linearizes with the governor exactly like every other prepared
// purpose. A later call with a drifted scope is denied, never replayed.
func (p *RBHomePreparePreparation) Row(ctx context.Context, request governor.Request, budget time.Duration, output BrowseOutput, current BrowseInput) (RBHomePreparePage, BrowseReadResult, error) {
	if p == nil || p.reader == nil || p.entry == nil || current.After != nil || current.Descending || current.KeepArticles {
		return RBHomePreparePage{}, BrowseReadResult{}, ErrInput
	}
	return liveRead(ctx, request, budget, output, p.reader.fixed.native.pool, current.Now, func(ctx context.Context, request governor.Request, budget time.Duration, at time.Time) (RBHomePreparePage, BrowseReadResult, error) {
		current.Now = at
		s, err := newSavedViewSession(p.reader.fixed.native, ctx, request, budget, output, current)
		if err != nil {
			return RBHomePreparePage{}, BrowseReadResult{}, err
		}
		if err = s.reserve(384 << 10); err != nil {
			return RBHomePreparePage{}, s.result(), err
		}
		var principal publicationPrincipalPin
		for attempt := 0; attempt < 2; attempt++ {
			s.cost.Attempts = attempt + 1
			check := func(q browseQuery) error {
				return p.revalidate(s, q, current, &principal)
			}
			final := func(q browseQuery) error {
				if e := p.revalidate(s, q, current, &principal); e != nil {
					return e
				}
				if !p.entry.delivered {
					if e := p.reader.a.HandoffPreparation(p.entry.lease); e != nil {
						return e
					}
					p.entry.delivered = true
				}
				return nil
			}
			err = s.window(false, rbHomePrepareMaxQueries, check)
			if err == nil {
				e := s.window(true, rbHomePrepareMaxQueries, final)
				err = s.finish(p.entry.scope.Principal, nil, e)
				if err == nil {
					return p.page(s), s.result(), nil
				}
			}
			if !errors.Is(err, ErrStale) || ctx.Err() != nil || !time.Now().Before(s.deadline) {
				break
			}
		}
		if errors.Is(err, ErrStale) {
			s.cost.Refusal = &governor.Refusal{Code: governor.Conflict}
			err = errors.Join(err, governor.ErrBusy)
		}
		if errors.Is(err, ErrStale) || errors.Is(err, ErrDenied) || errors.Is(err, ErrUnqualified) || errors.Is(err, governor.ErrSnapshotBudget) {
			p.clear()
			_ = p.entry.close(p.reader)
		}
		return RBHomePreparePage{}, s.result(), err
	})
}

func (p *RBHomePreparePreparation) revalidate(s *savedViewSession, query browseQuery, current BrowseInput, principal *publicationPrincipalPin) error {
	if current.Database != p.view.Database || current.TokenHash != p.view.TokenHash {
		return ErrDenied
	}
	scope, frame, err := p.readScope(s, query)
	if err != nil {
		return err
	}
	if !rbHomePrepareScopeExactEqual(scope, p.entry.scope) {
		return ErrStale
	}
	if scope.Principal != p.entry.scope.Principal {
		return ErrStale
	}
	if p.entry.pending != "" && frame.Manifest.Pending {
		// A pending manifest projects no rows to test. The retained page is a
		// named pending/refusal, not a composed shelf, and the exact principal and
		// scope were compared above.
		return nil
	}
	doc := homeManifestSourceProject(frame, s.input(current).Now).Manifest
	for _, row := range doc.Rows {
		if row.ID == p.row {
			return nil
		}
	}
	return ErrDenied
}

func (p *RBHomePreparePreparation) page(s *savedViewSession) RBHomePreparePage {
	page := RBHomePreparePage{RawPositions: p.entry.rawPositions, RawBindings: p.entry.rawBindings, Statements: s.cost.PublicationStatements}
	if p.entry.pending != "" {
		page.Pending = true
		page.PendingReason = p.entry.pending
		return page
	}
	for _, card := range p.entry.cards {
		card.Added = strings.Clone(card.Added)
		card.ActivityAt = strings.Clone(card.ActivityAt)
		page.Items = append(page.Items, card)
	}
	page.Exact, page.Exhausted = true, true
	return page
}

func (p *RBHomePreparePreparation) Close() error {
	if p == nil || p.entry == nil {
		return ErrInput
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clear()
	return p.entry.close(p.reader)
}

func (p *RBHomePreparePreparation) check() error {
	if p == nil || p.entry == nil || p.entry.closed {
		return ErrInput
	}
	if err := p.entry.ctx.Err(); err != nil {
		p.clear()
		_ = p.entry.close(p.reader)
		return err
	}
	if !time.Now().Before(p.entry.lease.Deadline()) {
		p.clear()
		_ = p.entry.close(p.reader)
		return governor.ErrPreparationExpired
	}
	if !p.entry.delivered {
		if err := p.reader.a.CheckPreparation(p.entry.lease); err != nil {
			p.clear()
			_ = p.entry.close(p.reader)
			return err
		}
	}
	return nil
}

// clear drops every typed value the job retained. It runs before physical
// retirement so the lease's exit attestation is not made while buffers remain.
func (p *RBHomePreparePreparation) clear() {
	p.streams = nil
	p.candidates = nil
	p.candidateList = nil
	p.considered = nil
	p.ordered = nil
	p.cut = nil
	p.active = nil
	p.works = nil
	p.completion = nil
	p.entities = nil
	p.hydrated = nil
	p.childrenOf = nil
	p.seasons = nil
}

func (p *RBHomePreparePreparation) reset() error {
	p.phase = 0
	p.streamsBuilt = false
	p.streams = nil
	p.candidates = map[int64]*rbHomePrepareCandidate{}
	p.candidateList = nil
	p.ordered = nil
	p.orderedDirty = false
	p.cut = nil
	p.resumeStreams = false
	p.active = map[int64]int64{}
	p.personalAfter = 0
	p.personalDone = p.row == "new_on_server"
	p.considered = map[int64]bool{}
	p.works = nil
	p.workAt = 0
	p.completion = map[int64]*rbHomePrepareWork{}
	p.entities = map[int64]*rbHomePrepareEntity{}
	p.seasons = map[int64]bool{}
	p.childrenOf = map[int64][]int64{}
	p.hydrated = map[int64]bool{}
	p.pending = ""
	p.rawRows, p.rawPositions, p.rawBindings = 0, 0, 0
	// Values from the discarded content attempt are not retained: the lease and
	// its payload ceiling survive, but the charged entity values do not.
	p.entry.cards = nil
	p.entry.retained = 0
	return p.check()
}

// adoptScope keeps the pinned authorization context but takes the fresh
// content revisions observed by a restart, so the retried attempt compares
// against the source it is actually reading.
func (e *rbHomePrepareEntry) adoptScope(scope rbHomePrepareScope) error {
	if e == nil || !e.captured || e.delivered || e.closed {
		return ErrInput
	}
	stringsBytes := rbPreparedFenceBytes(scope.Principal)
	scope.Principal = rbHomePrepareCloneFence(scope.Principal)
	for i := range scope.Libraries {
		stringsBytes += rbPreparedFenceBytes(scope.Libraries[i].Fence)
		scope.Libraries[i].Fence = rbHomePrepareCloneFence(scope.Libraries[i].Fence)
	}
	budget := e.lease.PayloadBytes() - 2*stringsBytes - 8192
	if budget <= 0 {
		return errRBHomePrepareCapacity
	}
	e.scope, e.payload = scope, budget
	return nil
}

func (e *rbHomePrepareEntry) capture(scope rbHomePrepareScope) error {
	if e == nil || e.captured {
		return ErrInput
	}
	stringsBytes := rbPreparedFenceBytes(scope.Principal)
	scope.Principal = rbHomePrepareCloneFence(scope.Principal)
	for i := range scope.Libraries {
		stringsBytes += rbPreparedFenceBytes(scope.Libraries[i].Fence)
		scope.Libraries[i].Fence = rbHomePrepareCloneFence(scope.Libraries[i].Fence)
	}
	budget := e.lease.PayloadBytes() - 2*stringsBytes - 8192
	if budget <= 0 {
		return errRBHomePrepareCapacity
	}
	e.scope, e.payload, e.captured = scope, budget, true
	return nil
}

func (e *rbHomePrepareEntry) retainValue(size int64) error {
	if e == nil || !e.captured || e.closed {
		return ErrInput
	}
	if size < 0 || size > e.payload-rbHomePreparePendingReserve-e.retained {
		return errRBHomePrepareCapacity
	}
	e.retained += size
	return nil
}

func (e *rbHomePrepareEntry) retainPage(page HomeFixedPage) error {
	if e == nil || !e.captured || e.complete || e.closed {
		return ErrInput
	}
	if page.Pending {
		if int64(len(page.PendingReason)) > e.payload-e.retained {
			return errRBHomePrepareCapacity
		}
		e.pending = strings.Clone(page.PendingReason)
		e.retained += int64(len(page.PendingReason))
		e.rawPositions, e.rawBindings = page.RawPositions, page.RawBindings
		return nil
	}
	var size int64
	for _, card := range page.Items {
		size += 192 + 2*int64(len(card.Added)) + 2*int64(len(card.ActivityAt))
	}
	if size > e.payload-rbHomePreparePendingReserve-e.retained {
		return errRBHomePrepareCapacity
	}
	e.retained += size
	for _, card := range page.Items {
		card.Added = strings.Clone(card.Added)
		card.ActivityAt = strings.Clone(card.ActivityAt)
		e.cards = append(e.cards, card)
	}
	e.rawPositions, e.rawBindings = page.RawPositions, page.RawBindings
	return nil
}

func (e *rbHomePrepareEntry) finish(r *RBHomePrepareReader) error {
	if e == nil || e.closed || e.complete || !e.captured {
		return ErrInput
	}
	if err := r.a.ReadyPreparation(e.lease); err != nil {
		_ = e.close(r)
		return err
	}
	e.complete = true
	return nil
}

func (e *rbHomePrepareEntry) close(r *RBHomePrepareReader) error {
	if e == nil {
		return ErrInput
	}
	if e.closed {
		return nil
	}
	e.cards = nil
	e.scope = rbHomePrepareScope{}
	e.pending = ""
	e.closed = true
	if err := r.a.CancelPreparation(e.lease); err != nil {
		return err
	}
	if err := r.a.RetirePreparation(e.lease); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, job := range r.jobs {
		if job != nil && job.entry == e {
			r.jobs[i] = nil
			break
		}
	}
	return nil
}

// rbHomePrepareScopeEqual proves a content restart observed the same identity,
// security, policy and scope context. Only catalogue content revisions may
// advance.
func rbHomePrepareScopeEqual(pinned, fresh rbHomePrepareScope) bool {
	if !rbPreparePinnedFenceEqual(pinned.Principal, fresh.Principal) {
		return false
	}
	if len(pinned.Libraries) != len(fresh.Libraries) {
		return false
	}
	for i := range pinned.Libraries {
		a, b := pinned.Libraries[i], fresh.Libraries[i]
		if a.ID != b.ID || a.NativeID != b.NativeID || a.Kind != b.Kind || a.CatalogueState != b.CatalogueState || a.Position != b.Position || a.OwnerPolicy != b.OwnerPolicy {
			return false
		}
		// A page already reduced from an older content revision cannot be
		// completed against a newer one; the fence comparison here is exact,
		// while ScopeAuthEqual below decides whether the difference is a
		// restartable content move or a denial.
		if a.Fence != b.Fence {
			return false
		}
	}
	return true
}

// rbHomePrepareScopeAuthEqual proves only the authorization context matched, so
// a scope change can be classified as content invalidatation for a restart
// rather than a denial.
func rbHomePrepareScopeAuthEqual(pinned, fresh rbHomePrepareScope) bool {
	if !rbPreparePinnedFenceEqual(pinned.Principal, fresh.Principal) {
		return false
	}
	if len(pinned.Libraries) != len(fresh.Libraries) {
		return false
	}
	for i := range pinned.Libraries {
		a, b := pinned.Libraries[i], fresh.Libraries[i]
		if a.ID != b.ID || a.NativeID != b.NativeID || a.Kind != b.Kind || a.CatalogueState != b.CatalogueState || a.Position != b.Position {
			return false
		}
		if !rbPreparePinnedFenceEqual(a.Fence, b.Fence) {
			return false
		}
	}
	return true
}

// rbHomePrepareScopeExactEqual is the handoff check: any drift invalidates the
// retained values.
func rbHomePrepareScopeExactEqual(pinned, fresh rbHomePrepareScope) bool {
	if pinned.Principal != fresh.Principal {
		return false
	}
	if len(pinned.Libraries) != len(fresh.Libraries) {
		return false
	}
	for i := range pinned.Libraries {
		a, b := pinned.Libraries[i], fresh.Libraries[i]
		if a.ID != b.ID || a.NativeID != b.NativeID || a.Kind != b.Kind || a.CatalogueState != b.CatalogueState || a.Position != b.Position || a.OwnerPolicy != b.OwnerPolicy {
			return false
		}
		if a.DataRevision != b.DataRevision || a.OrderRevision != b.OrderRevision || a.MetadataRevision != b.MetadataRevision || a.ViewerRevision != b.ViewerRevision {
			return false
		}
		if a.Fence != b.Fence {
			return false
		}
	}
	return true
}

func rbHomePrepareCloneFence(f browseFence) browseFence {
	for _, field := range []*string{&f.database, &f.token, &f.account, &f.profile, &f.membership, &f.lifetime, &f.library, &f.role, &f.memberLibraries, &f.profileLibraries, &f.profileLabels, &f.expiresAt, &f.horizon, &f.root, &f.bridgeRoot, &f.policyWitness} {
		*field = strings.Clone(*field)
	}
	return f
}
