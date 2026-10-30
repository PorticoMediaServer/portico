package httpapi

import (
	"github.com/PorticoMediaServer/portico/server/internal/httpapi/fixture"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// The principal cache is the one cache in this server that could do real harm:
// hold an entry a moment too long and a revoked session keeps working. These are
// the properties that stop it, each asserted rather than argued.
func TestPrincipalCacheIsFencedAgainstRevocation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	tier := performanceTier{name: "auth", shape: fixture.Tiny(), concurrentViewers: 1, iterations: 1, maximumP95: time.Minute, maximumP99: time.Minute, allowBoundedOverload: true}
	f := newLoadFixture(t, tier)
	token := f.viewers[0].AccessToken

	// It saves work: the second identical request resolves from the cache.
	ResetRouteCosts()
	if code, _, body := f.callTimed(token, "GET", "/v1/items?limit=5", nil); code != 200 {
		t.Fatalf("first %d %s", code, body)
	}
	first := RouteCostFor("GET /v1/items").Statements
	ResetRouteCosts()
	if code, _, body := f.callTimed(token, "GET", "/v1/items?limit=5", nil); code != 200 {
		t.Fatalf("second %d %s", code, body)
	}
	second := RouteCostFor("GET /v1/items").Statements
	t.Logf("GET /v1/items statements: %d uncached, %d cached", first, second)
	if second >= first {
		t.Fatalf("the second request cost %d statements against %d; the principal was not cached", second, first)
	}

	// A revocation lands, and the very next request is refused. The write is a
	// bare family UPDATE, not a gated security-fence transaction, so this is
	// the SQL-marker half of the two-signal discipline on its own.
	if _, err := f.db.Exec(`UPDATE authorization_session_families SET revoked=1 WHERE id=(SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, identity.Digest(token)); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := f.callTimed(token, "GET", "/v1/items?limit=5", nil); code != 401 {
		t.Fatalf("a revoked session was answered %d from the cache", code)
	}

	// A denial is never cached, so restoring the family can authenticate again.
	if _, err := f.db.Exec(`UPDATE authorization_session_families SET revoked=0 WHERE id IN(SELECT family_id FROM authorization_family_tokens WHERE token_hash=?)`, identity.Digest(token)); err != nil {
		t.Fatal(err)
	}
	if code, _, body := f.callTimed(token, "GET", "/v1/items?limit=5", nil); code != 200 {
		t.Fatalf("a restored session was answered %d %s", code, body)
	}

	// An authority lane must not read the cache. `GET
	// /v1/direct/profiles/{id}/restrictions` is registered on the security-fence
	// lane (route_lanes.go), that lane runs as dbwork.ClassSecurityFence
	// (admission.go), and strictPrincipal is true for that class
	// (principal_cache.go), so `resolvePrincipal` skips the cache entirely. The
	// `/v1/items` read above has already stored an entry for this token; live
	// or expired, the reads below must not consult it.
	//
	// The lane is read back from the measured request rather than assumed from
	// the generated table, so a route reclassified onto a cache-eligible lane
	// fails here instead of quietly turning this back into a repeat-cost check.
	profileRestrictions := "/v1/direct/profiles/" + f.viewers[0].Viewer.ProfileID + "/restrictions"
	hitsBefore, missesBefore := principalCacheCounters(t, f)
	ResetRouteCosts()
	if code, _, body := f.callTimed(token, "GET", profileRestrictions, nil); code != 200 {
		t.Fatalf("first %s %d %s", profileRestrictions, code, body)
	}
	strictFirst := RouteCostFor("GET /v1/direct/profiles/{id}/restrictions")
	ResetRouteCosts()
	if code, _, body := f.callTimed(token, "GET", profileRestrictions, nil); code != 200 {
		t.Fatalf("second %s %d %s", profileRestrictions, code, body)
	}
	strictSecond := RouteCostFor("GET /v1/direct/profiles/{id}/restrictions")
	hitsAfter, missesAfter := principalCacheCounters(t, f)
	if strictFirst.Lane != laneSecurityFence || strictSecond.Lane != laneSecurityFence {
		t.Fatalf("the authority read ran on lane %q then %q, not the security-fence lane its live resolution depends on", strictFirst.Lane, strictSecond.Lane)
	}
	if strictFirst.Requests != 1 || strictSecond.Requests != 1 {
		t.Fatalf("the authority reads were measured %d and %d times, not once each", strictFirst.Requests, strictSecond.Requests)
	}
	// Live authentication is positive SQL on both reads: whatever the cache
	// holds for this token, the fenced lane paid to resolve it again.
	if strictFirst.Statements == 0 || strictSecond.Statements == 0 {
		t.Fatalf("an authority-lane request ran no statements (%d then %d)", strictFirst.Statements, strictSecond.Statements)
	}
	if strictSecond.Statements < strictFirst.Statements {
		t.Fatalf("an authority-lane request was served from the principal cache (%d then %d statements)", strictFirst.Statements, strictSecond.Statements)
	}
	// Not only the cost: the cache's own entry point was never consulted, so
	// its counters cannot have moved for either read.
	if hitsAfter != hitsBefore || missesAfter != missesBefore {
		t.Fatalf("an authority-lane request consulted the principal cache (hits %d -> %d, misses %d -> %d); a fenced lane must skip it", hitsBefore, hitsAfter, missesBefore, missesAfter)
	}
	// And the reading above is not vacuous: the same token, on the cache-
	// eligible lane, does move those counters (a hit or, if the entry aged out,
	// a miss), so the counters really are the cache's own.
	if code, _, body := f.callTimed(token, "GET", "/v1/items?limit=5", nil); code != 200 {
		t.Fatalf("control GET /v1/items %d %s", code, body)
	}
	controlHits, controlMisses := principalCacheCounters(t, f)
	if controlHits+controlMisses <= hitsAfter+missesAfter {
		t.Fatalf("a cache-eligible read left the counters at %d hits and %d misses; the fenced-lane reading above proves nothing", controlHits, controlMisses)
	}
	t.Logf("%s statements: %d then %d; principal cache hits %d -> %d, misses %d -> %d (control: %d hits, %d misses)", profileRestrictions, strictFirst.Statements, strictSecond.Statements, hitsBefore, hitsAfter, missesBefore, missesAfter, controlHits, controlMisses)

	// An unknown token is refused every time and leaves nothing behind.
	for i := 0; i < 2; i++ {
		if code, _, _ := f.callTimed("not-a-real-token-that-is-long-enough-to-be-plausible-0000", "GET", "/v1/items", nil); code != 401 {
			t.Fatalf("an unknown token was answered %d", code)
		}
	}
}

// A revocation that commits while a check is still running must not be outlived by that
// check's answer: the entry is stamped with the generation read before the check began.
func TestAuthorityCachesDoNotOutliveARevocationThatRacedTheCheck(t *testing.T) {
	now := time.Now()
	before := dbwork.AuthorityGeneration()
	// ... the check reads "allowed" here, then the revocation commits ...
	dbwork.BumpAuthority()

	principals := newPrincipalCache()
	principals.store("digest", identity.Principal{}, now, before)
	if _, ok := principals.lookup("digest", now); ok {
		t.Fatal("a principal resolved before a revocation was served after it")
	}
	access := newAccessCache()
	access.grant("grant", now, before)
	if access.allowed("grant", now) {
		t.Fatal("a library grant resolved before a revocation was served after it")
	}
	restrictions := newRestrictionCache()
	restrictions.store("profile", identity.ContentRestrictions{}, now, before)
	if _, ok := restrictions.lookup("profile", now); ok {
		t.Fatal("restrictions read before a restriction change were served after it")
	}
	// Stamped with the current generation, the same entries are served.
	current := dbwork.AuthorityGeneration()
	principals.store("digest", identity.Principal{}, now, current)
	if _, ok := principals.lookup("digest", now); !ok {
		t.Fatal("a current entry was not served")
	}
}
