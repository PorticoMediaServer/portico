package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"
)

// asked sends a request as a browser page at an origin would, and returns the answer.
func asked(s *Server, origin, method, path, token string, headers ...string) *httptest.ResponseRecorder {
	r := newRequest(method, path, token, nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	for n := 0; n+1 < len(headers); n += 2 {
		r.Header.Set(headers[n], headers[n+1])
	}
	return serve(s, r)
}

// readable is whether a browser would let a page at origin read this answer.
func readable(w *httptest.ResponseRecorder, origin string) bool {
	return w.Header().Get("Access-Control-Allow-Origin") == origin
}

// Portico's hosted web app, running in a person's browser, may read this server's answers:
// what needs no sign-in and, with the person's token, what does. No answer ever says "any
// origin" or asks the browser to send cookies.
func TestTheHostedWebAppMayReadAnswers(t *testing.T) {
	s := openTestServer(t, filepath.Join(t.TempDir(), "main.db"))
	owner := onMainProfile(t, s)
	const hosted = "https://web.getportico.tv"
	for name, w := range map[string]*httptest.ResponseRecorder{
		"without a sign-in": asked(s, hosted, "GET", "/v1/system", ""),
		"signed in":         asked(s, hosted, "GET", "/v1/libraries", owner.AccessToken),
		"a refusal":         asked(s, hosted, "GET", "/v1/libraries", ""),
		"no such address":   asked(s, hosted, "GET", "/v1/nothing-here", ""),
	} {
		if !readable(w, hosted) || w.Header().Get("Access-Control-Allow-Credentials") != "" || !slices.Contains(w.Header().Values("Vary"), "Origin") ||
			w.Header().Get("Access-Control-Expose-Headers") == "" {
			t.Errorf("%s: answered %d with headers %v", name, w.Code, w.Header())
		}
	}
	if w := asked(s, hosted, "GET", "/v1/libraries", owner.AccessToken); w.Code != 200 {
		t.Errorf("the hosted web app's signed-in read answered %d", w.Code)
	}
	if w := asked(s, hosted, "GET", "/v1/libraries", ""); w.Code != 401 {
		t.Errorf("being an allowed origin stood in for a sign-in: %d", w.Code)
	}
}

// A page anywhere else gets no leave to read anything, whatever it sends, and neither does
// an address made to look like an allowed one. The server still answers: it is the browser
// that keeps the answer from the page, and a program that is not a browser was never held
// by any of this.
func TestOtherOriginsAndLookAlikesMayNotReadAnswers(t *testing.T) {
	s := openTestServer(t, filepath.Join(t.TempDir(), "main.db"))
	owner := onMainProfile(t, s)
	for _, origin := range []string{
		"https://evil.example",
		"https://web.getportico.tv.evil.example",
		"https://evilweb.getportico.tv",
		"https://getportico.tv",
		"http://web.getportico.tv",
		"https://web.getportico.tv:8443",
		"https://web.getportico.tv/",
		"https://WEB.getportico.tv",
		"https://web.getportico.tv https://evil.example",
		"null",
		"*",
		"http://example.com", // this server's own address in these tests: a same-origin page needs no leave
	} {
		for name, w := range map[string]*httptest.ResponseRecorder{
			"a read":      asked(s, origin, "GET", "/v1/system", ""),
			"with token":  asked(s, origin, "GET", "/v1/libraries", owner.AccessToken),
			"a preflight": asked(s, origin, "OPTIONS", "/v1/libraries", "", "Access-Control-Request-Method", "GET", "Access-Control-Request-Headers", "authorization"),
		} {
			h := w.Header()
			if h.Get("Access-Control-Allow-Origin") != "" || h.Get("Access-Control-Allow-Methods") != "" || h.Get("Access-Control-Allow-Headers") != "" ||
				h.Get("Access-Control-Allow-Credentials") != "" || !slices.Contains(h.Values("Vary"), "Origin") {
				t.Errorf("%s from %q: answered %d with headers %v", name, origin, w.Code, h)
			}
			if name == "a preflight" && w.Code < 400 {
				t.Errorf("a preflight from %q was answered %d", origin, w.Code)
			}
		}
	}
	// With no Origin at all (an app, a script, the server's own page reading) nothing changes.
	if w := asked(s, "", "GET", "/v1/libraries", owner.AccessToken); w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("a request with no origin: %d %v", w.Code, w.Header())
	}
}

// Before a request that carries a token or a JSON body, a browser asks whether the page may
// send it. An allowed origin is told yes without any sign-in, since that question never
// carries one, and told which methods and headers it may use.
func TestAPreflightFromAnAllowedOriginIsAnsweredWithoutSignIn(t *testing.T) {
	s := openTestServer(t, filepath.Join(t.TempDir(), "main.db"))
	onMainProfile(t, s)
	const hosted = "https://web.getportico.tv"
	for _, path := range []string{"/v1/libraries", "/v1/admin/settings/network", "/v1/auth/sign-in", "/v1/nothing-here"} {
		w := asked(s, hosted, "OPTIONS", path, "", "Access-Control-Request-Method", "PUT", "Access-Control-Request-Headers", "authorization, content-type, if-match")
		h := w.Header()
		if w.Code != http.StatusNoContent || !readable(w, hosted) || h.Get("Access-Control-Allow-Methods") != corsMethods ||
			h.Get("Access-Control-Allow-Headers") != corsHeaders || h.Get("Access-Control-Max-Age") == "" || h.Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("preflight for %s: answered %d with headers %v", path, w.Code, h)
		}
	}
	// The answer to the question changes nothing about the request that follows.
	if w := asked(s, hosted, "PUT", "/v1/admin/settings/network", ""); w.Code != 401 {
		t.Errorf("after a preflight, a save without a sign-in answered %d", w.Code)
	}
}

// The owner allows a web address of their own in the network settings: exactly one origin
// each, kept as a browser sends it. Anything that is not one origin is refused.
func TestTheOwnerAddsWebOrigins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.db")
	s := openTestServer(t, path)
	owner := onMainProfile(t, s)
	for _, bad := range []string{"*", "null", "https://*.example", "tools.example", "ftp://tools.example", "https://tools.example/app",
		"https://tools.example?x=1", "https://someone@tools.example", "https://", ""} {
		if got := status(t, s, "PUT", "/v1/admin/settings/network", owner.AccessToken, NetworkSettingsEdit{TrustedProxies: []string{}, WebOrigins: []string{bad}}); got != 400 {
			t.Errorf("allowing %q answered %d, want 400", bad, got)
		}
	}
	var saved NetworkSettings
	edit := NetworkSettingsEdit{TrustedProxies: []string{}, WebOrigins: []string{
		"http://localhost:5173", " HTTPS://Tools.Example:443/ ", "https://tools.example", "http://nas.example:8080", "https://web.getportico.tv"}}
	if w := call(t, s, "PUT", "/v1/admin/settings/network", owner.AccessToken, edit, &saved); w.Code != 200 ||
		!slices.Equal(saved.WebOrigins, []string{"http://localhost:5173", "https://tools.example", "http://nas.example:8080"}) ||
		!slices.Equal(saved.BuiltInWebOrigins, []string{"https://web.getportico.tv"}) {
		t.Fatalf("saving web origins: %d %+v", w.Code, saved)
	}
	// One warning: the address that is neither encrypted nor on the person's own machine.
	if len(saved.Warnings) != 1 || saved.Warnings[0].Code != "web_origin_not_encrypted" {
		t.Errorf("warnings: %+v", saved.Warnings)
	}
	check := func(s *Server, when string) {
		t.Helper()
		for origin, want := range map[string]bool{"http://localhost:5173": true, "https://tools.example": true, "http://nas.example:8080": true,
			"https://web.getportico.tv": true, "http://localhost:5174": false, "https://localhost:5173": false, "https://tools.example:8443": false,
			"https://tools.example.evil.example": false} {
			if got := readable(asked(s, origin, "GET", "/v1/system", ""), origin); got != want {
				t.Errorf("%s, a page at %s may read answers: %v, want %v", when, origin, got, want)
			}
		}
	}
	check(s, "after saving")
	closeDB(s.db)
	check(openTestServer(t, path), "after a restart")
	// A save that lists none takes them away again; the built-in one stays.
	s = openTestServer(t, path)
	if got := status(t, s, "PUT", "/v1/admin/settings/network", owner.AccessToken, NetworkSettingsEdit{TrustedProxies: []string{}}); got != 200 {
		t.Fatalf("clearing web origins answered %d", got)
	}
	if readable(asked(s, "https://tools.example", "GET", "/v1/system", ""), "https://tools.example") ||
		!readable(asked(s, "https://web.getportico.tv", "GET", "/v1/system", ""), "https://web.getportico.tv") {
		t.Error("after clearing, the owner's origin is still allowed or the built-in one is not")
	}
}

// The web app this server serves itself, and its files, are not the API: no header is added.
func TestNothingChangesOutsideTheAPI(t *testing.T) {
	s := openTestServer(t, filepath.Join(t.TempDir(), "main.db"))
	s.ServeWebWith(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("the web app")) }))
	w := asked(s, "https://web.getportico.tv", "GET", "/index.html", "")
	if w.Body.String() != "the web app" || w.Header().Get("Access-Control-Allow-Origin") != "" || len(w.Header().Values("Vary")) != 0 {
		t.Errorf("a file of the web app: %d %v", w.Code, w.Header())
	}
}
