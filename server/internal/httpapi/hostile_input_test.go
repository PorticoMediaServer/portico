package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/servicelog"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// Every route receives the same bounded battery of hostile bodies, queries and paths. The route table is the same one admission
// is built from, so a route added tomorrow is covered the day it is added
// rather than the day somebody remembers to write a test for it.
//
// The bar is not "returns an error". The bar is:
//
//   - the process does not panic. A contained panic is still a defect here:
//     containment exists for the case nobody thought of, not as a substitute for
//     validating input.
//   - the answer is never a 500. A malformed request is the client's mistake and
//     must be answered as one — 400, 401, 404, 405, 415, 422 — because a 500
//     tells an operator their server is broken when it is not, and because the
//     paths that produce a 500 are the paths that produce a crash next time.
//   - the answer is never a hang. Every one of these is served inside the test's
//     own deadline, and the lane budget is the backstop.
//
// Authorization is deliberately absent from most cases: an unauthenticated
// request reaching a handler at all is the interesting case, and a route that
// rejects it before parsing is a route that passes trivially, which is the
// correct outcome.

// hostileBodies is the battery. Each is a complete request body.
func hostileBodies(t *testing.T) []struct{ name, body string } {
	t.Helper()
	deep := strings.Repeat(`{"a":`, 2000) + `1` + strings.Repeat(`}`, 2000)
	wide := "[" + strings.TrimSuffix(strings.Repeat(`"x",`, 20000), ",") + "]"
	return []struct{ name, body string }{
		{"empty", ""},
		{"not json", "this is not json at all"},
		{"truncated object", `{"id":`},
		{"null", `null`},
		{"bare number", `12345`},
		{"array where object expected", `[1,2,3]`},
		{"object where array expected", `{"0":1}`},
		{"string where object expected", `"hello"`},
		{"boolean field types", `{"id":true,"name":false,"limit":"not a number","enabled":"yes"}`},
		{"numbers as strings", `{"limit":"99999999999999999999","offset":"-1","year":"1e400"}`},
		{"huge numbers", `{"limit":99999999999999999999,"offset":-9223372036854775808,"year":1e308}`},
		{"negative everything", `{"limit":-1,"offset":-1,"count":-1,"position":-1,"seconds":-1}`},
		{"deeply nested", deep},
		{"very wide array", wide},
		{"nul bytes", "{\"name\":\"a\x00b\",\"id\":\"\x00\"}"},
		{"invalid utf8", `{"name":"` + string([]byte{0xff, 0xfe, 0xfd}) + `"}`},
		{"path traversal", `{"path":"../../../../etc/passwd","file":"..\\..\\windows\\system32\\config\\sam"}`},
		{"sql shapes", `{"q":"'; DROP TABLE items; --","id":"1 OR 1=1","title":"\" OR \"\"=\""}`},
		{"template shapes", `{"name":"{{.}}","title":"${jndi:ldap://x/y}","overview":"%s%s%s%n"}`},
		{"html shapes", `{"title":"<script>alert(1)</script>","name":"<!--#exec cmd=\"id\"-->"}`},
		{"duplicate keys", `{"id":"a","id":"b","id":"c"}`},
		{"unknown fields", `{"totallyUnknownField":1,"anotherOne":{"nested":[1,2,3]}}`},
		{"empty strings everywhere", `{"id":"","name":"","title":"","q":"","path":"","kind":""}`},
		{"long strings", `{"name":"` + strings.Repeat("A", 100000) + `"}`},
		{"unicode direction marks", "{\"name\":\"a\u202eb\u200fc\",\"title\":\"\ufeff\"}"},
		{"scientific ids", `{"id":1e1000,"revision":-1e1000}`},
	}
}

// hostileQueries are appended to the route's path.
var hostileQueries = []string{
	"",
	"?limit=-1&offset=-1",
	"?limit=99999999999999999999&offset=99999999999999999999",
	"?limit=abc&offset=abc&cursor=abc",
	"?q=" + strings.Repeat("%41", 4000),
	"?q=%00%01%02",
	"?q=%ff%fe",
	"?cursor=" + strings.Repeat("A", 8000),
	"?sort=../../etc/passwd",
	"?filter=" + strings.Repeat("(", 500),
	"?a=1&a=2&a=3&a=4&a=5",
	"?" + strings.Repeat("k=v&", 2000),
}

// hostilePathValues fill in a route's wildcards.
var hostilePathValues = []string{
	"..",
	"%2e%2e%2f%2e%2e%2f",
	strings.Repeat("A", 4000),
	"a%00b",
	"'-OR-1=1",
	"-1",
	"99999999999999999999",
	"%ff",
	"a%20b%20c",
	"null",
}

func hostileFixture(t *testing.T) (http.Handler, string, func() string) {
	t.Helper()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	db, err := persistence.Open(filepath.Join(root, "db"))
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
	admin := administration.NewAt(db, root)
	admin.Discover = func(context.Context, time.Duration) ([]administration.TunerDevice, error) {
		return []administration.TunerDevice{}, nil
	}
	handler := New(Dependencies{Administration: admin, Scheduler: operations.NewScheduler(operations.New(db)), Access: AccessArea{Access: access.New(db), ClientLogs: servicelog.NewClientLogStore(db)}, DB: db, Identity: ident, Catalog: cat, Hosted: host,
		Ingestion: ingestion.New(db, cat, assets.Probe{}), Playback: playback.New(db), Console: operations.New(db)})
	secret, err := os.ReadFile(storagepaths.Path(root, "setup-token"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"authMode": "local", "setupToken": string(secret), "username": "owner", "password": "long-test-password", "name": "Owner"})
	w := httptest.NewRecorder()
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
	return handler, owner.AccessToken, func() string {
		t.Helper()
		var epoch int
		if err := db.QueryRow(`SELECT epoch FROM accounts WHERE id=?`, owner.Viewer.AccountID).Scan(&epoch); err != nil {
			t.Fatal(err)
		}
		fresh, err := ident.Issue(owner.Viewer.AccountID, owner.Viewer.ProfileID, "local", "owner", epoch)
		if err != nil {
			t.Fatal(err)
		}
		return fresh.AccessToken
	}
}

// routesUnderTest is every classified route, in a stable order, with its
// wildcards left in place for the caller to fill.
func routesUnderTest() []struct{ method, path string } {
	patterns := make([]string, 0, len(routeLanes))
	for pattern := range routeLanes {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)
	out := make([]struct{ method, path string }, 0, len(patterns))
	for _, pattern := range patterns {
		method, path, found := strings.Cut(pattern, " ")
		if !found {
			method, path = "GET", pattern
		}
		if !strings.HasPrefix(path, "/") {
			// A host-qualified pattern ("example.com/v1/…") is served by the same
			// handler; the path is what is attacked.
			if index := strings.IndexByte(path, '/'); index >= 0 {
				path = path[index:]
			} else {
				continue
			}
		}
		out = append(out, struct{ method, path string }{method, path})
	}
	return out
}

func TestEveryRouteSurvivesHostileInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	handler, token, renew := hostileFixture(t)
	bodies := hostileBodies(t)
	before := supervise.Panics()
	var failures []string
	for _, route := range routesUnderTest() {
		type attack struct{ name, body, query, path string }
		attacks := make([]attack, 0, len(bodies)+len(hostileQueries)+len(hostilePathValues))
		for _, body := range bodies {
			attacks = append(attacks, attack{body.name, body.body, "", fillWildcards(route.path, "hostile-id")})
		}
		for i, query := range hostileQueries {
			attacks = append(attacks, attack{fmt.Sprintf("query %d", i), "{}", query, fillWildcards(route.path, "hostile-id")})
		}
		for i, value := range hostilePathValues {
			attacks = append(attacks, attack{fmt.Sprintf("path %d", i), "{}", "", fillWildcards(route.path, value)})
		}
		for _, attack := range attacks {
			for _, authorized := range []bool{false, true} {
				r := httptest.NewRequest(route.method, attack.path+attack.query, strings.NewReader(attack.body))
				r.Header.Set("Content-Type", "application/json")
				if authorized {
					r.Header.Set("Authorization", "Bearer "+token)
				}
				budget := 2 * time.Second
				if strings.HasSuffix(route.path, "/events") {
					budget = 10 * time.Millisecond
				}
				ctx, cancel := context.WithTimeout(r.Context(), budget)
				r = r.WithContext(ctx)
				w := httptest.NewRecorder()
				done := make(chan struct{})
				go func() { defer close(done); handler.ServeHTTP(w, r) }()
				select {
				case <-done:
				case <-ctx.Done():
					// Streaming routes normally wait for their client to disconnect.
					select {
					case <-done:
					case <-time.After(time.Second):
						cancel()
						t.Fatalf("%s %s did not honor cancellation", route.method, route.path)
					}
				}
				cancel()
				// The empty body is valid on sign-out. Give subsequent cases
				// a fresh family instead of silently testing as a stranger.
				if authorized && (route.path == "/v1/sessions/current" || route.path == "/v1/direct/sessions/sign-out-everywhere") && w.Code < 300 {
					token = renew()
				}
				// Optional subsystems are deliberately absent from this fixture;
				// admission and those subsystem-unavailable responses may be 503.
				if w.Code >= 500 && w.Code != 503 {
					failures = append(failures, fmt.Sprintf("%s %s [%s] authorized=%v -> %d %s", route.method, attack.path, attack.name, authorized, w.Code, strings.TrimSpace(firstLine(w.Body.String()))))
				}
			}
		}
	}
	// Successful bodyless mutations must not silently revoke the fixture's
	// credential and turn all later authorized cases into trivial denials.
	canary := httptest.NewRequest("GET", "/v1/preferences?deviceClass=web", nil)
	canary.Header.Set("Authorization", "Bearer "+token)
	answer := httptest.NewRecorder()
	handler.ServeHTTP(answer, canary)
	if answer.Code != 200 {
		t.Errorf("hostile cases lost their authorized fixture: %d %s", answer.Code, answer.Body.String())
	}
	if grown := supervise.Panics() - before; grown > 0 {
		t.Errorf("%d panics were contained while serving hostile input; containment is the backstop, not the validation", grown)
	}
	if len(failures) > 0 {
		t.Fatalf("%d routes answered a malformed request with a server error:\n%s", len(failures), strings.Join(failures, "\n"))
	}
}

// Every hostile body against one heavily parsed route, so that the battery is
// applied in full rather than spread one-per-route.
func TestTheParsedRoutesSurviveEveryHostileBody(t *testing.T) {
	handler, token, _ := hostileFixture(t)
	routes := []struct{ method, path string }{
		{"POST", "/v1/libraries"},
		{"POST", "/v1/direct/sign-in"},
		{"POST", "/v1/setup"},
		{"GET", "/v1/search"},
		{"GET", "/v1/items"},
		{"POST", "/v1/playback/sessions"},
		{"PATCH", "/v1/me/preferences"},
	}
	before := supervise.Panics()
	for _, route := range routes {
		for _, body := range hostileBodies(t) {
			for _, query := range hostileQueries {
				r := httptest.NewRequest(route.method, route.path+query, strings.NewReader(body.body))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code >= 500 && w.Code != 503 {
					t.Errorf("%s %s%s [%s] -> %d %s", route.method, route.path, query, body.name, w.Code, firstLine(w.Body.String()))
				}
			}
		}
	}
	if grown := supervise.Panics() - before; grown > 0 {
		t.Fatalf("%d panics were contained while serving hostile bodies", grown)
	}
}

// A request that claims a body far larger than it sends, and one that sends far
// more than it claims, are both ordinary on a bad network and both are shapes a
// reader can be made to trust.
func TestMisdeclaredBodiesAreNotTrusted(t *testing.T) {
	handler, token, _ := hostileFixture(t)
	cases := []struct {
		name          string
		contentLength string
		body          string
	}{
		{"claims a gigabyte", "1073741824", `{"name":"x"}`},
		{"claims negative", "-1", `{"name":"x"}`},
		{"claims zero, sends much", "0", strings.Repeat(`{"name":"x"}`, 1000)},
		{"claims nonsense", "not-a-number", `{"name":"x"}`},
	}
	for _, c := range cases {
		r := httptest.NewRequest("POST", "/v1/libraries", strings.NewReader(c.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Length", c.contentLength)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code >= 500 && w.Code != 503 {
			t.Errorf("%s -> %d %s", c.name, w.Code, firstLine(w.Body.String()))
		}
	}
}

// Headers are input too, and the ones the server reads for identity and
// delivery are read before any handler runs.
func TestHostileHeadersAreNotTrusted(t *testing.T) {
	handler, _, _ := hostileFixture(t)
	headers := []struct{ name, value string }{
		{"Authorization", ""},
		{"Authorization", "Bearer"},
		{"Authorization", "Bearer "},
		{"Authorization", strings.Repeat("A", 20000)},
		{"Authorization", "Basic " + strings.Repeat("%", 200)},
		{"Authorization", "Bearer \x00\x01"},
		{"Accept-Encoding", strings.Repeat("gzip,", 2000)},
		{"Range", "bytes=-1-"},
		{"Range", "bytes=99999999999999999999-"},
		{"Range", strings.Repeat("bytes=0-1,", 5000)},
		{"If-None-Match", strings.Repeat(`W/"x",`, 5000)},
		{"Origin", "http://" + strings.Repeat("a", 10000)},
		{"X-Forwarded-For", strings.Repeat("1.2.3.4,", 5000)},
		{"Content-Type", strings.Repeat("application/json;", 2000)},
	}
	for _, header := range headers {
		r := httptest.NewRequest("GET", "/v1/me", nil)
		r.Header.Set(header.name, header.value)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code >= 500 && w.Code != 503 {
			t.Errorf("%s: %.40q -> %d %s", header.name, header.value, w.Code, firstLine(w.Body.String()))
		}
	}
}

func firstLine(s string) string {
	if index := strings.IndexByte(s, '\n'); index >= 0 {
		s = s[:index]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// fillWildcards replaces every {name} and {name...} segment of a route pattern
// with one hostile value, which is how a path parameter is attacked.
func fillWildcards(path, value string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			segments[i] = value
		}
	}
	return strings.Join(segments, "/")
}
