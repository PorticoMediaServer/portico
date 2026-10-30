package playback

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"sync"
)

// occurrenceRegistry is the process-side publication fence for media work. An
// intent is acquired before reading durable state; that same intent must be
// checked after the read and before a process or provider is published.
// No database or provider work is performed while mu is held.
var errOccurrenceClaimed = errors.New("playback occurrence is being retired")

type occurrenceRegistry struct {
	mu      sync.Mutex
	entries map[string]*occurrenceEntry
}
type occurrenceEntry struct {
	epoch        uint64
	blocked      bool
	committed    bool
	intents      int
	publications int
	quiescent    chan struct{}
	runs         map[*occurrenceRun]struct{}
	budgets      map[*prefetchRecord]struct{}
	claim        *occurrenceClaim
	stopped      *occurrenceClaim
}
type occurrenceIntent struct {
	registry *occurrenceRegistry
	id       string
	entry    *occurrenceEntry
	epoch    uint64
	released bool
}
type occurrenceRun struct {
	spillCancelled bool // guarded by HLS.mu; only this run was cancelled for spill
	generation     int
	requestID      string
	itemID         int64
	assetID        string
	cancel         context.CancelFunc
	done           <-chan struct{}
}
type occurrenceClaim struct {
	registry   *occurrenceRegistry
	id         string
	generation int
	entry      *occurrenceEntry
	epoch      uint64
	runs       []*occurrenceRun
	budgets    []*prefetchRecord
	phase      uint8 // 0 held, 1 committed, 2 aborted
	waiters    int
	delivered  bool
	fencing    bool
	fenceOwner *StopOccurrenceClaim
	cancelOnce sync.Once
}

func (r *occurrenceRegistry) intent(id string) (*occurrenceIntent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[string]*occurrenceEntry)
	}
	e := r.entries[id]
	if e == nil {
		e = &occurrenceEntry{runs: make(map[*occurrenceRun]struct{})}
		r.entries[id] = e
	}
	if e.blocked || e.committed {
		return nil, errOccurrenceClaimed
	}
	e.intents++
	return &occurrenceIntent{registry: r, id: id, entry: e, epoch: e.epoch}, nil
}

func (i *occurrenceIntent) currentLocked() bool {
	return !i.released && i.registry.entries[i.id] == i.entry && !i.entry.blocked && !i.entry.committed && i.entry.epoch == i.epoch
}

// install atomically checks the original ticket and records its exact run. The
// caller publishes process state only in publish; it must not perform I/O.
func (i *occurrenceIntent) install(run *occurrenceRun, publish func()) error {
	r := i.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if !i.currentLocked() {
		return errOccurrenceClaimed
	}
	i.entry.runs[run] = struct{}{}
	publish()
	return nil
}
func (i *occurrenceIntent) current() bool {
	r := i.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	return i.currentLocked()
}
func (i *occurrenceIntent) release() {
	r := i.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if i.released {
		return
	}
	i.released = true
	i.entry.intents--
	r.pruneLocked(i.id, i.entry)
}
func (r *occurrenceRegistry) finished(id string, e *occurrenceEntry, run *occurrenceRun) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries[id] != e {
		return
	}
	delete(e.runs, run)
	r.pruneLocked(id, e)
}
func (r *occurrenceRegistry) pruneLocked(id string, e *occurrenceEntry) {
	if r.entries[id] == e && e.intents == 0 && e.publications == 0 && len(e.runs) == 0 && len(e.budgets) == 0 && e.claim == nil {
		delete(r.entries, id)
	}
}

// ownsRun is liveness of an already installed exact resource, not admission.
// A held claim bumps start epochs but cannot revoke owned IO before acceptance.
func (r *occurrenceRegistry) ownsRun(id string, entry *occurrenceEntry, run *occurrenceRun) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries[id] != entry || entry.committed {
		return false
	}
	_, owned := entry.runs[run]
	return owned
}

// publication admits one original run's provider/process publication. The
// returned release is called when that publication has become owned or failed.
// A claim closes admission and waits for already-admitted publications outside
// the mutex; it never performs provider or database work under the mutex.
func (r *occurrenceRegistry) publication(id string, e *occurrenceEntry, run *occurrenceRun) (func(), error) {
	r.mu.Lock()
	if r.entries[id] != e || e.blocked || e.committed {
		r.mu.Unlock()
		return nil, errOccurrenceClaimed
	}
	if _, ok := e.runs[run]; !ok {
		r.mu.Unlock()
		return nil, errOccurrenceClaimed
	}
	e.publications++
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			e.publications--
			if e.publications == 0 && e.quiescent != nil {
				close(e.quiescent)
				e.quiescent = nil
			}
			r.pruneLocked(id, e)
			r.mu.Unlock()
		})
	}, nil
}

// claim fences new publication before the accepting transaction. Copies for
// one generation share the phase latch and the same quiescence barrier.
func (r *occurrenceRegistry) claim(ctx context.Context, id string, generation int) (*occurrenceClaim, error) {
	if id == "" || generation < 1 {
		return nil, errOccurrenceClaimed
	}
	r.mu.Lock()
	if r.entries == nil {
		r.entries = make(map[string]*occurrenceEntry)
	}
	e := r.entries[id]
	if e == nil {
		e = &occurrenceEntry{runs: make(map[*occurrenceRun]struct{})}
		r.entries[id] = e
	}
	// A branch still owning another generation (for example an in-flight
	// fragmented fallback) cannot be captured as this generation. Refuse before
	// closing admission rather than applying an id-only finalizer to it.
	for run := range e.runs {
		if run.generation != generation {
			r.mu.Unlock()
			return nil, errOccurrenceClaimed
		}
	}
	for budget := range e.budgets {
		if budget.generation != generation {
			r.mu.Unlock()
			return nil, errOccurrenceClaimed
		}
	}
	c := e.claim
	if c != nil && c.generation != generation {
		r.mu.Unlock()
		return nil, errOccurrenceClaimed
	}
	if c == nil {
		if e.blocked || e.committed {
			r.mu.Unlock()
			return nil, errOccurrenceClaimed
		}
		e.blocked = true
		e.epoch++
		c = &occurrenceClaim{registry: r, id: id, generation: generation, entry: e, epoch: e.epoch}
		for run := range e.runs {
			if run.generation == generation {
				c.runs = append(c.runs, run)
			}
		}
		for budget := range e.budgets {
			if budget.generation == generation {
				c.budgets = append(c.budgets, budget)
			}
		}
		e.claim = c
	}
	c.waiters++
	var wait <-chan struct{}
	if e.publications > 0 {
		if e.quiescent == nil {
			e.quiescent = make(chan struct{})
		}
		wait = e.quiescent
	}
	r.mu.Unlock()
	var waitErr error
	if wait != nil {
		select {
		case <-wait:
		case <-ctx.Done():
			waitErr = ctx.Err()
		}
	}
	r.mu.Lock()
	c.waiters--
	valid := c.phase == 0 && r.entries[id] == e && e.claim == c
	if waitErr == nil && valid {
		c.delivered = true
	}
	if waitErr != nil && valid && !c.delivered && c.waiters == 0 {
		c.phase = 2
		e.claim = nil
		e.blocked = false
		r.pruneLocked(id, e)
	}
	r.mu.Unlock()
	if waitErr != nil {
		return nil, waitErr
	}
	if !valid {
		return nil, errOccurrenceClaimed
	}
	return c, nil
}

// abort is valid only before the caller's transaction commits.
func (c *occurrenceClaim) abort() {
	r := c.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if c.phase != 0 || r.entries[c.id] != c.entry || c.entry.claim != c {
		return
	}
	c.phase = 2
	c.entry.claim = nil
	c.entry.blocked = false
	r.pruneLocked(c.id, c.entry)
}

// markCommitted is called only by the exact gated writer's after-commit hook.
// It performs no I/O and does not cancel while the writer is held.
func (c *occurrenceClaim) markCommitted() {
	r := c.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if c.phase != 0 || r.entries[c.id] != c.entry || c.entry.claim != c {
		return
	}
	c.phase = 1
	c.entry.committed = true
	c.entry.stopped = c
	c.entry.claim = nil
}

// commit joins only the captured run handles after the exact writer committed.
func (c *occurrenceClaim) commit(ctx context.Context) error {
	r := c.registry
	r.mu.Lock()
	if c.phase != 1 {
		r.mu.Unlock()
		return errOccurrenceClaimed
	}
	runs := append([]*occurrenceRun(nil), c.runs...)
	r.mu.Unlock()
	c.cancelOnce.Do(func() {
		for _, budget := range c.budgets {
			budget.retire()
		}
		r.mu.Lock()
		for _, budget := range c.budgets {
			delete(c.entry.budgets, budget)
		}
		r.mu.Unlock()
		for _, run := range runs {
			if run.cancel != nil {
				run.cancel()
			}
		}
	})
	for _, run := range runs {
		if run.done != nil {
			select {
			case <-run.done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	r.mu.Lock()
	r.pruneLocked(c.id, c.entry)
	r.mu.Unlock()
	return nil
}

// StopOccurrenceClaim holds process ownership and the frozen durable tuple.
// It is not a caller permission and cannot authorize or issue a media grant.
type StopOccurrenceClaim struct {
	phase      *occurrenceClaim
	original   context.Context
	id         string
	generation int
	tuple      stopOccurrenceTuple
}
type stopOccurrenceTuple struct {
	request, asset, account, profile, login, state, grant, grantToken, mode string
	item                                                                    int64
	generation                                                              int
	source, incarnation, pinnedAsset                                        string
	sourceGeneration, size, modified                                        int64
}

const stopOccurrenceSelect = `SELECT p.request_id,p.asset_id,p.item_id,p.account_id,p.profile_id,p.session_hash,p.state,p.generation,p.grant_hash,p.grant_token,p.mode,COALESCE(x.source_id,''),COALESCE(x.incarnation,''),COALESCE(x.configuration_generation,0),COALESCE(f.asset_id,''),COALESCE(f.size,0),COALESCE(f.modified_ns,0) FROM playback_sessions p LEFT JOIN playback_physical_source_pins x ON x.session_id=p.id LEFT JOIN playback_source_pins f ON f.session_id=p.id WHERE p.id=?`

func readStopOccurrence(r interface{ Scan(...any) error }) (stopOccurrenceTuple, error) {
	var t stopOccurrenceTuple
	err := r.Scan(&t.request, &t.asset, &t.item, &t.account, &t.profile, &t.login, &t.state, &t.generation, &t.grant, &t.grantToken, &t.mode, &t.source, &t.incarnation, &t.sourceGeneration, &t.pinnedAsset, &t.size, &t.modified)
	return t, err
}

var ErrOccurrenceChanged = errors.New("playback occurrence changed")

// ClaimStopOccurrence closes registration before reading the exact durable
// tuple. No resource is cancelled until an accepting transaction commits.
func (s *Service) ClaimStopOccurrence(ctx context.Context, id string, generation int) (*StopOccurrenceClaim, error) {
	if ctx == nil || dbwork.Snapshot(ctx) != nil {
		return nil, ErrOccurrenceChanged
	}
	phase, err := s.occurrenceRegistry().claim(ctx, id, generation)
	if err != nil {
		return nil, err
	}
	out := &StopOccurrenceClaim{phase: phase, original: ctx, id: id, generation: generation}
	out.tuple, err = readStopOccurrence(s.db.QueryRowContext(ctx, stopOccurrenceSelect, id))
	if err != nil || out.tuple.generation != generation || out.tuple.state == "stopped" || out.tuple.state == "ended" || out.tuple.state == "failed" {
		out.Abort()
		if err != nil {
			return nil, err
		}
		return nil, ErrOccurrenceChanged
	}
	return out, nil
}

// RebindAfterDrainTx admits only the state/login changes made by the renderer's
// evidence drain in this exact writer. The caller repeats current authority and
// the original v1/media/child binding; all physical and occurrence facts remain
// frozen. It cannot adopt a successor generation or a stopped/failed row.
func (c *StopOccurrenceClaim) RebindAfterDrainTx(ctx context.Context, tx *sql.Tx, check func(context.Context, *sql.Tx) error) error {
	if c == nil || ctx == nil || tx == nil || check == nil || dbwork.Snapshot(ctx) != tx || !dbwork.InWriteTx(ctx) || c.original.Done() != ctx.Done() || c.original.Err() != nil || ctx.Err() != nil {
		return ErrOccurrenceChanged
	}
	if err := check(ctx, tx); err != nil {
		return err
	}
	actual, err := readStopOccurrence(tx.QueryRowContext(ctx, stopOccurrenceSelect, c.id))
	if err != nil {
		return err
	}
	if actual.state == "stopped" || actual.state == "failed" {
		return ErrOccurrenceChanged
	}
	original := c.tuple
	original.state, original.login = actual.state, actual.login
	if actual != original {
		return ErrOccurrenceChanged
	}
	r := c.phase.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if c.phase.phase != 0 || c.phase.fencing || r.entries[c.id] != c.phase.entry || c.phase.entry.claim != c.phase {
		return ErrOccurrenceChanged
	}
	c.tuple = actual
	return nil
}

// FenceTx requires the original live writer and an original-caller check. Its
// after-commit marker cannot be forged by a caller or fired by a rollback.
func (c *StopOccurrenceClaim) FenceTx(ctx context.Context, tx *sql.Tx, check func(context.Context, *sql.Tx) error) error {
	if c == nil || ctx == nil || tx == nil || check == nil || dbwork.Snapshot(ctx) != tx || !dbwork.InWriteTx(ctx) || c.original.Done() != ctx.Done() || c.original.Err() != nil || ctx.Err() != nil {
		return ErrOccurrenceChanged
	}
	r := c.phase.registry
	r.mu.Lock()
	if c.phase.phase != 0 || c.phase.fencing || r.entries[c.id] != c.phase.entry || c.phase.entry.claim != c.phase {
		r.mu.Unlock()
		return ErrOccurrenceChanged
	}
	c.phase.fencing = true
	c.phase.fenceOwner = c
	r.mu.Unlock()
	if err := check(ctx, tx); err != nil {
		return err
	}
	actual, err := readStopOccurrence(tx.QueryRowContext(ctx, stopOccurrenceSelect, c.id))
	if err != nil {
		return err
	}
	if actual != c.tuple {
		return ErrOccurrenceChanged
	}
	t := c.tuple
	result, err := tx.ExecContext(ctx, `UPDATE playback_sessions SET state='stopped' WHERE id=? AND generation=? AND state=? AND request_id=? AND item_id=? AND asset_id=? AND session_hash=? AND grant_hash=?`, c.id, c.generation, t.state, t.request, t.item, t.asset, t.login, t.grant)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrOccurrenceChanged
	}
	return dbwork.AfterCommit(tx, c.phase.markCommitted)
}
func (c *StopOccurrenceClaim) Abort() {
	if c == nil {
		return
	}
	r := c.phase.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	phase := c.phase
	if phase.phase != 0 || r.entries[c.id] != phase.entry || phase.entry.claim != phase || phase.fenceOwner != nil && phase.fenceOwner != c {
		return
	}
	phase.phase = 2
	phase.entry.claim = nil
	phase.entry.blocked = false
	r.pruneLocked(c.id, phase.entry)
}
func (c *StopOccurrenceClaim) Commit(ctx context.Context) error {
	if c == nil || ctx == nil {
		return ErrOccurrenceChanged
	}
	return c.phase.commit(ctx)
}

// occurrenceRegistry returns the registration fence shared with the HLS
// engine, or the service-owned fence on servers without an HLS engine.
func (s *Service) occurrenceRegistry() *occurrenceRegistry {
	if s.hls != nil {
		return &s.hls.occurrences
	}
	return &s.occurrences
}

// FinishStoppedOccurrence joins only this generation's registered processes.
// The caller has already committed its grant fence (and, for keyed commands,
// its receipt). Unlike StopV1 this never performs an id-only database update.
// A retained committed claim can finish even after a successor replaced the
// durable row; its run handles are still the exact original ones.
func (s *Service) FinishStoppedOccurrence(ctx context.Context, id string, generation int) error {
	if ctx == nil || dbwork.Snapshot(ctx) != nil {
		return ErrOccurrenceChanged
	}
	r := s.occurrenceRegistry()
	r.mu.Lock()
	var committed *occurrenceClaim
	if e := r.entries[id]; e != nil && e.stopped != nil && e.stopped.generation == generation {
		committed = e.stopped
	}
	r.mu.Unlock()
	if committed != nil {
		return committed.commit(ctx)
	}
	// The old generation must already be durably stopped or retired. Reprove
	// this before acquiring or cancelling any recovery process handles.
	var actual int
	var state string
	err := s.db.QueryRowContext(ctx, `SELECT generation,state FROM playback_sessions WHERE id=?`, id).Scan(&actual, &state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && (actual < generation || (actual == generation && state != "stopped")) {
		return ErrOccurrenceChanged
	}
	phase, err := r.claim(ctx, id, generation)
	if err != nil {
		if !errors.Is(err, errOccurrenceClaimed) {
			return err
		}
		// A successor may already own the registry. Join only the original
		// generation's exact handles, without fencing successor publication.
		r.mu.Lock()
		e := r.entries[id]
		if e == nil {
			r.mu.Unlock()
			return nil
		}
		old := &occurrenceClaim{registry: r, id: id, generation: generation, entry: e, phase: 1}
		for run := range e.runs {
			if run.generation == generation {
				old.runs = append(old.runs, run)
			}
		}
		for budget := range e.budgets {
			if budget.generation == generation {
				old.budgets = append(old.budgets, budget)
			}
		}
		r.mu.Unlock()
		return old.commit(ctx)
	}
	defer phase.abort()
	err = s.db.QueryRowContext(ctx, `SELECT generation,state FROM playback_sessions WHERE id=?`, id).Scan(&actual, &state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && (actual < generation || (actual == generation && state != "stopped")) {
		return ErrOccurrenceChanged
	}
	// The exact stopped row is the durable acceptance proof on recovery, when
	// an unknown COMMIT may not have delivered the original after-commit hook.
	phase.markCommitted()
	return phase.commit(ctx)
}
