package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// The periodic re-authorisation of a subtitle document costs a header, not the
// document — and only for a viewer who is still entitled to it.
func TestSubtitleDocumentRevalidation(t *testing.T) {
	body := `{"version":1,"timeDomain":"source-relative","cues":[]}`
	allowed := true
	checks := 0
	check := func() error {
		checks++
		if !allowed {
			return identity.ErrUnauthorized
		}
		return nil
	}
	serve := func(match string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/v1/media/subtitles/grant/res/3", nil)
		if match != "" {
			r.Header.Set("If-None-Match", match)
		}
		serveSubtitleDocument(w, r, "res", 3, strings.NewReader(body), check)
		return w
	}
	first := serve("")
	etag := first.Header().Get("ETag")
	if first.Code != 200 || first.Body.String() != body || etag != `"res.3"` || first.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(first.Code, first.Body.String(), first.Header())
	}
	before := checks
	again := serve(etag)
	if again.Code != 304 || again.Body.Len() != 0 || again.Header().Get("Cache-Control") != "no-store" || checks == before {
		t.Fatal("an unchanged document was sent again, or sent without authorisation", again.Code, again.Body.Len(), checks-before)
	}
	// Another revision, a weak validator and a list all behave.
	if w := serve(`"res.2"`); w.Code != 200 || w.Body.String() != body {
		t.Fatal(w.Code)
	}
	if w := serve(`W/"res.3"`); w.Code != 200 {
		t.Fatal("a weak validator matched a strong comparison")
	}
	if w := serve(`"other", "res.3"`); w.Code != 304 {
		t.Fatal(w.Code)
	}
	// Revoked: the matching validator buys nothing. No 304, no body.
	allowed = false
	revoked := serve(etag)
	if revoked.Code == 304 || revoked.Code == 200 || revoked.Body.String() == body || revoked.Header().Get("ETag") != "" {
		t.Fatal("a revoked viewer was answered from its validator", revoked.Code, revoked.Header())
	}
	if revoked.Code != 401 && revoked.Code != 403 {
		t.Fatal("revocation status", revoked.Code)
	}
}
