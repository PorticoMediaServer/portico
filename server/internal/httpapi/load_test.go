package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/httpapi/fixture"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// The concurrency design is only worth what it can be shown to do. This test
// drives the server the way a household actually does — many viewers browsing,
// searching, opening detail pages, recording personal state and controlling
// playback — while bulk catalogue work runs underneath, and then asserts on the
// numbers the design is about: no lock retry escaped to a caller, the pool never
// exceeded its policy, and the latency a person experienced stayed inside budget.
//
// PORTICO_PERFORMANCE_TIER=smoke is the quick shape; with no tier set the load run is skipped, which keeps the ordinary suite fast.
// =release runs the shape the server is designed for: 100 concurrent viewers
// against a catalogue large enough that nothing is answered from a warm page.

type performanceTier struct {
	name string
	// shape is the catalogue this tier runs against. It is a real catalogue —
	// assets, attributes, genres, credits, personal state, shows, albums, books
	// and collections — because a fixture of bare `items` rows makes every
	// visibility predicate match nothing and hides the whole cost of the read
	// path behind empty results.
	shape             fixture.Shape
	catalogItems      int
	concurrentViewers int
	iterations        int
	// restrictedViewers is how many of the viewers carry a rating ceiling,
	// blocked labels and a narrowed library list. The restriction predicate is on
	// every hot read, so a tier with none of them measures the easy half.
	restrictedViewers int
	// think models the pause between a person's requests. Without it the test
	// measures a hundred load generators in lockstep rather than a hundred
	// viewers, and the answer it gives is about the harness, not the server.
	think      time.Duration
	maximumP95 time.Duration
	maximumP99 time.Duration
	// allowBoundedOverload lets the smoke tier accept honest refusals: it runs on
	// whatever machine a developer has, so a 503 there is admission working, not
	// a defect. The release and deep tiers accept none.
	allowBoundedOverload bool
	// mediaPath, when set, is a real media file the fixture's one playable item
	// is backed by, so a test can stream actual bytes through the media routes
	// rather than sixteen placeholder ones.
	mediaPath string
	// capabilities publishes a device capability document for every viewer, as
	// a real client does before it plays. It is off where a test measures a
	// viewer's first authenticated request.
	capabilities bool
	// gated turns the audit's budgets into hard gates. The smoke tier reports
	// them; the tiers that make a capacity claim assert them.
	gated bool
}

func currentPerformanceTier() performanceTier {
	switch os.Getenv("PORTICO_PERFORMANCE_TIER") {
	case "release":
		shape := fixture.Release()
		return performanceTier{name: "release", shape: shape, catalogItems: shape.Items(), concurrentViewers: 100, iterations: 6,
			restrictedViewers: 10, think: 250 * time.Millisecond, maximumP95: 500 * time.Millisecond, maximumP99: time.Second, gated: true}
	case "deep":
		shape := fixture.Deep()
		return performanceTier{name: "deep", shape: shape, catalogItems: shape.Items(), concurrentViewers: 200, iterations: 6,
			restrictedViewers: 50, think: 250 * time.Millisecond, maximumP95: 750 * time.Millisecond, maximumP99: 1500 * time.Millisecond, gated: true}
	default:
		shape := fixture.Smoke()
		return performanceTier{name: "smoke", shape: shape, catalogItems: shape.Items(), concurrentViewers: 24, iterations: 3,
			restrictedViewers: 1, maximumP95: 2 * time.Second, maximumP99: 3 * time.Second, allowBoundedOverload: true}
	}
}

type loadFixture struct {
	handler     http.Handler
	db          *sql.DB
	catalog     *catalog.Service
	library     string
	bulkLibrary string
	owner       identity.Envelope
	// viewers are separate accounts and profiles. Modelling a household as one
	// shared profile would be a different test: every personal-state write and
	// every playback generation would collide by design rather than by load.
	viewers []identity.Envelope
	items   []string
	// playable is the one item backed by a real file, so the playback leg of the
	// workload exercises the real session lifecycle.
	playable string
	root     string
}

func newLoadFixture(t *testing.T, tier performanceTier) *loadFixture {
	t.Helper()
	root := t.TempDir()
	// The catalogue is built once per shape and schema, cached in the system
	// temporary directory, and copied per run. Building a deep fixture takes
	// minutes; copying it takes seconds, and a load test that rebuilds its
	// catalogue every run is a load test nobody runs.
	path := filepath.Join(root, "db")
	// A deep catalogue takes a long time to build the first time. Saying so
	// every hundred thousand rows is the difference between a slow build and one
	// that looks hung.
	began := time.Now()
	last := 0
	built, err := fixture.BuildWithConfig(context.Background(), tier.shape, fixture.Config{CanonicalCreditDepartments: true}, path, func(done, total int) {
		if done-last < 100000 && done != total {
			return
		}
		last = done
		t.Logf("building the %s catalogue: %d/%d rows after %s", tier.name, done, total, time.Since(began).Round(time.Second))
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ident, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	cat := catalog.New(db)
	host, err := hosted.New(db, ident, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	player := playback.New(db)
	scanner := ingestion.New(db, cat, assets.Probe{})
	watchdog := dbwork.NewWatchdog(db)
	d := Dependencies{DB: db, Identity: ident, Catalog: cat, Ingestion: scanner, Playback: player, Hosted: host, Watchdog: watchdog}
	facets := composeHTTPFacetFixture(t, &d)
	handler := New(d)

	secret, err := os.ReadFile(storagepaths.Path(root, "setup-token"))
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"authMode": "local", "setupToken": string(secret), "username": "owner", "password": "long-test-password", "name": "Load"})
	setup := httptest.NewRequest("POST", "/v1/setup", bytes.NewReader(body))
	setup.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(w, setup)
	if w.Code != 201 {
		t.Fatalf("setup %d %s", w.Code, w.Body.String())
	}
	var owner identity.Envelope
	if err = json.Unmarshal(w.Body.Bytes(), &owner); err != nil {
		t.Fatal(err)
	}
	library := catalog.Library{ID: built.Movies}
	if err = fixture.Verify(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	// The bulk writers work on a second library. A scan of one library has no
	// business invalidating a viewer's page in another, and keeping them apart is
	// what lets this test measure contention for the writer rather than measure
	// revision conflicts.
	incoming := filepath.Join(root, "incoming")
	if err = os.MkdirAll(incoming, 0700); err != nil {
		t.Fatal(err)
	}
	bulk, err := cat.Create("Incoming", "movie", incoming)
	if err != nil {
		t.Fatal(err)
	}

	// One real playable item, so the playback leg of the workload is the real
	// session lifecycle rather than an approximation of it. It goes into the
	// bulk library, which the fixture does not populate, so the item the viewers
	// browse and the item they play are separate. A tier that streams real bytes
	// supplies a real file for it.
	film := filepath.Join(root, "Film.mp4")
	if err = writeFixtureMedia(film, tier.mediaPath); err != nil {
		t.Fatal(err)
	}
	catTest := catalogtest.New(t, db)
	bulkHandle := catTest.Handle(bulk.ID)
	info, err := os.Stat(film)
	if err != nil {
		t.Fatal(err)
	}
	playableItem := catTest.Entity(compactcatalog.Entity{Library: bulkHandle, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey(incoming, film, 0), Title: "Film", Year: 2020, Added: "2026-01-01T00:00:00.000Z"}, nil)
	catTest.Write(func(ctx context.Context, tx *sql.Tx) error {
		asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: film, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 60})
		if err != nil {
			return err
		}
		return compactcatalog.LinkAssetTx(ctx, tx, playableItem.ID, asset, compactcatalog.Link{})
	})
	catTest.Drain()
	var viewerEnvelopes []identity.Envelope
	playable, _, err := cat.List(catalog.Viewer{Profile: owner.Viewer.ProfileID, Libraries: []string{bulk.ID}}, bulk.ID, "", 1)
	if err != nil || len(playable) != 1 {
		t.Fatalf("the fixture produced no playable item: %v", err)
	}

	// Each viewer is a real member account with its own primary profile. A
	// household is several people, and modelling it as one shared profile would
	// make every personal-state write and every playback generation collide by
	// construction rather than under load.
	allowed := fmt.Sprintf(`["%s","%s","%s","%s","%s"]`, library.ID, bulk.ID, built.Shows, built.Music, built.Books)
	restriction := fixture.Restriction(built)
	for index := 0; index < tier.concurrentViewers; index++ {
		account, profile := identity.Token(), identity.Token()
		if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,x'00',?,1)`, account, account, profile); err != nil {
			t.Fatal(err)
		}
		// The schema installs a membership and a primary profile alongside an
		// account, so these settle the values rather than create the rows.
		if _, err = db.Exec(`INSERT INTO direct_memberships(account_id,role,allowed_libraries,revision,disabled) VALUES(?,'member',?,1,0)
 ON CONFLICT(account_id) DO UPDATE SET role='member',allowed_libraries=excluded.allowed_libraries,disabled=0`, account, allowed); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO direct_profiles(id,account_id,name,is_primary,position) VALUES(?,?,?,1,0)
 ON CONFLICT(id) DO UPDATE SET account_id=excluded.account_id,is_primary=1,deleted=0`, profile, account, fmt.Sprintf("Viewer %03d", index)); err != nil {
			t.Fatal(err)
		}
		// A slice of the household is restricted: a rating ceiling, blocked labels
		// and a narrowed library list, all three, because a profile carrying one
		// of them exercises one clause of the predicate and none of the others.
		if index < tier.restrictedViewers {
			narrowed, _ := json.Marshal(restriction.Libraries)
			if _, err = db.Exec(`UPDATE direct_profiles SET allowed_libraries=? WHERE id=?`, string(narrowed), profile); err != nil {
				t.Fatal(err)
			}
			blocked, _ := json.Marshal(restriction.BlockedLabels)
			if _, err = db.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,blocked_labels,allow_unrated,revision)
 VALUES(?,?,?,0,1) ON CONFLICT(profile_id) DO UPDATE SET maximum_age=excluded.maximum_age,blocked_labels=excluded.blocked_labels,allow_unrated=0,revision=profile_restrictions.revision+1`,
				profile, restriction.MaximumAge, string(blocked)); err != nil {
				t.Fatalf("restricting viewer %d: %v", index, err)
			}
		}
		envelope, issueErr := ident.Issue(account, profile, "local", "member", 1)
		if issueErr != nil {
			t.Fatal(issueErr)
		}
		viewerEnvelopes = append(viewerEnvelopes, envelope)
		if tier.capabilities {
			payload, _ := json.Marshal(webCapabilities())
			request := httptest.NewRequest("PUT", "/v1/me/devices/current/capabilities", bytes.NewReader(payload))
			request.Header.Set("Authorization", "Bearer "+envelope.AccessToken)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNoContent {
				t.Fatalf("viewer %d capabilities: %d %s", index, response.Code, response.Body.String())
			}
		}
	}
	rows, err := db.Query(`SELECT e.id FROM catalog_entities e JOIN catalog_libraries l ON l.id=e.library_id WHERE l.library_id=? AND e.kind=? ORDER BY e.id LIMIT 512`, library.ID, compactcatalog.Movie)
	if err != nil {
		t.Fatal(err)
	}
	f := &loadFixture{handler: handler, db: db, catalog: cat, library: library.ID, bulkLibrary: bulk.ID, owner: owner, viewers: viewerEnvelopes, root: root, playable: playableItem.Public}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			f.items = append(f.items, catTest.Public(id))
		}
	}
	rows.Close()
	if len(f.items) == 0 {
		t.Fatal("the fixture produced no items")
	}
	// Credits must exercise the same actor/starring recommendation path as
	// provider ingestion. Merely having credit rows would miss label drift.
	code, _, recommendationBody := f.callTimed(f.owner.AccessToken, "GET", "/v1/items/"+f.items[0]+"/recommendations", nil)
	var recommended struct {
		Rows []catalog.HomeRow `json:"rows"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(recommendationBody), &recommended) != nil {
		t.Fatalf("fixture actor recommendations: %d %s", code, recommendationBody)
	}
	starring := false
	for _, row := range recommended.Rows {
		if row.Relation == "starring" && row.Provider != "" && row.EvidenceID != "" && len(row.Entries) != 0 {
			starring = true
		}
	}
	if !starring {
		t.Fatal("fixture is missing provider-style actor/starring recommendations")
	}
	catTest.Drain()
	facets.Start()
	// Tiny statement probes expect a settled facet answer. Prepare only their
	// exact requests outside instrumentation; other fixture users (including the
	// cold-principal-cache test) retain their original first-request behavior.
	if tier.shape == fixture.Tiny() && (tier.name == "cost" || tier.name == "shape") && len(f.viewers) != 0 {
		request := func(ctx context.Context, method, path, body string) *httptest.ResponseRecorder {
			r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
			r.Header.Set("Authorization", "Bearer "+f.viewers[0].AccessToken)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			return w
		}
		for _, field := range []string{"contentRating", "genre"} {
			// The cold-scope and ready-BASE paths have distinct preparation
			// keys. Settle both through the same exact HTTP request.
			for range 2 {
				w := readyHTTPFacetFixture(t, "/v1/libraries/"+f.library+"/facets?field="+field, request, facets.WaitPrepared)
				if w.Code != http.StatusOK {
					t.Fatalf("warm fixture facet %s: %d %s", field, w.Code, w.Body.String())
				}
			}
		}
	}
	return f
}

// cacheReport reads the authority caches through the diagnostics route the
// server publishes them on, which is the same view an operator has.
func (f *loadFixture) cacheReport() string {
	code, _, body := f.callTimed(f.owner.AccessToken, "GET", "/v1/admin/diagnostics/concurrency", nil)
	if code != 200 {
		return fmt.Sprintf("principalCache unavailable (%d)", code)
	}
	var out struct {
		Authorization struct {
			Principals struct {
				Entries  int    `json:"entries"`
				Capacity int    `json:"capacity"`
				Hits     uint64 `json:"hits"`
				Misses   uint64 `json:"misses"`
			} `json:"principals"`
			LibraryGrants int `json:"libraryGrants"`
		} `json:"authorizationCaches"`
	}
	if json.Unmarshal([]byte(body), &out) != nil {
		return "principalCache unreadable"
	}
	principals := out.Authorization.Principals
	rate := 0.0
	if total := principals.Hits + principals.Misses; total > 0 {
		rate = 100 * float64(principals.Hits) / float64(total)
	}
	return fmt.Sprintf("principalCache hits=%d misses=%d (%.0f%% hit) entries=%d/%d libraryGrants=%d",
		principals.Hits, principals.Misses, rate, principals.Entries, principals.Capacity, out.Authorization.LibraryGrants)
}

func seedCatalogue(db *sql.DB, library string, count int) error {
	const batch = 500
	for start := 0; start < count; start += batch {
		err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
			for i := start; i < start+batch && i < count; i++ {
				if err := tl6BulkMovieTx(context.Background(), tx, library, fmt.Sprintf("load-%06d", i), fmt.Sprintf("Title %06d", i), 1950+i%75, ""); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// viewerResult is one viewer's observed experience.
type viewerResult struct {
	latencies []time.Duration
	// byRoute is the same latencies split by the leg of the session that
	// produced them. A run-wide p95 says the server was slow; a per-leg p95 says
	// which page was, which is the number a fix is aimed at.
	byRoute map[string][]time.Duration
	nonOK   int
	// busy counts honest admission refusals: 503 server_busy and 429 search_busy.
	busy int
	// refused counts an authorisation answer a restricted viewer is supposed to
	// get: the title it asked for is blocked, or the library is outside its list.
	refused int
	// stale counts 409 answers. A catalogue that is being written while it is
	// being read will move under a reader, and telling the client so is the
	// design working: it is never served a silently shifted page. These are
	// counted, bounded, and not treated as failures.
	stale int
	// failures records what failed and with what status, so a regression names
	// the route rather than only the count.
	failures map[string]int
}

func TestMixedViewerLoadUnderBackgroundWork(t *testing.T) {
	if tl6RaceDetector {
		// Every ceiling in this test is a measured latency. Under the race detector
		// the same work takes roughly ten times as long, so the test would be
		// reporting on the detector rather than on the server, and its refusal and
		// revision-conflict counts would be about contention the detector created.
		t.Skip("latency ceilings are not meaningful under the race detector")
	}
	// A load run takes more than half a minute even at the smoke shape, so it runs
	// only when a tier is named (PORTICO_PERFORMANCE_TIER=smoke|release|deep). The
	// default suite's guards for the same property are the write-gate tests
	// (TestForegroundReadsNeverWaitForTheWriteGate and the lane tests).
	if tier := os.Getenv("PORTICO_PERFORMANCE_TIER"); tier != "smoke" && tier != "release" && tier != "deep" {
		t.Skip("load run: set PORTICO_PERFORMANCE_TIER=smoke, release or deep")
	}
	tier := currentPerformanceTier()
	dbwork.ResetProbes()
	t.Cleanup(dbwork.ResetProbes)
	f := newLoadFixture(t, tier)

	// The bulk work runs for the whole measurement window. These are the two
	// shapes that matter: a scan publishing catalogue rows in small batches on
	// the write-heavy lane, and a metadata refresh rewriting projections on the
	// metadata lane. Both go through the write gate at background-media, and both
	// yield at every batch boundary.
	ctx, stop := context.WithCancel(context.Background())
	var scanned, refreshed atomic.Int64
	var background sync.WaitGroup
	background.Add(2)
	go func() {
		defer background.Done()
		runBulkWriter(ctx, f.db, "scan", func(ctx context.Context, tx *sql.Tx, n int) error {
			return tl6BulkMovieTx(ctx, tx, f.bulkLibrary, fmt.Sprintf("scan-%06d", n), fmt.Sprintf("Scanned %06d", n), 2001, "")
		}, &scanned)
	}()
	go func() {
		defer background.Done()
		runBulkWriter(ctx, f.db, "metadata", func(ctx context.Context, tx *sql.Tx, n int) error {
			id := 1 + n%256
			return tl6BulkMovieTx(ctx, tx, f.bulkLibrary, fmt.Sprintf("scan-%06d", id), fmt.Sprintf("Scanned %06d", id), 2001, fmt.Sprintf("refreshed %d", n))
		}, &refreshed)
	}()

	before := struct {
		pool      dbwork.PoolStats
		gate      dbwork.GateStats
		authority uint64
	}{dbwork.Pool(f.db), dbwork.WriteGate().Stats(), dbwork.AuthorityGeneration()}
	// The longest hold is a maximum, not a counter, so it cannot be subtracted:
	// it is reset, or it reports the fixture's own 500-row build transactions
	// rather than anything the run did.
	dbwork.WriteGate().ResetPeak()
	ResetRouteCosts()
	if os.Getenv("PORTICO_LOAD_PROFILE") == "1" {
		dbwork.ProfileStatements(true)
		t.Cleanup(func() { dbwork.ProfileStatements(false) })
	}
	start := time.Now()
	results := make([]viewerResult, tier.concurrentViewers)
	var viewers sync.WaitGroup
	for index := 0; index < tier.concurrentViewers; index++ {
		viewers.Add(1)
		go func(index int) {
			defer viewers.Done()
			results[index] = f.runViewer(tier, index)
		}(index)
	}
	viewers.Wait()
	elapsed := time.Since(start)
	stop()
	background.Wait()

	var all []time.Duration
	perRoute := map[string][]time.Duration{}
	nonOK, busy, stale, refused := 0, 0, 0, 0
	failures := map[string]int{}
	for _, result := range results {
		all = append(all, result.latencies...)
		for route, latencies := range result.byRoute {
			perRoute[route] = append(perRoute[route], latencies...)
		}
		nonOK += result.nonOK
		busy += result.busy
		stale += result.stale
		refused += result.refused
		for key, count := range result.failures {
			failures[key] += count
		}
	}
	if len(all) == 0 {
		t.Fatal("no requests were measured")
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	p50 := all[len(all)*50/100]
	p95 := all[min(len(all)-1, len(all)*95/100)]
	p99 := all[min(len(all)-1, len(all)*99/100)]

	routes := make([]string, 0, len(perRoute))
	for route := range perRoute {
		routes = append(routes, route)
	}
	sort.Strings(routes)
	for _, route := range routes {
		latencies := perRoute[route]
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		t.Logf("  leg %-18s n=%4d p50=%7s p95=%7s p99=%7s max=%7s", route, len(latencies),
			latencies[len(latencies)*50/100].Round(time.Millisecond),
			latencies[min(len(latencies)-1, len(latencies)*95/100)].Round(time.Millisecond),
			latencies[min(len(latencies)-1, len(latencies)*99/100)].Round(time.Millisecond),
			latencies[len(latencies)-1].Round(time.Millisecond))
	}
	if os.Getenv("PORTICO_LOAD_PROFILE") == "1" {
		// Where the time went, by statement shape. A statement count says a route
		// is expensive; only this says which of its statements is.
		for index, entry := range dbwork.StatementProfile() {
			if index >= 25 {
				break
			}
			t.Logf("  sql %8.0f ms  n=%6d  mean=%7.1f us  %s", entry.Millis(), entry.Count, entry.MeanMicros(), entry.Shape)
		}
	}

	pool := dbwork.Pool(f.db)
	gate := dbwork.WriteGate().Stats()
	// Report the measured window, not the fixture's own seeding.
	gate.Acquired -= before.gate.Acquired
	gate.Queued -= before.gate.Queued
	gate.QueueWaitMilli -= before.gate.QueueWaitMilli
	pool.WaitCount -= before.pool.WaitCount
	pool.WaitMillis -= before.pool.WaitMillis
	pressure := dbwork.Pressure()
	t.Logf("tier=%s viewers=%d requests=%d catalogue=%d elapsed=%s p50=%s p95=%s p99=%s",
		tier.name, tier.concurrentViewers, len(all), tier.catalogItems, elapsed.Round(time.Millisecond), p50.Round(time.Millisecond), p95.Round(time.Millisecond), p99.Round(time.Millisecond))
	t.Logf("pool open=%d/%d inUse=%d waitCount=%d waitMillis=%d", pool.OpenConnections, pool.MaxOpenConnections, pool.InUse, pool.WaitCount, pool.WaitMillis)
	t.Logf("writeGate acquired=%d queued=%d queueWaitMillis=%d maxHeldMillis=%d", gate.Acquired, gate.Queued, gate.QueueWaitMilli, gate.MaxHeldMilli)
	t.Logf("background scanBatches=%d refreshBatches=%d yields=%d yieldWaitMillis=%d", scanned.Load(), refreshed.Load(), pressure.Yields, pressure.YieldWaitMillis)
	// The authority caches are what keep a request from re-resolving its viewer
	// out of the database. They are fenced on the authority generation, so a
	// write that merely looks like an authority write empties all of them at
	// once: the generation delta over a run says whether they could hold at all,
	// and the hit rate says whether they did.
	t.Logf("authority generationBumps=%d %s", dbwork.AuthorityGeneration()-before.authority, f.cacheReport())
	t.Logf("failed=%d admissionRefused=%d revisionConflicts=%d restrictionRefusals=%d", nonOK, busy, stale, refused)
	if len(failures) > 0 {
		keys := []string{}
		for key := range failures {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			t.Logf("  failure %s x%d", key, failures[key])
		}
	}

	// The pool must never exceed the validated policy, whatever the load.
	if pool.OpenConnections > pool.MaxOpenConnections || pool.MaxOpenConnections != dbwork.DefaultPolicy().MaxOpenConns {
		t.Fatalf("the connection pool left its policy: %#v", pool)
	}
	// Not one caller may have seen a lock escape. This is the whole point of the
	// write gate: contention is resolved by queueing, never by failing.
	if lockEscapes.Load() != 0 {
		t.Fatalf("%d operations escaped as SQLITE_BUSY or SQLITE_LOCKED", lockEscapes.Load())
	}
	// Bulk work must have made progress. A design that simply stops scanning
	// whenever anyone is using the server would pass every latency budget and be
	// useless.
	// Background batches never wait for foreground quiet. Allow startup time,
	// then require both loops to make progress under sustained request traffic.
	if elapsed > 2*time.Second && (scanned.Load() == 0 || refreshed.Load() == 0) {
		t.Fatalf("background work was starved over %s: scan=%d refresh=%d", elapsed.Round(time.Millisecond), scanned.Load(), refreshed.Load())
	}
	// A request may be refused (the server said no, honestly) or told its view
	// moved (409). Neither is a failure. Anything else is.
	if nonOK != 0 {
		t.Fatalf("%d requests failed for a reason other than admission or revision movement", nonOK)
	}
	// Revision conflicts are bounded: if a viewer's page moved under them more
	// than rarely, the catalogue write pattern, not the reader, is at fault.
	if stale*20 > len(all) {
		t.Fatalf("%d of %d requests hit a revision conflict; the read model is churning", stale, len(all))
	}
	assertCostGates(t, tier, len(all), elapsed, pool, gate, scanned.Load()*10)
	if !tier.allowBoundedOverload {
		// Refusal is the server telling the truth about its ceiling, so the gate is
		// on how much of it there is, not on whether any exists. A fifth of the
		// workload being shed means this host has run out of headroom for this
		// number of viewers, which is a capacity finding rather than a defect.
		// Historical allowances describe performance debt; they must not turn a
		// capacity test's zero-refusal target into a passing overload run.
		if busy != 0 {
			t.Fatalf("%d of %d requests were refused; the host has no headroom left at this viewer count", busy, len(all))
		}
		ceilingP95, ceilingP99 := tier.maximumP95, tier.maximumP99
		if p95 > ceilingP95 || p99 > ceilingP99 {
			t.Fatalf("latency budget exceeded: p95=%s (max %s) p99=%s (max %s)", p95, ceilingP95, p99, ceilingP99)
		}
	}
}

// lockEscapes counts any lock error a caller actually saw. It is package-level
// because the background writers and the viewers both report into it.
var lockEscapes atomic.Int64

func runBulkWriter(ctx context.Context, db *sql.DB, name string, write func(context.Context, *sql.Tx, int) error, counter *atomic.Int64) {
	// Ten rows per transaction: small enough that a one-core server releases the
	// writer often enough for a playback control write to win between batches.
	const batchSize = 10
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	n := 0
	for ctx.Err() == nil {
		if !dbwork.Yield(ctx) {
			return
		}
		batchStarted := time.Now()
		err := dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			for i := 0; i < batchSize; i++ {
				n++
				if err := write(ctx, tx, n); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			if dbwork.Retryable(err) {
				lockEscapes.Add(1)
			}
			if ctx.Err() != nil {
				return
			}
			continue
		}
		counter.Add(1)
		// Use the same production pacing as catalogue/metadata workers. The
		// pause is outside the completed transaction and foreground write gate.
		if !dbwork.PaceBackground(ctx, batchStarted) {
			return
		}
	}
}

func (f *loadFixture) callTimed(token, method, path string, body any) (int, time.Duration, string) {
	return f.callTimedHeaders(token, method, path, nil, body)
}

func (f *loadFixture) callTimedHeaders(token, method, path string, headers map[string]string, body any) (int, time.Duration, string) {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	r.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	start := time.Now()
	f.handler.ServeHTTP(w, r)
	return w.Code, time.Since(start), w.Body.String()
}

// runViewer is one person's session: the home screen, two browse pages, a
// search, a detail page, a personal-state write, and a playback session created,
// reported on and stopped.
func (f *loadFixture) runViewer(tier performanceTier, index int) viewerResult {
	result := viewerResult{failures: map[string]int{}, byRoute: map[string][]time.Duration{}}
	token := f.owner.AccessToken
	if index < len(f.viewers) {
		token = f.viewers[index].AccessToken
	}
	// A restricted viewer is refused content it may not see and libraries outside
	// its list. That is the restriction predicate working, not a failure, and a
	// tier that counted it as one would be a tier that could only pass by leaving
	// restrictions out.
	restricted := index < tier.restrictedViewers
	label := ""
	record := func(code int, elapsed time.Duration) {
		result.latencies = append(result.latencies, elapsed)
		result.byRoute[label] = append(result.byRoute[label], elapsed)
		if code < 200 || code > 299 {
			result.failures[fmt.Sprintf("%s=%d", label, code)]++
			switch code {
			case 503, 429:
				result.busy++
			case 409:
				result.stale++
			case 401, 403, 404:
				if restricted {
					result.refused++
					return
				}
				result.nonOK++
			default:
				result.nonOK++
			}
		}
	}
	debug := os.Getenv("PORTICO_LOAD_DEBUG") == "1"
	call := func(name, method, path string, body any) int {
		if tier.think > 0 {
			// A little jitter as well as a pause: a hundred clients that pause for
			// exactly the same time stay in lockstep, which is not what a household
			// looks like either.
			time.Sleep(tier.think/2 + time.Duration(rand.Int63n(int64(tier.think))))
		}
		label = name
		code, elapsed, response := f.callTimed(token, method, path, body)
		if debug && (code < 200 || code > 299) && code != 503 {
			fmt.Printf("DEBUG %s %s -> %d %s\n", method, path, code, response[:min(300, len(response))])
		}
		record(code, elapsed)
		return code
	}
	item := f.items[index%len(f.items)]
	for iteration := 0; iteration < tier.iterations; iteration++ {
		call("home", "GET", "/v1/home", nil)
		call("browse-title", "POST", "/v1/libraries/"+f.library+"/browse", map[string]any{"pivot": "movies", "sort": []map[string]string{{"field": "title", "direction": "asc"}}, "limit": 40})
		call("browse-added", "POST", "/v1/libraries/"+f.library+"/browse", map[string]any{"pivot": "movies", "sort": []map[string]string{{"field": "added", "direction": "desc"}}, "limit": 40})
		// The term has to match. The fixture titles are drawn from a Zipfian
		// vocabulary of `WordNNN` tokens, and this used to search for `Title
		// NNNNNN`, which occurs in no title in the catalogue: every group matched
		// nothing and the most expensive read the server serves was measured
		// empty. Drawing from the same vocabulary the fixture writes means a
		// common token matches a great many documents and a rare one matches few,
		// which is the distribution a real search has.
		call("search", "GET", fmt.Sprintf("/v1/search?q=Word%03d&limit=10", index%400), nil)
		call("item", "GET", "/v1/items/"+item, nil)
		call("detail", "GET", "/v1/items/"+item+"/detail", nil)
		call("personal-state", "PUT", "/v1/items/"+item+"/personal-state", map[string]any{"favorite": iteration%2 == 0, "expectedRevision": int64(iteration), "operationId": fmt.Sprintf("personal-%d-%d", index, iteration)})

		label = "playback-create"
		code, elapsed, body := f.callTimedHeaders(token, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": fmt.Sprintf("load-viewer-%06d-%06d", index, iteration)}, map[string]string{"itemId": f.playable, "startFrom": "beginning"})
		record(code, elapsed)
		if code != 201 {
			continue
		}
		// The rest of the lifecycle a real client performs: report progress, read
		// the session back, then stop it.
		var session playbackv1.SessionView
		if json.Unmarshal([]byte(body), &session) != nil || session.ID == "" {
			continue
		}
		label = "playback-observer"
		code, elapsed, body = f.callTimed(token, "POST", "/v1/playback/sessions/"+session.ID+"/observer", map[string]any{"generation": session.Presentation.Generation})
		record(code, elapsed)
		var grant playbackv1.ObserverGrant
		if code == 200 && json.Unmarshal([]byte(body), &grant) == nil {
			call("playback-progress", "PUT", "/v1/playback/sessions/"+session.ID+"/observation", playbackv1.Observation{Epoch: grant.Epoch, Seq: 1, Generation: session.Presentation.Generation, State: "playing", PositionMs: int64((10 + iteration) * 1000), Rate: 1})
		}
		// The Stop below drains the sample before ending the session.
		call("playback-read", "GET", "/v1/playback/sessions/"+session.ID, nil)
		call("playback-stop", "DELETE", "/v1/playback/sessions/"+session.ID, nil)
	}
	return result
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// writeFixtureMedia backs the fixture's playable item. Sixteen placeholder bytes
// are enough for the session lifecycle; a tier that streams real bytes supplies
// a real file.
func writeFixtureMedia(path, source string) error {
	if source == "" {
		return os.WriteFile(path, []byte("0123456789abcdef"), 0600)
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// routeBudgetClass maps a served route to the class whose budget governs it.
// The mapping is explicit rather than inferred from the lane, because two routes
// can share a lane and mean very different things to a person: a library listing
// and a home page are both `browsing`, and only one of them is a page.
func routeBudgetClass(pattern string) string {
	switch pattern {
	case "GET /v1/me":
		return "auth-only"
	case "GET /v1/libraries", "GET /v1/items", "GET /v1/me/library-navigation", "GET /v1/libraries/{id}/browse-capabilities":
		return "navigation"
	case "GET /v1/home", "GET /v1/content", "GET /v1/libraries/{id}/content":
		return "composite page"
	case "GET /v1/home/rows/{id}", "GET /v1/suggestions", "GET /v1/items/{id}/recommendations", "GET /v1/shows/{id}/recommendations":
		return "row page"
	case "POST /v1/libraries/{id}/browse", "GET /v1/libraries/{id}/facets":
		return "browse"
	case "GET /v1/items/{id}/detail", "GET /v1/items/{id}":
		return "detail"
	case "GET /v1/search":
		return "search"
	case "GET /v1/people":
		return "people"
	case "GET /v1/media/{grant}/{file}", "GET /v1/media/{grant}", "GET /v1/items/{id}/art/{kind}":
		return "media body"
	}
	if strings.HasPrefix(pattern, "POST /v1/playback") || strings.HasPrefix(pattern, "DELETE /v1/playback") || strings.HasPrefix(pattern, "GET /v1/playback") {
		return "playback"
	}
	if strings.Contains(pattern, "personal-state") || strings.Contains(pattern, "/v1/saved") || strings.Contains(pattern, "/v1/playlists") {
		return "personal state"
	}
	return ""
}

// assertCostGates is the half of the tier that a fast machine cannot pass by
// being fast. A statement count is a property of the query shape: it is the same
// on a laptop and on a NAS, and it is the number that regressed silently to
// produce the state this workstream started from.
//
// Above the smoke tier these are hard gates. Where this build does not yet meet
// one, the allowance is named in `Allowances()` with the number it actually
// reaches and the unfinished work it is waiting on — so the gate still fails on
// a regression, and the debt is written down rather than dissolved.
func assertCostGates(t *testing.T, tier performanceTier, requests int, elapsed time.Duration, pool dbwork.PoolStats, gate dbwork.GateStats, scannedRows int64) {
	t.Helper()
	budgets := RouteBudgets()
	allowances := map[string]Allowance{}
	for _, allowance := range Allowances() {
		allowances[allowance.Route] = allowance
	}
	costs := RouteCosts()
	for _, cost := range costs {
		if cost.Requests == 0 {
			continue
		}
		class := routeBudgetClass(cost.Route)
		budget, known := budgets[class]
		if !known {
			continue
		}
		statements, transactions := budget.MaxStatements, budget.MaxTransaction
		note := ""
		if allowance, ok := allowances[cost.Route]; ok {
			statements, transactions = allowance.Statements, allowance.Transactions
			note = "  [allowance: " + allowance.Why + "]"
		}
		t.Logf("%-42s %-14s requests=%4d statements mean=%5.1f max=%3d  acquisitions mean=%4.1f max=%2d  (budget %d/%d)%s",
			cost.Route, class, cost.Requests, cost.MeanStatements(), cost.MaxStatements, cost.MeanAcquisitions(), cost.MaxAcquisitions, statements, transactions, note)
		if !tier.gated {
			continue
		}
		if cost.MaxStatements > statements {
			t.Errorf("%s cost %d statements against a ceiling of %d", cost.Route, cost.MaxStatements, statements)
		}
		if cost.MaxAcquisitions > transactions {
			t.Errorf("%s took %d pooled connections against a ceiling of %d", cost.Route, cost.MaxAcquisitions, transactions)
		}
	}
	if !tier.gated {
		return
	}
	run := DefaultRunBudget()
	allowance := CurrentRunAllowance()
	t.Logf("pool wait ceiling %.1f per request  [allowance: %s]", allowance.PoolWaitPerRequest, allowance.Why)
	run.PoolWaitPerRequest = allowance.PoolWaitPerRequest
	t.Logf("scanner floor %.0f rows/s  [allowance: %s]", allowance.ScannerItemsPerSecond, allowance.ScannerWhy)
	run.ScannerItemsPerSecond = allowance.ScannerItemsPerSecond
	if waits := float64(pool.WaitCount) / float64(max(1, requests)); waits > run.PoolWaitPerRequest {
		t.Errorf("callers waited on the connection pool %.1f times per request against a ceiling of %.1f: the queue has moved somewhere admission cannot see it",
			waits, run.PoolWaitPerRequest)
	}
	if share := float64(pool.WaitMillis) / float64(max(1, elapsed.Milliseconds())); share > run.PoolWaitShareOfWall*float64(tier.concurrentViewers) {
		t.Errorf("accumulated pool wait was %.0f%% of wall time per viewer against a ceiling of %.0f%%",
			100*share/float64(tier.concurrentViewers), 100*run.PoolWaitShareOfWall)
	}
	if time.Duration(gate.MaxHeldMilli)*time.Millisecond > run.GateHold {
		t.Errorf("the longest write-gate hold was %d ms against a ceiling of %s: a long hold is never load, it is a transaction held across work that does not belong inside one",
			gate.MaxHeldMilli, run.GateHold)
	}
	if rate := float64(scannedRows) / elapsed.Seconds(); rate < run.ScannerItemsPerSecond {
		t.Errorf("background work made %.0f rows/s against a floor of %.0f: a server that stops scanning whenever anyone is using it passes every latency budget and lets the catalogue rot",
			rate, run.ScannerItemsPerSecond)
	}
}
