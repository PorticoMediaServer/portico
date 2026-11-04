package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// This fixture predates the data-layer cutover. Preserve whole responses and
// ID relationships. Only generated IDs/grants, requestId and lease clock vary;
// URLs retain their complete path/shape. Rebuild lanes must not re-record it.
func TestPlaybackV1WireCompatibility(t *testing.T) {
	f := newV1Fixture(t, 1)
	f.call("PUT", "/v1/me/devices/current/capabilities", nil, webCapabilities(), 204, nil)
	type answer struct {
		Name   string `json:"name"`
		Status int    `json:"status"`
		ETag   string `json:"etag,omitempty"`
		Body   any    `json:"body,omitempty"`
	}
	answers := []answer{}
	add := func(name string, w *httptest.ResponseRecorder, binary bool) {
		a := answer{Name: name, Status: w.Code, ETag: w.Header().Get("ETag")}
		if binary {
			a.Body = w.Body.Bytes()
		} else if w.Body.Len() > 0 {
			if err := json.Unmarshal(w.Body.Bytes(), &a.Body); err != nil {
				t.Fatal(err)
			}
		}
		answers = append(answers, a)
	}
	key := map[string]string{"Idempotency-Key": "compatibility-start-0001"}
	request := startBody(f.items[0], map[string]any{"startFrom": "beginning"})
	var initial, replay, seek, paused playbackv1.SessionView
	add("start", f.call("POST", "/v1/playback/sessions", key, request, 201, &initial), false)
	if !regexp.MustCompile(`^ps_[A-Za-z0-9_-]{20}$`).MatchString(initial.ID) {
		t.Fatalf("session ID format: %q", initial.ID)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(initial.VersionID) || !regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`).MatchString(initial.ItemID) || initial.ItemID != f.items[0] {
		t.Fatal("start lost item/version identity")
	}
	if !regexp.MustCompile(`^/v1/media/[A-Za-z0-9_-]{43}$`).MatchString(initial.Presentation.URL) {
		t.Fatalf("media URL format: %q", initial.Presentation.URL)
	}
	add("same-body-replay", f.call("POST", "/v1/playback/sessions", key, request, 201, &replay), false)
	if replay.ID != initial.ID || replay.VersionID != initial.VersionID || replay.Presentation.URL != initial.Presentation.URL {
		t.Fatal("replay changed IDs or grant")
	}
	add("media-range", f.raw("GET", initial.Presentation.URL, "", map[string]string{"Range": "bytes=0-3"}, nil), true)
	path := "/v1/playback/sessions/" + initial.ID
	add("seek", f.call("PATCH", path, map[string]string{"If-Match": initial.Revision}, map[string]any{"seek": map[string]any{"id": "compatibility-seek-0001", "positionMs": 12500}}, 200, &seek), false)
	add("pause", f.call("PATCH", path, map[string]string{"If-Match": seek.Revision}, map[string]any{"state": "paused"}, 200, &paused), false)
	add("stale-write", f.raw("PATCH", path, f.owner.AccessToken, map[string]string{"If-Match": initial.Revision}, map[string]any{"state": "playing"}), false)
	add("stop", f.call("DELETE", path, nil, nil, 204, nil), false)
	add("retired-grant", f.raw("GET", initial.Presentation.URL, "", nil, nil), false)
	aliases := map[string]string{initial.ID: "<session:1>", initial.ItemID: "<item:1>", initial.VersionID: "<version:1>"}
	grant := strings.TrimPrefix(initial.Presentation.URL, "/v1/media/")
	var normalize func(string, any) any
	normalize = func(path string, v any) any {
		switch value := v.(type) {
		case map[string]any:
			if path == ".body" || path == ".body.error" {
				delete(value, "requestId")
			}
			for k, x := range value {
				value[k] = normalize(path+"."+k, x)
			}
		case []any:
			for i, x := range value {
				value[i] = normalize(path, x)
			}
		case string:
			if path == ".body.lease.expiresAt" || path == ".body.error.current.lease.expiresAt" {
				return "<lease-clock>"
			}
			if alias, ok := aliases[value]; ok {
				return alias
			}
			return strings.ReplaceAll(value, grant, "<grant:1>")
		}
		return v
	}
	raw, err := json.Marshal(answers)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	raw, err = json.MarshalIndent(normalize("", decoded), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	file := filepath.Join("testdata", "playback_wire_v1.json")
	if os.Getenv("PORTICO_PLAYBACK_COMPAT_RECORD") == "1" {
		if err = os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(expected) {
		t.Fatalf("playback wire contract changed; rebuilds must preserve the recorded oracle\ngot %s", raw)
	}
}
