package playbackv1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// Scanning a selection in the request is bounded (P16): past scanBudgetKeys
// keys or scanBudgetTime the command answers with what it has and the rest of
// the snapshot is built in the background.
const (
	scanBudgetKeys = 50_000
	scanBudgetTime = 2 * time.Second
	// countBudget bounds the fenced count a large shuffle needs up front.
	countBudget = 5 * time.Second
	// buildLifetime bounds one background build's read snapshot.
	buildLifetime = 30 * time.Minute
)

type scanBudget struct {
	keys  int64
	until time.Time
}

func (b scanBudget) spent(keys int64) bool {
	return b.keys > 0 && keys >= b.keys || !b.until.IsZero() && time.Now().After(b.until)
}

// ErrQueueBuilding is an entry of a snapshot still being built: retry shortly.
var ErrQueueBuilding = errors.New("queue building")

// queueStart is a start that waits for a building snapshot: an item anchor the
// scan hadn't reached, or a shuffle whose size wasn't known in time.
type queueStart struct {
	Mode       string `json:"mode"` // "ordered" | "shuffle"
	AnchorItem string `json:"anchorItem,omitempty"`
	// AnchorAt is a playlist occurrence's source position the scan hadn't reached.
	AnchorAt *int64 `json:"anchorAt,omitempty"`
	Seed     int64  `json:"seed,omitempty"`
	// Base is the source position of this snapshot's first entry when the
	// queue was created (segments before it).
	Base int64 `json:"base"`
}

// buildCaller is the viewer a background build filters for.
type buildCaller struct {
	Viewer identity.Viewer `json:"viewer"`
	Epoch  int             `json:"epoch"`
}

// selectorKeys is a selector's key query in canonical order, after the
// caller's container checks: kind is "items" or "selector"; specials says a
// show's specials are in.
type selectorKeys struct {
	iterator   catalog.QueueKeyIterator
	checkBasis func(context.Context, *sql.Tx) error
	query      string
	args       []any
	kind       string
	specials   bool
	revision   string
	label      string
}

// keysQuery checks the caller may use the selector's container and returns
// its key query (see openKeys).
func (s *Service) keysQuery(ctx context.Context, tx *sql.Tx, p identity.Principal, in SegmentInput) (selectorKeys, error) {
	switch {
	case in.Source.Journey != nil:
		return s.journeyKeysQuery(ctx, tx, p, *in.Source.Journey)
	case in.Source.Container != nil && in.Source.Container.Kind == "mix":
		if s.MixKeys == nil {
			return selectorKeys{}, ErrUnsupportedSelector
		}
		ids, err := s.MixKeys(ctx, tx, p, in.Source.Container.ID)
		if err != nil {
			return selectorKeys{}, err
		}
		if !validRadioBatch(ids, catalog.MaxPlaylistEntries) {
			return selectorKeys{}, ErrNotFound
		}
		return selectorKeys{query: `SELECT value FROM json_each(?) ORDER BY CAST(key AS INTEGER)`, args: []any{jsonList(ids)}, kind: "selector", revision: in.Source.Container.ID}, nil
	case isRadioSource(in.Source):
		if s.RadioKeys == nil {
			return selectorKeys{}, ErrUnsupportedSelector
		}
		if in.Order != nil && in.Order.Mode != "source" {
			return selectorKeys{}, &FieldError{Path: "order.mode"}
		}
		batch, err := s.RadioKeys(ctx, tx, p, catalog.RadioBatchRequest{Seed: in.Source.Container.ID, Start: true, Limit: radioBatchSize, Now: s.now()})
		if err != nil {
			return selectorKeys{}, err
		}
		if !validRadioBatch(batch.IDs, radioBatchSize) {
			return selectorKeys{}, ErrNotFound
		}
		var label string
		if err = tx.QueryRowContext(ctx, `SELECT title FROM catalog_entities WHERE public_id=pid_blob(?) AND retired=0`, in.Source.Container.ID).Scan(&label); err != nil {
			return selectorKeys{}, err
		}
		return selectorKeys{query: `SELECT value FROM json_each(?) ORDER BY CAST(key AS INTEGER)`, args: []any{jsonList(batch.IDs)}, kind: "radio", label: label}, nil
	case in.Source.isQuery():
		if s.QueryKeys == nil {
			return selectorKeys{}, ErrUnsupportedSelector
		}
		selection, err := s.QueryKeys(ctx, tx, p, in.Source)
		if err != nil {
			return selectorKeys{}, err
		}
		if selection.Iterator != nil {
			check := selection.CheckBasis
			selection.CheckBasis = func(ctx context.Context, tx *sql.Tx) error {
				if check != nil {
					if err := check(ctx, tx); err != nil {
						return err
					}
				}
				return selection.Iterator.CheckBasis(ctx, tx)
			}
		}
		return selectorKeys{iterator: selection.Iterator, query: selection.SQL, args: selection.Args, kind: "selector", revision: selection.Revision, label: selection.Label, checkBasis: selection.CheckBasis}, nil
	case in.Source.Items != nil:
		// Keep the caller's order; unknown and inaccessible ids are dropped alike.
		// catalog_entities is facts, so no readiness wait.
		return selectorKeys{query: `WITH wanted(id,ord) AS (SELECT value,key FROM json_each(?)) SELECT w.id FROM wanted w JOIN catalog_entities i ON i.public_id=pid_blob(w.id) JOIN catalog_kinds k ON k.id=i.kind AND k.playable=1 ORDER BY w.ord`, args: []any{jsonList(in.Source.Items.IDs)}, kind: "items"}, nil
	}
	c := in.Source.Container
	spec, ok := containers[c.Kind]
	if !ok {
		return selectorKeys{}, ErrUnsupportedSelector
	}
	if err := compactcatalog.CheckReadiness(ctx, tx, containerDomains(c.Kind)...); err != nil {
		return selectorKeys{}, err
	}
	args := []any{c.ID}
	if spec.args != nil {
		var err error
		if args, err = spec.args(c.ID); err != nil {
			return selectorKeys{}, err
		}
	}
	if c.Kind == "playlist" {
		var authority, account, profile string
		if err := tx.QueryRowContext(ctx, `SELECT owner_authority,owner_account,owner_profile FROM catalog_playlists WHERE token=? AND deleted=0`, c.ID).Scan(&authority, &account, &profile); err != nil {
			return selectorKeys{}, ErrNotFound
		}
		if authority != p.Authority || account != p.AccountID || profile != p.ProfileID {
			return selectorKeys{}, ErrNotFound
		}
	} else if spec.library != "" {
		var library string
		if err := tx.QueryRowContext(ctx, spec.library, args[0]).Scan(&library); err != nil {
			return selectorKeys{}, ErrNotFound
		}
		if s.LibraryCheck != nil {
			if err := s.LibraryCheck(ctx, tx, p, library); err != nil {
				return selectorKeys{}, ErrNotFound
			}
		}
	}
	specials := false
	if c.Kind == "show" {
		if in.Anchor != nil && in.Anchor.ItemID != "" {
			var season int
			_ = tx.QueryRowContext(ctx, `SELECT COALESCE(s.number,1) FROM catalog_entities item JOIN catalog_episodes e ON e.entity_id=item.id LEFT JOIN catalog_seasons s ON s.entity_id=e.season_id WHERE item.public_id=pid_blob(?)`, in.Anchor.ItemID).Scan(&season)
			specials = season == 0
		}
		args = append(args, specials)
	}
	return selectorKeys{query: spec.keys, args: args, kind: "selector", specials: specials}, nil
}

// snapshotScan turns a key query into chunks. Every key passes the caller's
// item fence (library access, restrictions, member limits) one chunk per
// statement: an inaccessible item is absent, counts nowhere, and never reaches
// a window (SEC-02, P11/P12).
type snapshotScan struct {
	// A finite Journey must fail before staging if any captured key is refused.
	exact bool
	s     *Service
	ctx   context.Context
	snap  *snapshot
	queue string
	limit int64
	// keep: a snapshot that fits in one batch stays in memory for the queue
	// command's own write; otherwise every batch is written as it fills.
	keep   bool
	budget scanBudget
	// stage runs in the first batch write, before the snapshot row.
	stage func(context.Context, *sql.Tx) error
	// onBatch runs in every batch write after its chunks (the background builder).
	onBatch func(context.Context, *sql.Tx) error
	skip    int64 // source rows a resumed build has already accounted for
	chunkNo int64
	// accounted is the source rows fully accounted for at the last key taken:
	// a resumed scan starts there.
	accounted int64
	// anchorAt is the anchor's position in source order (before filtering); the
	// segment starts at the first playable key at or after it.
	anchorAt   int64
	anchorItem string
	anchor     int64 // the anchor's ordinal once found (-1 until then)
}

func (sc *snapshotScan) writeBatch() error {
	snap := sc.snap
	err := dbwork.WithWriteTxContext(sc.ctx, sc.s.DB, dbwork.ClassBackgroundMedia, func(ctx context.Context, w *sql.Tx) error {
		if snap.checkBasis != nil {
			if err := snap.checkBasis(ctx, w); err != nil {
				return err
			}
		}
		if !snap.stored {
			if sc.stage != nil {
				if err := sc.stage(ctx, w); err != nil {
					return err
				}
			}
			if _, err := w.ExecContext(ctx, `INSERT INTO queue_v1_snapshots(id,queue_id,kind,selector,label,count,width,music_only) VALUES(?,?,?,?,'',0,0,1)`, snap.id, sc.queue, snap.kind, snap.selector); err != nil {
				return err
			}
		}
		for _, c := range snap.pending {
			if err := writeQueueChunkTx(ctx, w, snap.id, c.no, c.keys, false); err != nil {
				return err
			}
		}
		if sc.onBatch != nil {
			return sc.onBatch(ctx, w)
		}
		return nil
	})
	if err == nil {
		snap.stored, snap.pending = true, snap.pending[:0]
	}
	return err
}

// scan reads keys to the end, or to the budget (stopping at a chunk boundary
// with snap.partial set and snap.seen at the first unaccounted source row).
func (sc *snapshotScan) scan(tx *sql.Tx, keys *sql.Rows, p identity.Principal) error {
	snap := sc.snap
	visible, err := sc.s.visibleFilter(sc.ctx, tx, p)
	if err != nil {
		return err
	}
	row := int64(0) // absolute source position
	// pending's source positions: a playlist may hold an item more than once,
	// so a position belongs to an occurrence, not to an id.
	positions := make([]int64, 0, chunkKeys)
	pending := make([]string, 0, chunkKeys)
	chunk := make([]string, 0, chunkKeys)
	stopped := false
	flush := func() error {
		if len(chunk) == 0 {
			return nil
		}
		snap.pending = append(snap.pending, chunkRow{no: sc.chunkNo, keys: encodeChunk(chunk)})
		sc.chunkNo++
		chunk = chunk[:0]
		// Kept for the command's own write while it fits in one batch; past that,
		// and always for the background builder, written a batch at a time.
		if sc.keep && len(snap.pending) > snapshotBatchChunks || !sc.keep && len(snap.pending) >= snapshotBatchChunks {
			return sc.writeBatch()
		}
		return nil
	}
	admit := func() error {
		if snap.checkBasis != nil {
			if err := dbwork.WithReadSnapshot(sc.ctx, sc.s.DB, func(ctx context.Context) error { return snap.checkBasis(ctx, dbwork.Snapshot(ctx)) }); err != nil {
				return err
			}
		}
		allowed, err := visible(pending)
		if err != nil {
			return err
		}
		if sc.exact && len(allowed) != len(pending) {
			return ErrUnsupportedSelector
		}
		// allowed is pending in order with the hidden keys left out: each kept
		// key is the next pending occurrence of that id.
		next := 0
		for _, id := range allowed {
			for next < len(pending) && pending[next] != id {
				next++
			}
			if next == len(pending) {
				return errors.New("queue snapshot: visibility filter changed the key order")
			}
			at := positions[next]
			next++
			if snap.count >= min(sc.limit, MaxSnapshotKeys) {
				return ErrQueueTooLarge
			}
			if sc.anchor < 0 && sc.anchorAt >= 0 && at >= sc.anchorAt {
				sc.anchor = snap.count
			}
			chunk = append(chunk, id)
			snap.count++
			sc.accounted = at + 1
			if len(chunk) == chunkKeys {
				if err = flush(); err != nil {
					return err
				}
				if sc.budget.spent(snap.count) {
					// Stop at this chunk boundary: every source row up to this key is
					// accounted for; the builder continues after it.
					snap.partial, snap.seen, stopped = true, sc.accounted, true
					return nil
				}
			}
		}
		pending, positions = pending[:0], positions[:0]
		return nil
	}
	for keys.Next() {
		var id string
		if err = keys.Scan(&id); err != nil {
			return err
		}
		position := row
		row++
		if position < sc.skip {
			continue // a resumed build: this row was accounted for before
		}
		if sc.anchorItem != "" && id == sc.anchorItem && sc.anchorAt < 0 {
			sc.anchorAt = position
		}
		positions = append(positions, position)
		if pending = append(pending, id); len(pending) == chunkKeys {
			if err = admit(); err != nil {
				return err
			}
			if stopped {
				return nil
			}
		}
	}
	if err = keys.Err(); err != nil {
		return err
	}
	if err = admit(); err != nil || stopped {
		return err
	}
	snap.seen = max(row, sc.skip)
	return flush()
}

// buildSnapshot scans one selector in canonical order into chunks and finds
// the anchor's ordinal in the same pass (one scan; no per-page re-query), in a
// read snapshot. limit bounds its keys (ErrQueueTooLarge beyond). stage runs in
// the first background batch's write, before the snapshot row: a new queue
// uses it to write its row, which the snapshot references. With a budget the
// scan may stop early (snap.partial); the command then commits it as building.
func (s *Service) buildSnapshot(ctx context.Context, p identity.Principal, queue string, in SegmentInput, path string, limit int64, stage func(context.Context, *sql.Tx) error, budget scanBudget) (snapshot, error) {
	if err := in.Source.validate(path + ".source"); err != nil {
		return snapshot{}, err
	}
	if in.Anchor != nil && in.Anchor.EntryID != "" && (in.Source.Container == nil || in.Source.Container.Kind != "playlist") {
		return snapshot{}, &FieldError{Path: path + ".anchor.entryId"}
	}
	raw, _ := json.Marshal(in.Source)
	snap := snapshot{id: newShortID("s"), selector: string(raw), anchor: -1}
	var tx *sql.Tx
	var done func()
	var err error
	retained := false
	var capture *queryCapture
	if in.Source.isQuery() {
		capture, err = s.openCapture(ctx)
		if err == nil {
			tx, done = capture.tx, capture.close
		}
		budget.keys = chunkKeys // answer as soon as the first playable batch exists
	} else {
		tx, done, err = dbwork.BeginRead(ctx, s.DB)
	}
	if err != nil {
		return snap, err
	}
	defer func() {
		if !retained {
			done()
		}
	}()
	k, err := s.keysQuery(ctx, tx, p, in)
	if err != nil {
		return snap, err
	}
	if k.iterator != nil {
		// The local iterator owns only short worker snapshots. In particular,
		// never retain the admission capture while waiting on rejected keys.
		done()
		return s.buildLocalOrderedSnapshot(ctx, p, queue, in, snap, k, limit, budget)
	}
	keys, err := tx.QueryContext(ctx, k.query, k.args...)
	if err != nil {
		return snap, err
	}
	defer keys.Close()
	kind, specials := k.kind, k.specials
	snap.kind, snap.sourceRevision, snap.label, snap.checkBasis = kind, k.revision, k.label, k.checkBasis
	sc := &snapshotScan{s: s, ctx: ctx, snap: &snap, queue: queue, limit: limit, keep: true, budget: budget, stage: stage, anchorAt: -1, anchor: -1, exact: in.Source.Journey != nil}
	if in.Anchor != nil {
		sc.anchorItem = in.Anchor.ItemID
		if in.Anchor.Resume && in.Source.Container != nil && in.Source.Container.Kind == "show" {
			sc.anchorAt = s.resumeIndex(ctx, tx, p, in.Source.Container.ID, specials)
		}
		if in.Anchor.EntryID != "" {
			// The pressed occurrence, found in this same read; one removed since
			// starts at the item (when named), as an item anchor does.
			if at, ok := occurrencePosition(ctx, tx, in.Source.Container.ID, in.Anchor.EntryID); ok {
				sc.anchorAt, sc.anchorItem = at, ""
			}
		}
	}
	if kind == "items" || in.Source.Journey != nil {
		sc.budget = scanBudget{} // at most 500 explicit keys or 128 Journey keys
	}
	if err = sc.scan(tx, keys, p); err != nil {
		return snap, err
	}
	if in.Source.Journey != nil && snap.count != int64(in.Source.Journey.Length) {
		return snap, ErrUnsupportedSelector
	}
	snap.anchor = sc.anchor
	if snap.partial && snap.anchor < 0 && sc.anchorItem != "" {
		snap.anchorItem = sc.anchorItem
	}
	if snap.partial && snap.anchor < 0 && sc.anchorItem == "" && sc.anchorAt >= 0 {
		at := sc.anchorAt
		snap.anchorAt = &at
	}
	if in.Anchor != nil && in.Anchor.Position != nil && *in.Anchor.Position >= 0 && *in.Anchor.Position < snap.count {
		snap.anchor = *in.Anchor.Position
	}
	// A label only for something the caller can play: a restricted show's or
	// collection's title never reaches a segment (P12).
	if snap.count > 0 && snap.label == "" {
		snap.label = s.snapshotLabel(ctx, tx, in, snap)
	}
	if capture != nil && snap.partial {
		capture.keys = k // includes the recommendation order and its composition day
		s.build.mu.Lock()
		if s.build.captures == nil {
			s.build.captures = make(map[string]*queryCapture)
		}
		s.build.captures[snap.id] = capture
		s.build.mu.Unlock()
		retained = true
	}
	return snap, nil
}

// occurrencePosition is a playlist entry's position in the playlist's key
// query (entry order, cleared entries left out), counted on the entry order
// index: the scan's anchorAt for that occurrence.
func occurrencePosition(ctx context.Context, tx *sql.Tx, playlist, entry string) (int64, bool) {
	var at int64
	err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM catalog_playlist_entries other INDEXED BY catalog_playlist_entries_page WHERE other.playlist_id=e.playlist_id AND (other.order_key,other.id)<(e.order_key,e.id) AND other.item_id IS NOT NULL) FROM catalog_playlists p JOIN catalog_playlist_entries e ON e.playlist_id=p.id WHERE p.token=? AND p.deleted=0 AND e.token=? AND e.item_id IS NOT NULL`, playlist, entry).Scan(&at)
	return at, err == nil
}

func (s *Service) snapshotLabel(ctx context.Context, tx *sql.Tx, in SegmentInput, snap snapshot) string {
	if in.Source.Journey != nil {
		return ""
	}
	if in.Source.isQuery() {
		return snap.label
	}
	if snap.kind == "items" {
		return fmt.Sprintf("%d items", snap.count)
	}
	c := in.Source.Container
	args := []any{c.ID}
	if spec := containers[c.Kind]; spec.args != nil {
		args, _ = spec.args(c.ID)
	}
	label := ""
	_ = tx.QueryRowContext(ctx, containers[c.Kind].label, args[0]).Scan(&label)
	if label == "" {
		label = c.Kind
	}
	return label
}

// countAndPick is what a large shuffle needs to start at once, in one fenced
// read-only pass over the selection: how many keys it has for this caller, and
// the first entry with its ordinal. That's the anchor item when there is one,
// else a uniform pick made by seeded reservoir sampling, so every device with
// the same seed and library picks the same one. The shuffle then orders the
// rest around it (as it does for an anchored shuffle). ok is false when the
// pass runs out of countBudget.
//
// The pass fences every key, seconds at 1M keys. For a caller the fence can't
// narrow (Unrestricted) the size is the container's own, so it is kept per
// catalog revision (queue_counts.go) and the next such shuffle skips the pass:
// the pick is the same one the pass makes (reservoirOrdinal), read with one
// unfenced ordered seek.
func (s *Service) countAndPick(ctx context.Context, p identity.Principal, in SegmentInput, seed int64, anchorItem string) (n, ordinal int64, key string, ok bool) {
	limit := countBudget
	if s.QueueCountBudget > 0 {
		limit = s.QueueCountBudget
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	tx, done, err := dbwork.BeginRead(ctx, s.DB)
	if err != nil {
		return 0, 0, "", false
	}
	n, ordinal, key, ok, store := s.countAndPickTx(ctx, tx, p, in, seed, anchorItem)
	done()
	if store != nil {
		s.storeContainerCount(ctx, *store)
	}
	return n, ordinal, key, ok
}

func (s *Service) countAndPickTx(ctx context.Context, tx *sql.Tx, p identity.Principal, in SegmentInput, seed int64, anchorItem string) (n, ordinal int64, key string, ok bool, store *containerCount) {
	k, err := s.keysQuery(ctx, tx, p, in)
	if err != nil {
		return 0, 0, "", false, nil
	}
	if k.iterator != nil {
		// Cold search never runs a foreground count for shuffle. The existing
		// growing snapshot installs its shuffle when the bounded replay finishes.
		k.iterator.Close()
		return 0, 0, "", false, nil
	}
	cached := s.containerCountFor(ctx, tx, p, in, k)
	if cached != nil && cached.keys > 0 {
		if n, ordinal, key, ok = cached.pick(ctx, tx, k, seed, anchorItem); ok {
			return n, ordinal, key, true, nil
		}
	}
	visible, err := s.visibleFilter(ctx, tx, p)
	if err != nil {
		return 0, 0, "", false, nil
	}
	keys, err := tx.QueryContext(ctx, k.query, k.args...)
	if err != nil {
		return 0, 0, "", false, nil
	}
	defer keys.Close()
	// A counting pass needs no chunk alignment: larger fence batches, fewer statements.
	const batch = 4096
	ordinal = -1
	raw := int64(0)
	pending := make([]string, 0, batch)
	check := func() error {
		allowed, err := visible(pending)
		pending = pending[:0]
		if err != nil {
			return err
		}
		for _, id := range allowed {
			switch {
			case anchorItem != "":
				if ordinal < 0 && id == anchorItem {
					ordinal, key = n, id
				}
			case reservoirPick(seed, n):
				ordinal, key = n, id
			}
			n++
		}
		return nil
	}
	for keys.Next() {
		var id string
		if err = keys.Scan(&id); err != nil {
			return 0, 0, "", false, nil
		}
		raw++
		if pending = append(pending, id); len(pending) == batch {
			if check() != nil {
				return 0, 0, "", false, nil
			}
		}
	}
	if keys.Err() != nil || check() != nil || n == 0 {
		return 0, 0, "", false, nil
	}
	// Kept only when the fence admitted every key: then the container's own
	// order is this caller's, and a later seek by ordinal lands on the same key.
	if cached != nil && raw == n {
		cached.keys = n
		store = cached
	}
	if ordinal < 0 {
		return 0, 0, "", false, store
	}
	return n, ordinal, key, true, store
}

// reservoirPick says whether the i-th key (from 0) replaces the pick so far:
// with probability 1/(i+1), from the seed alone.
func reservoirPick(seed, i int64) bool {
	x := uint64(seed)*0x9E3779B97F4A7C15 ^ uint64(i)
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return x%uint64(i+1) == 0
}

// builder finishes building snapshots in the background, one at a time, at
// background priority, resuming where a scan stopped (also after a restart).
type builder struct {
	mu       sync.Mutex
	running  bool
	again    bool
	resume   sync.Once
	captures map[string]*queryCapture
}

// resumeOnce: the first queue request after a start continues what a restart
// interrupted, in the background. An idle server does nothing (no statement
// at boot).
func (s *Service) resumeOnce() {
	s.build.resume.Do(func() { supervise.Go("playbackv1.queue-resume", s.ResumeQueueBuilds) })
}

// ResumeQueueBuilds continues any snapshot left building (e.g. by a restart),
// and removes what a crashed large create left staged. Called directly, it
// also stands for the lazy resume: the first queue request after it starts
// nothing more.
func (s *Service) ResumeQueueBuilds() {
	s.build.resume.Do(func() {})
	s.SweepQueues(context.Background())
	s.build.mu.Lock()
	s.PauseQueueBuilds = false
	s.build.mu.Unlock()
	s.build.kick(s)
}

// HoldQueueBuilds stops background builds and saves from taking another step
// until ResumeQueueBuilds (tests). The lazy resume after a start is spent, so
// the next queue request cannot release the hold behind the caller's back;
// a builder already running stops before its next snapshot or save step.
func (s *Service) HoldQueueBuilds() {
	s.build.resume.Do(func() {})
	s.build.mu.Lock()
	s.PauseQueueBuilds = true
	s.build.mu.Unlock()
}

// requestBudget is how much of a selection a request snapshots itself.
func (s *Service) requestBudget() scanBudget {
	keys := int64(scanBudgetKeys)
	if s.QueueScanBudget > 0 {
		keys = s.QueueScanBudget
	}
	return scanBudget{keys: keys, until: time.Now().Add(scanBudgetTime)}
}

func (b *builder) kick(s *Service) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.PauseQueueBuilds {
		return
	}
	if b.running {
		b.again = true
		return
	}
	b.running = true
	supervise.Go("playbackv1.queue-builder", func() { b.run(s) })
}

func (b *builder) run(s *Service) {
	for {
		// A hold (tests) stops the builder between steps; ResumeQueueBuilds kicks it again.
		b.mu.Lock()
		if s.PauseQueueBuilds {
			b.running, b.again = false, false
			b.mu.Unlock()
			return
		}
		b.mu.Unlock()
		var id string
		err := s.DB.QueryRow(`SELECT id FROM queue_v1_snapshots WHERE state='building' ORDER BY rowid LIMIT 1`).Scan(&id)
		if err == nil {
			err = s.continueBuild(context.Background(), id)
			if errors.Is(err, compactcatalog.ErrBuilding) {
				// A catalogue change is still publishing: the build resumes from
				// its seen row once it has, rather than ending the queue early.
				time.Sleep(time.Second)
				continue
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				// A queue replaced or deleted meanwhile took its snapshot with it.
				var exists bool
				if s.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM queue_v1_snapshots WHERE id=?)`, id).Scan(&exists) == nil && exists {
					log.Printf("Queue snapshot %s couldn't finish building: %v", id, err)
					s.failBuild(id)
				}
			}
			continue
		}
		// Queue saves (NEW-37) come after every building snapshot, a step at a
		// time, so a long copy never delays a queue's own build.
		if profile, key, ok := s.nextSave(); ok {
			// A hold must also stop a save step whose row appeared after this
			// iteration passed the check at the top: a test that pauses before
			// asking for a save must see the copy untouched until it resumes.
			b.mu.Lock()
			if s.PauseQueueBuilds {
				b.running, b.again = false, false
				b.mu.Unlock()
				return
			}
			b.mu.Unlock()
			progressed, err := s.continueSave(context.Background(), profile, key)
			switch {
			case err == nil && progressed:
				continue
			case err != nil && dbwork.Retryable(err):
				time.Sleep(time.Second) // a busy database: the same step again shortly
				continue
			case err != nil && !errors.Is(err, context.Canceled):
				// Anything else would fail the same way again: stop this copy
				// (what it saved stays) rather than leave it "saving" forever.
				log.Printf("A queue save couldn't continue: %v", err)
				if s.endSave(context.Background(), profile, key, "interrupted") == nil {
					continue
				}
			}
		}
		b.mu.Lock()
		if b.again {
			b.again = false
			b.mu.Unlock()
			continue
		}
		b.running = false
		b.mu.Unlock()
		return
	}
}

// continueBuild resumes one building snapshot from its seen row and finishes it.
func (s *Service) continueBuild(parent context.Context, id string) error {
	defer s.releaseCapture(id)
	var queue, kind, selector, caller, start, label, sourceRevision string
	var count, built, seen int64
	if err := s.DB.QueryRowContext(parent, `SELECT queue_id,kind,selector,caller,start,label,count,built,seen,source_revision FROM queue_v1_snapshots WHERE id=? AND state='building'`, id).Scan(&queue, &kind, &selector, &caller, &start, &label, &count, &built, &seen, &sourceRevision); err != nil {
		return err
	}
	var who buildCaller
	var in SegmentInput
	if json.Unmarshal([]byte(caller), &who) != nil || json.Unmarshal([]byte(selector), &in.Source) != nil {
		return ErrNotFound
	}
	var pendingStart *queueStart
	if start != "" {
		pendingStart = &queueStart{}
		if json.Unmarshal([]byte(start), pendingStart) != nil {
			return ErrNotFound
		}
		if pendingStart.AnchorItem != "" {
			in.Anchor = &Anchor{ItemID: pendingStart.AnchorItem}
		}
	}
	p := identity.Principal{Viewer: who.Viewer, Epoch: who.Epoch}
	ctx, cancel := context.WithTimeout(parent, buildLifetime)
	defer cancel()
	var tx *sql.Tx
	var done func()
	var err error
	capture := s.takeCapture(id)
	if capture != nil {
		tx, done = capture.tx, capture.close
	} else {
		tx, done, err = dbwork.BeginRead(ctx, s.DB)
	}
	if err != nil {
		return err
	}
	defer done()
	var k selectorKeys
	if capture != nil {
		k = capture.keys
	} else {
		k, err = s.keysQuery(ctx, tx, p, in)
	}
	if err != nil {
		return err
	}
	if in.Source.isQuery() && sourceRevision != k.revision {
		return errSelectionChanged
	}
	if k.iterator != nil {
		done()
		return s.continueLocalOrderedBuild(ctx, p, id, queue, kind, selector, label, built, count, pendingStart, k)
	}
	keys, err := tx.QueryContext(ctx, k.query, k.args...)
	if err != nil {
		return err
	}
	defer keys.Close()
	// A declared count (a shuffle that started at once) stays; otherwise the
	// segment grows with the build.
	declared := count > built
	snap := snapshot{id: id, kind: kind, selector: selector, count: built, anchor: -1, stored: true, checkBasis: k.checkBasis}
	sc := &snapshotScan{s: s, ctx: ctx, snap: &snap, queue: queue, limit: s.queueKeyLimit(), skip: seen, chunkNo: (built + chunkKeys - 1) / chunkKeys, anchorAt: -1, anchor: -1}
	if in.Anchor != nil {
		sc.anchorItem = in.Anchor.ItemID
	}
	if pendingStart != nil && pendingStart.AnchorAt != nil {
		sc.anchorAt = *pendingStart.AnchorAt
	}
	lastEvent := time.Now()
	sc.onBatch = func(ctx context.Context, w *sql.Tx) error {
		if _, err := w.ExecContext(ctx, `UPDATE queue_v1_snapshots SET built=?,seen=? WHERE id=?`, snap.count, sc.accounted, id); err != nil {
			return err
		}
		if declared {
			return nil
		}
		// A growing segment: a queue event at most every second.
		if err := s.growSegmentTx(ctx, w, queue, id, snap.count, pendingStart, sc.anchor, time.Since(lastEvent) >= time.Second); err != nil {
			return err
		}
		if time.Since(lastEvent) >= time.Second {
			lastEvent = time.Now()
		}
		return nil
	}
	if err = sc.scan(tx, keys, p); err != nil {
		return err
	}
	// Finish: the last chunks, the final count, ready; and the start that waited.
	return dbwork.WithWriteTxContext(ctx, s.DB, dbwork.ClassBackgroundMedia, func(ctx context.Context, w *sql.Tx) error {
		if snap.checkBasis != nil {
			if err := snap.checkBasis(ctx, w); err != nil {
				return err
			}
		}
		for _, c := range snap.pending {
			if err := writeQueueChunkTx(ctx, w, id, c.no, c.keys, false); err != nil {
				return err
			}
		}
		if label == "" && snap.count > 0 {
			label = s.snapshotLabel(ctx, tx, in, snap)
		}
		if _, err := w.ExecContext(ctx, `UPDATE queue_v1_snapshots SET built=?,seen=?,count=?,label=?,state='ready',start='' WHERE id=?`, snap.count, snap.seen, snap.count, label, id); err != nil {
			return err
		}
		return s.finishBuildTx(ctx, w, queue, id, snap.count, pendingStart, sc.anchor)
	})
}

// setGrowingCountTx sets the snapshot's last segment (the one its unbuilt keys
// extend; an earlier split keeps its own range) to end at built.
func setGrowingCountTx(ctx context.Context, w *sql.Tx, snap string, built int64) error {
	_, err := w.ExecContext(ctx, `UPDATE queue_v1_segments SET count=?-first_ordinal WHERE snapshot_id=? AND first_ordinal=(SELECT max(first_ordinal) FROM queue_v1_segments WHERE snapshot_id=?) AND ?>=first_ordinal`, built, snap, snap, built)
	return err
}

// growSegmentTx sets a growing segment's count to what's built and keeps the
// current entry where it is (positions after the segment move). A waiting
// ordered start begins once its anchor is reached.
func (s *Service) growSegmentTx(ctx context.Context, w *sql.Tx, queue, snap string, built int64, start *queueStart, anchor int64, announce bool) error {
	q, err := loadQueue(ctx, w, queue)
	if err != nil {
		return err
	}
	before, err := s.readLayout(ctx, w, q)
	if err != nil {
		return err
	}
	if _, err = before.loadExistingSonicRange(q.current, 1); err != nil {
		return err
	}
	current, hasCurrent := before.currentEntry()
	if err = before.extendShuffleOrder(ctx, w, snap, built); err != nil {
		return err
	}
	if err = setGrowingCountTx(ctx, w, snap, built); err != nil {
		return err
	}
	after, err := s.readLayout(ctx, w, q)
	if err != nil {
		return err
	}
	if hasCurrent && !q.shuffle {
		if src, _, ok := after.sourceIndexOf(current); ok {
			q.current = src
		}
	}
	if start != nil && start.Mode == "ordered" && anchor >= 0 {
		if src, _, ok := after.sourceIndexOf(snap + "-" + strconv.FormatInt(anchor, 10)); ok {
			q.current = src
			start.Mode = "" // begun
			if _, err = w.ExecContext(ctx, `UPDATE queue_v1_snapshots SET start='' WHERE id=?`, snap); err != nil {
				return err
			}
			announce = true
		}
	}
	return s.bumpQueueTx(ctx, w, q, announce)
}

// finishBuildTx settles a finished snapshot's segment and a start that waited
// for it: an ordered start at its anchor (or the segment's first entry), a
// shuffle over the final size from its seed.
func (s *Service) finishBuildTx(ctx context.Context, w *sql.Tx, queue, snap string, built int64, start *queueStart, anchor int64) error {
	q, err := loadQueue(ctx, w, queue)
	if err != nil {
		return err
	}
	before, err := s.readLayout(ctx, w, q)
	if err != nil {
		return err
	}
	if _, err = before.loadExistingSonicRange(q.current, 1); err != nil {
		return err
	}
	current, hasCurrent := before.currentEntry()
	if err = before.extendShuffleOrder(ctx, w, snap, built); err != nil {
		return err
	}
	if err = setGrowingCountTx(ctx, w, snap, built); err != nil {
		return err
	}
	after, err := s.readLayout(ctx, w, q)
	if err != nil {
		return err
	}
	switch {
	case start != nil && start.Mode == "ordered":
		ordinal := max(anchor, 0)
		if src, _, ok := after.sourceIndexOf(snap + "-" + strconv.FormatInt(ordinal, 10)); ok {
			q.current = src
		}
	case start != nil && start.Mode == "shuffle":
		total := after.total()
		if total > 0 {
			q.shuffle, q.seed, q.domain, q.lap = true, start.Seed, total, 0
			if sh, err := NewShuffle(uint64(total), uint32(start.Seed), 1); err == nil {
				first, _ := sh.At(0)
				q.first = int64(first)
			}
			q.current = 0
		}
	case hasCurrent:
		if src, _, ok := after.sourceIndexOf(current); ok {
			if q.shuffle && q.domain != after.total() {
				after.q = q
				after.reshuffle(src)
				q = after.q
			} else if !q.shuffle {
				q.current = src
			}
		}
	}
	return s.bumpQueueTx(ctx, w, q, true)
}

// bumpQueueTx writes a queue's position fields; announced changes also take a
// new revision and a queue event.
func (s *Service) bumpQueueTx(ctx context.Context, w *sql.Tx, q queueRow, announce bool) error {
	if announce {
		q.revision++
		q.updated = s.now().UnixMilli()
	}
	if _, err := w.ExecContext(ctx, `UPDATE queues_v1 SET revision=?,current_position=?,shuffle_on=?,shuffle_seed=?,shuffle_lap=?,shuffle_domain=?,shuffle_first=?,updated_ms=? WHERE id=?`, q.revision, q.current, q.shuffle, q.seed, q.lap, q.domain, q.first, q.updated, q.id); err != nil {
		return err
	}
	if !announce {
		return nil
	}
	l, err := s.readLayout(ctx, w, q)
	if err != nil {
		return err
	}
	if _, err = l.loadExistingSonicRange(q.current, 1); err != nil {
		return err
	}
	if err = s.queueEventsTx(ctx, w, l, s.now()); err != nil {
		return err
	}
	s.hub.wake()
	return nil
}

// failBuild keeps what a failed build made: the segment ends at its built keys
// and a start that waited begins with them (the queue never points past them).
func (s *Service) failBuild(id string) {
	ctx := context.Background()
	err := dbwork.WithWriteTxContext(ctx, s.DB, dbwork.ClassBackgroundMedia, func(ctx context.Context, w *sql.Tx) error {
		var queue, start string
		var built int64
		if err := w.QueryRowContext(ctx, `SELECT queue_id,start,built FROM queue_v1_snapshots WHERE id=? AND state='building'`, id).Scan(&queue, &start, &built); err != nil {
			return err
		}
		if _, err := w.ExecContext(ctx, `UPDATE queue_v1_snapshots SET state='failed',count=?,start='' WHERE id=?`, built, id); err != nil {
			return err
		}
		var waiting *queueStart
		if start != "" {
			waiting = &queueStart{}
			if json.Unmarshal([]byte(start), waiting) != nil {
				waiting = nil
			}
		}
		return s.finishBuildTx(ctx, w, queue, id, built, waiting, -1)
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Printf("Queue snapshot %s: its failure couldn't be recorded: %v", id, err)
	}
}
