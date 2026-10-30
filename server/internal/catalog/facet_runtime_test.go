package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

type facetRuntimeFixture struct {
	c       *catalogtest.Catalog
	r       *FacetRuntime
	store   *readprep.Store
	ctx     context.Context
	viewer  Viewer
	request FacetRequest
	scope   FacetBaseScope
	item    catalogtest.Item
	clock   atomic.Int64
	allowed atomic.Bool
	checks  atomic.Int64
}

func runtimeFixture(t *testing.T, n int, cap int64) *facetRuntimeFixture {
	t.Helper()
	f := &facetRuntimeFixture{}
	f.c = catalogtest.Open(t)
	facetInstallDraftOnBaseline(t, f.c)
	lib := f.c.Library("films", "Films", "movie", "/films")
	f.item = f.c.Movie(lib, "/films/one.mkv", "One", 2000)
	names := make([]string, n)
	for i := range names {
		names[i] = fmt.Sprintf("Drama %02d", i)
	}
	f.c.Genres(f.item.ID, "fixture", names...)
	f.c.Drain()
	f.clock.Store(time.Now().UnixNano())
	now := func() time.Time { return time.Unix(0, f.clock.Load()) }
	var err error
	f.store, err = readprep.New(readprep.Config{Workers: 1, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.store.Close() })
	f.r, err = NewFacetRuntime(f.c.DB, f.store, CheckFacetWindowAuthority, cap)
	if err != nil {
		t.Fatal(err)
	}
	f.r.now = now
	f.r.windows.now = now
	f.scope = FacetBaseScope{LibraryID: lib, Library: "films", Field: "genre", Policy: FacetBasePolicy(identity.ContentRestrictions{}), Source: UnrestrictedFacetBaseSource}
	f.viewer = Viewer{Profile: "profile", Fence: "fence", Libraries: []string{"films"}}
	f.request = FacetRequest{Library: "films", Field: "genre", Q: "d", Limit: 2, LimitPresent: true}
	f.allowed.Store(true)
	f.ctx = WithFacetWindowAuthority(context.Background(), func(ctx context.Context, caller readprep.Caller, s FacetBaseSnapshot) error {
		f.checks.Add(1)
		if !f.allowed.Load() || caller != "caller" || !s.Scope.valid() || s.Scope.Policy != f.scope.Policy || s.Scope.Library != "films" {
			return readprep.ErrStale
		}
		if s.ID != 0 {
			a, err := CaptureFacetAuthority(ctx, dbwork.ReadHandle(ctx, f.c.DB), s.Scope)
			if err != nil {
				return err
			}
			if a != s.Authority {
				return readprep.ErrStale
			}
		}
		return nil
	})
	return f
}
func (f *facetRuntimeFixture) advance(d time.Duration) { f.clock.Add(int64(d)) }
func (f *facetRuntimeFixture) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ended := make(chan error, 1)
	go func() { ended <- f.r.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-ended:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("runtime did not stop")
		}
	})
}
func runtimeReady(t *testing.T, f *facetRuntimeFixture, out SimpleFacetResult, err error) SimpleFacetPage {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for out.Page == nil {
		if time.Now().After(deadline) {
			out.Release()
			t.Fatal("runtime did not produce exact page")
		}
		id := ""
		if out.Preparing != nil {
			id = out.Preparing.QueryID
		}
		if out.Fallback != nil {
			id = out.Fallback.Preparation.QueryID
		}
		out.Release()
		if id == "" {
			t.Fatal("missing exact/preparing/fallback")
		}
		time.Sleep(time.Millisecond)
		out, err = f.r.Poll(f.ctx, "caller", id)
		if err != nil {
			t.Fatal(err)
		}
	}
	page := *out.Page
	out.Release()
	return page
}
func runtimeBuild(t *testing.T, f *facetRuntimeFixture) *facetRuntimeDemand {
	t.Helper()
	d, err := f.r.demand(f.scope)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		f.r.maintain(context.Background(), d)
		f.r.mu.Lock()
		ready, terminal := d.ready, d.err
		f.r.mu.Unlock()
		if terminal != nil {
			t.Fatal(terminal)
		}
		if ready != 0 {
			return d
		}
	}
	t.Fatal("bounded production source did not complete")
	return nil
}

func TestFacetRuntimeColdCoalescesPrivateHandlesAndValidatesOriginalRequest(t *testing.T) {
	f := runtimeFixture(t, 7, 128<<20)
	first, err := f.r.Initial(f.ctx, "caller", f.viewer, f.request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Preparing == nil || first.Page != nil || !strings.HasPrefix(first.Preparing.QueryID, "C") {
		t.Fatal(first)
	}
	id := first.Preparing.QueryID
	first.Release()
	second, err := f.r.Initial(f.ctx, "caller", f.viewer, f.request)
	if err != nil {
		t.Fatal(err)
	}
	otherID := second.Preparing.QueryID
	second.Release()
	if id == otherID || f.store.Stats().Entries != 1 || f.store.Stats().Handles != 2 {
		t.Fatal("cold key lost coalescing/privacy", f.store.Stats())
	}
	if _, err = f.r.Poll(f.ctx, "other", id); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("cross caller", err)
	}
	f.start(t)
	out, err := f.r.Poll(f.ctx, "caller", id)
	page := runtimeReady(t, f, out, err)
	if page.Window.QueryID != id || page.Total != 7 || page.Window.Returned != 2 || page.Window.Next == "" {
		t.Fatal(page)
	}
	h, _ := scopeHandle(id)
	cached, e := f.store.Poll(f.ctx, "caller", h)
	if e != nil {
		t.Fatal(e)
	}
	var wrapped facetScopeSummary
	if e = json.Unmarshal(cached.Payload, &wrapped); e != nil || !wrapped.Window.CompleteMatchSet || len(wrapped.Window.CompleteMatchIDs) != 7 {
		cached.Release()
		t.Fatal("cold scan bypassed prefix factory", e)
	}
	cached.Release()
	token, err := f.r.windows.decode(page.Window.Next)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []FacetRequest{
		{Library: "elsewhere", Field: "genre", Q: "d"}, {Library: "films", Field: "studio", Q: "d"},
		{Library: "films", Field: "genre", Q: "x"}, {Library: "films", Field: "genre", Q: "d", Limit: 3, LimitPresent: true},
	} {
		if _, err = f.r.Continue(f.ctx, "caller", f.viewer, bad, page.Window.Next); err == nil {
			t.Fatal("changed original request accepted", bad)
		}
	}
	inherited := f.request
	inherited.LimitPresent = false
	inherited.Limit = 200
	inherited.Q = "D"
	inherited.After = page.Window.Next
	out, err = f.r.Continue(f.ctx, "caller", f.viewer, inherited, page.Window.Next)
	next := runtimeReady(t, f, out, err)
	if next.Window.Limit != 2 || next.Window.Basis != page.Window.Basis {
		t.Fatal(next)
	}
	follow, err := f.r.windows.decode(next.Window.Reload)
	if err != nil || follow.expires != token.expires {
		t.Fatal("continuation renewed original expiry", err)
	}
	if f.store.Stats().Deliveries != 0 {
		t.Fatal("delivery leak", f.store.Stats())
	}
}

func TestFacetRuntimeFallbackAfterPrivateTenSecondsThenExactWithSameID(t *testing.T) {
	f := runtimeFixture(t, 18, 128<<20)
	req := f.request
	req.Q = ""
	req.Limit = 2
	out, err := f.r.Initial(f.ctx, "caller", f.viewer, req)
	if err != nil {
		t.Fatal(err)
	}
	id := out.Preparing.QueryID
	out.Release()
	f.advance(9 * time.Second)
	out, err = f.r.Poll(f.ctx, "caller", id)
	if err != nil || out.Preparing == nil {
		t.Fatal("early fallback", out, err)
	}
	out.Release()
	f.advance(time.Second)
	out, err = f.r.Poll(f.ctx, "caller", id)
	if err != nil || out.Fallback == nil || !out.Fallback.TotalIsLowerBound || out.Fallback.Total != 0 {
		t.Fatal(out, err)
	}
	out.Release()
	d, _ := f.r.demand(f.scope)
	f.r.maintain(context.Background(), d)
	f.r.maintain(context.Background(), d)
	out, err = f.r.Poll(f.ctx, "caller", id)
	if err != nil || out.Fallback == nil {
		t.Fatal(out, err)
	}
	if len(out.Fallback.Values) == 0 || len(out.Fallback.Values) > 2 || out.Fallback.Preparation.QueryID != id || !out.Fallback.Values[0].CountIsLowerBound {
		t.Fatal("staged bounded honest facts", out.Fallback)
	}
	out.Release()
	runtimeBuild(t, f)
	out, err = f.r.Poll(f.ctx, "caller", id)
	page := runtimeReady(t, f, out, err)
	if page.Window.QueryID != id || page.Total != 18 || page.TotalIsLowerBound {
		t.Fatal(page)
	}
	f.allowed.Store(false)
	if _, err = f.r.Poll(f.ctx, "caller", id); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("cached ready bypassed authority", err)
	}
}

func TestFacetRuntimeColdAuthChecksAndCancelledPrivateDemand(t *testing.T) {
	f := runtimeFixture(t, 1, 128<<20)
	out, err := f.r.Initial(f.ctx, "caller", f.viewer, f.request)
	if err != nil {
		t.Fatal(err)
	}
	id := out.Preparing.QueryID
	out.Release()
	if f.checks.Load() < 3 {
		t.Fatal("pending not authorized before/after", f.checks.Load())
	}
	if err = f.r.Cancel(f.ctx, "other", id); err != nil {
		t.Fatal(err)
	}
	out, err = f.r.Poll(f.ctx, "caller", id)
	if err != nil {
		t.Fatal(err)
	}
	out.Release()
	if err = f.r.Cancel(f.ctx, "caller", id); err != nil {
		t.Fatal(err)
	}
	if _, err = f.r.Poll(f.ctx, "caller", id); !errors.Is(err, readprep.ErrStale) {
		t.Fatal(err)
	}
	out, err = f.r.Initial(f.ctx, "caller", f.viewer, f.request)
	if err != nil {
		t.Fatal(err)
	}
	id = out.Preparing.QueryID
	out.Release()
	f.allowed.Store(false)
	f.advance(11 * time.Second)
	if _, err = f.r.Poll(f.ctx, "caller", id); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("fallback leaked revoked scope", err)
	}
	if f.store.Stats().Deliveries != 0 {
		t.Fatal(f.store.Stats())
	}
}

func TestFacetRuntimeOldCompleteSurvivesContentBuildingAndNoopDoesNotWrite(t *testing.T) {
	f := runtimeFixture(t, 5, 128<<20)
	d := runtimeBuild(t, f)
	out, err := f.r.Initial(f.ctx, "caller", f.viewer, f.request)
	page := runtimeReady(t, f, out, err)
	before := dbwork.Publications()
	f.r.maintain(context.Background(), d)
	if dbwork.Publications() != before {
		t.Fatal("unchanged COMPLETE admitted writer")
	}
	f.c.Genres(f.item.ID, "fixture", "Replacement")
	out, err = f.r.Initial(f.ctx, "caller", f.viewer, f.request)
	old := runtimeReady(t, f, out, err)
	if old.Window.Basis != page.Window.Basis || !reflect.DeepEqual(old.Values, page.Values) {
		t.Fatal("content churn rejected complete B", old)
	}
	req := f.request
	req.After = page.Window.Next
	out, err = f.r.Continue(f.ctx, "caller", f.viewer, req, page.Window.Next)
	next := runtimeReady(t, f, out, err)
	if next.Total != 5 || next.Window.Basis != page.Window.Basis {
		t.Fatal(next)
	}
}

func TestFacetRuntimeDriftQuietGapDoublesCapsAndResetsAfterAcceptedBase(t *testing.T) {
	f := runtimeFixture(t, 18, 128<<20)
	d, _ := f.r.demand(f.scope)
	f.r.maintain(context.Background(), d)
	original := d.build
	if original == 0 {
		t.Fatal("missing staged work")
	}
	f.c.Genres(f.item.ID, "fixture", "New A", "New B", "New C")
	f.c.Drain() // New binding: restart/retirement waits until upstream is quiet.
	f.r.maintain(context.Background(), d)
	if d.gap != 2*time.Second || d.retiring != original {
		t.Fatal(d.gap, d.retiring)
	}
	for d.retiring != 0 {
		f.r.maintain(context.Background(), d)
	}
	publications := dbwork.Publications()
	f.advance(1999 * time.Millisecond)
	f.r.maintain(context.Background(), d)
	if d.build != 0 || dbwork.Publications() != publications {
		t.Fatal("restarted before quiet gap")
	}
	f.advance(time.Millisecond)
	f.c.Drain()
	f.r.maintain(context.Background(), d)
	if d.build == 0 {
		t.Fatal("did not restart after quiet gap")
	}
	f.c.Genres(f.item.ID, "fixture", "Newer A", "Newer B")
	f.c.Drain()
	f.r.maintain(context.Background(), d)
	if d.gap != 4*time.Second {
		t.Fatal("second drift did not double", d.gap)
	}
	for d.retiring != 0 {
		f.r.maintain(context.Background(), d)
	}
	for i := 0; i < 8; i++ {
		f.r.maintenanceFailure(context.Background(), d, 0, compactcatalog.ErrFacetProducerChanged)
	}
	if d.gap != 2*time.Minute {
		t.Fatal("unbounded drift delay", d.gap)
	}
	f.advance(2 * time.Minute)
	f.c.Drain()
	runtimeBuild(t, f)
	if d.gap != 0 || !d.next.IsZero() {
		t.Fatal("accepted base did not reset backoff", d.gap, d.next)
	}
}

func TestFacetRuntimeColdBaseCapacityTerminalAndSourceWritesIndependent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := runtimeFixture(t, 1, 64<<10)
	f.c.Genres(f.item.ID, "fixture", strings.Repeat("x", 128<<10))
	f.c.Drain()
	out, err := f.r.Initial(f.ctx, "caller", f.viewer, f.request)
	if err != nil {
		t.Fatal(err)
	}
	id := out.Preparing.QueryID
	out.Release()
	f.start(t)
	deadline := time.Now().Add(8 * time.Second)
	for {
		out, err = f.r.Poll(f.ctx, "caller", id)
		out.Release()
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("capacity stayed in endless preparing")
		}
		time.Sleep(time.Millisecond)
	}
	if !errors.Is(err, readprep.ErrCapacity) {
		t.Fatal("capacity was not typed terminal", err)
	}
	var used int64
	if err = f.c.DB.QueryRow(`SELECT used_bytes FROM catalog_facet_base_usage WHERE id=1`).Scan(&used); err != nil || used > f.r.baseLimit {
		t.Fatal("global BASE ledger exceeded", used, err)
	}
	f.c.Genres(f.item.ID, "fixture", "Accepted source write")
}

func TestFacetRuntimeGenericPrefixSourceUsesFullGrantVectorAndBoundedRegistry(t *testing.T) {
	f := runtimeFixture(t, 1, 128<<20)
	capture := func(_ context.Context, _ compactcatalog.ReadQuery, s FacetBaseScope) (FacetBasePlan, error) {
		return FacetBasePlan{Scope: s, Witness: "exact:" + s.Source, Producer: &facetFixtureProducer{facts: []FacetBaseFact{{Value: s.Source, Label: "Genre", Count: 1}}}}, nil
	}
	if err := f.r.RegisterSource("search-video-genre-v1", capture); err != nil {
		t.Fatal(err)
	}
	if err := f.r.RegisterSource("search-video-genre-v1", capture); !errors.Is(err, ErrFacetBaseInput) {
		t.Fatal("source replacement allowed", err)
	}
	ids := []int64{}
	for _, grants := range []string{
		fmt.Sprintf(`[{"id":%d,"library":"films"}]`, f.scope.LibraryID),
		fmt.Sprintf(`[{"id":%d,"library":"films"},{"id":1000,"library":"other-lifetime:2"}]`, f.scope.LibraryID),
	} {
		scope := f.scope
		scope.Source = "search-video-genre-v1:" + grants
		if _, err := f.r.Demand(context.Background(), scope); !errors.Is(err, compactcatalog.ErrBuilding) {
			t.Fatal(err)
		}
		d := f.r.demands[scope]
		for i := 0; i < 10 && d.ready == 0; i++ {
			f.r.maintain(context.Background(), d)
		}
		snap, err := f.r.Demand(context.Background(), scope)
		if err != nil || snap.Scope != scope {
			t.Fatal(snap, err)
		}
		ids = append(ids, snap.ID)
	}
	if ids[0] == ids[1] {
		t.Fatal("different member grant vectors shared base", ids)
	}
	for i := 0; i < 64; i++ {
		s := f.scope
		s.Source = fmt.Sprintf("search-video-genre-v1:[%d]", i)
		_, _ = f.r.demand(s)
	}
	if len(f.r.demands) != 64 || f.r.registryBytes > f.r.registryLimit {
		t.Fatal("registry escaped cap", len(f.r.demands), f.r.registryBytes)
	}
	s := f.scope
	s.Source = "search-video-genre-v1:[999]"
	if _, err := f.r.demand(s); !errors.Is(err, ErrFacetBaseCapacity) {
		t.Fatal(err)
	}
}

func TestFacetRuntimeIdleEvictionUsesBoundedGCAndStalesRetainedHandle(t *testing.T) {
	f := runtimeFixture(t, 18, 128<<20)
	runtimeBuild(t, f)
	out, err := f.r.Initial(f.ctx, "caller", f.viewer, f.request)
	page := runtimeReady(t, f, out, err)
	f.r.started = f.r.now()
	f.advance(6 * time.Minute)
	f.r.nextDemand()
	if len(f.r.demands) != 0 {
		t.Fatal("idle demand retained")
	}
	for i := 0; i < 80; i++ {
		var before, after int
		if err = f.c.DB.QueryRow(`SELECT count(*) FROM catalog_facet_base_rows`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		if err = f.r.collect(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err = f.c.DB.QueryRow(`SELECT count(*) FROM catalog_facet_base_rows`).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if before-after > 8 {
			t.Fatal("GC exceeded one small raw quantum", before, after)
		}
	}
	if _, err = f.r.Poll(f.ctx, "caller", page.Window.QueryID); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("evicted basis still readable", err)
	}
	var bases, classes int
	f.c.DB.QueryRow(`SELECT count(*) FROM catalog_facet_bases`).Scan(&bases)
	f.c.DB.QueryRow(`SELECT count(*) FROM catalog_facet_base_classes`).Scan(&classes)
	if bases != 0 || classes != 0 {
		t.Fatal("bounded GC did not retire metadata", bases, classes)
	}
}

func TestFacetRuntimeFallbackSnapshotSurvivesConcurrentStagingRetirement(t *testing.T) {
	f := runtimeFixture(t, 18, 128<<20)
	req := f.request
	req.Q = ""
	out, err := f.r.Initial(f.ctx, "caller", f.viewer, req)
	if err != nil {
		t.Fatal(err)
	}
	id := out.Preparing.QueryID
	out.Release()
	d, _ := f.r.demand(f.scope)
	f.r.maintain(context.Background(), d)
	f.r.maintain(context.Background(), d)
	f.advance(10 * time.Second)
	staged := d.build
	var retired atomic.Bool
	ctx := WithFacetWindowAuthority(context.Background(), func(ctx context.Context, caller readprep.Caller, s FacetBaseSnapshot) error {
		if err := CheckFacetWindowAuthority(f.ctx, caller, s); err != nil {
			return err
		}
		if s.ID == staged && dbwork.Snapshot(ctx) != nil && retired.CompareAndSwap(false, true) {
			ended := make(chan error, 1)
			go func() {
				ended <- dbwork.WithWriteTxContext(context.Background(), f.c.DB, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error { _, err := f.r.retire(ctx, tx, staged); return err })
			}()
			select {
			case err := <-ended:
				return err
			case <-time.After(5 * time.Second):
				return errors.New("retirement blocked by read snapshot")
			}
		}
		return nil
	})
	out, err = f.r.Poll(ctx, "caller", id)
	if err != nil {
		t.Fatal("racing fallback became failure", err)
	}
	defer out.Release()
	if !retired.Load() || out.Fallback == nil || len(out.Fallback.Values) != 2 || !out.Fallback.TotalIsLowerBound {
		t.Fatal("fallback lost coherent snapshot", out)
	}
}

func TestFacetRuntimeColdDeterministicFailureIsTerminal(t *testing.T) {
	f := runtimeFixture(t, 1, 128<<20)
	out, err := f.r.Initial(f.ctx, "caller", f.viewer, f.request)
	if err != nil {
		t.Fatal(err)
	}
	id := out.Preparing.QueryID
	out.Release()
	f.c.Exec(`INSERT INTO catalog_derivation_failures(domain,entity_id,reason,failed_ms) VALUES(33,?,'deterministic fixture failure',1)`, f.item.ID)
	f.start(t)
	deadline := time.Now().Add(8 * time.Second)
	for {
		out, err = f.r.Poll(f.ctx, "caller", id)
		out.Release()
		if err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failure stayed in preparing")
		}
		time.Sleep(time.Millisecond)
	}
	if !errors.Is(err, readprep.ErrFailed) {
		t.Fatal("deterministic failure did not terminate", err)
	}
}

type runtimeRetiringProducer struct {
	facetFixtureProducer
	steps atomic.Int64
}

func (p *runtimeRetiringProducer) RetireFacetBaseStep(_ context.Context, _ *sql.Tx, _ FacetBaseSnapshot) (bool, error) {
	return p.steps.Add(1) >= 2, nil
}
func TestFacetRuntimeGenericSourceRetiresExtraRowsBeforeBase(t *testing.T) {
	f := runtimeFixture(t, 1, 128<<20)
	producer := &runtimeRetiringProducer{facetFixtureProducer: facetFixtureProducer{facts: []FacetBaseFact{{Value: "a", Label: "A", Count: 1}}}}
	capture := func(_ context.Context, _ compactcatalog.ReadQuery, s FacetBaseScope) (FacetBasePlan, error) {
		return FacetBasePlan{Scope: s, Witness: "source-v1", Producer: producer}, nil
	}
	if err := f.r.RegisterSource("extra", capture); err != nil {
		t.Fatal(err)
	}
	scope := f.scope
	scope.Source = "extra:[]"
	d, err := f.r.demand(scope)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10 && d.ready == 0; i++ {
		f.r.maintain(context.Background(), d)
	}
	id := d.ready
	if id == 0 {
		t.Fatal("custom producer did not activate")
	}
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE catalog_facet_base_classes SET active_base=NULL WHERE active_base=?`, id)
		return err
	})
	for i := 0; i < 3; i++ {
		done := false
		err := dbwork.WithWriteTxContext(context.Background(), f.c.DB, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			done, err = f.r.retire(ctx, tx, id)
			return err
		})
		if err != nil || done != (i == 2) {
			t.Fatal("source/shared retirement did not yield in bounded phases", i, done, err)
		}
		if i == 0 {
			var complete, rows int
			f.c.DB.QueryRow(`SELECT complete,total FROM catalog_facet_bases WHERE id=?`, id).Scan(&complete, &rows)
			if complete != 0 || rows != 1 {
				t.Fatal("partial retirement still readable or shared rows removed too early", complete, rows)
			}
		}
	}
	if producer.steps.Load() != 3 {
		t.Fatal(producer.steps.Load())
	}
}

func TestFacetRuntimeRunServesAllCoalescedDemandsAndStops(t *testing.T) {
	f := runtimeFixture(t, 5, 128<<20)
	// One is already accepted; its no-op pass must not strand the second demand.
	runtimeBuild(t, f)
	other := f.request
	other.Field = "year"
	other.Q = ""
	other.Limit = 1
	out, err := f.r.Initial(f.ctx, "caller", f.viewer, other)
	if err != nil {
		t.Fatal(err)
	}
	id := out.Preparing.QueryID
	out.Release()
	f.start(t)
	out, err = f.r.Poll(f.ctx, "caller", id)
	page := runtimeReady(t, f, out, err)
	if page.Field != "year" || len(page.Values) != 1 || page.Values[0].Value != "2000" {
		t.Fatal(page)
	}
}

func TestFacetRuntimeCompletedGCSweepSleepsAndFailedReadsDoNotPinRegistry(t *testing.T) {
	f := runtimeFixture(t, 18, 128<<20)
	d := runtimeBuild(t, f)
	for i := 0; i < 100 && f.r.gcPending; i++ {
		if err := f.r.collect(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if f.r.gcPending || f.r.gcComplete != 15 {
		t.Fatal("GC never completed a bounded directory sweep", f.r.gcComplete)
	}
	before := dbwork.Publications()
	for i := 0; i < 20; i++ {
		if err := f.r.collect(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if dbwork.Publications() != before {
		t.Fatal("completed GC sweep kept acquiring writer")
	}
	f.r.mu.Lock()
	d.err = ErrFacetBaseCapacity
	original := d.touched
	f.r.mu.Unlock()
	f.advance(4 * time.Minute)
	if _, err := f.r.demand(f.scope); err != nil {
		t.Fatal(err)
	}
	if d.touched != original {
		t.Fatal("failed request renewed terminal retention")
	}
	f.advance(time.Minute)
	f.r.nextDemand()
	if len(f.r.demands) != 0 || !f.r.gcPending {
		t.Fatal("terminal demand did not expire/restart cleanup")
	}
}

func TestFacetRuntimeRestrictedAuthorityRejectsOldPolicyFactsWithoutRejectingGenreChurn(t *testing.T) {
	f := runtimeFixture(t, 3, 128<<20)
	maximum := 100
	f.viewer.Restrictions = identity.ContentRestrictions{MaximumAge: &maximum, BlockedLabels: []string{"adult"}}
	f.scope.Policy = FacetBasePolicy(f.viewer.Restrictions)
	f.scope.Source = RestrictedMemberFacetBaseSource
	runtimeBuild(t, f)
	out, err := f.r.Initial(f.ctx, "caller", f.viewer, f.request)
	old := runtimeReady(t, f, out, err)
	if old.Total != 3 {
		t.Fatal("restricted production reducer did not count visible facts", old)
	}
	f.c.Genres(f.item.ID, "fixture", "New content")
	out, err = f.r.Initial(f.ctx, "caller", f.viewer, f.request)
	still := runtimeReady(t, f, out, err)
	if still.Window.Basis != old.Window.Basis {
		t.Fatal("genre content churn invalidated narrow authority")
	}
	f.c.Attributes(f.item.ID, "label", "adult")
	if _, err = f.r.Poll(f.ctx, "caller", old.Window.QueryID); !errors.Is(err, readprep.ErrStale) {
		t.Fatal("old policy facts survived eligibility revocation", err)
	}
	out, err = f.r.Initial(f.ctx, "caller", f.viewer, f.request)
	if err != nil || out.Page != nil || out.Preparing == nil {
		t.Fatal("revoked facts returned instead of cold preparation", out, err)
	}
	out.Release()
}

type runtimeFinalCapacityProducer struct{}

func (runtimeFinalCapacityProducer) Next(ctx context.Context, tx *sql.Tx, s FacetBaseSnapshot, _ string) (FacetBaseBatch, error) {
	_, err := tx.ExecContext(ctx, `INSERT INTO runtime_candidates(base_id,bytes) VALUES(?,zeroblob(131072))`, s.ID)
	return FacetBaseBatch{Done: true}, err
}
func (runtimeFinalCapacityProducer) ValidatePublication(context.Context, *sql.Tx, FacetBaseSnapshot) error {
	return nil
}
func TestFacetRuntimeFinalSourceOnlyBatchCannotPublishBeyondGlobalLedger(t *testing.T) {
	f := runtimeFixture(t, 1, 64<<10)
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TABLE runtime_candidates(base_id INTEGER REFERENCES catalog_facet_bases(id),bytes BLOB); CREATE TRIGGER runtime_candidate_charge AFTER INSERT ON runtime_candidates BEGIN UPDATE catalog_facet_base_usage SET used_bytes=used_bytes+length(NEW.bytes) WHERE id=1; END;`)
		return err
	})
	source := func(_ context.Context, _ compactcatalog.ReadQuery, s FacetBaseScope) (FacetBasePlan, error) {
		return FacetBasePlan{Scope: s, Witness: "extra-candidate-source", Producer: runtimeFinalCapacityProducer{}}, nil
	}
	if err := f.r.RegisterSource("candidate-only", source); err != nil {
		t.Fatal(err)
	}
	scope := f.scope
	scope.Source = "candidate-only"
	d, err := f.r.demand(scope)
	if err != nil {
		t.Fatal(err)
	}
	f.r.maintain(context.Background(), d)
	f.r.maintain(context.Background(), d)
	if !errors.Is(d.err, ErrFacetBaseCapacity) || d.ready != 0 {
		t.Fatal("oversize final candidate phase activated", d.err, d.ready)
	}
	var candidates, active int
	f.c.DB.QueryRow(`SELECT count(*) FROM runtime_candidates`).Scan(&candidates)
	f.c.DB.QueryRow(`SELECT count(*) FROM catalog_facet_base_classes WHERE active_base IS NOT NULL`).Scan(&active)
	if candidates != 0 || active != 0 {
		t.Fatal("failed final phase did not roll back atomically", candidates, active)
	}
	if _, err = NewFacetRuntime(f.c.DB, f.store, CheckFacetWindowAuthority, 1); !errors.Is(err, ErrFacetBaseCapacity) {
		t.Fatal("unaccounted registry constructed below fixed overhead", err)
	}
}

func TestFacetRuntimeScanAndUpstreamPendingAdmitNoWritersIncludingRetireGC(t *testing.T) {
	f := runtimeFixture(t, 18, 128<<20)
	d := runtimeBuild(t, f)
	out, err := f.r.Initial(f.ctx, "caller", f.viewer, f.request)
	old := runtimeReady(t, f, out, err)
	f.c.Genres(f.item.ID, "fixture", "Changed A", "Changed B")
	f.c.Drain()
	f.r.maintain(context.Background(), d)
	staged := d.build
	if staged == 0 {
		t.Fatal("missing staged successor")
	}
	f.r.maintenanceFailure(context.Background(), d, staged, compactcatalog.ErrFacetProducerChanged)
	if d.retiring != staged {
		t.Fatal("fixture did not request retirement")
	}
	checkNoWriters := func() {
		t.Helper()
		before := dbwork.WriteGate().Stats()
		published := dbwork.Publications()
		var called bool
		f.r.maintain(context.Background(), d)
		if err := f.r.collect(context.Background()); err != nil {
			t.Fatal(err)
		}
		err := f.r.writeBase(context.Background(), func(context.Context, *sql.Tx) error { called = true; return nil })
		after := dbwork.WriteGate().Stats()
		if !errors.Is(err, errFacetRuntimeDeferred) || called || after.Acquired != before.Acquired || after.HeldMilli != before.HeldMilli || dbwork.Publications() != published {
			t.Fatal("pending scan/source admitted BASE/GC writer", called, err, before, after)
		}
		var exists bool
		if err = f.c.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_facet_bases WHERE id=?)`, staged).Scan(&exists); err != nil || !exists {
			t.Fatal("retiring base changed while deferred", exists, err)
		}
	}
	for _, state := range []string{"queued", "running"} {
		f.c.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES('runtime-scan','films',?,'2026-10-28T00:00:00Z') ON CONFLICT(id) DO UPDATE SET status=excluded.status`, state)
		if _, err := compactcatalog.CaptureFacetProducerFence(context.Background(), f.c.DB); err != nil {
			t.Fatal("not testing clean batch gap", err)
		}
		checkNoWriters()
		out, err = f.r.Initial(f.ctx, "caller", f.viewer, f.request)
		served := runtimeReady(t, f, out, err)
		if served.Window.Basis != old.Window.Basis || served.Total != 18 {
			t.Fatal("scan blocked COMPLETE reads", served)
		}
	}
	f.c.Exec(`UPDATE jobs SET status='paused' WHERE id='runtime-scan'`)
	called := false
	if err := f.r.writeBase(context.Background(), func(context.Context, *sql.Tx) error { called = true; return nil }); err != nil || !called {
		t.Fatal("paused job counted as active writer", err)
	}
	f.c.Exec(`UPDATE jobs SET status='complete' WHERE id='runtime-scan'`)
	f.c.Genres(f.item.ID, "fixture", "Undrained source")
	checkNoWriters()
	f.c.Exec(`INSERT INTO catalog_derivation_failures(domain,entity_id,reason,failed_ms) VALUES(33,?,'failed source',1)`, f.item.ID)
	checkNoWriters()
	rows, err := f.c.DB.Query(`EXPLAIN QUERY PLAN SELECT EXISTS(SELECT 1 FROM jobs INDEXED BY catalog_facet_active_scans WHERE status IN('queued','running'))`)
	if err != nil {
		t.Fatal(err)
	}
	indexed := false
	for rows.Next() {
		var a, b, c int
		var detail string
		if err = rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		indexed = indexed || strings.Contains(detail, "catalog_facet_active_scans")
	}
	rows.Close()
	if !indexed {
		t.Fatal("active scan predicate did not use partial index")
	}
}

func TestFacetRuntimeScanStartingDuringWriterAdmissionCannotMutateBase(t *testing.T) {
	f := runtimeFixture(t, 1, 128<<20)
	held, err := dbwork.Begin(context.Background(), f.c.DB, dbwork.ClassInteractive)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	var called atomic.Bool
	ended := make(chan error, 1)
	go func() {
		ended <- f.r.writeBase(context.Background(), func(context.Context, *sql.Tx) error { called.Store(true); return nil })
	}()
	deadline := time.Now().Add(5 * time.Second)
	for dbwork.WriteGate().Stats().Waiting[dbwork.ClassBackgroundMedia.String()] == 0 {
		if time.Now().After(deadline) {
			t.Fatal("runtime did not queue behind writer")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err = held.Tx().ExecContext(held.Context(), `INSERT INTO jobs(id,library_id,status,created_at) VALUES('admission-race','films','running','2026-10-28T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err = held.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-ended:
		if !errors.Is(err, errFacetRuntimeDeferred) || called.Load() {
			t.Fatal("scan admission race executed BASE writes", err, called.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deferred writer did not finish")
	}
}

func TestFacetRuntimeFallbackContentionDoesNotBypassBudgetForOrdinaryScalar(t *testing.T) {
	f := runtimeFixture(t, 1, 128<<20)
	f.store.Close()
	store, err := readprep.New(readprep.Config{Workers: 1, MaxBytes: 128 << 10, Now: f.r.now})
	if err != nil {
		t.Fatal(err)
	}
	f.store = store
	f.r, err = NewFacetRuntime(f.c.DB, store, CheckFacetWindowAuthority, 128<<20)
	if err != nil {
		t.Fatal(err)
	}
	f.r.now = func() time.Time { return time.Unix(0, f.clock.Load()) }
	f.r.windows.now = f.r.now
	value := strings.Repeat("x", 45<<10)
	f.c.Genres(f.item.ID, "fixture", value)
	f.c.Drain()
	req := f.request
	req.Q = ""
	out, err := f.r.Initial(f.ctx, "caller", f.viewer, req)
	if err != nil {
		t.Fatal(err)
	}
	id := out.Preparing.QueryID
	out.Release()
	d, _ := f.r.demand(f.scope)
	f.r.maintain(context.Background(), d)
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		snap, err := facetBaseSnapshot(ctx, tx, d.build, false)
		if err != nil {
			return err
		}
		b := FacetBases{DB: f.c.DB, MaxBytes: f.r.baseLimit}
		return b.put(ctx, tx, snap, FacetBaseFact{Value: value, Label: value, Count: 1})
	})
	f.advance(10 * time.Second)
	out, err = f.r.Poll(f.ctx, "caller", id)
	out.Release()
	if !errors.Is(err, readprep.ErrCapacity) {
		t.Fatal("ordinary scalar bypassed admission as oversized exception", err)
	}
	if f.store.Stats().Deliveries != 0 || f.store.Stats().Bytes > f.store.MaxBytes() {
		t.Fatal("fallback leaked or exceeded reservation", f.store.Stats())
	}
}

type runtimePanicProducer struct{}

func (runtimePanicProducer) Next(context.Context, *sql.Tx, FacetBaseSnapshot, string) (FacetBaseBatch, error) {
	panic("runtime producer fixture panic")
}
func (runtimePanicProducer) ValidatePublication(context.Context, *sql.Tx, FacetBaseSnapshot) error {
	return nil
}
func TestFacetRuntimePanicStopsAndNotifiesDemandsBeforeSupervisorRecovery(t *testing.T) {
	f := runtimeFixture(t, 1, 128<<20)
	capture := func(_ context.Context, _ compactcatalog.ReadQuery, s FacetBaseScope) (FacetBasePlan, error) {
		return FacetBasePlan{Scope: s, Witness: "panic-source", Producer: runtimePanicProducer{}}, nil
	}
	if err := f.r.RegisterSource("panic-source", capture); err != nil {
		t.Fatal(err)
	}
	scope := f.scope
	scope.Source = "panic-source"
	d, err := f.r.demand(scope)
	if err != nil {
		t.Fatal(err)
	}
	recovered := make(chan any, 1)
	go func() { defer func() { recovered <- recover() }(); _ = f.r.Run(context.Background()) }()
	select {
	case value := <-recovered:
		if value == nil {
			t.Fatal("fixture panic did not reach supervisor boundary")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("panic failed to release worker/writer")
	}
	f.r.mu.Lock()
	stopped, terminal := f.r.stopped, d.err
	f.r.mu.Unlock()
	if !stopped || !errors.Is(terminal, readprep.ErrClosed) {
		t.Fatal("panic left runtime/cold demand live", stopped, terminal)
	}
	if _, err = f.r.Demand(context.Background(), scope); !errors.Is(err, readprep.ErrClosed) {
		t.Fatal("stopped worker accepted cold demand", err)
	}
	if _, err := dbwork.ExecWrite(context.Background(), f.c.DB, dbwork.ClassInteractive, `UPDATE catalog_facet_base_usage SET used_bytes=used_bytes WHERE id=1`); err != nil {
		t.Fatal("panic retained writer gate", err)
	}
}
