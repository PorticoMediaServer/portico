package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// With a web app given, the server answers it at every address that is not the API's, and
// nothing under /v1/ ever reaches it: an unknown API address is still the API's 404.
func TestTheWebAppNeverShadowsTheAPI(t *testing.T) {
	s := newTestServer(t)
	s.ServeWebWith(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("the app")) }))
	answer := func(method, path string) (int, string) {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(method, path, nil))
		return w.Code, w.Body.String()
	}
	for _, path := range []string{"/", "/libraries", "/v1x", "/assets/app.js"} {
		if code, body := answer("GET", path); code != 200 || body != "the app" {
			t.Errorf("%s answered %d %q; want the app", path, code, body)
		}
	}
	for _, path := range []string{"/v1", "/v1/", "/v1/nothing-here", "/v1/system/../../etc"} {
		if code, body := answer("GET", path); body == "the app" || code == 200 {
			t.Errorf("%s answered %d %q; the API's own refusal was wanted", path, code, body)
		}
	}
	if code, body := answer("GET", "/v1/system"); code != 200 || body == "the app" {
		t.Errorf("/v1/system answered %d", code)
	}
}
