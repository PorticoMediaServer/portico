package httpapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/hosted"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

const corsExposeTestOrigin = "https://web.getportico.tv"

func corsExposeHandler(t *testing.T) http.Handler {
	t.Helper()
	root := t.TempDir()
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	id, err := identity.New(db, root)
	if err != nil {
		t.Fatal(err)
	}
	host, err := hosted.New(db, id, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return New(Dependencies{DB: db, Identity: id, Catalog: catalog.New(db), Hosted: host, Origins: []string{corsExposeTestOrigin}})
}

func corsExposedNames(t *testing.T, h http.Handler, method, origin, target string, authorize bool) []string {
	t.Helper()
	r := httptest.NewRequest(method, "http://127.0.0.1:32500"+target, nil)
	r.Header.Set("Origin", origin)
	if authorize {
		r.Header.Set("Authorization", "Bearer pk_tool.secret")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var names []string
	for _, name := range strings.Split(w.Header().Get("Access-Control-Expose-Headers"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func corsHasName(names []string, want string) bool {
	for _, name := range names {
		if strings.EqualFold(name, want) {
			return true
		}
	}
	return false
}

// Cross-origin clients can read retry and representation headers; the retired
// report-cadence header must no longer be exposed.
func TestCrossOriginClientsCanReadPlaybackHeaders(t *testing.T) {
	h := corsExposeHandler(t)
	names := corsExposedNames(t, h, "OPTIONS", corsExposeTestOrigin, "/v1/playback/sessions/s/observation", false)
	for _, want := range []string{"Retry-After", "ETag", "Idempotent-Replayed"} {
		if !corsHasName(names, want) {
			t.Fatalf("%s is not exposed to an allowed origin: %v", want, names)
		}
	}
	if corsHasName(names, "Report-Every-Ms") {
		t.Fatalf("retired report cadence is exposed: %v", names)
	}
	// A tool on any other origin may call with a token, so its preflight is
	// answered too; the real request is held to the token rule.
	if names = corsExposedNames(t, h, "OPTIONS", "https://tool.example", "/v1/playback/sessions/s/observation", false); !corsHasName(names, "Retry-After") {
		t.Fatalf("a token-carrying tool cannot read retry guidance: %v", names)
	}
}

// Converted audio publishes decoder trim as response headers, and a browser
// hides them cross-origin unless the server names them in its exposure set.
// This checks the published set and its bounded filtering; the real
// ConvertedSource Opus/AAC frame0 and resume integration is read separately.
func TestCrossOriginConvertedAudioTrimIsExposed(t *testing.T) {
	h := corsExposeHandler(t)
	trim := []string{"X-Audio-Trim-Start", "X-Audio-Trim-End", "X-Audio-Frames"}
	for _, client := range []struct {
		name, method, origin, target string
		authorize                    bool
	}{
		{"hosted web app", "GET", corsExposeTestOrigin, "/v1/items", false},
		// The API key below is deliberately invalid: this establishes that a
		// token-carrying tool origin sees the exposure list, not that the route
		// authenticated a viewer.
		{"token-carrying tool origin", "GET", "https://tool.example", "/v1/items", true},
		{"converted audio preflight", "OPTIONS", corsExposeTestOrigin, "/v1/media/convert/opus", false},
	} {
		names := corsExposedNames(t, h, client.method, client.origin, client.target, client.authorize)
		if corsHasName(names, "Report-Every-Ms") {
			t.Fatalf("%s: retired report cadence is exposed: %v", client.name, names)
		}
		for _, want := range trim {
			if !corsHasName(names, want) {
				t.Fatalf("%s: %s is not exposed to %s: %v", client.name, want, client.origin, names)
			}
		}
		for _, want := range []string{"Retry-After", "ETag"} {
			if !corsHasName(names, want) {
				t.Fatalf("%s: existing %s exposure was lost: %v", client.name, want, names)
			}
		}
	}

	// A browser keeps only the headers the server published. Apply that set as
	// a bounded filter to representative frame0, resumed and AAC responses and
	// confirm the published values survive. These are plausible sample values,
	// not a producer or web-consumer run: X-Audio-Trim-End carries trailing
	// padding, not an absolute endpoint.
	exposed := corsExposedNames(t, h, "GET", corsExposeTestOrigin, "/v1/items", false)
	visible := func(header http.Header) map[string]string {
		out := map[string]string{}
		for name, values := range header {
			for _, published := range exposed {
				if strings.EqualFold(name, published) && len(values) > 0 {
					out[name] = values[0]
				}
			}
		}
		return out
	}
	for _, conversion := range []struct {
		name               string
		start, end, frames string
	}{
		{"frame0 opus", "312", "64", "44000"},
		{"resumed opus", "312", "96", "48000"},
		{"aac priming", "1024", "512", "47104"},
	} {
		header := http.Header{}
		header.Set("Content-Type", "audio/ogg")
		header.Set("Cache-Control", "private, no-store")
		header.Set("X-Audio-Trim-Start", conversion.start)
		header.Set("X-Audio-Trim-End", conversion.end)
		header.Set("X-Audio-Frames", conversion.frames)
		seen := visible(header)
		if seen["X-Audio-Trim-Start"] != conversion.start || seen["X-Audio-Trim-End"] != conversion.end || seen["X-Audio-Frames"] != conversion.frames {
			t.Fatalf("%s: published trim was filtered out: %v", conversion.name, seen)
		}
		if _, leaked := seen["Cache-Control"]; leaked {
			t.Fatalf("%s: the filter exposed an unpublished header", conversion.name)
		}
	}
}
