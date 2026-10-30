package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestDirectServerNetworkingReadsAreUnconfiguredDocuments(t *testing.T) {
	d, owner := logoutHTTPFixture(t)
	if d.Networking != nil || d.Hosted.Configured() {
		t.Fatal("fixture must be a direct server without a Hosted controller")
	}
	h := New(d)
	for _, path := range []string{"/v1/networking/claim", "/v1/networking/certificate", "/v1/networking/remote"} {
		anonymous := httptest.NewRecorder()
		h.ServeHTTP(anonymous, httptest.NewRequest("GET", path, nil))
		if anonymous.Code != 401 {
			t.Fatalf("%s anonymous status %d", path, anonymous.Code)
		}
		request := httptest.NewRequest("GET", path, nil)
		request.Header.Set("Authorization", "Bearer "+owner.AccessToken)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
		var document map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil || document["state"] != "unconfigured" {
			t.Fatalf("%s document: %v %v", path, document, err)
		}
		if path == "/v1/networking/claim" {
			identity, ok := document["identity"].(map[string]any)
			if !ok || identity["serverId"] != d.Identity.ID() || identity["localGeneration"] != "0" {
				t.Fatalf("claim identity: %v", document["identity"])
			}
		}
	}
}
