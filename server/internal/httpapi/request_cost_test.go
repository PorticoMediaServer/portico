package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/httpapi/fixture"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// The statement counter is the instrument every later phase is measured with, so
// it gets its own test: one request in, a number out, attributed to the route
// that produced it.
//
// The route is not a strict one. `GET /v1/me` is registered on the default lane
// (route_lanes.go), and `strictPrincipal` is false for that lane, so
// `resolvePrincipal` consults the principal cache on any non-private peer
// (router.go). A session that has already been resolved is therefore answered
// from memory and legitimately charges no statement and no pooled connection.
// "The first request charges SQL" is a claim about cache state, not about the
// route, so this test establishes the state itself:
//
//   - a session the fixture has never presented — cold, must charge SQL;
//   - the same session immediately repeated — warm, may charge nothing, and the
//     zero is accepted only when the cache counts it as a hit;
//   - the same session after the documented rotation signal — cold again, so a
//     route that merely stopped being measured cannot pass as a cached one.
//
// Only then does the composite comparison follow, against the cold
// authentication number rather than the warm one.
func TestRequestCostIsAttributedToOneRoute(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	tier := performanceTier{name: "cost", shape: fixture.Tiny(), concurrentViewers: 2, iterations: 1, maximumP95: time.Minute, maximumP99: time.Minute, allowBoundedOverload: true}
	f := newLoadFixture(t, tier)
	// The fixture prepares facet windows through viewers[0]'s session, so that
	// principal is already in the router's cache and cannot stand in for a first
	// request. viewers[1] is a real member session no fixture request has used.
	coldToken := f.viewers[1].AccessToken

	// Settle the owner's own principal first, so the counter reads below cannot
	// contribute a miss of their own even if this fixture's peer were treated as
	// remote rather than home.
	if code, _, body := f.callTimed(f.owner.AccessToken, "GET", "/v1/me", nil); code != 200 {
		t.Fatalf("owner GET /v1/me %d %s", code, body)
	}
	hitsBefore, missesBefore := principalCacheCounters(t, f)

	ResetRouteCosts()
	observedBefore := dbworkReads()
	if code, _, body := f.callTimed(coldToken, "GET", "/v1/me", nil); code != 200 {
		t.Fatalf("cold GET /v1/me %d %s", code, body)
	}
	cold := RouteCostFor("GET /v1/me")
	if cold.Requests != 1 {
		t.Fatalf("expected one measured request, got %d", cold.Requests)
	}
	if cold.Lane != "default" {
		t.Fatalf("GET /v1/me ran on lane %q, not the default lane this test's cache states assume", cold.Lane)
	}
	if cold.Statements == 0 {
		t.Fatal("a first, uncached authentication ran no statements, which cannot be true")
	}
	if cold.Transactions == 0 {
		t.Fatal("a first, uncached authentication opened no transaction, which cannot be true")
	}
	if cold.Acquisitions == 0 {
		t.Fatal("a first, uncached authentication took no pooled connection, which cannot be true")
	}
	// The attributed counter and the process-wide one must agree that real SQL
	// ran; a broken attribution cannot hide behind the other instrument.
	if dbworkReads() <= observedBefore {
		t.Fatalf("the route meter saw %d statements but the process-wide meter saw none: %#v", cold.Statements, cold)
	}

	// The immediate repeat. It is allowed to cost nothing — that is the point of
	// the cache — but then it must cost nothing because the cache served it.
	ResetRouteCosts()
	if code, _, body := f.callTimed(coldToken, "GET", "/v1/me", nil); code != 200 {
		t.Fatalf("warm GET /v1/me %d %s", code, body)
	}
	warm := RouteCostFor("GET /v1/me")
	hitsAfter, missesAfter := principalCacheCounters(t, f)
	if warm.Requests != 1 {
		t.Fatalf("the repeat was measured %d times, not once: %#v", warm.Requests, warm)
	}
	if warm.Statements >= cold.Statements {
		t.Fatalf("the repeat cost %d statements against the cold request's %d; the principal cache did not hold", warm.Statements, cold.Statements)
	}
	if hitsAfter < hitsBefore+1 {
		t.Fatalf("the repeat charged %d statements but the principal cache recorded no hit (hits %d -> %d): a zero here would not be a cache hit", warm.Statements, hitsBefore, hitsAfter)
	}
	if missesAfter != missesBefore+1 {
		t.Fatalf("expected exactly one live resolution in the window (the cold request), saw %d more misses (misses %d -> %d, hits %d -> %d)", missesAfter-missesBefore, missesBefore, missesAfter, hitsBefore, hitsAfter)
	}
	t.Logf("GET /v1/me cold statements=%d transactions=%d acquisitions=%d | warm statements=%d transactions=%d acquisitions=%d (cache hits %d -> %d, misses %d -> %d)",
		cold.Statements, cold.Transactions, cold.Acquisitions, warm.Statements, warm.Transactions, warm.Acquisitions,
		hitsBefore, hitsAfter, missesBefore, missesAfter)

	// The entry is fenced on the authority generation. The rotation signal is the
	// documented one for an authority change that does not go through a gated
	// write (dbwork.BumpAuthority), and after it the same session must pay for
	// live state again. That is the other half of the warm-zero proof: the zero
	// came from a valid entry, not from a route the meter stopped watching.
	dbwork.BumpAuthority()
	ResetRouteCosts()
	if code, _, body := f.callTimed(coldToken, "GET", "/v1/me", nil); code != 200 {
		t.Fatalf("rotated GET /v1/me %d %s", code, body)
	}
	rotated := RouteCostFor("GET /v1/me")
	if rotated.Requests != 1 || rotated.Statements == 0 || rotated.Acquisitions == 0 {
		t.Fatalf("after an authority rotation the same session was still answered for free: %#v", rotated)
	}

	ResetRouteCosts()
	if code, _, body := f.callTimed(coldToken, "GET", "/v1/home", nil); code != 200 {
		t.Fatalf("GET /v1/home %d %s", code, body)
	}
	home := RouteCostFor("GET /v1/home")
	if home.Requests != 1 || home.Statements == 0 {
		t.Fatalf("home cost was not recorded: %#v", home)
	}
	t.Logf("GET /v1/home statements=%d transactions=%d acquisitions=%d", home.Statements, home.Transactions, home.Acquisitions)
	// A composite page must cost more than an authentication, or the counter is
	// not seeing the composition. The authentication number is the cold one: the
	// warm repeat is allowed to be zero and cannot set the bar.
	if home.Statements <= cold.Statements {
		t.Fatalf("home (%d statements) did not cost more than a cold /v1/me (%d)", home.Statements, cold.Statements)
	}
	// Nothing else may have been attributed to this route.
	if other := RouteCostFor("GET /v1/me"); other.Requests != 0 {
		t.Fatalf("a second route was charged for this request: %#v", other)
	}
}

// principalCacheCounters reads the live principal-cache hit and miss counts from
// the owner diagnostics route. The read is made from the loopback peer: a
// locality-private request resolves its principal live instead of consulting
// the cache, so reading the counters does not move them.
func principalCacheCounters(t *testing.T, f *loadFixture) (hits, misses uint64) {
	t.Helper()
	r := httptest.NewRequest("GET", "/v1/admin/diagnostics/concurrency", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("Authorization", "Bearer "+f.owner.AccessToken)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("GET /v1/admin/diagnostics/concurrency %d %s", w.Code, w.Body.String())
	}
	var document struct {
		Authorization struct {
			Principals struct {
				Hits   uint64 `json:"hits"`
				Misses uint64 `json:"misses"`
			} `json:"principals"`
		} `json:"authorizationCaches"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &document); err != nil {
		t.Fatalf("principal cache counters: %v", err)
	}
	return document.Authorization.Principals.Hits, document.Authorization.Principals.Misses
}

// measureRoute reports what one request cost, counted two ways: the attributed
// per-request counter, and the process-wide counter. The two agree only when
// every statement on the path reached the database through a context. Where they
// disagree, the difference is exactly the set of call sites still using
// `database/sql`'s context-free forms — which is a useful number in its own
// right while the read paths are being converted.
func measureRoute(t *testing.T, f *loadFixture, token, pattern, method, path string, body any) (RouteCost, int64) {
	t.Helper()
	ResetRouteCosts()
	before := dbworkReads()
	code, _, response := f.callTimed(token, method, path, body)
	if code < 200 || code > 299 {
		t.Fatalf("%s %s -> %d %s", method, path, code, response)
	}
	return RouteCostFor(pattern), dbworkReads() - before
}

func dbworkReads() int64 { return int64(dbwork.Reads().Statements) }

// The statement budgets in the audit's load-test tier are the numbers this
// records. It is a report, not a gate: the gates live in the load test.
func TestReadPathStatementBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	tier := performanceTier{name: "cost", shape: fixture.Tiny(), concurrentViewers: 1, iterations: 1, maximumP95: time.Minute, maximumP99: time.Minute, allowBoundedOverload: true}
	f := newLoadFixture(t, tier)
	token := f.viewers[0].AccessToken
	item := f.items[0]
	cases := []struct {
		name, pattern, method, path string
		body                        any
	}{
		{"auth", "GET /v1/me", "GET", "/v1/me", nil},
		{"libraries", "GET /v1/libraries", "GET", "/v1/libraries", nil},
		{"items", "GET /v1/items", "GET", "/v1/items?limit=100", nil},
		{"home", "GET /v1/home", "GET", "/v1/home", nil},
		{"browse", "POST /v1/libraries/{id}/browse", "POST", "/v1/libraries/" + f.library + "/browse",
			map[string]any{"pivot": "movies", "sort": []map[string]string{{"field": "title", "direction": "asc"}}, "limit": 40}},
		{"detail", "GET /v1/items/{id}/detail", "GET", "/v1/items/" + item + "/detail", nil},
		{"search", "GET /v1/search", "GET", "/v1/search?q=Title&limit=10", nil},
		{"facets", "GET /v1/libraries/{id}/facets", "GET", "/v1/libraries/" + f.library + "/facets?field=contentRating", nil},
	}
	for _, c := range cases {
		cost, total := measureRoute(t, f, token, c.pattern, c.method, c.path, c.body)
		t.Logf("%-10s attributed statements=%3d transactions=%2d acquisitions=%2d | observed statements=%3d",
			c.name, cost.Statements, cost.Transactions, cost.Acquisitions, total)
	}
}
