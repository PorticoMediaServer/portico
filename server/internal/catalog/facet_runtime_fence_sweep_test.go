package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// facetSweepQuanta is the number of producer quanta one counted BASE needs. A
// build takes one turn per quantum, so a scheduler that interleaves no-op audits
// between quanta needs facetSweepQuanta·(retained scopes + 1) turns instead.
const facetSweepQuanta = 3

// facetSweepCounter counts the observable work of one scheduling step: a source
// capture (audit or quantum), a producer quantum, and a turn that selected an
// already-ready basis (which can only be a re-verification audit).
type facetSweepCounter struct {
	mu       sync.Mutex
	captures map[string]int
	quanta   map[string]int
	audits   map[string]int
}

func newFacetSweepCounter() *facetSweepCounter {
	return &facetSweepCounter{captures: map[string]int{}, quanta: map[string]int{}, audits: map[string]int{}}
}

func (c *facetSweepCounter) bump(bucket map[string]int, source string) {
	c.mu.Lock()
	bucket[source]++
	c.mu.Unlock()
}

func (c *facetSweepCounter) capture(source string) { c.bump(c.captures, source) }
func (c *facetSweepCounter) quantum(source string) { c.bump(c.quanta, source) }
func (c *facetSweepCounter) audit(source string)   { c.bump(c.audits, source) }

func (c *facetSweepCounter) of(bucket map[string]int, source string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return bucket[source]
}

func (c *facetSweepCounter) captured(source string) int {
	return c.of(c.captures, source)
}

func (c *facetSweepCounter) quantumOf(source string) int {
	return c.of(c.quanta, source)
}

func (c *facetSweepCounter) auditOf(source string) int {
	return c.of(c.audits, source)
}

func (c *facetSweepCounter) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captures = map[string]int{}
	c.quanta = map[string]int{}
	c.audits = map[string]int{}
}

// facetSweepProducer emits one bounded fact per quantum and never changes its
// witness: only a real source or authority change may invalidate the basis.
type facetSweepProducer struct {
	counter *facetSweepCounter
	source  string
}

func (p *facetSweepProducer) Next(_ context.Context, _ *sql.Tx, _ FacetBaseSnapshot, cursor string) (FacetBaseBatch, error) {
	p.counter.quantum(p.source)
	step := 0
	if cursor != "" {
		parsed, err := strconv.Atoi(cursor)
		if err != nil {
			return FacetBaseBatch{}, ErrFacetBaseInput
		}
		step = parsed
	}
	if step >= facetSweepQuanta {
		return FacetBaseBatch{Cursor: cursor, Done: true}, nil
	}
	return FacetBaseBatch{
		Facts:  []FacetBaseFact{{Value: fmt.Sprintf("value-%d", step), Label: "Genre", Count: 1}},
		Cursor: strconv.Itoa(step + 1),
		Done:   step+1 == facetSweepQuanta,
	}, nil
}

func (p *facetSweepProducer) ValidatePublication(context.Context, *sql.Tx, FacetBaseSnapshot) error {
	return nil
}

// facetSweepFixture adds one registered counting source family to the shared
// runtime fixture. Prefix matching gives every scope its own class, witness and
// counter without a second runtime.
func facetSweepFixture(t *testing.T, n int) (*facetRuntimeFixture, *facetSweepCounter) {
	t.Helper()
	f := runtimeFixture(t, n, 128<<20)
	c := newFacetSweepCounter()
	capture := func(_ context.Context, _ compactcatalog.ReadQuery, s FacetBaseScope) (FacetBasePlan, error) {
		c.capture(s.Source)
		return FacetBasePlan{Scope: s, Witness: "facet-sweep-source-v1", Producer: &facetSweepProducer{counter: c, source: s.Source}}, nil
	}
	if err := f.r.RegisterSource("sweep-counted", capture); err != nil {
		t.Fatal(err)
	}
	return f, c
}

func facetSweepScope(f *facetRuntimeFixture, name string) FacetBaseScope {
	s := f.scope
	s.Source = "sweep-counted:" + name
	return s
}

// facetSweepDriver runs the supervised loop's scheduling path one turn at a
// time — the same turn-start writer guard, fence-keyed dirty take, single-demand
// choice and maintain call — so a bounded proof can count turns instead of
// racing a goroutine.
type facetSweepDriver struct {
	f *facetRuntimeFixture
	c *facetSweepCounter
}

func (d *facetSweepDriver) turn(ctx context.Context) error {
	if err := d.f.r.writerReady(ctx); err != nil {
		return err
	}
	demand, _ := d.f.r.schedule(d.f.r.now())
	if demand == nil {
		// No selection is only legal when nothing is due; stranding due work here
		// would hide a scheduler that never returns to a pending/retiring basis.
		d.f.r.mu.Lock()
		now := d.f.r.now()
		stranded := false
		for _, pending := range d.f.r.demands {
			if pending.due(now) {
				stranded = true
			}
		}
		d.f.r.mu.Unlock()
		if stranded {
			return errors.New("scheduler skipped due facet work")
		}
		return nil
	}
	d.f.r.mu.Lock()
	source := demand.scope.Source
	audit := demand.ready != 0 && demand.build == 0 && demand.retiring == 0
	d.f.r.mu.Unlock()
	if audit {
		d.c.audit(source)
	}
	d.f.r.maintain(ctx, demand)
	return nil
}

// until drives turns until done, treating a deferred writer (source family
// mid-change) as a retry, exactly as Run's two-second deferral does.
func (d *facetSweepDriver) until(t *testing.T, limit int, done func() bool) int {
	t.Helper()
	for turn := 0; turn < limit; turn++ {
		if done() {
			return turn
		}
		if err := d.turn(context.Background()); err != nil {
			if errors.Is(err, errFacetRuntimeDeferred) {
				continue
			}
			t.Fatal(err)
		}
	}
	t.Fatalf("facet scheduler did not converge in %d turns", limit)
	return limit
}

func facetSweepReady(f *facetRuntimeFixture, scope FacetBaseScope) func() bool {
	return func() bool { return f.r.demands[scope].ready != 0 }
}

// warmFacetSweepBases builds and retains n ready bases and returns their scopes.
func warmFacetSweepBases(t *testing.T, f *facetRuntimeFixture, d *facetSweepDriver, n int) []FacetBaseScope {
	t.Helper()
	scopes := make([]FacetBaseScope, n)
	for i := range scopes {
		scopes[i] = facetSweepScope(f, fmt.Sprintf("ready-%02d", i))
		if _, err := f.r.demand(scopes[i]); err != nil {
			t.Fatal(err)
		}
		d.until(t, 200, facetSweepReady(f, scopes[i]))
	}
	return scopes
}

// TestFacetRuntimeSelfWakeDoesNotRescanReadyBases is the regression for the cold
// wait: a committed BASE quantum names the producer tables in its own reads, so
// WakeOnTables wakes this runtime's dirty signal and the next turn used to treat
// that as "len(demands) things changed". The budget is now keyed to the producer
// and authority fence, so one more cold scope costs the same work with two or
// eight retained ready bases and never re-captures an already-verified one.
func TestFacetRuntimeSelfWakeDoesNotRescanReadyBases(t *testing.T) {
	if testing.Short() {
		t.Skip("measured slow fixture; full deterministic coverage runs in the merge queue")
	}
	type measurement struct {
		turns, targetCaptures int
	}
	measure := func(tb *testing.T, n int) measurement {
		f, c := facetSweepFixture(tb, 3)
		d := &facetSweepDriver{f: f, c: c}
		// The supervised loop's own subscription: a committed quantum reads the
		// fence tables inside its writer transaction, so it wakes this signal
		// again. This is the measured self-wake, not an injected one.
		off := dbwork.WakeOnTables(f.r.dirty, facetRuntimeWakeTables...)
		defer off()

		retained := warmFacetSweepBases(tb, f, d, n)
		before := make(map[string]int64, n)
		for _, scope := range retained {
			before[scope.Source] = f.r.demands[scope].ready
			if before[scope.Source] == 0 {
				tb.Fatalf("retained basis %s never became ready", scope.Source)
			}
		}
		c.reset()

		target := facetSweepScope(f, "cold-target")
		if _, err := f.r.demand(target); err != nil {
			tb.Fatal(err)
		}
		targetSource := target.Source
		turns := d.until(tb, 400, facetSweepReady(f, target))

		for _, scope := range retained {
			if got := c.captured(scope.Source); got != 0 {
				tb.Fatalf("ready basis %s was re-captured %d times during one cold build", scope.Source, got)
			}
			if got := c.auditOf(scope.Source); got != 0 {
				tb.Fatalf("ready basis %s was audited %d times during one cold build", scope.Source, got)
			}
			if got := c.quantumOf(scope.Source); got != 0 {
				tb.Fatalf("ready basis %s was rebuilt during one cold build", scope.Source)
			}
			if got := f.r.demands[scope].ready; got != before[scope.Source] {
				tb.Fatalf("ready basis %s changed identity %d -> %d", scope.Source, before[scope.Source], got)
			}
		}
		quanta := c.quantumOf(targetSource)
		if quanta != facetSweepQuanta {
			tb.Fatalf("cold build needed %d producer quanta, want %d", quanta, facetSweepQuanta)
		}
		captures := c.captured(targetSource)
		if captures > facetSweepQuanta+2 {
			tb.Fatalf("cold build captured its source %d times for %d quanta", captures, quanta)
		}
		// This is the pre-fix amplification in one call: a busy turn used to walk
		// straight into a ready, idle basis. It must now select nothing at all.
		if picked := f.r.nextDemand(false); picked != nil {
			tb.Fatalf("busy turn still selected verified ready basis %s", picked.scope.Source)
		}
		return measurement{turns: turns, targetCaptures: captures}
	}

	small := measure(t, 2)
	large := measure(t, 8)
	if small.turns != large.turns {
		t.Fatalf("cold build turns grew with retained scopes: 2 -> %d, 8 -> %d", small.turns, large.turns)
	}
	if small.targetCaptures != large.targetCaptures {
		t.Fatalf("cold build captures grew with retained scopes: 2 -> %d, 8 -> %d", small.targetCaptures, large.targetCaptures)
	}
	if large.turns > facetSweepQuanta+1 {
		t.Fatalf("cold build took %d turns for %d quanta with 8 retained bases", large.turns, facetSweepQuanta)
	}
}

// TestFacetRuntimeFenceSweepReverifiesEveryReadyBaseOnce is the positive half: a
// real source change and an authority-only policy change must still re-verify
// every retained ready basis, exactly once each. Without it the flat measurement
// above could be satisfied by silently disabling refresh.
func TestFacetRuntimeFenceSweepReverifiesEveryReadyBaseOnce(t *testing.T) {
	const retained = 4
	cases := []struct {
		name   string
		change func(t *testing.T, f *facetRuntimeFixture)
	}{
		{"source", func(t *testing.T, f *facetRuntimeFixture) {
			f.c.Genres(f.item.ID, "fixture", "Sweep change")
			f.c.Drain()
		}},
		{"policy", func(t *testing.T, f *facetRuntimeFixture) {
			// Authority-only: the policy generation moves, the content fence does
			// not. A sweep keyed to the producer fence alone would miss this, so the
			// change goes through the gated writer (and its wake) like production.
			f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE catalog_facet_source_state SET policy_epoch=policy_epoch+1 WHERE singleton=1`)
				return err
			})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, c := facetSweepFixture(t, 3)
			maxAge := 100
			f.scope.Policy = FacetBasePolicy(identity.ContentRestrictions{MaximumAge: &maxAge})
			d := &facetSweepDriver{f: f, c: c}
			off := dbwork.WakeOnTables(f.r.dirty, facetRuntimeWakeTables...)
			defer off()

			scopes := warmFacetSweepBases(t, f, d, retained)
			c.reset()
			tc.change(t, f)
			if tc.name == "policy" {
				// The content fence must be UNCHANGED here, or the case would prove
				// nothing about authority-only changes.
				f.r.mu.Lock()
				verified := f.r.swept.producer
				f.r.mu.Unlock()
				current, err := compactcatalog.CaptureFacetProducerFence(context.Background(), f.c.DB)
				if err != nil || current != verified {
					t.Fatalf("policy-only change moved the producer fence: %v", err)
				}
				// Retained facts bound to the old policy generation must not be
				// served while the replacement prepares.
				for _, scope := range scopes {
					if _, err := f.r.active(context.Background(), scope); !errors.Is(err, compactcatalog.ErrBuilding) {
						t.Fatalf("old policy facts still active for %s: %v", scope.Source, err)
					}
				}
			}

			verifiedAll := func() bool {
				for _, scope := range scopes {
					if c.auditOf(scope.Source) == 0 {
						return false
					}
				}
				return true
			}
			d.until(t, 400, verifiedAll)
			for _, scope := range scopes {
				if got := c.auditOf(scope.Source); got != 1 {
					t.Fatalf("ready basis %s audited %d times for one change, want 1", scope.Source, got)
				}
			}
		})
	}
}

// TestFacetRuntimeFenceSweepKeepsWriterActivationGuards proves the narrow sweep
// did not touch writer admission: an active scan still refuses every BASE write,
// and the same demand completes once it clears.
func TestFacetRuntimeFenceSweepKeepsWriterActivationGuards(t *testing.T) {
	f, c := facetSweepFixture(t, 3)
	d := &facetSweepDriver{f: f, c: c}
	off := dbwork.WakeOnTables(f.r.dirty, facetRuntimeWakeTables...)
	defer off()

	scope := facetSweepScope(f, "guarded")
	if _, err := f.r.demand(scope); err != nil {
		t.Fatal(err)
	}
	f.c.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('sweep-scan','films','running','2026-10-28')`)
	if err := f.r.writerReady(context.Background()); !errors.Is(err, errFacetRuntimeDeferred) {
		t.Fatalf("writer admission granted under an active scan: %v", err)
	}
	publications := dbwork.Publications()
	if err := d.turn(context.Background()); !errors.Is(err, errFacetRuntimeDeferred) {
		t.Fatalf("turn ran under an active scan: %v", err)
	}
	if dbwork.Publications() != publications {
		t.Fatal("refused turn published BASE state")
	}
	demand := f.r.demands[scope]
	if demand.ready != 0 || demand.build != 0 || demand.retiring != 0 {
		t.Fatalf("refused turn installed BASE work: %+v", demand)
	}

	f.c.Exec(`DELETE FROM jobs WHERE id='sweep-scan'`)
	d.until(t, 200, facetSweepReady(f, scope))
	if got := c.quantumOf(scope.Source); got != facetSweepQuanta {
		t.Fatalf("guarded demand needed %d quanta after the scan cleared, want %d", got, facetSweepQuanta)
	}
}

// TestFacetRuntimeTerminalDemandIsNeverRearmedOrDue pins the invariant that keeps
// a fixed terminal BASIS result from becoming a permanent 1 ms queue. A demand can
// retain a ready basis and still be terminal — maintenanceFailure produces exactly
// that shape — so a fence move must not arm it and it must not be due, while its
// retirement must still progress.
func TestFacetRuntimeTerminalDemandIsNeverRearmedOrDue(t *testing.T) {
	f, c := facetSweepFixture(t, 3)
	d := &facetSweepDriver{f: f, c: c}
	off := dbwork.WakeOnTables(f.r.dirty, facetRuntimeWakeTables...)
	defer off()

	scope := facetSweepScope(f, "terminal")
	if _, err := f.r.demand(scope); err != nil {
		t.Fatal(err)
	}
	d.until(t, 200, facetSweepReady(f, scope))
	demand := f.r.demands[scope]
	base := demand.ready
	c.reset()

	// The shape maintenanceFailure produces for a non-retryable read: the ready
	// basis stays, the result becomes fixed.
	f.r.maintenanceFailure(context.Background(), demand, 0, ErrFacetBaseInput)
	f.r.mu.Lock()
	frozen := demand.err
	armed, ready := demand.verify, demand.ready
	f.r.mu.Unlock()
	if frozen == nil || ready != base || armed {
		t.Fatalf("terminal result did not freeze the demand: err=%v ready=%d verify=%t", frozen, ready, armed)
	}

	// A real producer and policy generation move must not re-arm it.
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE catalog_facet_source_state SET source_epoch=source_epoch+1,policy_epoch=policy_epoch+1 WHERE singleton=1`)
		return err
	})
	// The baseline is taken after this fixture's own invalidation write: that
	// gated transaction commits one batch and so legitimately publishes
	// (dbwork.Publications counts committed gated writes). It is not runtime
	// work. Baseline and assertion still bracket the entire no-work window —
	// writer guard, schedule, due check and both selections below — so any
	// capture, quantum or publication performed there still fails the test.
	publications := dbwork.Publications()
	if err := f.r.writerReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, busy := f.r.schedule(f.r.now())
	if busy {
		t.Fatal("terminal demand kept the scheduler busy")
	}
	f.r.mu.Lock()
	armed, ready, still := demand.verify, demand.ready, demand.err
	f.r.mu.Unlock()
	if armed || ready != base || still != frozen {
		t.Fatalf("fence move re-armed a terminal demand: verify=%t ready=%d err=%v", armed, ready, still)
	}
	if demand.due(f.r.now()) {
		t.Fatal("terminal demand was due without retirement work")
	}
	for _, pendingOnly := range []bool{true, false} {
		if picked := f.r.nextDemand(pendingOnly); picked != nil {
			t.Fatalf("terminal demand selected on pendingOnly=%t: %s", pendingOnly, picked.scope.Source)
		}
	}
	if c.captured(scope.Source) != 0 || c.quantumOf(scope.Source) != 0 || dbwork.Publications() != publications {
		t.Fatal("terminal demand performed work")
	}

	// Retirement is the one thing that must still progress. The base must not be
	// the active image for the bounded retire step to accept it.
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE catalog_facet_base_classes SET active_base=NULL WHERE active_base=?`, base)
		return err
	})
	f.r.mu.Lock()
	demand.retiring = base
	f.r.mu.Unlock()
	if !demand.due(f.r.now()) {
		t.Fatal("terminal retirement was not due")
	}
	d.until(t, 100, func() bool {
		f.r.mu.Lock()
		defer f.r.mu.Unlock()
		return demand.retiring == 0
	})
	f.r.mu.Lock()
	armed, ready, still = demand.verify, demand.ready, demand.err
	f.r.mu.Unlock()
	if armed || ready != base || still != frozen {
		t.Fatalf("retirement disturbed the terminal result: verify=%t ready=%d err=%v", armed, ready, still)
	}
	if demand.due(f.r.now()) {
		t.Fatal("reclaimed terminal demand was due again")
	}
}

// TestFacetRuntimeMidTurnFenceMoveIsStillReverified covers the mark/clear race:
// schedule runs first, then maintain/collect/writeBase run the writer guard again
// and overwrite r.fence, then an audit can clear a verification. A change landing
// in that window must still re-verify every ready basis on the next turn.
func TestFacetRuntimeMidTurnFenceMoveIsStillReverified(t *testing.T) {
	f, c := facetSweepFixture(t, 3)
	d := &facetSweepDriver{f: f, c: c}
	off := dbwork.WakeOnTables(f.r.dirty, facetRuntimeWakeTables...)
	defer off()

	scopes := warmFacetSweepBases(t, f, d, 2)
	c.reset()

	// A quiet turn: nothing due, nothing swept.
	if err := f.r.writerReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if picked, _ := f.r.schedule(f.r.now()); picked != nil {
		t.Fatalf("quiet turn selected %s", picked.scope.Source)
	}

	// The change lands between schedule and the rest of the turn. The guard that
	// maintain/collect run next overwrites r.fence with the moved state.
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE catalog_facet_source_state SET source_epoch=source_epoch+1 WHERE singleton=1`)
		return err
	})
	if err := f.r.writerReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The audit that follows in the same turn matches — this scope's witness and
	// authority did not move — so it clears its own verification. That must not
	// swallow the move observed around it.
	audited := scopes[0]
	f.r.maintain(context.Background(), f.r.demands[audited])
	if got := c.captured(audited.Source); got != 1 {
		t.Fatalf("mid-turn audit captured %d times, want 1", got)
	}

	d.until(t, 100, func() bool {
		for _, scope := range scopes {
			if c.auditOf(scope.Source) == 0 {
				return false
			}
		}
		return true
	})
	for _, scope := range scopes {
		if got := c.auditOf(scope.Source); got != 1 {
			t.Fatalf("ready basis %s audited %d times after a mid-turn move, want 1", scope.Source, got)
		}
	}
	if got := c.captured(audited.Source); got != 2 {
		t.Fatalf("mid-turn move was not re-read: %d captures", got)
	}
	if got := c.captured(scopes[1].Source); got != 1 {
		t.Fatalf("unrelated ready basis captured %d times, want 1", got)
	}
}
