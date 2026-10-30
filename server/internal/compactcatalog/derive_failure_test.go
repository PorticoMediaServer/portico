package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

// failingDomain is a derived domain registered only for a test: it finishes
// every id that is not bad, and a rebuild enumerates all.
const failingDomain = 97

type failingDerivation struct {
	mu       sync.Mutex
	bad      map[int64]bool
	finished map[int64]bool
	attempts map[int64]int
}

func (f *failingDerivation) recover(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.bad, id)
}

func (f *failingDerivation) done(id int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.finished[id]
}

// tried is how many batches of one id alone were attempted.
func (f *failingDerivation) tried(id int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts[id]
}

func registerFailingDomain(t *testing.T, all []int64, bad ...int64) *failingDerivation {
	t.Helper()
	f := &failingDerivation{bad: map[int64]bool{}, finished: map[int64]bool{}, attempts: map[int64]int{}}
	for _, id := range bad {
		f.bad[id] = true
	}
	Register(failingDomain, Derivation{
		Version: 1,
		Drain: func(_ context.Context, _ *sql.Tx, keys []Key, _ int) ([]int64, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(keys) == 1 {
				f.attempts[keys[0].ID]++
			}
			for _, k := range keys {
				if f.bad[k.ID] {
					return nil, errors.New("this id never derives")
				}
			}
			for _, k := range keys {
				f.finished[k.ID] = true
			}
			return keyIDs(keys), nil
		},
		Backfill: func(_ context.Context, _ *sql.Tx, after int64, limit int) ([]int64, error) {
			var ids []int64
			for _, id := range all {
				if id > after && len(ids) < limit {
					ids = append(ids, id)
				}
			}
			return ids, nil
		},
	})
	t.Cleanup(func() {
		registry.Lock()
		delete(registry.domains, failingDomain)
		registry.Unlock()
	})
	return f
}

// steppedClock is a Worker's clock a test moves by hand.
type steppedClock struct{ at time.Time }

func (c *steppedClock) now() time.Time { return c.at }

// stepPastFailures runs the worker the way its loop does, a failed Step being
// retried, and returns how many Steps failed.
func stepPastFailures(t *testing.T, w *Worker, steps int) int {
	t.Helper()
	failures := 0
	for i := 0; i < steps; i++ {
		if _, err := w.Step(context.Background(), BatchLimit); err != nil {
			failures++
		}
	}
	return failures
}

func failingFixture(t *testing.T, name string) (*sql.DB, int64) {
	t.Helper()
	db := cc1OpenDB(t, name)
	tv := cc1Library(t, db, "tv", "TV", "tv", "/tv")
	show := cc1Entity(t, db, Entity{Library: tv, Kind: Show, Key: "show:show", Title: "Show", Year: 2020}, map[string]any{"local_key": "show"})
	cc1Drain(t, db)
	return db, show
}

func queuedJobs(t *testing.T, db *sql.DB) int {
	t.Helper()
	return cc1Scalar(t, db, `SELECT COUNT(*) FROM catalog_content_eligible_jobs`)
}

// An id whose derivation always fails stays queued, but the step workers and
// every other id keep going behind it, and it is tried again on a schedule
// that backs off rather than on every pass.
func TestStepWorkersRunBehindAnIDThatNeverDerives(t *testing.T) {
	db, show := failingFixture(t, "failing-id.sqlite")
	f := registerFailingDomain(t, nil, 2)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return TouchTx(ctx, tx, failingDomain, 1, 2, 3)
	})
	clock := &steppedClock{at: time.Unix(1_800_000_000, 0)}
	w := NewWorker(db)
	w.now = clock.now
	if stepPastFailures(t, w, 12) == 0 {
		t.Fatal("the failing id was never reported")
	}
	if !f.done(1) || !f.done(3) {
		t.Fatal("ids beside the failing one were not derived")
	}
	queueContentEligible(t, db, show)
	tried := f.tried(2)
	if failures := stepPastFailures(t, w, 12); failures != 0 || f.tried(2) != tried {
		t.Fatalf("the failing id was tried %d more times before its next turn was due (%d failed steps)", f.tried(2)-tried, failures)
	}
	if n := queuedJobs(t, db); n != 0 {
		t.Fatalf("a step worker's job waited behind the failing id: %d queued", n)
	}
	if n := cc1Scalar(t, db, `SELECT COUNT(*) FROM catalog_dirty WHERE domain=? AND entity_id=2`, failingDomain); n != 1 {
		t.Fatalf("the failing id left the queue: %d rows", n)
	}
	// Each failed try waits longer than the last, up to a ceiling.
	var waits []time.Duration
	for i := 0; i < 12; i++ {
		wait := w.retryWait()
		if wait <= 0 {
			t.Fatalf("no next try is scheduled for the failing id after %d tries", i)
		}
		waits = append(waits, wait)
		clock.at = clock.at.Add(wait - time.Millisecond)
		if stepPastFailures(t, w, 3) != 0 {
			t.Fatal("the failing id was tried before it was due")
		}
		clock.at = clock.at.Add(time.Millisecond)
		if stepPastFailures(t, w, 3) != 1 {
			t.Fatalf("the failing id was not tried exactly once when due (try %d)", i)
		}
	}
	if waits[0] != retryFirst || waits[1] != 2*retryFirst || waits[len(waits)-1] != retryMax {
		t.Fatalf("waits between tries: %v", waits)
	}
	// Once it derives, it leaves the queue and nothing more is scheduled.
	f.recover(2)
	clock.at = clock.at.Add(retryMax)
	if stepPastFailures(t, w, 3) != 0 || !f.done(2) || w.retryWait() != 0 {
		t.Fatalf("the id that now derives: done=%v, next try in %v", f.done(2), w.retryWait())
	}
	if n := cc1Scalar(t, db, `SELECT COUNT(*) FROM catalog_dirty WHERE domain=?`, failingDomain); n != 0 {
		t.Fatalf("%d ids are still queued", n)
	}
}

// Two ids that fail take turns being tried again, so the one whose cause is
// repaired is not kept waiting by the one that never recovers.
func TestSetAsideIDsTakeTurns(t *testing.T) {
	db, _ := failingFixture(t, "failing-pair.sqlite")
	f := registerFailingDomain(t, nil, 2, 4)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return TouchTx(ctx, tx, failingDomain, 1, 2, 3, 4, 5)
	})
	clock := &steppedClock{at: time.Unix(1_800_000_000, 0)}
	w := NewWorker(db)
	w.now = clock.now
	stepPastFailures(t, w, 30)
	for _, id := range []int64{1, 3, 5} {
		if !f.done(id) {
			t.Fatalf("id %d beside the failing ones was not derived", id)
		}
	}
	f.recover(4)
	for i := 0; i < 4 && !f.done(4); i++ {
		clock.at = clock.at.Add(retryMax)
		stepPastFailures(t, w, 3)
	}
	if !f.done(4) {
		t.Fatal("the higher id was never tried again behind the lower one that still fails")
	}
	if f.done(2) || cc1Scalar(t, db, `SELECT COUNT(*) FROM catalog_dirty WHERE domain=? AND entity_id=2`, failingDomain) != 1 {
		t.Fatal("the id that still fails left the queue")
	}
}

// A rebuild skips a failed title, finishes, and retries that title later.
func TestPoisonRebuildFinishesWhileOtherWorkContinues(t *testing.T) {
	db, show := failingFixture(t, "failing-rebuild.sqlite")
	f := registerFailingDomain(t, []int64{1, 2, 3, 4, 5}, 3)
	clock := &steppedClock{at: time.Unix(1_800_000_000, 0)}
	w := NewWorker(db)
	w.now = clock.now
	stepPastFailures(t, w, 40)
	rebuilding := func() int {
		return cc1Scalar(t, db, `SELECT rebuilding FROM catalog_derivations WHERE domain=?`, failingDomain)
	}
	if rebuilding() != 0 || CheckReadiness(context.Background(), db, failingDomain) != nil {
		t.Fatal("a failed title prevented rebuild publication")
	}
	if !f.done(1) || !f.done(2) || !f.done(4) || !f.done(5) {
		t.Fatalf("the rebuild went past the id it could not derive: %v", f.finished)
	}
	queueContentEligible(t, db, show)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return TouchTx(ctx, tx, failingDomain, 9)
	})
	tried := f.tried(3)
	if failures := stepPastFailures(t, w, 12); failures != 0 || f.tried(3) != tried {
		t.Fatalf("the stalled rebuild was tried %d more times before its next turn was due (%d failed steps)", f.tried(3)-tried, failures)
	}
	if n := queuedJobs(t, db); n != 0 {
		t.Fatalf("a step worker's job waited behind the stalled rebuild: %d queued", n)
	}
	if !f.done(9) {
		t.Fatal("an id queued in the rebuilding domain waited behind the stalled rebuild")
	}
	if rebuilding() != 0 {
		t.Fatal("queued work restarted a completed rebuild")
	}
	f.recover(3)
	for i := 0; i < 6 && !f.done(3); i++ {
		clock.at = clock.at.Add(retryMax)
		stepPastFailures(t, w, 12)
	}
	if rebuilding() != 0 || CheckReadiness(context.Background(), db, failingDomain) != nil {
		t.Fatal("the rebuild did not finish once its id derived")
	}
	for _, id := range []int64{1, 2, 3, 4, 5} {
		if !f.done(id) {
			t.Fatalf("id %d was not rebuilt", id)
		}
	}
}

// The supervised loop is woken by every catalogue commit, its own included. An
// id that never derives must still be tried on the Worker's own schedule, not
// once per wake.
func TestSupervisedLoopTriesAFailingIDOnItsOwnSchedule(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, show := failingFixture(t, "failing-loop.sqlite")
	f := registerFailingDomain(t, nil, 2)
	first, max, floor := retryFirst, retryMax, worker.MinSignalInterval
	retryFirst, retryMax, worker.MinSignalInterval = 400*time.Millisecond, 400*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { retryFirst, retryMax, worker.MinSignalInterval = first, max, floor })
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return TouchTx(ctx, tx, failingDomain, 1, 2, 3)
	})
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	supervise.Go("test.catalogue-derived", func() {
		defer close(stopped)
		NewWorker(db).Run(ctx)
	})
	defer func() {
		cancel()
		<-stopped
	}()
	// Other writers keep committing to the catalogue the whole time.
	started := time.Now()
	for i := int64(0); time.Since(started) < 1500*time.Millisecond; i++ {
		if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
			return TouchTx(ctx, tx, failingDomain, 100+i)
		}); err != nil {
			t.Fatal(err)
		}
		if i == 20 {
			queueContentEligible(t, db, show)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !f.done(1) || !f.done(3) || !f.done(100) {
		t.Fatal("ids beside the failing one were not derived")
	}
	for deadline := time.Now().Add(5 * time.Second); queuedJobs(t, db) != 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("a step worker's job waited behind the failing id")
		}
	}
	// About 150 wakes arrived. Finding the id costs a few tries, and each
	// later one waits out the Worker's own interval.
	if tried := f.tried(2); tried < 2 || tried > 12 {
		t.Fatalf("the failing id was tried %d times in a second and a half", tried)
	}
}
