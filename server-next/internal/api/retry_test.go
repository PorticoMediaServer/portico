package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playqueue"
)

// Sending POST twice is safe for these routes without an Idempotency-Key, for the reason given.
// Every other POST creates something and must honour the key; the test below fails for a POST
// in neither list, so a new creating route cannot be added without it.
var repeatSafeWithoutKey = map[string]string{
	"POST /v1/auth/remembered":                    "reads the accounts named by device-held tokens; changes nothing",
	"POST /v1/auth/owner-reset":                   "one reset code file, rate limited; a repeat keeps the same pending reset",
	"POST /v1/auth/owner-reset/confirm":           "uses up its reset code: a repeat is refused",
	"POST /v1/setup/owner":                        "happens once: a repeat is told the server is set up",
	"POST /v1/auth/sign-in":                       "a lost answer leaves a sign-in nobody holds, which expires",
	"POST /v1/auth/two-factor":                    "the second step of a sign-in: as sign-in",
	"POST /v1/auth/refresh":                       "carries its own request id: a repeat gets the same tokens",
	"POST /v1/auth/password":                      "sets the password",
	"POST /v1/auth/password-reset/confirm":        "uses up its code: a repeat is refused",
	"POST /v1/auth/codes":                         "a lost answer leaves a TV code nobody holds, which expires",
	"POST /v1/auth/codes/{id}/approve":            "sets the code's state",
	"POST /v1/auth/codes/{id}/redeem":             "uses up its code: a repeat is refused",
	"POST /v1/auth/invitations/{id}/accept":       "a repeat with the same account and password signs in to that account",
	"POST /v1/me/two-factor":                      "starts two-step sign-in again, replacing what was not confirmed",
	"POST /v1/me/two-factor/confirm":              "sets the state",
	"POST /v1/me/two-factor/recovery-codes":       "replaces the codes; the answer is the only copy either way",
	"POST /v1/profiles/{id}/select":               "sets the device's profile",
	"POST /v1/admin/members/{id}/password-reset":  "replaces the member's reset code",
	"POST /v1/system/identity-proof":              "changes nothing",
	"POST /v1/items/state:undo":                   "uses up its undo offer: a repeat is refused",
	"POST /v1/queues/{id}/entries":                "needs If-Match: a repeat has an old revision and is refused",
	"POST /v1/queues/{id}/entries/{entryId}/move": "needs If-Match: a repeat has an old revision and is refused",
	"POST /v1/queues/{id}/advance":                "sets the current entry",
	"POST /v1/queues/{id}/prepare-next":           "changes nothing that lasts",
	"POST /v1/queues/{id}/commit-next":            "sets the current entry",
}

// An app that never had the answer to a creating request sends it again with the same
// Idempotency-Key, after a dropped connection and after a server restart. Each repeat gets
// the thing made the first time, nothing is made twice, and the key with a different body is
// refused.
func TestCreatesAreSafeToRepeat(t *testing.T) {
	th := newTheatre(t)
	token := func() string { return th.owner.AccessToken }
	var queueID string
	cases := []struct {
		route        string
		path         func() string
		body, other  any
		count        func() int // how many of the thing exist; nil where the id is the whole proof
		inMemoryOnly bool       // a restart ends it by design
	}{
		{route: "POST /v1/profiles", body: ProfileCreate{Name: "Guest"}, other: ProfileCreate{Name: "Other"},
			count: func() int { return th.named("/v1/profiles", "name", "Guest") }},
		{route: "POST /v1/admin/members", body: MemberCreate{Username: "sam", Password: testPassword},
			other: MemberCreate{Username: "alex", Password: testPassword},
			count: func() int { return th.named("/v1/admin/members", "username", "sam") }},
		{route: "POST /v1/admin/invitations", body: InvitationCreate{Note: "for Sam"}, other: InvitationCreate{Note: "for Alex"},
			count: func() int { return th.named("/v1/admin/invitations", "note", "for Sam") }},
		{route: "POST /v1/admin/api-keys", body: KeyCreate{Name: "Tool", Scopes: []string{"read"}},
			other: KeyCreate{Name: "Tool", Scopes: []string{"admin"}},
			count: func() int { return th.named("/v1/admin/api-keys", "name", "Tool") }},
		{route: "POST /v1/me/tokens", body: KeyCreate{Name: "Script", Scopes: []string{"read"}},
			other: KeyCreate{Name: "Other", Scopes: []string{"read"}},
			count: func() int { return th.named("/v1/me/tokens", "name", "Script") }},
		{route: "POST /v1/me/app-passwords", body: KeyCreate{Name: "Old TV"}, other: KeyCreate{Name: "Other"},
			count: func() int { return th.named("/v1/me/app-passwords", "name", "Old TV") }},
		{route: "POST /v1/queues", body: QueueCreate{Source: playqueue.QueueSource{Items: th.eps[:]}},
			other: QueueCreate{Source: playqueue.QueueSource{Items: []string{th.eps[1]}}}},
		{route: "POST /v1/queues/{id}/playlist", path: func() string { return "/v1/queues/" + queueID + "/playlist" },
			body: QueueSave{Name: "Evening"}, other: QueueSave{Name: "Morning"}},
		{route: "POST /v1/playback/sessions", body: PlaybackStart{ItemID: th.film}, other: PlaybackStart{ItemID: th.song},
			inMemoryOnly: true},
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
		if c.route == "POST /v1/queues" {
			queueID = first
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
			t.Errorf("%s creates something or not? Test that it honours the Idempotency-Key, or say in repeatSafeWithoutKey why a repeat is safe", r.Pattern)
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
