package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"testing"
)

func TestQualityDefaultsR25PublishedPolicyStoredOriginal(t *testing.T) {
	f := preferenceHTTP(t)
	read := func(transport string) map[string]any {
		r := httptest.NewRequest("GET", "/v1/playback/delivery-policy", nil)
		r.RemoteAddr = "93.184.216.34:5000"
		r.Header.Set("Authorization", "Bearer "+f.token)
		r.Header.Set("X-Portico-Device-Class", "web")
		r.Header.Set(playback.TransportClassHeader, transport)
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body["policy"].(map[string]any)
	}
	if got := read("wifi"); got["qualityMode"] != "original" || got["networkClass"] != "remote" {
		t.Fatal(got)
	}
	if got := read("satellite"); got["qualityMode"] != "original" {
		t.Fatal(got)
	}
	if got := read("cellular"); got["qualityMode"] != "720p-high" || got["networkClass"] != "remote" {
		t.Fatal(got)
	}
	w := f.call("PATCH", "/v1/preferences", map[string]any{"expectedRevision": 1, "idempotencyKey": "stored-original", "scope": "profile-device-class", "deviceClass": "web", "values": map[string]any{"quality.remote": "original"}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// The remote choice is explicit only for that lane; cellular has its own
	// saved preference and must keep its default until the viewer changes it.
	if got := read("wifi"); got["qualityMode"] != "original" {
		t.Fatal("stored remote Original overwritten", got)
	}
	if got := read("cellular"); got["qualityMode"] != "720p-high" {
		t.Fatal("remote preference leaked into the mobile lane", got)
	}
	w = f.call("PATCH", "/v1/preferences", map[string]any{"expectedRevision": 2, "idempotencyKey": "stored-mobile-original", "scope": "profile-device-class", "deviceClass": "web", "values": map[string]any{"quality.mobile": "original"}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if got := read("cellular"); got["qualityMode"] != "original" {
		t.Fatal("stored mobile Original overwritten", got)
	}
}
