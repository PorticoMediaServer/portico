// Package compactcatalog is the catalogue: its write API (write.go) and the
// derived data kept from its facts (search, browse, availability, counts).
//
// Facts are written synchronously by the write API in the writer's own
// transaction. Derived data is queued on catalog_dirty(domain, entity_id,
// revision) by that API and by small triggers on the fact tables, and one
// supervised Worker drains it in bounded batches. A derivation can always be
// rebuilt from facts: raising its Version rebuilds it in the background after
// startup while readers continue using the serving generation.
package compactcatalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// Derived domains. Numbers are stored (catalog_dirty, catalog_derivations) and
// permanent: never reuse one.
const (
	DomainSearch       = 17 // catalog_search_documents and the title FTS
	DomainBrowseRows   = 18 // catalog_browse_rows, buckets, counted rows, Home buckets
	DomainBrowseEdges  = 19 // catalog_browse_memberships
	DomainAvailability = 20 // catalog_item_availability, catalog_asset_links.available
	DomainRelated      = 21 // catalog_related_facets
	DomainItemMetrics  = 25 // per-item duration/rating fanned out to containers
	DomainAssetMetrics = 26 // an asset's change fanned out to its items
	DomainCategories   = 27 // movie genre/decade/rating categories
	// DomainInheritedAttributes republishes an item's browse attributes that it
	// inherits from its show, album or book (registered by package metadata).
	DomainInheritedAttributes = 31
	// DomainContainerRefresh re-derives a show's, season's, album's, artist's,
	// book's or collection's browse row after its members changed. It waits
	// while its producers' derived work is queued (deferredProducers), so a
	// container whose members arrive over many batches — a show's episodes, an
	// artist's thousands of songs while a library is first built — is refreshed
	// once instead of once per batch of its members.
	DomainContainerRefresh = 35
)

// deferredDomains wait until the domains that produce their work have no work
// queued, or until they have waited deferredWait since they were last served,
// whichever is first.
// Facet and missing-title projections are consumers, not reasons to defer a
// container refresh: its browse updates invalidate their source epoch. Waiting
// on those consumers throttles every refresh quantum by deferredWait while
// repeatedly restarting their private work. Both consumers capture the global
// source epoch, so their turns wait for refreshes to settle, just as they wait
// for the source fanouts that refreshes consume.
var deferredDomains = map[int]bool{DomainContainerRefresh: true}

const deferredWait = 10 * time.Second

// deferredProducers is the closed set of registered domains whose queued work
// can later queue a deferred domain's own work, directly or through one
// another (P2-59 pressure tail, R5). A container refresh waits for these and
// for nothing else: 36-40 feed only the facet consumers and must not delay it,
// while 41-43 do through the source fanouts. 33 and 44 are consumers, not
// producers; they stay hard-fenced on the refresh by the guards above. Every
// domain in deferredDomains needs an entry here.
var deferredProducers = map[int][]int{
	DomainContainerRefresh: {
		DomainBrowseRows,
		DomainBrowseEdges,
		DomainAvailability,
		DomainItemMetrics,
		DomainAssetMetrics,
		DomainLibraryAvailability,
		DomainInheritedAttributes,
		DomainAssetSources,
		DomainEntitySources,
		DomainLibrarySources,
	},
}

// domainPriority (milliseconds) lets a domain's turn come that much before
// the plain round robin would give it. A library being built shows its titles
// in browse and search first; memberships and availability follow, and the
// fanouts that summarise members into their containers run after their
// inputs settle rather than redoing work as each member arrives. Every domain
// with work still gets a turn at least about once a second.
var domainPriority = map[int]int{DomainBrowseRows: 1000, DomainSearch: 1000, DomainBrowseEdges: 500, DomainAvailability: 500}

// nextDomainSQL lists the domains with work in the order their turns come.
// ?1 is the served time (ms) before which a deferred domain's wait is over.
// A deferred domain coalesces only its closed producer set (deferredProducers),
// so unrelated domains neither delay it nor are delayed by it.
var nextDomainSQL = func() string {
	domains := []int{}
	for d := range deferredDomains {
		domains = append(domains, d)
	}
	sort.Ints(domains)
	coalesce := ""
	for _, d := range domains {
		producers := make([]string, 0, len(deferredProducers[d]))
		for _, p := range deferredProducers[d] {
			producers = append(producers, fmt.Sprint(p))
		}
		coalesce += fmt.Sprintf(`
 AND (domain NOT IN(%d) OR served_ms<=?1 OR NOT EXISTS(SELECT 1 FROM catalog_derivations o WHERE o.domain IN(%s) AND (o.rebuilding=1 OR EXISTS(SELECT 1 FROM catalog_dirty d WHERE d.domain=o.domain))))`, d, strings.Join(producers, ","))
	}
	priority := ""
	for _, d := range []int{DomainSearch, DomainBrowseRows, DomainBrowseEdges, DomainAvailability} {
		priority += fmt.Sprintf(" WHEN %d THEN %d", d, domainPriority[d])
	}
	return `SELECT domain,rebuilding FROM catalog_derivations st WHERE (rebuilding=1 OR EXISTS(SELECT 1 FROM catalog_dirty d WHERE d.domain=st.domain))
 AND (st.domain<>33 OR (NOT EXISTS(SELECT 1 FROM catalog_dirty WHERE domain IN(35,36,37,38,39,40,41,42,43)) AND NOT EXISTS(SELECT 1 FROM catalog_derivations WHERE domain IN(35,36,37,38,39,40,41,42,43) AND rebuilding=1) AND NOT EXISTS(SELECT 1 FROM catalog_derivation_failures WHERE domain IN(35,36,37,38,39,40,41,42,43))))
 AND (st.domain<>44 OR (NOT EXISTS(SELECT 1 FROM catalog_dirty WHERE domain IN(35,41)) AND NOT EXISTS(SELECT 1 FROM catalog_derivation_failures WHERE domain IN(35,41))))` + coalesce + `
 ORDER BY served_ms-CASE domain` + priority + ` ELSE 0 END,domain`
}()

// BatchLimit is the fixed units per transaction of callers that do not pace
// by time (and the first batch of a paced Worker).
const BatchLimit = 8

// The background Worker sizes each domain's batches to hold the write gate
// for about BatchHoldTarget: units cost from tens of microseconds (a queue
// entry already up to date) to milliseconds (a title with many files), and a
// fixed count either holds the gate too long for heavy units or commits too
// often for light ones, re-deriving every container its members share once
// per commit. A batch stays within [batchFloor, BatchMax] units.
const (
	BatchHoldTarget = 20 * time.Millisecond
	BatchMax        = 64
	batchFloor      = 2
)

// ErrBuilding is an internal signal for an unpublished restriction class.
// Browse documents consume it as an empty first generation.
var ErrBuilding = errors.New("library visibility is unavailable")

// Key is one queued unit: an entity (or asset, as the domain defines) and the
// revision it was queued at.
type Key struct {
	ID       int64
	Revision int64
}

// Derivation is how one domain's derived data is kept. Drain processes a
// bounded batch inside the Worker's transaction and returns the ids it
// finished; an unfinished id keeps its queue row (a domain with long fanouts
// keeps its own cursor, restarting it when the id's revision changes).
// Backfill enumerates the domain's ids after a cursor for a rebuild.
type Derivation struct {
	Version  int
	Drain    func(ctx context.Context, tx *sql.Tx, keys []Key, limit int) ([]int64, error)
	Backfill func(ctx context.Context, tx *sql.Tx, after int64, limit int) ([]int64, error)
}

var registry = struct {
	sync.RWMutex
	domains map[int]Derivation
}{domains: map[int]Derivation{}}

// Register installs a domain's derivation; called from init.
func Register(domain int, d Derivation) {
	if domain < 1 || domain > 99 || d.Version < 1 || d.Drain == nil || d.Backfill == nil {
		panic(fmt.Sprintf("invalid catalogue derivation %d", domain))
	}
	registry.Lock()
	defer registry.Unlock()
	registry.domains[domain] = d
}

func derivation(domain int) (Derivation, bool) {
	registry.RLock()
	defer registry.RUnlock()
	d, ok := registry.domains[domain]
	return d, ok
}

func registeredDomains() []int {
	registry.RLock()
	defer registry.RUnlock()
	out := make([]int, 0, len(registry.domains))
	for d := range registry.domains {
		out = append(out, d)
	}
	sort.Ints(out)
	return out
}

// TouchTx queues ids of a domain in the caller's transaction, bumping the
// revision of an id already queued so a drain in progress runs it again.
func TouchTx(ctx context.Context, tx *sql.Tx, domain int, ids ...int64) error {
	switch {
	case len(ids) == 0:
		return nil
	case len(ids) == 1:
		_, err := tx.ExecContext(ctx, touchOne, domain, ids[0])
		return err
	}
	for start := 0; start < len(ids); start += 500 {
		end := min(start+500, len(ids))
		if _, err := tx.ExecContext(ctx, touchMany, domain, mustJSON(ids[start:end])); err != nil {
			return err
		}
	}
	return nil
}

const (
	touchOne  = `INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(?,?,1) ON CONFLICT(domain,entity_id) DO UPDATE SET revision=revision+1`
	touchMany = `INSERT INTO catalog_dirty(domain,entity_id,revision) SELECT ?,value,1 FROM json_each(?) WHERE 1 ON CONFLICT(domain,entity_id) DO UPDATE SET revision=revision+1`
)

// CheckReadiness leaves the serving generation available during rebuilds.
func CheckReadiness(ctx context.Context, q ReadQuery, domains ...int) error { return nil }

// ReadQuery is a transaction or handle a reader passes in.
type ReadQuery interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Worker drains catalog_dirty and runs the derived step workers.
type Worker struct {
	db *sql.DB
	mu sync.Mutex
	// isolate names domains whose last batch failed: their batches are one id
	// until one succeeds, so a bad id can't hold a whole batch back. poison is
	// the ids that then still failed, one by one; they are set aside (a set: a
	// second bad id must not stall the rest). A failed rebuild unit is recorded
	// durably and passed over so the new generation can publish.
	isolate map[int]bool
	poison  map[int]map[int64]bool
	// What was set aside is tried again when everything else is idle, one id
	// a turn (retryAfter is each domain's place in its turn), and not before
	// retryAt: every failed try doubles retryDelay.
	retryAfter map[int]int64
	retryAt    time.Time
	retryDelay time.Duration
	// backfillTurn alternates queued ids and rebuild enumeration, so edits
	// aren't queued behind a rebuild.
	backfillTurn map[int]bool
	stepTurn     int
	sourceTurn   bool
	// Source41 commits one raw quantum per source. Rotate its claim point so
	// a prefenced input waiting on another asset's custody cannot starve the
	// source that releases it. This is worker-local scheduling, not progress.
	assetSourceAfter int64
	domainTurns      int
	ready            bool
	now              func() time.Time
	// holdTarget paces batches by time (zero: the caller's fixed limit);
	// unitCost is each domain's recent cost per unit (key -1: step workers).
	holdTarget time.Duration
	unitCost   map[int]time.Duration
	// unitRows is each domain's recent rows changed per unit, so a paced batch
	// also stays inside the maintenance write plan's row budget.
	unitRows map[int]float64
	// lastUnits is each domain's previous paced batch size: a batch may at
	// most double it, so a domain whose units suddenly grow can't overshoot
	// by more than one doubling.
	lastUnits map[int]int
	last      lastBatch
}

type lastBatch struct {
	domain, units int
	took          time.Duration
}

// LastBatch reports the latest committed batch (diagnostics and the scale
// tool): its domain (-1 the step workers, -2 none), units and how long it
// held the write gate, commit included.
func (w *Worker) LastBatch() (domain, units int, took time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last.domain, w.last.units, w.last.took
}

// PaceToHold sizes each batch to hold the write gate for about target, up to
// the limit a Step is given. Run paces to BatchHoldTarget.
func (w *Worker) PaceToHold(target time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.holdTarget = target
}

// singleHeadDomain identifies drains whose key window is independent of the
// raw-row budget passed to their internal cursor walk.
func singleHeadDomain(domain int) bool {
	switch domain {
	case DomainBrowseRows, DomainBrowseEdges, DomainAvailability,
		DomainItemMetrics, DomainAssetMetrics, DomainLibraryAvailability,
		DomainFacetCounts, DomainContainerRefresh,
		DomainAssetSources, DomainEntitySources, DomainLibrarySources, DomainMissingSources:
		return true
	default:
		return false
	}
}

// batchFor is the units of the next batch of a domain: the limit when not
// paced, else what its recent cost per unit says fits the hold target.
func (w *Worker) batchFor(domain, limit int) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.holdTarget <= 0 {
		return limit
	}
	if domain == DomainSearch {
		// Replacing one document includes native FTS writes and prefix-count
		// triggers. Their spike is not predicted by preceding cheap documents.
		// Keep this atomic unit on the existing single-unit plan; cleanup has
		// its separate raw-leaf quantum below and never takes that exception.
		return 1
	}
	// A cursor drain reports one holder key even when it consumed a larger
	// raw page. Its key count cannot size that page: applying the doubling
	// guard would permanently pin every such drain at two raw rows. The
	// measured row/hold limits below and each drain's raw cap still apply.
	if last, ok := w.lastUnits[domain]; ok && !singleHeadDomain(domain) {
		limit = min(limit, max(1, 2*last))
	}
	if rows := w.unitRows[domain]; rows > 0 {
		// Keep a raw unit's allowance for batch bookkeeping and a changing
		// mix of insert/update/delete effects. Filling the estimate to 160
		// caused repeated 170/176-row turns as those mixes alternated.
		budget := dbwork.DefaultPlan(dbwork.ClassMaintenance).Rows - 32
		limit = min(limit, max(1, int(float64(budget)/rows)))
	}
	cost, ok := w.unitCost[domain]
	if !ok || cost <= 0 {
		// Nothing is known about this domain's units yet: start with one, so
		// a first batch of expensive units can't overshoot the plan.
		return 1
	}
	return max(min(limit, batchFloor), min(limit, BatchMax, int(w.holdTarget/cost)))
}

// observeBatch folds a committed batch's cost per unit into its domain's
// estimate (an even blend of the last estimate and this batch).
func (w *Worker) observeBatch(domain, units int, took time.Duration, rows int64) {
	if units <= 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.holdTarget <= 0 {
		return
	}
	if w.unitRows == nil {
		w.unitRows, w.lastUnits = map[int]float64{}, map[int]int{}
	}
	w.lastUnits[domain] = units
	// Rows per unit rise at once and fall by halves: a spike shrinks the next
	// batch immediately, and one cheap batch can't undo it.
	perUnit := float64(max(rows, 0)) / float64(units)
	if old, ok := w.unitRows[domain]; ok && perUnit < old {
		perUnit = (old + perUnit) / 2
	}
	w.unitRows[domain] = perUnit
	if w.unitCost == nil {
		w.unitCost = map[int]time.Duration{}
	}
	cost := took / time.Duration(units)
	if old, ok := w.unitCost[domain]; ok {
		cost = (old + cost) / 2
	}
	w.unitCost[domain] = max(cost, time.Microsecond)
}

// A set-aside id is tried again after retryFirst, then after twice as long
// each time it fails, up to retryMax. The loop is woken by every catalogue
// commit, its own included, so the Worker keeps this schedule itself.
var retryFirst, retryMax = 5 * time.Second, 5 * time.Minute

func NewWorker(db *sql.DB) *Worker {
	return &Worker{db: db, isolate: map[int]bool{}, poison: map[int]map[int64]bool{}, retryAfter: map[int]int64{}, backfillTurn: map[int]bool{}, now: time.Now}
}

// retryWait is how long until what was set aside is due to be tried again;
// zero when nothing is waiting.
func (w *Worker) retryWait() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.retryAt.IsZero() {
		return 0
	}
	return max(w.retryAt.Sub(w.now()), 0)
}

// Run is supervised by the server. It is change-driven: every catalogue write
// reaches catalog_dirty or a job table, and that commit wakes it.
func (w *Worker) Run(ctx context.Context) {
	w.mu.Lock()
	if w.holdTarget == 0 {
		w.holdTarget = BatchHoldTarget
	}
	w.mu.Unlock()
	signal := worker.NewSignal()
	unregister := dbwork.WakeOnTables(signal, "catalog_*", "compact_*", "reading_path_dirty_work", "reading_path_source_barrier", "reading_path_availability_pending")
	defer unregister()
	worker.Run(ctx, "catalogue derived data", signal, func(ctx context.Context) time.Duration {
		if !dbwork.Yield(ctx) {
			return 0
		}
		batchStarted := time.Now()
		n, err := w.Step(ctx, BatchMax)
		if ctx.Err() != nil {
			return 0
		}
		if err != nil {
			log.Printf("Catalogue derived data will retry: %v", err)
			return 5 * time.Second
		}
		if n > 0 {
			if !dbwork.PaceBackground(ctx, batchStarted) {
				return 0
			}
			return time.Millisecond
		}
		// Blocked Reading scopes are parked until a dependency write wakes
		// this loop; only a quiet-gap hold needs the timer.
		wait := w.retryWait()
		if d := readingPathNextDue(ctx, w.db); d > 0 && (wait == 0 || d < wait) {
			wait = d
		}
		return wait
	})
}

// startupFailures bounds how many set-aside titles a start reads back.
const startupFailures = 20000

// prepare records every registered domain and starts a rebuild of any whose
// code version is newer than the one its data was built with. Rows only; the
// rebuild itself runs in later Steps.
func (w *Worker) prepare(ctx context.Context) error {
	w.mu.Lock()
	ready := w.ready
	w.mu.Unlock()
	if ready {
		return nil
	}
	err := dbwork.WithWriteTx(ctx, w.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		for _, domain := range registeredDomains() {
			d, _ := derivation(domain)
			var old int
			err := tx.QueryRowContext(ctx, `SELECT version FROM catalog_derivations WHERE domain=?`, domain).Scan(&old)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if old > 0 && d.Version > old {
				if domain == DomainFacetCounts {
					// A phase-layout change must reject interrupted raw stages as well
					// as rebuilding accepted rows. Reset lazily under the raw8 budget.
					if _, err := tx.ExecContext(ctx, `UPDATE catalog_facet_source_state SET source_epoch=source_epoch+1 WHERE singleton=1`); err != nil {
						return err
					}
				}
				if err := beginGeneration(ctx, tx); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_derivations(domain,version,rebuilding,first_build) VALUES(?,?,1,1) ON CONFLICT(domain) DO UPDATE SET version=excluded.version,rebuilding=1,first_build=0,rebuild_cursor=0,cleaning=CASE WHEN excluded.domain=17 THEN 1 ELSE 0 END WHERE excluded.version>catalog_derivations.version`, domain, d.Version); err != nil {
				return err
			}
		}
		// The titles set aside after failing, newest first and no more than
		// startupFailures of them: a start never reads a table that could have
		// a row per title. One left out is simply tried again and set aside
		// again if it still fails.
		rows, err := tx.QueryContext(ctx, `SELECT domain,entity_id FROM catalog_derivation_failures ORDER BY failed_ms DESC LIMIT ?`, startupFailures)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var domain int
			var id int64
			if err := rows.Scan(&domain, &id); err != nil {
				return err
			}
			w.mu.Lock()
			if w.poison[domain] == nil {
				w.poison[domain] = map[int64]bool{}
			}
			w.poison[domain][id] = true
			w.retryAt = w.now().Add(retryFirst)
			w.retryDelay = retryFirst
			w.mu.Unlock()
		}
		return rows.Err()
	})
	if err == nil {
		// Each sampled holder is checked in its own maintenance writer so the
		// startup sample never holds the writer across all of them.
		err = sampleBrowseCounterIntegrity(ctx, w.db)
	}
	if err == nil {
		w.mu.Lock()
		w.ready = true
		w.mu.Unlock()
	}
	return err
}

// Rebuild starts rebuilding one domain from facts (tests and diagnostics).
func (w *Worker) Rebuild(ctx context.Context, domain int) error {
	return dbwork.WithWriteTx(ctx, w.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		if err := beginGeneration(ctx, tx); err != nil {
			return err
		}
		if domain == DomainFacetCounts {
			// Explicit Rebuild(33) matches a domain-33 version upgrade. The shared
			// source epoch invalidates facet and missing-source work; availability
			// captures the epoch but deliberately does not restart.
			if _, err := tx.ExecContext(ctx, `UPDATE catalog_facet_source_state SET source_epoch=source_epoch+1 WHERE singleton=1`); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE catalog_derivations SET rebuilding=1,first_build=0,rebuild_cursor=0,cleaning=CASE WHEN domain=17 THEN 1 ELSE 0 END WHERE domain=?`, domain)
		return err
	})
}

// Step commits at most about limit units of derived work: one domain's queued
// ids (or rebuild enumeration), else the step workers round-robin. What was
// set aside for failing is tried again only once both are idle and its next
// try is due, so it holds neither back.
func (w *Worker) Step(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 500 {
		return 0, fmt.Errorf("catalogue batch must contain 1..500 units")
	}
	if err := w.prepare(ctx); err != nil {
		return 0, err
	}
	w.mu.Lock()
	unpaced := w.holdTarget <= 0
	w.mu.Unlock()
	if unpaced {
		// Only fixtures and tools step without pacing; the server's Run always
		// paces to the hold target and stays inside the maintenance plan.
		ctx = dbwork.WithPlan(ctx, DrainPlan)
	}
	n := 0
	domain, failed := -1, int64(0)
	step := -1
	retried, backfilled := false, false
	paced := -2 // what the batch's cost is charged to: a domain, or -1 for the step workers
	// The hold runs from the gate's acquisition (the transaction function
	// starting) through the commit.
	var began time.Time
	var rows int64
	cleaningSearch := false
	err := dbwork.WithWriteTx(ctx, w.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		began = time.Now()
		var before int64
		if e := tx.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&before); e != nil {
			return e
		}
		defer func() {
			var after int64
			if tx.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after) == nil {
				rows = after - before
				if rows > 160 && n != 1 {
					log.Printf("F maintenance profile: domain=%d step=%d units=%d changed=%d rebuilding=%t retry=%t", domain, step, n, rows, backfilled, retried)
				}
			}
			if n == 1 && !unpaced && !cleaningSearch {
				// One unit is as small as a batch gets: its fan-out is the
				// unit's own, held to the unit plan rather than the batch plan.
				// The transaction is live here, so UsePlan cannot fail.
				_ = dbwork.UsePlan(tx, derivationUnitPlan)
			}
		}()
		n, domain, failed, step, retried, backfilled = 0, -1, 0, -1, false, false
		paced = -2
		var rebuilding int
		var err error
		w.sourceTurn = !w.sourceTurn
		if w.sourceTurn {
			paced = -1
			n, err = StepVisibilitySourceFanout(ctx, tx, min(limit, upstreamQuantum))
			if err != nil {
				return err
			}
			if n > 0 {
				return finishGeneration(ctx, tx)
			}
		}
		w.domainTurns++
		if w.domainTurns%4 == 0 {
			paced = -1
			n, step, err = w.runSteps(ctx, tx, w.batchFor(-1, limit))
			if err != nil || n > 0 {
				return err
			}
		}

		if domain, rebuilding, err = w.workable(ctx, tx); err != nil {
			domain = -1
			return err
		}
		if domain < 0 {
			var s int
			paced = -1
			n, s, err = w.runSteps(ctx, tx, w.batchFor(-1, limit))
			step = s
			if err != nil || n > 0 || !w.retryDue() {
				return err
			}
			// Everything else is idle and a try is due.
			err = tx.QueryRowContext(ctx, `SELECT domain,rebuilding FROM catalog_derivations st WHERE rebuilding=1
 OR EXISTS(SELECT 1 FROM catalog_dirty d WHERE d.domain=st.domain) ORDER BY served_ms,domain LIMIT 1`).Scan(&domain, &rebuilding)
			if errors.Is(err, sql.ErrNoRows) {
				// Nothing is queued or rebuilding any more, so nothing is
				// set aside either.
				domain = -1
				w.mu.Lock()
				w.poison = map[int]map[int64]bool{}
				w.retryAt, w.retryDelay = time.Time{}, 0
				w.mu.Unlock()
				return nil
			}
			if err != nil {
				domain = -1
				return err
			}
			retried = true
		}
		paced = domain
		batch := w.batchFor(domain, limit)
		if domain == DomainSearch && rebuilding == 1 {
			if err = tx.QueryRowContext(ctx, `SELECT cleaning FROM catalog_derivations WHERE domain=17`).Scan(&cleaningSearch); err != nil {
				return err
			}
			if cleaningSearch {
				// Prefix leaves and their count effects have a structural raw
				// bound. Do not size this phase from ordinary document costs.
				batch = min(limit, searchCleanupRawRows)
			}
		}
		n, failed, backfilled, err = w.drainDomain(ctx, tx, domain, rebuilding, batch, retried)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, `UPDATE catalog_derivations SET served_ms=? WHERE domain=?`, time.Now().UnixMilli(), domain)
		if err != nil {
			return err
		}
		return finishGeneration(ctx, tx)
	})
	took := time.Since(began)
	if !began.IsZero() && took > dbwork.SlowReadThreshold {
		log.Printf("Catalogue maintenance turn held the writer for %s: domain=%d lastStep=%d units=%d rows=%d backfill=%t retry=%t", took.Round(time.Millisecond), domain, step, n, rows, backfilled, retried)
	}
	if err != nil {
		w.setAside(ctx, domain, failed, step, backfilled, retried, err)
		return 0, err
	}
	if paced > -2 && !cleaningSearch {
		w.observeBatch(paced, n, took, rows)
	}
	w.mu.Lock()
	w.last = lastBatch{paced, n, took}
	w.mu.Unlock()
	if domain >= 0 {
		w.mu.Lock()
		delete(w.isolate, domain)
		delete(w.poison[domain], failed)
		if retried {
			w.retryAt, w.retryDelay = time.Time{}, 0
		}
		w.mu.Unlock()
	}
	return n, nil
}

// retryDue reports whether what was set aside may be tried again now.
func (w *Worker) retryDue() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return !w.now().Before(w.retryAt)
}

// workable is the next domain with work the Worker can finish: queued ids that
// are not set aside, or a rebuild with more source ids. A domain
// with neither waits behind the step workers. It answers -1 when there is none.
func (w *Worker) workable(ctx context.Context, tx *sql.Tx) (int, int, error) {
	next := nextDomainSQL
	waited := w.now().Add(-deferredWait).UnixMilli()
	w.mu.Lock()
	aside := map[int][]int64{}
	for domain, ids := range w.poison {
		for id := range ids {
			aside[domain] = append(aside[domain], id)
		}
	}
	w.mu.Unlock()
	if len(aside) == 0 {
		var domain, rebuilding int
		err := tx.QueryRowContext(ctx, next+` LIMIT 1`, waited).Scan(&domain, &rebuilding)
		if errors.Is(err, sql.ErrNoRows) {
			return -1, 0, nil
		}
		return domain, rebuilding, err
	}
	// One row per derived domain at most.
	rows, err := tx.QueryContext(ctx, next, waited)
	if err != nil {
		return -1, 0, err
	}
	var candidates [][2]int
	for rows.Next() {
		var c [2]int
		if err = rows.Scan(&c[0], &c[1]); err != nil {
			rows.Close()
			return -1, 0, err
		}
		candidates = append(candidates, c)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return -1, 0, err
	}
	for _, c := range candidates {
		domain, rebuilding := c[0], c[1]
		if rebuilding == 1 {
			return domain, rebuilding, nil
		}
		var queued int
		err = tx.QueryRowContext(ctx, `SELECT 1 FROM catalog_dirty WHERE domain=? AND entity_id NOT IN(SELECT value FROM json_each(?)) LIMIT 1`, domain, mustJSON(aside[domain])).Scan(&queued)
		if err == nil {
			return domain, rebuilding, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return -1, 0, err
		}
	}
	return -1, 0, nil
}

// derivationUnitPlan bounds a paced batch of a single unit. A unit (a show's,
// album's or collection's derived rows) is indivisible today, so a large one
// can exceed the maintenance plan on its own; this keeps it measured against a
// reviewed bound instead of passing unnoticed.
var derivationUnitPlan = dbwork.WritePlan{Name: "catalogue-derivation-unit", Rows: 8192, Pages: 2048, VMOps: 8_000_000, Why: "one derived unit is written atomically with its dependents; splitting a unit's fan-out is writer-discipline item H"}

// DrainPlan is the write plan of an unpaced Step, as Drain and fixtures take:
// up to 500 units in one transaction, which the server's paced loop never takes.
var DrainPlan = dbwork.WritePlan{Name: "catalogue-drain", Why: "fixtures and tools build a whole catalogue before anything reads it, with nothing else waiting on the writer; the server's loop uses paced batches inside the maintenance plan"}

// Drain runs the worker to completion: every queued id and every pending step.
// It is for fixtures, tools and tests that build a catalogue and then read it;
// the server runs the worker in the background instead.
func Drain(ctx context.Context, db *sql.DB) error {
	w := NewWorker(db)
	for {
		n, err := w.Step(ctx, 500)
		if err != nil || n == 0 {
			return err
		}
	}
}

// drainDomain runs one batch of a domain. retry is the turn of what was set
// aside for failing: one id, the next in the domain's rotation, and after the
// last of them it resumes source enumeration. It reports the id of a
// batch of one and whether the batch came from rebuild enumeration.
func (w *Worker) drainDomain(ctx context.Context, tx *sql.Tx, domain, rebuilding, limit int, retry bool) (int, int64, bool, error) {
	d, ok := derivation(domain)
	if domain == DomainSearch && rebuilding == 1 {
		var cleaning, slot int
		if err := tx.QueryRowContext(ctx, `SELECT cleaning,active_slot FROM catalog_derivations WHERE domain=17`).Scan(&cleaning, &slot); err != nil {
			return 0, 0, false, err
		}
		if cleaning == 1 {
			n, err := cleanSearchGeneration(ctx, tx, 1-slot, limit)
			return n, 0, false, err
		}
	}
	if !ok {
		// A domain queued by an older build: nothing derives it any more.
		_, err := tx.ExecContext(ctx, `DELETE FROM catalog_dirty WHERE domain=?`, domain)
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE catalog_derivations SET rebuilding=0 WHERE domain=?`, domain)
		}
		return 1, 0, false, err
	}
	w.mu.Lock()
	skip := make([]int64, 0, len(w.poison[domain]))
	for id := range w.poison[domain] {
		skip = append(skip, id)
	}
	batch := limit
	// These drains advance one durable holder/source cursor, even when the
	// caller supplies many keys. Fetching the rest of that queue every turn
	// makes a one-head walk quadratic and reports untouched keys as work.
	// Keep the raw-row limit passed to Drain unchanged; only its key window is
	// one, including rebuild enumeration and the covered-prefix proof.
	if singleHeadDomain(domain) {
		batch = 1
	}
	if w.isolate[domain] || retry {
		batch = 1
	}

	after := w.retryAfter[domain]
	assetAfter := w.assetSourceAfter
	// Alternate source enumeration and queued edits at bounded checkpoints.
	queueTurn := rebuilding == 0 || !w.backfillTurn[domain]
	if rebuilding == 1 {
		w.backfillTurn[domain] = !w.backfillTurn[domain]
	}
	w.mu.Unlock()
	var keys []Key
	var err error
	switch {
	case retry:
		const one = `SELECT entity_id,revision FROM catalog_dirty WHERE domain=? AND entity_id>? ORDER BY entity_id LIMIT 1`
		if keys, err = readKeys(tx.QueryContext(ctx, one, domain, after)); err != nil {
			return 0, 0, false, err
		}
		if len(keys) == 0 && rebuilding == 0 && after != 0 {
			if keys, err = readKeys(tx.QueryContext(ctx, one, domain, 0)); err != nil {
				return 0, 0, false, err
			}
		}
		after = 0
		if len(keys) == 1 {
			after = keys[0].ID
		}
		w.mu.Lock()
		w.retryAfter[domain] = after
		w.mu.Unlock()
	case queueTurn && domain == DomainAssetSources:
		const nextAsset = `SELECT entity_id,revision FROM catalog_dirty WHERE domain=? AND entity_id>? AND entity_id NOT IN(SELECT value FROM json_each(?)) ORDER BY entity_id LIMIT 1`
		if keys, err = readKeys(tx.QueryContext(ctx, nextAsset, domain, assetAfter, mustJSON(skip))); err != nil {
			return 0, 0, false, err
		}
		if len(keys) == 0 {
			const firstAsset = `SELECT entity_id,revision FROM catalog_dirty WHERE domain=? AND entity_id>=-9223372036854775808 AND entity_id NOT IN(SELECT value FROM json_each(?)) ORDER BY entity_id LIMIT 1`
			if keys, err = readKeys(tx.QueryContext(ctx, firstAsset, domain, mustJSON(skip))); err != nil {
				return 0, 0, false, err
			}
		}
		if len(keys) > 0 {
			w.mu.Lock()
			w.assetSourceAfter = keys[0].ID
			w.mu.Unlock()
		}
	case queueTurn:
		if keys, err = readKeys(tx.QueryContext(ctx, `SELECT entity_id,revision FROM catalog_dirty WHERE domain=? AND entity_id NOT IN(SELECT value FROM json_each(?)) ORDER BY entity_id LIMIT ?`, domain, mustJSON(skip), batch)); err != nil {
			return 0, 0, false, err
		}
	}
	backfilled := false
	if len(keys) == 0 && rebuilding == 1 {
		var cursor int64
		if err = tx.QueryRowContext(ctx, `SELECT rebuild_cursor FROM catalog_derivations WHERE domain=?`, domain).Scan(&cursor); err != nil {
			return 0, 0, false, err
		}
		ids, err := d.Backfill(ctx, tx, cursor, batch)
		if err != nil {
			return 0, 0, false, err
		}
		if len(ids) == 0 {
			_, err = tx.ExecContext(ctx, `UPDATE catalog_derivations SET rebuilding=0,rebuild_cursor=0,active_slot=CASE WHEN domain=17 AND first_build=0 THEN 1-active_slot ELSE active_slot END,first_build=0 WHERE domain=?`, domain)
			return 1, 0, true, err
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if _, err = tx.ExecContext(ctx, `UPDATE catalog_derivations SET rebuild_cursor=? WHERE domain=?`, ids[len(ids)-1], domain); err != nil {
			return 0, 0, false, err
		}
		// Enumerated ids go through the queue, so an unfinished one keeps its
		// continuation and a failing one can be isolated like any other.
		if domain == DomainFacetCounts {
			// Enumeration is not a source change. Preserve queued continuations;
			// producer touches and source-epoch changes still invalidate them.
			_, err = tx.ExecContext(ctx, `INSERT INTO catalog_dirty(domain,entity_id,revision) SELECT ?,value,1 FROM json_each(?) WHERE 1 ON CONFLICT(domain,entity_id) DO NOTHING`, domain, mustJSON(ids))
		} else {
			err = TouchTx(ctx, tx, domain, ids...)
		}
		if err != nil {
			return 0, 0, false, err
		}
		if keys, err = readKeys(tx.QueryContext(ctx, `SELECT entity_id,revision FROM catalog_dirty WHERE domain=? AND entity_id IN(SELECT value FROM json_each(?)) ORDER BY entity_id`, domain, mustJSON(ids))); err != nil {
			return 0, 0, false, err
		}
		backfilled = true
	}
	if len(keys) == 0 {
		return 0, 0, backfilled, nil
	}
	// A first import often queues the same keys a rebuild will enumerate.
	// When this queue batch covers the next source prefix, successful drains
	// also satisfy that prefix. Do not derive it a second time on the next
	// enumeration turn. Source gaps and unfinished fanouts still stop progress.
	var covered []int64
	if rebuilding == 1 && queueTurn && !retry {
		var cursor int64
		if err = tx.QueryRowContext(ctx, `SELECT rebuild_cursor FROM catalog_derivations WHERE domain=?`, domain).Scan(&cursor); err != nil {
			return 0, 0, false, err
		}
		ids, sourceErr := d.Backfill(ctx, tx, cursor, batch)
		if sourceErr != nil {
			return 0, 0, false, sourceErr
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		queued := make(map[int64]bool, len(keys))
		for _, key := range keys {
			queued[key.ID] = true
		}
		for _, id := range ids {
			if !queued[id] {
				break
			}
			covered = append(covered, id)
		}
	}
	var single int64
	if len(keys) == 1 {
		single = keys[0].ID
	}
	done, err := d.Drain(ctx, tx, keys, limit)
	if err != nil {
		return 0, single, backfilled, fmt.Errorf("catalogue domain %d: %w", domain, err)
	}
	if len(covered) > 0 {
		finished := make(map[int64]bool, len(done))
		for _, id := range done {
			finished[id] = true
		}
		var through int64
		for _, id := range covered {
			if !finished[id] {
				break
			}
			through = id
		}
		if through > 0 {
			if _, err = tx.ExecContext(ctx, `UPDATE catalog_derivations SET rebuild_cursor=? WHERE domain=?`, through, domain); err != nil {
				return 0, single, backfilled, err
			}
		}
	}
	// Acknowledge exactly the revision this batch saw; an id touched again in
	// the meantime stays queued.
	seen := make(map[int64]int64, len(keys))
	for _, k := range keys {
		seen[k.ID] = k.Revision
	}
	for _, id := range done {
		if revision, ok := seen[id]; ok {
			if err := acknowledgeDerived(ctx, tx, domain, id, revision); err != nil {
				return 0, single, backfilled, err
			}
		}
	}
	return len(keys), single, backfilled, nil
}

// Acknowledge one primary key at a time. A tuple IN over JSON can select only
// the domain prefix and walk its whole queue; a batch must cost its own keys.
// The revision guard keeps an edit queued when derivation touched it again.
func acknowledgeDerived(ctx context.Context, tx *sql.Tx, domain int, id, revision int64) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_derivation_failures WHERE domain=? AND entity_id=?`, domain, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM catalog_dirty WHERE domain=? AND entity_id=? AND revision=?`, domain, id, revision)
	return err
}

// steps are the derived workers that keep their own job tables (fed by
// triggers on the derived tables above them). They run round-robin when no
// domain has work it can finish.
const (
	bookEvidenceStep        = 4
	bookVisibleStep         = 5
	recommendationTasteStep = 7
	readingAttributionStep  = 8
)

var steps = [...]func(context.Context, *sql.Tx, int) (int, error){StepCollectionVisibleCounts, StepCategoryVisibility, StepVisibilitySortBuckets, StepPersonalSortBuckets, StepBookContextEvidence, StepBookGroupVisibleCounts, StepContentEligible, StepRecProfiles, readingAttributionStep: StepReadingAttribution, StepReadingPaths, StepVisibilitySourceFanout}

func (w *Worker) runSteps(ctx context.Context, tx *sql.Tx, limit int) (int, int, error) {
	w.mu.Lock()
	kind := w.stepTurn % len(steps)
	paced := w.holdTarget > 0
	w.mu.Unlock()
	total, idle, lastKind := 0, 0, -1
	// Stop adding steps once the batch has changed a write plan's worth of
	// rows; the next batch takes the rest.
	var before int64
	if err := tx.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&before); err != nil {
		return 0, kind, err
	}
	budget := dbwork.DefaultPlan(dbwork.ClassMaintenance).Rows
	for total < limit && idle < len(steps) {
		var changed int64
		if err := tx.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&changed); err != nil {
			return 0, kind, err
		}
		if total > 0 && changed-before >= budget {
			break
		}
		stepLimit := limit - total
		if paced {
			// One raw quantum of one family per writer. Seven-axis sort
			// replacement costs at most 37 row effects per pair; four pairs
			// fit with bookkeeping. Evidence/taste/group jobs remain atomic
			// units under their existing single-unit plan.
			stepLimit = min(stepLimit, 4)
			if kind == bookEvidenceStep || kind == bookVisibleStep || kind == recommendationTasteStep {
				stepLimit = 1
			}
		}
		if kind == readingAttributionStep {
			w.mu.Lock()
			paced := w.holdTarget > 0
			w.mu.Unlock()
			if paced {
				// Accepted-value resolution is per title. A fast unrelated step
				// must not grow the next Reading writer beyond four raw inputs.
				stepLimit = min(stepLimit, 4)
			}
		}
		n, err := steps[kind](ctx, tx, stepLimit)
		if err != nil {
			return 0, kind, err
		}
		if n > 0 {
			lastKind = kind
			total += n
			if paced || kind == readingAttributionStep || kind == readingAttributionStep+1 {
				// Its EOF, obsolete cleanup and ready publication must be
				// separate committed batches, including an empty library. Do
				// not re-enter this step in the same writer transaction.
				break
			}
			continue
		}
		idle++
		kind = (kind + 1) % len(steps)
	}
	w.mu.Lock()
	w.stepTurn = (kind + 1) % len(steps)
	w.mu.Unlock()
	return total, lastKind, nil
}

// setAside keeps one failing unit from stalling the Worker: the transaction
// rolled back, so the work is retried, but a failing domain moves behind the
// others and retries one id at a time; once isolated, the one id that still
// fails is set aside until the rest of the queue drains, the step workers are
// idle and its next try is due. Rebuild enumeration passes it after recording
// its failure. A failing step worker yields its turn.
func (w *Worker) setAside(ctx context.Context, domain int, id int64, step int, backfilled, retried bool, cause error) {
	if domain < 0 {
		if step >= 0 {
			log.Printf("Catalogue derived worker %d failed and yields its turn: %v", step, cause)
			w.mu.Lock()
			w.stepTurn = (step + 1) % len(steps)
			w.mu.Unlock()
		}
		return
	}
	w.mu.Lock()
	single := id != 0 && (w.isolate[domain] || w.poison[domain][id])
	if single {
		if w.poison[domain] == nil {
			w.poison[domain] = map[int64]bool{}
		}
		w.poison[domain][id] = true

	}
	w.isolate[domain] = true
	var again time.Duration
	if retried {
		w.retryDelay = min(max(2*w.retryDelay, retryFirst), retryMax)
		w.retryAt = w.now().Add(w.retryDelay)
		again = w.retryDelay
	}
	w.mu.Unlock()
	switch {
	case retried:
		log.Printf("Catalogue id %d in domain %d failed again; it stays queued and is tried again in %s: %v", id, domain, again, cause)
	case single:
		log.Printf("Catalogue id %d in domain %d failed; it stays queued and the rest continues: %v", id, domain, cause)
	default:
		log.Printf("Catalogue domain %d failed and retries one id at a time: %v", domain, cause)
	}
	err := dbwork.WithWriteTx(ctx, w.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		if single {
			if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_derivation_failures(domain,entity_id,reason,failed_ms) VALUES(?,?,?,?) ON CONFLICT(domain,entity_id) DO UPDATE SET reason=excluded.reason,attempts=attempts+1,failed_ms=excluded.failed_ms`, domain, id, cause.Error(), w.now().UnixMilli()); err != nil {
				return err
			}
			if backfilled {
				if err := TouchTx(ctx, tx, domain, id); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE catalog_derivations SET rebuild_cursor=max(rebuild_cursor,?) WHERE domain=?`, id, domain); err != nil {
					return err
				}
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE catalog_derivations SET served_ms=? WHERE domain=?`, time.Now().UnixMilli(), domain)
		return err
	})
	if err != nil && ctx.Err() == nil {
		log.Printf("Catalogue could not set the failed work aside: %v", err)
	}
}

func readKeys(rows *sql.Rows, err error) ([]Key, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []Key
	for rows.Next() {
		var k Key
		if err = rows.Scan(&k.ID, &k.Revision); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func keyIDs(keys []Key) []int64 {
	ids := make([]int64, len(keys))
	for i, k := range keys {
		ids[i] = k.ID
	}
	return ids
}

// scanIDs reads one column of integer ids.
func scanIDs(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func mustJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
