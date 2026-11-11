package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A creating POST must honour the Idempotency-Key: an app that never had the answer sends the
// request again with the same key and must get the thing made the first time. Each area's
// retry_<area>_test.go says, in an init, which of its routes are tested here (retryAreas) and
// which are safe to repeat without a key and why (safeToRepeat). The test fails for a POST in
// neither, so a new creating route cannot be added without one; and an area adds its entries in
// its own file, so two areas never edit the same one.

// retryCase is one creating route. body is the request; other is a different body sent with the
// same key, which must be refused.
type retryCase struct {
	route        string
	path         func() string // the path, where it holds an id made earlier; else the route's
	body, other  any
	count        func() int      // how many of the thing exist; nil where the id is the whole proof
	inMemoryOnly bool            // a restart ends it by design
	created      func(id string) // told the id the first request made, for a later case's path
}

// retryAreas are the areas' creating routes, each given the test's server.
var retryAreas []func(th *theatre) []retryCase

// repeatSafeWithoutKey is every POST that is safe to send twice without a key, with why.
var repeatSafeWithoutKey = map[string]string{}

// safeToRepeat adds an area's routes that need no Idempotency-Key, with the reason for each.
func safeToRepeat(reasons map[string]string) {
	for route, why := range reasons {
		if _, twice := repeatSafeWithoutKey[route]; twice {
			panic(route + " is listed as safe to repeat by two areas")
		}
		repeatSafeWithoutKey[route] = why
	}
}

// An app that never had the answer to a creating request sends it again with the same
// Idempotency-Key, after a dropped connection and after a server restart. Each repeat gets
// the thing made the first time, nothing is made twice, and the key with a different body is
// refused.
func TestCreatesAreSafeToRepeat(t *testing.T) {
	th := newTheatre(t)
	token := func() string { return th.owner.AccessToken }
	var cases []retryCase
	for _, area := range retryAreas {
		cases = append(cases, area(th)...)
	}

	keyed := map[string]bool{}
	for _, c := range cases {
		keyed[c.route] = true
		path := strings.TrimPrefix(c.route, "POST ")
		if c.path != nil {
			path = c.path()
		}
		key := "retry-" + path
		send := func(body any) (int, string, map[string]any) {
			t.Helper()
			r := newRequest("POST", path, token(), body)
			r.Header.Set("Idempotency-Key", key)
			w := serve(th.s, r)
			var out map[string]any
			json.Unmarshal(w.Body.Bytes(), &out)
			return w.Code, createdID(out), out
		}
		code, first, firstAnswer := send(c.body)
		if code >= 300 || first == "" {
			t.Fatalf("%s answered %d: %v", c.route, code, firstAnswer)
		}
		if c.created != nil {
			c.created(first)
		}
		repeats := []string{"after a dropped connection"}
		if !c.inMemoryOnly {
			repeats = append(repeats, "after a restart")
		}
		for _, when := range repeats {
			if when == "after a restart" {
				th.restart()
			}
			code, again, answer := send(c.body)
			if code >= 300 || again != first {
				t.Errorf("%s sent again %s answered %d with %q, want the first answer's %q: %v", c.route, when, code, again, first, answer)
			}
			if c.count != nil {
				if n := c.count(); n != 1 {
					t.Errorf("%s sent again %s: %d exist, want 1", c.route, when, n)
				}
			}
			// A key's secret is not kept. While the key has never been used, a repeat answers a
			// new secret and the unseen one stops working; the test then uses the key, so the next
			// repeat must give no secret, say why, and leave the delivered secret working.
			if delivered, _ := firstAnswer["secret"].(string); strings.HasPrefix(delivered, keyPrefix) {
				secret, _ := answer["secret"].(string)
				if when == "after a dropped connection" {
					if secret == "" || status(t, th.s, "GET", "/v1/me", secret, nil) != 200 {
						t.Errorf("%s: a repeat before the key was used gave no working secret: %v", c.route, answer)
					}
					if status(t, th.s, "GET", "/v1/me", delivered, nil) != 401 {
						t.Errorf("%s: the secret nobody was meant to have still works", c.route)
					}
					firstAnswer = answer
				} else if secret != "" || answer["note"] == nil || status(t, th.s, "GET", "/v1/me", delivered, nil) != 200 {
					t.Errorf("%s: a repeat after the key was used answered %v; want no secret, a note, and the used secret still working", c.route, answer)
				}
			}
		}
		if code, _, answer := send(c.other); code != 422 {
			t.Errorf("%s with the same key and another body answered %d, want 422: %v", c.route, code, answer)
		}
	}

	registered := map[string]bool{}
	for _, r := range th.s.Routes() {
		registered[r.Pattern] = true
	}
	for pattern := range repeatSafeWithoutKey {
		if !registered[pattern] {
			t.Errorf("%s is listed as safe to repeat but is not a route", pattern)
		}
	}
	for _, r := range th.s.Routes() {
		if strings.HasPrefix(r.Pattern, "POST ") && !keyed[r.Pattern] && repeatSafeWithoutKey[r.Pattern] == "" {
			t.Errorf("%s creates something or not? In its area's retry_<area>_test.go, add it to retryAreas to test that it honours "+
				"the Idempotency-Key, or to safeToRepeat with why a repeat is safe", r.Pattern)
		}
	}
}

// createdID finds the id of what a creating request made, wherever its answer puts it.
func createdID(answer map[string]any) string {
	for _, field := range []string{"id", "playlistId", "sessionId"} {
		if id, ok := answer[field].(string); ok {
			return id
		}
	}
	for _, inner := range []string{"key", "queue", "session"} {
		if m, ok := answer[inner].(map[string]any); ok {
			return createdID(m)
		}
	}
	return ""
}

// named counts the entries of a list route whose field has the value.
func (th *theatre) named(path, field, value string) int {
	th.Helper()
	var page struct{ Items []map[string]any }
	if w := call(th.T, th.s, "GET", path, th.owner.AccessToken, nil, &page); w.Code != 200 {
		th.Fatalf("GET %s answered %d: %s", path, w.Code, w.Body)
	}
	n := 0
	for _, item := range page.Items {
		if item[field] == value {
			n++
		}
	}
	return n
}

// restart closes the server and opens it again on the same database, then renews the
// owner's sign-in, as an app does when the server no longer takes its access token.
func (th *theatre) restart() {
	th.Helper()
	closeDB(th.s.db)
	th.s = openTestServer(th.T, th.path)
	th.s.now = func() time.Time { return th.clock }
	th.s.playback.SetEngine(th.engine, th.engine)
	code, renewed := renew(th.T, th.s, th.owner.RefreshToken, 0, "after-restart-"+db.NewID("req"))
	if code != 200 {
		th.Fatalf("renewing after the restart answered %d", code)
	}
	th.owner = renewed
}
