package httpapi

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/server/internal/httpapi/fixture"
	"strings"
	"testing"
	"time"
)

// A home document is hundreds of kilobytes of JSON on the wire and about a
// seventh of that compressed. On the networks this server is expected to serve
// over, that difference is whether the page arrives.
func TestCompositeResponsesCompressAndMediaDoesNot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	tier := performanceTier{name: "gzip", shape: fixture.Tiny(), concurrentViewers: 1, iterations: 1, maximumP95: time.Minute, maximumP99: time.Minute, allowBoundedOverload: true}
	f := newLoadFixture(t, tier)
	token := f.viewers[0].AccessToken

	ask := func(path, encoding string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, bytes.NewReader(nil))
		r.Header.Set("Authorization", "Bearer "+token)
		if encoding != "" {
			r.Header.Set("Accept-Encoding", encoding)
		}
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}

	plain := ask("/v1/home", "")
	if plain.Code != 200 {
		t.Fatalf("home %d %s", plain.Code, plain.Body.String())
	}
	if plain.Header().Get("Content-Encoding") != "" {
		t.Fatal("a client that did not ask for gzip was sent gzip")
	}
	zipped := ask("/v1/home", "gzip")
	if zipped.Code != 200 {
		t.Fatalf("home %d", zipped.Code)
	}
	if zipped.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("home was not compressed for a client that asked (%d bytes)", plain.Body.Len())
	}
	if !strings.Contains(strings.Join(zipped.Header().Values("Vary"), ","), "Accept-Encoding") {
		t.Fatal("compressed Home did not vary by encoding")
	}
	if zipped.Body.Len() >= plain.Body.Len() {
		t.Fatalf("compression made the body larger: %d against %d", zipped.Body.Len(), plain.Body.Len())
	}
	t.Logf("GET /v1/home %d bytes uncompressed, %d gzipped (%.0f%%)", plain.Body.Len(), zipped.Body.Len(), 100*float64(zipped.Body.Len())/float64(plain.Body.Len()))

	// The bytes must survive the round trip, not merely be smaller.
	reader, err := gzip.NewReader(bytes.NewReader(zipped.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	// Separately composed responses have fresh timestamps and cursor expiry
	// signatures. Compare cursor payloads too, excluding only their expiry;
	// membership, ranking fingerprints and paging positions must still agree.
	strip := func(raw []byte) string {
		var document map[string]any
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatalf("not the JSON document: %v", err)
		}
		delete(document, "generatedAt")
		var scrub func(any)
		scrub = func(v any) {
			switch x := v.(type) {
			case map[string]any:
				for key, value := range x {
					if key == "nextCursor" {
						if raw, ok := value.(string); ok && raw != "" {
							payload, _, ok := strings.Cut(raw, ".")
							if !ok {
								t.Fatal("unsigned home cursor")
							}
							data, err := base64.RawURLEncoding.DecodeString(payload)
							if err != nil {
								t.Fatal(err)
							}
							var cursor map[string]any
							if err = json.Unmarshal(data, &cursor); err != nil {
								t.Fatal(err)
							}
							delete(cursor, "expires")
							x[key] = cursor
						}
					} else {
						scrub(value)
					}
				}
			case []any:
				for _, value := range x {
					scrub(value)
				}
			}
		}
		scrub(document)
		normalised, _ := json.Marshal(document)
		return string(normalised)
	}
	if strip(decoded) != strip(plain.Body.Bytes()) {
		t.Fatal("the compressed body decoded to something other than the plain one")
	}

	// Home contains current live/spotlight facts and is always recomposed.
	if plain.Header().Get("ETag") != "" {
		t.Fatal("time-dependent Home advertised a reusable validator")
	}
	// Home rows also carry live command offers and must be recomposed, even
	// when a client presents a wildcard validator.
	r := httptest.NewRequest("GET", "/v1/home/rows/continue", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("If-None-Match", "*")
	live := httptest.NewRecorder()
	f.handler.ServeHTTP(live, r)
	if live.Code != http.StatusOK || live.Header().Get("ETag") != "" || live.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Home command offers were reusable: %d %v", live.Code, live.Header())
	}
	// A stable catalogue grid still honors validators. A 304 has no body.
	path := "/v1/libraries/" + f.library + "/content?view=browse&limit=20"
	stable := ask(path, "")
	if stable.Code != http.StatusOK || stable.Header().Get("Cache-Control") != revalidate {
		t.Fatal(stable.Code, stable.Header(), stable.Body.String())
	}
	tag := stable.Header().Get("ETag")
	if tag == "" {
		t.Fatal("stable grid carried no ETag")
	}
	zippedGrid := ask(path, "gzip")
	if zippedGrid.Code != http.StatusOK || zippedGrid.Header().Get("Content-Encoding") != "gzip" || zippedGrid.Header().Get("ETag") != "W/"+tag || !strings.Contains(strings.Join(zippedGrid.Header().Values("Vary"), ","), "Accept-Encoding") {
		t.Fatalf("compressed grid lost its representation headers: %d %v", zippedGrid.Code, zippedGrid.Header())
	}
	r = httptest.NewRequest("GET", path, bytes.NewReader(nil))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Accept-Encoding", "gzip")
	r.Header.Set("If-None-Match", tag)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	if w.Code != 304 {
		t.Fatalf("a matching If-None-Match on the grid answered %d", w.Code)
	}
	if w.Body.Len() != 0 || w.Header().Get("Content-Encoding") != "" {
		t.Fatalf("the 304 carried %d bytes and Content-Encoding %q", w.Body.Len(), w.Header().Get("Content-Encoding"))
	}
	if weakETag(w.Header().Get("ETag")) != tag {
		t.Fatalf("304 changed the grid validator: %q", w.Header().Get("ETag"))
	}
	r = httptest.NewRequest("GET", path, nil)
	r.Header.Set("If-None-Match", tag)
	denied := httptest.NewRecorder()
	f.handler.ServeHTTP(denied, r)
	if denied.Code != http.StatusUnauthorized || denied.Header().Get("ETag") != "" {
		t.Fatalf("anonymous client revalidated a private grid: %d %v", denied.Code, denied.Header())
	}
}
