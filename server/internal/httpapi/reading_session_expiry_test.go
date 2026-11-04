package httpapi

import (
	"context"
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
	check(m.SessionID, selected.Session.AccessToken, 403, "forbidden")
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
