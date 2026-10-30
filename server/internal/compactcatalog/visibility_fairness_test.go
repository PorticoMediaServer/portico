package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

// Workers keep the accepted visibility source queue live next to ordinary
// derived work. Step alternates the source pass with domain and step turns, so
// a continuously busy source queue cannot starve queued domain work, and busy
// domain work cannot starve the source queue. Both cursors must advance while
// the other side still has work.
func TestWorkerStepAlternatesSourceAndDomainFairness(t *testing.T) {
	db := cc1OpenDB(t, "visibility-fairness.sqlite")
	lib := cc1Library(t, db, "fairness", "Fairness", "movie", "/fairness")
	ids := make([]int64, 16)
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		for i := range ids {
			id, _, err := UpsertEntityTx(ctx, tx, Entity{Library: lib, Kind: Movie,
				Key: fmt.Sprintf("movie:fairness:%02d", i), Title: fmt.Sprintf("Fairness %02d", i), Year: 2000 + i})
			if err != nil {
				return err
			}
			asset, _, err := UpsertAssetTx(ctx, tx, Asset{Path: fmt.Sprintf("/fairness/%02d.mkv", i), Size: 1000, Duration: float64(600 + i)})
			if err != nil {
				return err
			}
			if err = LinkAssetTx(ctx, tx, id, asset, Link{}); err != nil {
				return err
			}
			ids[i] = id
		}
		return nil
	})
	cc1Drain(t, db)
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_browse_rows WHERE library_id=?`, lib); n != len(ids) {
		t.Fatalf("fixture has %d browse rows, want %d", n, len(ids))
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_source_jobs`); n != 0 {
		t.Fatalf("source queue started with %d keys", n)
	}
	if n := cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty`); n != 0 {
		t.Fatalf("derived queue started with %d keys", n)
	}

	// Source work: a browse-row value the other row triggers ignore queues one
	// coalesced kind-1 key per title.
	for _, id := range ids {
		cc1Exec(t, db, `UPDATE catalog_browse_rows SET random_key=random_key+1 WHERE entity_id=?`, id)
	}
	sourceKeys := func() int {
		return cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_source_jobs`)
	}
	if n := sourceKeys(); n < len(ids) {
		t.Fatalf("queued %d source keys, want at least %d", n, len(ids))
	}

	// Domain work: one item-metric key per title. Unlike an attribute edit this
	// queues no second source kind, so both cursors stay measurable.
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return TouchTx(ctx, tx, DomainItemMetrics, ids...)
	})
	domainKeys := func() int {
		return cc1Scalar(t, db, `SELECT count(*) FROM catalog_dirty WHERE domain=?`, DomainItemMetrics)
	}
	if n := domainKeys(); n < len(ids) {
		t.Fatalf("queued %d item-metric keys, want at least %d", n, len(ids))
	}

	metricCursor := func() string {
		var cursor string
		if err := db.QueryRow(`SELECT COALESCE(group_concat(item_id||':'||phase||':'||after_asset||':'||after_entity||':'||after_source,'|'),'') FROM catalog_item_metric_jobs`).Scan(&cursor); err != nil {
			t.Fatal(err)
		}
		return cursor
	}
	w := NewWorker(db)
	sourceProgress, domainProgress := 0, 0
	sourceWhileDomain, domainWhileSource := false, false
	for i := 0; i < 12; i++ {
		beforeSource, beforeDomain := sourceKeys(), domainKeys()
		beforeCursor := metricCursor()
		n, err := w.Step(context.Background(), 1)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Fatalf("worker stalled with %d source keys and %d item-metric keys queued", beforeSource, beforeDomain)
		}
		afterSource, afterDomain := sourceKeys(), domainKeys()
		if afterSource < beforeSource {
			sourceProgress++
			if beforeDomain > 0 {
				sourceWhileDomain = true
			}
		}
		if afterDomain < beforeDomain || metricCursor() != beforeCursor {
			domainProgress++
			if beforeSource > 0 {
				domainWhileSource = true
			}
		}
	}
	if sourceProgress < 2 {
		t.Fatalf("source queue advanced in %d of 12 steps", sourceProgress)
	}
	if domainProgress < 2 {
		t.Fatalf("item-metric dirty cursor advanced in %d of 12 steps", domainProgress)
	}
	if !domainWhileSource {
		t.Fatal("domain work made no progress while source keys stayed queued")
	}
	if !sourceWhileDomain {
		t.Fatal("source work made no progress while domain work stayed queued")
	}
	if n := sourceKeys(); n == 0 {
		t.Fatal("the source queue drained before the fairness window ended, so domain work ran without source pressure")
	}
	if n := domainKeys(); n == 0 {
		t.Fatal("the domain queue drained before the fairness window ended, so source work ran without domain pressure")
	}
}

// Facet producer readiness reads the exact accepted visibility frontier rather
// than a stubbed check. A non-retaining generation accepts everything below the
// earliest owed source event, so a pending key does not hold its own accepted
// frontier open and later churn cannot lower it. A retaining generation pins
// the fence at the visibility target its build captured. Both keep the existing
// upstream dirty and failure barriers.
func TestFacetReadinessFrontierBarrierAndRetainedTarget(t *testing.T) {
	db := cc1OpenDB(t, "facet-visibility-frontier.sqlite")
	lib := cc1Library(t, db, "frontier", "Frontier", "movie", "/frontier")
	entity := facetTestEntity(t, db, lib, "movie:frontier")
	cc1Drain(t, db)
	ctx := context.Background()

	sequence := func() int64 {
		clock, err := VisibilitySourceClock(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		return clock
	}
	// seed accepts one source event without expanding it: the raw write API
	// would also queue domain and row work, which would mask the exact source
	// barrier under test. The frontier itself is still read by the production
	// CaptureFacetProducerFence/ValidateFacetProducerFence path.
	seed := func(subject, seq int64) {
		cc1Exec(t, db, `INSERT INTO compact_visibility_source_jobs(kind,library_id,subject_id,sequence,first_pending) VALUES(?,?,?,?,?)`,
			1, lib, subject, seq, seq)
		cc1Exec(t, db, `UPDATE compact_visibility_source_state SET sequence=max(sequence,?) WHERE id=1`, seq)
	}
	readyAt := func(target int64) bool {
		var ready bool
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			ready, err = VisibilitySourceTargetReadyTx(ctx, tx, target)
			return err
		})
		return ready
	}
	validate := func(fence FacetProducerFence) error {
		var err error
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			err = ValidateFacetProducerFence(ctx, tx, fence)
			return nil
		})
		return err
	}

	// A settled catalogue is ready and its fence is the accepted clock.
	settled, err := CaptureFacetProducerFence(ctx, db)
	if err != nil {
		t.Fatalf("settled catalogue not ready: %v", err)
	}
	if settled.VisibilitySequence != sequence() {
		t.Fatalf("settled fence %d, accepted clock %d", settled.VisibilitySequence, sequence())
	}
	if err = CheckNormalizedFacetReadiness(ctx, db); err != nil {
		t.Fatalf("settled catalogue not ready: %v", err)
	}

	base := sequence()
	first := base + 1
	seed(entity, first)
	fence, err := CaptureFacetProducerFence(ctx, db)
	if err != nil {
		t.Fatalf("one accepted-but-unexpanded event blocked readiness: %v", err)
	}
	if fence.VisibilitySequence != first-1 {
		t.Fatalf("non-retaining frontier %d, want the accepted source frontier %d", fence.VisibilitySequence, first-1)
	}
	if err = CheckNormalizedFacetReadiness(ctx, db); err != nil {
		t.Fatalf("the earliest owed event must not block its own accepted frontier: %v", err)
	}

	// Later churn stays owed without moving the accepted frontier backwards.
	later := first + 7
	seed(entity+1, later)
	churned, err := CaptureFacetProducerFence(ctx, db)
	if err != nil {
		t.Fatalf("later source churn blocked readiness: %v", err)
	}
	if churned.VisibilitySequence != first-1 {
		t.Fatalf("later churn moved the frontier %d -> %d", first-1, churned.VisibilitySequence)
	}

	// Existing upstream gates are preserved: the frontier projection never
	// discards a dirty or failed producer family.
	cc1Exec(t, db, `INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(33,?,1)`, entity)
	blockedFence, err := CaptureFacetProducerFence(ctx, db)
	if !errors.Is(err, ErrBuilding) {
		t.Fatalf("existing upstream dirty gate accepted: %v", err)
	}
	if blockedFence.VisibilitySequence != first-1 {
		t.Fatalf("barrier lost its frontier %d while preserving another gate", blockedFence.VisibilitySequence)
	}
	if err = CheckNormalizedFacetReadiness(ctx, db); !errors.Is(err, ErrBuilding) {
		t.Fatalf("normalized readiness bypassed the upstream dirty gate: %v", err)
	}
	cc1Exec(t, db, `DELETE FROM catalog_dirty WHERE domain=33 AND entity_id=?`, entity)
	if err = CheckNormalizedFacetReadiness(ctx, db); err != nil {
		t.Fatalf("frontier not ready again after the upstream gate cleared: %v", err)
	}
	cc1Exec(t, db, `INSERT INTO catalog_derivation_failures(domain,entity_id,reason,attempts,failed_ms) VALUES(33,?,'test',1,0)`, entity)
	if _, err = CaptureFacetProducerFence(ctx, db); !errors.Is(err, ErrFacetProducerFailed) {
		t.Fatalf("existing producer failure hidden by the frontier: %v", err)
	}
	cc1Exec(t, db, `DELETE FROM catalog_derivation_failures WHERE domain=33 AND entity_id=?`, entity)

	// The fence binds the source image: appended events do not invalidate it,
	// an edit to the earliest owed event does.
	fence, err = CaptureFacetProducerFence(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	seed(entity+2, first+8)
	if err = validate(fence); err != nil {
		t.Fatalf("later churn invalidated an accepted frontier: %v", err)
	}
	cc1Exec(t, db, `UPDATE compact_visibility_source_jobs SET first_pending=? WHERE kind=1 AND library_id=? AND subject_id=?`, first-3, lib, entity)
	if err = validate(fence); !errors.Is(err, ErrFacetProducerChanged) {
		t.Fatalf("an edit to the earliest owed event was not a source change: %v", err)
	}

	// The exact pending barrier is first_pending<=target: the event exactly at
	// a target is owed, the same target one below it is covered.
	if !readyAt(first - 4) {
		t.Fatalf("target %d below the earliest owed event %d is not ready", first-4, first-3)
	}
	if readyAt(first - 3) {
		t.Fatalf("target %d accepted an unexpanded event at the same sequence", first-3)
	}

	// A retaining generation pins the fence at its captured visibility target,
	// not at the moving accepted frontier, and later churn leaves it alone.
	target := first + 7
	cc1Exec(t, db, `UPDATE catalog_generations SET retaining=1,visibility_sequence=? WHERE singleton=1`, target)
	retained, err := CaptureFacetProducerFence(ctx, db)
	if !errors.Is(err, ErrBuilding) {
		t.Fatalf("retaining generation accepted as exact: %v", err)
	}
	if retained.VisibilitySequence != target {
		t.Fatalf("retaining fence %d, want the generation's fixed visibility target %d", retained.VisibilitySequence, target)
	}
	if readyAt(target) {
		t.Fatalf("target %d accepted an owed event at the same sequence", target)
	}
	seed(entity+3, target+40)
	retainedAgain, err := CaptureFacetProducerFence(ctx, db)
	if !errors.Is(err, ErrBuilding) {
		t.Fatalf("retaining generation released by later churn: %v", err)
	}
	if retainedAgain.VisibilitySequence != target {
		t.Fatalf("later churn moved the retained target %d -> %d", target, retainedAgain.VisibilitySequence)
	}

	// Released from retention, the same queue is read through the accepted
	// frontier again and later churn does not hold it open.
	cc1Exec(t, db, `UPDATE catalog_generations SET retaining=0 WHERE singleton=1`)
	released, err := CaptureFacetProducerFence(ctx, db)
	if err != nil {
		t.Fatalf("released generation still blocked by source churn: %v", err)
	}
	if released.VisibilitySequence != first-4 {
		t.Fatalf("released frontier %d, want the earliest owed event less one (%d)", released.VisibilitySequence, first-4)
	}
}

func TestFacetReadinessAdvancesWhileLaterSourceJobsStayQueued(t *testing.T) {
	db := cc1OpenDB(t, "facet-frontier-appends.sqlite")
	lib := cc1Library(t, db, "appends", "Appends", "movie", "/appends")
	entity := facetTestEntity(t, db, lib, "movie:appends")
	cc1Drain(t, db)
	ctx := context.Background()
	clock, err := VisibilitySourceClock(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	next := clock + 1
	enqueue := func() {
		cc1Exec(t, db, `INSERT INTO compact_visibility_source_jobs(kind,library_id,subject_id,sequence,first_pending) VALUES(1,?,?,?,?)`, lib, entity+next, next, next)
		cc1Exec(t, db, `UPDATE compact_visibility_source_state SET sequence=? WHERE id=1`, next)
		next++
	}
	enqueue()
	enqueue()
	last := clock
	for turn := 0; turn < 5; turn++ {
		// A later event is appended on EVERY turn. The queue never becomes empty.
		enqueue()
		cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
			_, err := StepVisibilitySourceFanout(ctx, tx, 1)
			return err
		})
		if n := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_source_jobs`); n == 0 {
			t.Fatal("fixture lost continuous pending work")
		}
		fence, err := CaptureFacetProducerFence(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if fence.VisibilitySequence <= last {
			t.Fatalf("accepted frontier stalled: %d -> %d", last, fence.VisibilitySequence)
		}
		last = fence.VisibilitySequence
	}
}
