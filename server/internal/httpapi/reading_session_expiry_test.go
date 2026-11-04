package httpapi

import (
	"bytes"
	"context"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestReadingSessionExpiredAndOtherProfileHidden(t *testing.T) {
	r := newReadingFixture(t, "book", "pdf")
	h := New(r.d)
	m := readingOpened(t, r, h)
	token := r.f.owner.AccessToken
	check := func(id, token string, want int, code string) {
		t.Helper()
		for _, suffix := range []string{"/progress", "/source", "/assets?path=chapter.xhtml", "/pages/0", "/search?q=book", "/annotations"} {
			w := readingHTTP(t, h, "GET", "/v1/reading/sessions/"+id+suffix, token, m.Revision, nil)
			if w.Code != want || !strings.Contains(w.Body.String(), `"code":"`+code+`"`) {
				t.Fatalf("%s: %d %s; want %d %s", suffix, w.Code, w.Body.String(), want, code)
			}
		}
	}
	owner := readingOwnerLogin(t, r)
	profiles, err := r.f.id.CreateDirectProfile(context.Background(), owner.AccountToken, "Other reader", "blue")
	if err != nil {
		t.Fatal(err)
	}
	var otherID string
	for _, p := range profiles.Profiles {
		if p.Name == "Other reader" {
			otherID = p.ID
		}
	}
	selected, err := r.f.id.SelectDirectProfile(context.Background(), owner.AccountToken, otherID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	check(m.SessionID, selected.Session.AccessToken, 410, "reading_session_expired")
	// Hold request identity fixed so all body bytes and headers can be compared,
	// including the otherwise random per-error request ID.
	identical := func(requester string) {
		t.Helper()
		for _, suffix := range []string{"/progress", "/source", "/assets?path=chapter.xhtml", "/pages/0", "/search?q=book", "/annotations"} {
			request := func(id string) *httptest.ResponseRecorder {
				req := httptest.NewRequest("GET", "/v1/reading/sessions/"+id+suffix, nil)
				req.Header.Set("Authorization", "Bearer "+requester)
				req.Header.Set("If-Match", `"`+m.Revision+`"`)
				w := httptest.NewRecorder()
				w.Header().Set("X-Request-Id", "reading-session-privacy-test")
				h.ServeHTTP(w, req)
				return w
			}
			unknown, foreign := request("missing-session"), request(m.SessionID)
			if unknown.Code != 410 || foreign.Code != unknown.Code || !bytes.Equal(foreign.Body.Bytes(), unknown.Body.Bytes()) || !reflect.DeepEqual(foreign.Header(), unknown.Header()) {
				t.Fatalf("%s foreign = %d %s %v; unknown = %d %s %v", suffix, foreign.Code, foreign.Body.String(), foreign.Header(), unknown.Code, unknown.Body.String(), unknown.Header())
			}
		}
	}
	identical(selected.Session.AccessToken) // Another profile and token.
	identical(owner.Session.AccessToken)    // Same profile, independently bound token.

	if err := r.s.CloseSession(r.p, m.SessionID); err != nil {
		t.Fatal(err)
	}
	check(m.SessionID, token, 410, "reading_session_expired")
	check("missing-session", token, 410, "reading_session_expired")
	// W-19: the ninth open succeeds and evicts the LRU session. Its old ID
	// must carry the same expired response as an explicit or idle close.
	first := readingOpened(t, r, h)
	for i := 0; i < 8; i++ {
		readingOpened(t, r, h)
	}
	check(first.SessionID, token, 410, "reading_session_expired")
}
