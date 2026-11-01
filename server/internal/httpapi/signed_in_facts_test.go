package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// O-169: the server version and the owner's home networks are never served
// before sign-in, and any signed-in person gets them: About shows the version,
// and apps learn the home networks (a VPN or tailnet range the owner added
// counts as home for routing from the first sign-in on).
func TestSignedInCallersGetVersionAndHomeNetworks(t *testing.T) {
	f := administrationFixtureNew(t)
	read := func(path, token string) map[string]any {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "127.0.0.1:4242"
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		var out map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		return out
	}
	if _, ok := read("/v1/system", "")["version"]; ok {
		t.Fatal("the version is served before sign-in")
	}
	if _, ok := read("/v1/networking/connection", "")["lanNetworks"]; ok {
		t.Fatal("home networks are served before sign-in")
	}
	if _, ok := read("/v1/system", "not-a-session")["version"]; ok {
		t.Fatal("an invalid token got the version")
	}
	for _, token := range []string{f.owner, f.member} {
		if v, _ := read("/v1/system", token)["version"].(string); v == "" {
			t.Fatal("a signed-in caller got no version")
		}
		networks, _ := read("/v1/networking/connection", token)["lanNetworks"].([]any)
		if len(networks) == 0 {
			t.Fatal("a signed-in caller got no home networks")
		}
	}
}
