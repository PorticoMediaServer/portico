package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

func TestFacetRuntimeColdCompletesUnderContinuousTaggedIngest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	// Keep the functional scheduling proof small under the race detector.
	// The opt-in 300k raw-member test below owns throughput acceptance.
	f := runtimeFixture(t, 32, 128<<20)
	t.Cleanup(func() { dbwork.ForgetCatalogIngestWindow(f.c.DB, f.r.ingestWindow) })
	f.r.now = time.Now
	// Scale only the new scheduler's clock for this focused real-gate test.
	// Production remains three minutes starvation / a progress-renewed ten-minute cap.
	f.r.starveAfter = 35 * time.Millisecond
	f.r.ingestHold = 500 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	idle := make([]time.Duration, 0, 80)
	for i := 0; i < 80; i++ {
		began := time.Now()
		if _, err := dbwork.ExecWrite(ctx, f.c.DB, dbwork.ClassInteractive, `UPDATE admin_revision SET revision=revision+1 WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		idle = append(idle, time.Since(began))
	}
	var changedValue string
	if err := f.c.DB.QueryRow(`SELECT value FROM catalog_facet_counts WHERE library_id=? AND field='genre' ORDER BY value LIMIT 1`, f.scope.LibraryID).Scan(&changedValue); err != nil {
		t.Fatal(err)
	}
	var commits atomic.Int64
	var mu sync.Mutex
	expected := map[int64]int64{}
	ingestDone := make(chan error, 1)
	go func() {
		tagged := dbwork.WithCatalogIngest(ctx)
		for {
			var epoch, total int64
			err := dbwork.WithWriteTx(tagged, f.c.DB, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
				if _, err := tx.ExecContext(tagged, `UPDATE catalog_facet_counts SET total=total+1 WHERE library_id=? AND field='genre' AND value=?`, f.scope.LibraryID, changedValue); err != nil {
					return err
				}
				if _, err := tx.ExecContext(tagged, `UPDATE catalog_facet_source_state SET source_epoch=source_epoch+1 WHERE singleton=1`); err != nil {
					return err
				}
				if err := tx.QueryRowContext(tagged, `SELECT source_epoch FROM catalog_facet_source_state WHERE singleton=1`).Scan(&epoch); err != nil {
					return err
				}
				return tx.QueryRowContext(tagged, `SELECT sum(total) FROM catalog_facet_counts WHERE library_id=? AND field='genre'`, f.scope.LibraryID).Scan(&total)
			})
			if err != nil {
				ingestDone <- err
				return
			}
			mu.Lock()
			expected[epoch] = total
			mu.Unlock()
			commits.Add(1)
			select {
			case <-ctx.Done():
				ingestDone <- ctx.Err()
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	// A real queued/running scan status used to block the base forever. Its tagged
	// source commits now park while the existing derivations/fence may finish.
	f.c.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('ingest','films','running','2026-10-28')`)
	waitUntil := time.Now().Add(time.Second)
	for commits.Load() < 3 {
		if time.Now().After(waitUntil) {
			t.Fatal("ingest did not start")
		}
		time.Sleep(time.Millisecond)
	}
	d, err := f.r.demand(f.scope)
	if err != nil {
		t.Fatal(err)
	}
	// Run the same real source adapter and writer transactions the supervised
	// worker uses, alongside interactive writes. No in-memory fake producer.
	var latencies []time.Duration
	began := time.Now()
	deadline := began.Add(3 * time.Second)
	for {
		f.r.maintain(ctx, d)
		started := time.Now()
		if _, err := dbwork.ExecWrite(ctx, f.c.DB, dbwork.ClassInteractive, `UPDATE admin_revision SET revision=revision+1 WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		latencies = append(latencies, time.Since(started))
		f.r.mu.Lock()
		ready, terminal := d.ready, d.err
		f.r.mu.Unlock()
		if terminal != nil {
			t.Fatal(terminal)
		}
		if ready != 0 {
			snap, err := f.r.active(ctx, f.scope)
			if err != nil {
				t.Fatal(err)
			}
			var witness unrestrictedFacetWitness
			if err = json.Unmarshal([]byte(snap.Witness), &witness); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			want, found := expected[witness.Fence.SourceEpoch]
			mu.Unlock()
			var got int64
			if err = f.c.DB.QueryRow(`SELECT COALESCE(sum(-negative_count),0) FROM catalog_facet_base_rows WHERE base_id=?`, snap.ID).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if !found || got != want {
				t.Fatalf("exact count %d != accepted ingest epoch %d total %d (found %v)", got, witness.Fence.SourceEpoch, want, found)
			}

			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cold class starved despite bounded ingest window")
		}
		time.Sleep(time.Millisecond)
	}
	during := commits.Load()
	f.r.cooperateWithColdBuild(ctx)
	if dbwork.CatalogIngestWindowActive(f.c.DB) {
		t.Fatal("ready class kept ingest held")
	}
	deadline = time.Now().Add(time.Second)
	for commits.Load() == during {
		if time.Now().After(deadline) {
			t.Fatal("ingest did not resume")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-ingestDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	dbwork.ForgetCatalogIngestWindow(f.c.DB, f.r.ingestWindow)
	sort.Slice(idle, func(i, j int) bool { return idle[i] < idle[j] })
	t.Logf("interactive idle n=%d p50=%s p99=%s", len(idle), idle[len(idle)/2], idle[len(idle)-1])
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	t.Logf("cold exact ready in %s; %d ingest commits retained/resumed; interactive n=%d p50=%s p99=%s", time.Since(began), commits.Load(), len(latencies), latencies[len(latencies)/2], latencies[max(0, (99*len(latencies)+99)/100-1)])
}

func TestFacetRuntimeIngestWindowFinishesOneColdClassBeforeNext(t *testing.T) {
	f := runtimeFixture(t, 1, 128<<20)
	f.r.now = time.Now
	first, err := f.r.demand(f.scope)
	if err != nil {
		t.Fatal(err)
	}
	other := f.scope
	other.Field = "year"
	second, err := f.r.demand(other)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	first.waitingSince = now.Add(-4 * time.Minute)
	second.waitingSince = now.Add(-3 * time.Minute)
	f.r.ingestHold = time.Second
	t.Cleanup(func() { dbwork.ForgetCatalogIngestWindow(f.c.DB, f.r.ingestWindow) })
	f.r.cooperateWithColdBuild(context.Background())
	token := f.r.ingestWindow
	if token == 0 || f.r.nextDemand() != first {
		t.Fatal("cold window did not focus oldest class")
	}
	first.ready = 1
	f.r.cooperateWithColdBuild(context.Background())
	if f.r.ingestWindow != token || f.r.nextDemand() != second {
		t.Fatal("remaining window was not given to next cold class")
	}
	second.ready = 2
	f.r.cooperateWithColdBuild(context.Background())
	if dbwork.CatalogIngestWindowActive(f.c.DB) {
		t.Fatal("completed classes kept ingest held")
	}
}

func TestFacetRuntimeColdMemberScaleUnderContinuousIngest(t *testing.T) {
	if os.Getenv("PORTICO_FACET_COLD_SCALE") == "" {
		t.Skip("set PORTICO_FACET_COLD_SCALE=1 for the 100k member measurement")
	}
	members := 100000
	if raw := os.Getenv("PORTICO_FACET_COLD_MEMBERS"); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &members); err != nil {
			t.Fatal(err)
		}
	}
	db, scope := facetRestrictedFixture(t, false)

	scope.Field = "studio"
	age := 13
	scope.Policy = FacetBasePolicy(identity.ContentRestrictions{MaximumAge: &age})
	if _, err := db.Exec(`ALTER TABLE catalog_facet_source_state ADD COLUMN policy_epoch INTEGER NOT NULL DEFAULT 1;
 ALTER TABLE catalog_facet_source_state ADD COLUMN genre_epoch INTEGER NOT NULL DEFAULT 1;
 CREATE TABLE jobs(id TEXT PRIMARY KEY,status TEXT NOT NULL);
 CREATE INDEX catalog_facet_active_scans ON jobs(status);
 INSERT INTO jobs VALUES('import','running');
 CREATE TABLE admin_revision(id INTEGER PRIMARY KEY,revision INTEGER NOT NULL);
 INSERT INTO admin_revision VALUES(1,0);
 WITH RECURSIVE n(i) AS(SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<?) INSERT INTO catalog_entities SELECT i,1 FROM n;
 INSERT INTO catalog_browse_rows SELECT id,1,1,id,2000 FROM catalog_entities;
 INSERT INTO catalog_item_attribute_edges SELECT id,1,'PG' FROM catalog_entities;
 INSERT INTO catalog_facet_members SELECT id,'studio','Shared',1 FROM catalog_entities;`, members); err != nil {
		t.Fatal(err)
	}
	// Reopen the seeded algorithm fixture with the production split foreground /
	// background reader pools and single WAL/NORMAL writer, including real meters.
	var databaseID int
	var databaseName, databasePath string
	if err := db.QueryRow(`PRAGMA database_list`).Scan(&databaseID, &databaseName, &databasePath); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var openErr error
	db, openErr = dbwork.OpenHandle(databasePath, dbwork.DefaultPolicy())
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer db.Close()

	store, err := readprep.New(readprep.Config{Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	r, err := NewFacetRuntime(db, store, CheckFacetWindowAuthority, 128<<20)
	if err != nil {
		t.Fatal(err)
	}
	r.starveAfter = 50 * time.Millisecond
	defer func() { dbwork.ForgetCatalogIngestWindow(db, r.ingestWindow) }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	schedule, err := dbwork.NewCheckpointSchedule(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	checkpointDone := make(chan struct{})
	go func() {
		defer close(checkpointDone)
		timer := time.NewTicker(dbwork.CheckpointPollInterval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				schedule.Tick(ctx)
			}
		}
	}()

	idle := make([]time.Duration, 0, 400)
	for i := 0; i < 400; i++ {
		began := time.Now()
		if _, err := dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive, `UPDATE admin_revision SET revision=revision+1 WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		idle = append(idle, time.Since(began))
	}
	var commits atomic.Int64
	var mu sync.Mutex
	expected := map[int64]bool{}
	ingestDone := make(chan error, 1)
	go func() {
		tag := dbwork.WithCatalogIngest(ctx)
		other := false
		for {
			other = !other
			value := "Shared"
			if other {
				value = "Other"
			}
			var epoch int64
			err := dbwork.WithWriteTx(tag, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
				if _, err := tx.ExecContext(tag, `UPDATE catalog_facet_members SET value=? WHERE entity_id=1`, value); err != nil {
					return err
				}
				return tx.QueryRowContext(tag, `UPDATE catalog_facet_source_state SET source_epoch=source_epoch+1 WHERE singleton=1 RETURNING source_epoch`).Scan(&epoch)
			})
			if err != nil {
				ingestDone <- err
				return
			}
			mu.Lock()
			expected[epoch] = other
			mu.Unlock()
			commits.Add(1)
			select {
			case <-ctx.Done():
				ingestDone <- ctx.Err()
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	for commits.Load() < 3 {
		time.Sleep(time.Millisecond)
	}
	d, err := r.demand(scope)
	if err != nil {
		t.Fatal(err)
	}
	userDone := make(chan []time.Duration, 1)
	userErr := make(chan error, 1)
	go func() {
		var latency []time.Duration
		timer := time.NewTicker(5 * time.Millisecond)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				userDone <- latency
				return
			case <-timer.C:
				began := time.Now()
				_, err := dbwork.ExecWrite(ctx, db, dbwork.ClassInteractive, `UPDATE admin_revision SET revision=revision+1 WHERE id=1`)
				if err != nil {
					if !errors.Is(err, context.Canceled) {
						userErr <- err
					}
					userDone <- latency
					return
				}
				latency = append(latency, time.Since(began))
			}
		}
	}()
	began := time.Now()
	deadline := began.Add(11 * time.Minute)
	turns := 0
	runtimeDone := make(chan error, 1)
	go func() { runtimeDone <- r.Run(ctx) }()
	for {
		r.mu.Lock()
		d.touched = time.Now()
		ready, terminal := d.ready, d.err
		r.mu.Unlock()
		turns++
		if terminal != nil {
			t.Fatal(terminal)
		}
		if ready != 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("member class exceeded finite window")
		}
		time.Sleep(5 * time.Millisecond)
	}

	elapsed := time.Since(began)
	snap, err := r.active(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	var witness restrictedFacetWitness
	if err = json.Unmarshal([]byte(snap.Witness), &witness); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	other, found := expected[witness.Fence.SourceEpoch]
	mu.Unlock()
	if !found {
		t.Fatal("accepted source epoch missing from oracle")
	}
	var total, others int64
	if err = db.QueryRow(`SELECT COALESCE(sum(-r.negative_count),0),COALESCE(sum(CASE WHEN v.value='Other' THEN -r.negative_count ELSE 0 END),0) FROM catalog_facet_base_rows r JOIN catalog_facet_base_values v ON v.id=r.value_id WHERE r.base_id=?`, snap.ID).Scan(&total, &others); err != nil {
		t.Fatal(err)
	}
	wantOther := int64(0)
	if other {
		wantOther = 1
	}
	if total != int64(members) || others != wantOther {
		t.Fatalf("mixed source image: total=%d other=%d want=%d/%d", total, others, members, wantOther)
	}
	before := commits.Load()
	resumeDeadline := time.Now().Add(time.Second)
	for commits.Load() == before {
		if time.Now().After(resumeDeadline) {
			t.Fatal("ingest failed to resume")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-ingestDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	loaded := <-userDone
	<-checkpointDone
	if err := <-runtimeDone; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case err := <-userErr:
		t.Fatal(err)
	default:
	}
	percentile := func(x []time.Duration, p int) time.Duration {
		sort.Slice(x, func(i, j int) bool { return x[i] < x[j] })
		return x[max(0, (p*len(x)+99)/100-1)]
	}
	t.Logf("cold exact %d members: %d readiness polls, %d bounded source/policy steps, %s wall, %.0f members/s; interactive idle n=%d p50=%s p99=%s; loaded n=%d p50=%s p99=%s; %d ingest commits retained and resumed", members, turns, d.ingestSteps, elapsed, float64(members)/elapsed.Seconds(), len(idle), percentile(idle, 50), percentile(idle, 99), len(loaded), percentile(loaded, 50), percentile(loaded, 99), commits.Load())
}

func TestFacetRuntimeColdMemberSlicesKeepDefaultWritePlan(t *testing.T) {
	f := runtimeFixture(t, 64, 128<<20)
	f.r.now = time.Now
	scope := f.scope
	age := 13
	scope.Policy = FacetBasePolicy(identity.ContentRestrictions{MaximumAge: &age})
	scope.Source = RestrictedMemberFacetBaseSource
	d, err := f.r.demand(scope)
	if err != nil {
		t.Fatal(err)
	}
	d.waitingSince = time.Now().Add(-4 * time.Minute)
	defer func() { dbwork.ForgetCatalogIngestWindow(f.c.DB, f.r.ingestWindow) }()
	dbwork.ResetPlans()
	deadline := time.Now().Add(5 * time.Second)
	for d.ready == 0 {
		f.r.cooperateWithColdBuild(context.Background())
		f.r.maintainColdBurst(context.Background(), d)
		if d.err != nil {
			t.Fatal(d.err)
		}
		if time.Now().After(deadline) {
			t.Fatal("bounded member slices did not finish")
		}
	}
	stats := dbwork.Plans()
	plan := dbwork.DefaultPlan(dbwork.ClassBackgroundMedia)
	if stats.Measured == 0 || stats.Largest.Rows > plan.Rows || stats.Largest.Pages > plan.Pages || stats.Largest.VMOps > plan.VMOps {
		t.Fatalf("cold member slices escaped original plan: %+v", stats)
	}
	t.Logf("cold member slices measured=%d max rows/pages/VM=%d/%d/%d", stats.Measured, stats.Largest.Rows, stats.Largest.Pages, stats.Largest.VMOps)
}
