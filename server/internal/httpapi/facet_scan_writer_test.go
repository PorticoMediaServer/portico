package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// This is one small real scan comparison, not a timing threshold or capacity
// qualification. Run alone for meaningful process-global gate deltas. Both
// databases use persistence.Open's production baseline through the admission
// fixture; no shadow schema, fabricated jobs, media decoder or remote source.
func TestFacetScanWriterComparisonSmallFixture(t *testing.T) {
	if os.Getenv("PORTICO_FACET_SCAN_WRITER_COMPARISON") != "1" {
		t.Skip("opt-in scan timing comparison; set PORTICO_FACET_SCAN_WRITER_COMPARISON=1 and run alone")
	}
	media := t.TempDir()
	for i := 0; i < 32; i++ {
		name := fmt.Sprintf("Writer Fixture %02d (%d).mp4", i, 1990+i)
		if err := os.WriteFile(filepath.Join(media, name), []byte("file-list-only fixture\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Prepare BOTH fresh databases and legacy projections before either sample.
	on := prepareFacetScanWriter(t, media, true)
	off := prepareFacetScanWriter(t, media, false)
	// ON first supplies real private poll paths, replayed against nil adapters
	// in OFF. Initial request shapes and authenticated request counts match.
	polls := on.run(t, nil)
	off.run(t, polls)
}

type facetScanWriterFixture struct {
	f       *tl11AdmissionFixture
	library string
	scan    *ingestion.Service
	source  *compactcatalog.Worker
	bases   *catalog.FacetRuntime
	store   *readprep.Store
}

func prepareFacetScanWriter(t *testing.T, media string, enabled bool) *facetScanWriterFixture {
	t.Helper()
	f := newTL11AdmissionFixture(t, "movie")
	lib, err := f.d.Catalog.Create("Scan writer comparison", "movie", media)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := f.d.Catalog.ScanPolicy(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	policy.Tier, policy.Operations = "file_list_only", nil
	if err = dbwork.WithWriteTx(context.Background(), f.db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		return catalog.ApplyScanPolicyTx(context.Background(), tx, lib.ID, policy)
	}); err != nil {
		t.Fatal(err)
	}
	x := &facetScanWriterFixture{f: f, library: lib.ID, scan: ingestion.New(f.db, f.d.Catalog, assets.Probe{}), source: compactcatalog.NewWorker(f.db)}
	// Keep this SAME prepared worker for Run, so its registration/rebuild setup
	// is outside the sample. Drain actual source work, never bypass its fence.
	for steps := 0; ; steps++ {
		if steps == 10000 {
			t.Fatal("source projection did not quiesce during fixture preparation")
		}
		n, stepErr := x.source.Step(context.Background(), 500)
		if stepErr != nil {
			t.Fatal(stepErr)
		}
		if n == 0 {
			break
		}
	}
	if _, err = compactcatalog.CaptureFacetProducerFence(context.Background(), f.db); err != nil {
		t.Fatalf("fixture source fence is not ready: %v", err)
	}
	// Enforce the composed baseline and real active-scan index, not local DDL.
	var index string
	if err = f.db.QueryRow(`SELECT sql FROM sqlite_schema WHERE name='catalog_facet_active_scans'`).Scan(&index); err != nil || !strings.Contains(index, "'queued','running'") {
		t.Fatalf("production active-scan index missing: %q %v", index, err)
	}
	for _, name := range []string{"catalog_facet_base_usage", "catalog_facet_base_rows", "catalog_search_genre_candidates", "catalog_search_genre_value_walk"} {
		var found bool
		if err = f.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE name=?)`, name).Scan(&found); err != nil || !found {
			t.Fatalf("composed production baseline missing %s: %v", name, err)
		}
	}
	if enabled {
		x.store, err = readprep.New(readprep.Config{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { x.store.Close() })
		x.bases, err = catalog.NewFacetRuntime(f.db, x.store, catalog.CheckFacetWindowAuthority, 0)
		if err != nil {
			t.Fatal(err)
		}
		genres, err := catalog.NewGenreRuntime(f.db, x.store, catalog.CheckFacetWindowAuthority, x.bases)
		if err != nil {
			t.Fatal(err)
		}
		f.d.FacetReads, f.d.GenreReads = x.bases, genres
	}
	f.h = New(f.d)
	return x
}

// Single-statement snapshot: job status, every new BASE/candidate row family,
// ledger bytes, and insertion high water. Sequence changes catch insert/delete
// churn that a net row-count comparison alone would miss on this fresh DB.
type facetScanWriterState struct {
	status string
	facts  [11]int64
}

func facetScanWriterSnapshot(ctx context.Context, db *sql.DB, job string) (facetScanWriterState, error) {
	var s facetScanWriterState
	args := []any{&s.status}
	for i := range s.facts {
		args = append(args, &s.facts[i])
	}
	err := db.QueryRowContext(ctx, `SELECT status,
 (SELECT count(*) FROM catalog_facet_base_classes),
 (SELECT count(*) FROM catalog_facet_bases),
 (SELECT count(*) FROM catalog_facet_base_values),
 (SELECT count(*) FROM catalog_facet_base_texts),
 (SELECT count(*) FROM catalog_facet_base_rows),
 (SELECT count(*) FROM catalog_facet_base_work),
 (SELECT count(*) FROM catalog_search_genre_ready),
 (SELECT count(*) FROM catalog_search_genre_targets),
 (SELECT count(*) FROM catalog_search_genre_candidates),
 (SELECT used_bytes FROM catalog_facet_base_usage WHERE id=1),
 COALESCE((SELECT sum(seq) FROM sqlite_sequence WHERE name IN('catalog_facet_base_classes','catalog_facet_bases','catalog_facet_base_values','catalog_facet_base_texts')),0)
 FROM jobs WHERE id=?`, job).Scan(args...)
	return s, err
}

func facetScanWriterLog(t *testing.T, mode, interval string, before, after dbwork.GateStats, elapsed time.Duration) {
	t.Helper()
	b, a := before.ByClass[dbwork.ClassBackgroundMedia.String()], after.ByClass[dbwork.ClassBackgroundMedia.String()]
	t.Logf("SCAN_WRITER mode=%s interval=%s elapsed_ms=%.3f held_ms=%d acquired=%d bg_held_ms=%d bg_acquired=%d active_at_end=%t bg_current_held_ms=%d",
		mode, interval, float64(elapsed)/float64(time.Millisecond), after.HeldMilli-before.HeldMilli, after.Acquired-before.Acquired,
		a.HeldMilli-b.HeldMilli, a.Acquired-b.Acquired, after.Active, a.CurrentHeldMilli)
	for class, a := range after.ByClass {
		b := before.ByClass[class]
		if a.Acquired != b.Acquired || a.HeldMilli != b.HeldMilli {
			t.Logf("SCAN_WRITER mode=%s interval=%s class=%s held_ms=%d acquired=%d", mode, interval, class, a.HeldMilli-b.HeldMilli, a.Acquired-b.Acquired)
		}
	}
}

func (x *facetScanWriterFixture) run(t *testing.T, replayPolls []string) []string {
	t.Helper()
	f := x.f
	mode := "disabled"
	if x.bases != nil {
		mode = "enabled"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var workers []<-chan error
	start := func(fn func() error) {
		done := make(chan error, 1)
		workers = append(workers, done)
		go func() { done <- fn() }()
	}
	joined := false
	join := func() {
		if joined {
			return
		}
		cancel()
		for _, done := range workers {
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Errorf("%s worker: %v", mode, err)
				}
			case <-time.After(5 * time.Second):
				t.Errorf("%s worker failed to join", mode)
			}
		}
		joined = true
	}
	defer join()
	job, err := x.scan.QueueContext(ctx, x.library, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := facetScanWriterSnapshot(ctx, f.db, job.ID)
	if err != nil || initial.status != "queued" {
		t.Fatalf("real queued scan: %+v %v", initial, err)
	}
	queuedStart, queuedAt := dbwork.WriteGate().Stats(), time.Now()
	if x.bases != nil {
		measured, cost := dbwork.Measure(ctx)
		start(func() error { return x.bases.Run(measured) })
		// Wait for actual runtime read work under its own context, not a sleep
		// that assumes the goroutine has reached the scan gate.
		for cost().Statements == 0 {
			select {
			case <-ctx.Done():
				t.Fatal("facet runtime never checked real queued scan")
			case <-time.After(time.Millisecond):
			}
		}
	}
	paths := []string{
		"/v1/libraries/" + x.library + "/facets?field=decade&limit=8",
		"/v1/libraries/" + x.library + "/facets?field=genre&limit=8",
		"/v1/search/genres?limit=8",
	}
	polls := replayPolls
	traffic := func(collect bool) {
		t.Helper()
		want := http.StatusServiceUnavailable
		if x.bases != nil {
			want = http.StatusAccepted
		}
		for _, path := range paths {
			w := f.request(http.MethodGet, path, nil, nil)
			if w.Code != want {
				t.Fatalf("%s %s: %d %s", mode, path, w.Code, w.Body)
			}
			if collect && x.bases != nil {
				location := w.Header().Get("Location")
				if !strings.HasPrefix(location, "/v1/") {
					t.Fatalf("missing real poll path: %v", w.Header())
				}
				polls = append(polls, location)
			}
		}
		if len(polls) != 3 {
			t.Fatalf("need three real poll paths, got %d", len(polls))
		}
		for _, path := range polls {
			w := f.request(http.MethodGet, path, nil, nil)
			if w.Code != want {
				t.Fatalf("%s poll %s: %d %s", mode, path, w.Code, w.Body)
			}
		}
	}
	traffic(true)
	queuedEnd := dbwork.WriteGate().Stats()
	queued, err := facetScanWriterSnapshot(ctx, f.db, job.ID)
	if err != nil || queued.status != "queued" || queued.facts != initial.facts {
		t.Fatalf("queued Initial/Poll changed BASE/ledger: before=%+v after=%+v err=%v", initial, queued, err)
	}
	if queuedEnd.Acquired != queuedStart.Acquired {
		t.Fatalf("queued runtime/HTTP acquired writer: %d", queuedEnd.Acquired-queuedStart.Acquired)
	}
	facetScanWriterLog(t, mode, "queued_reads", queuedStart, queuedEnd, time.Since(queuedAt))

	// Real scan + already-prepared legacy source worker in BOTH modes. All
	// their observed writer holds count, including any new runtime hold.
	before, began := dbwork.WriteGate().Stats(), time.Now()
	start(func() error { x.source.Run(ctx); return nil })
	start(func() error { x.scan.Run(ctx); return nil })
	runningTraffic, activeSamples := false, 0
	var terminal facetScanWriterState
	var after dbwork.GateStats
	var elapsed time.Duration
	for {
		s, err := facetScanWriterSnapshot(ctx, f.db, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if s.status != "queued" && s.status != "running" {
			// First terminal observation: counters first, then immediate cancel
			// and join. No projection drain or BASE build in the sample tail.
			after, elapsed, terminal = dbwork.WriteGate().Stats(), time.Since(began), s
			cancel()
			break
		}
		activeSamples++
		if s.facts != initial.facts {
			t.Fatalf("BASE/ledger changed WHILE scan %s: before=%v after=%v", s.status, initial.facts, s.facts)
		}
		if s.status == "running" && !runningTraffic {
			traffic(false)
			runningTraffic = true
		}
		select {
		case <-ctx.Done():
			t.Fatal("scan did not reach terminal state")
		case <-time.After(time.Millisecond):
		}
	}
	join()
	joinedStats := dbwork.WriteGate().Stats()
	facetScanWriterLog(t, mode, "scan", before, after, elapsed)
	facetScanWriterLog(t, mode, "cancel_join_tail", after, joinedStats, time.Since(began)-elapsed)
	if terminal.status != "complete" || !runningTraffic {
		t.Fatalf("scan not successful with live HTTP traffic: terminal=%+v runningTraffic=%t", terminal, runningTraffic)
	}
	state, err := x.scan.Get(job.ID)
	if err != nil || state.Processed != 32 || state.Analyzed != 0 || state.Warnings != 0 {
		t.Fatalf("file-list-only result: %+v %v", state, err)
	}
	var films, years int
	if err = f.db.QueryRow(`SELECT count(*),count(DISTINCT year) FROM catalog_entities WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND kind=1`, x.library).Scan(&films, &years); err != nil || films != 32 || years != 32 {
		t.Fatalf("actual scanned catalogue: films=%d years=%d err=%v", films, years, err)
	}
	t.Logf("SCAN_WRITER mode=%s terminal=%s processed=%d analyzed=%d warnings=%d films=%d distinct_years=%d active_snapshots=%d queued_requests=6 running_requests=6 initial_facts=%v terminal_facts=%v",
		mode, state.Status, state.Processed, state.Analyzed, state.Warnings, films, years, activeSamples, initial.facts, terminal.facts)
	if x.bases != nil {
		// Verify the runtime dispatcher's captured HTTP guard reauthenticates
		// after logout, even when reusing the pre-logout request context.
		r := facetTestRequest(f, paths[0])
		p, err := f.d.Identity.Authenticate(f.token)
		if err != nil {
			t.Fatal(err)
		}
		authctx, caller, err := f.d.facetReadContext(r, p)
		if err != nil {
			t.Fatal(err)
		}
		restrictions, _, err := f.d.viewerRestrictions(r, p)
		if err != nil {
			t.Fatal(err)
		}
		snapshot := catalog.FacetBaseSnapshot{Scope: catalog.FacetBaseScope{Library: x.library, Policy: catalog.FacetBasePolicy(restrictions)}}
		if err = catalog.CheckFacetWindowAuthority(authctx, caller, snapshot); err != nil {
			t.Fatalf("captured HTTP guard was not initially authorized: %v", err)
		}
		if err = f.d.Identity.LogoutToken(context.Background(), f.token); err != nil {
			t.Fatal(err)
		}
		err = catalog.CheckFacetWindowAuthority(authctx, caller, snapshot)
		if !errors.Is(err, identity.ErrUnauthorized) {
			t.Fatalf("captured HTTP guard did not live-reauthenticate: %v", err)
		}
		if w := f.request(http.MethodGet, polls[0], nil, nil); w.Code != http.StatusUnauthorized {
			t.Fatalf("logged-out HTTP poll: %d %s", w.Code, w.Body)
		}
		if err = x.store.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return polls
}
