package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstalledWebAssetsStayInsidePublicRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"index.html": "<main>Portico</main>", "manifest.json": "{}",
		// Named as the build names them: a content hash before the extension.
		"assets/index-Bq3xK9aZ.js": "export default 1", "assets/Player-C3d_x-9Q.js": "export default 2", "assets/Player-C3d_x-9Q.js.map": "{}",
		"assets/Auth-BIi20xdI.css": "main{}", "assets/manrope-var-latin-BxY7Qf2k.woff2": "font",
		// Copied from the public folder under their own, unversioned names.
		"assets/tmdb.svg": "<svg/>", "assets/portico-wordmark-mono-white.svg": "<svg/>", "assets/index-test.js": "export default 3",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("PRIVATE DATA"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "assets", "escape.txt")); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Dependencies{WebDirectory: root}.installationRoutes(mux)
	for _, tc := range []struct {
		path, accept string
		status       int
		body, cache  string
	}{
		{"/", "text/html", 200, "<main>Portico</main>", "no-cache"},
		{"/libraries/example/items/title", "text/html", 200, "<main>Portico</main>", "no-cache"},
		{"/assets/index-Bq3xK9aZ.js", "*/*", 200, "export default 1", "public, max-age=31536000, immutable"},
		{"/assets/Player-C3d_x-9Q.js", "*/*", 200, "export default 2", "public, max-age=31536000, immutable"},
		{"/assets/Player-C3d_x-9Q.js.map", "*/*", 200, "{}", "public, max-age=31536000, immutable"},
		{"/assets/Auth-BIi20xdI.css", "*/*", 200, "main{}", "public, max-age=31536000, immutable"},
		{"/assets/manrope-var-latin-BxY7Qf2k.woff2", "*/*", 200, "font", "public, max-age=31536000, immutable"},
		{"/assets/tmdb.svg", "*/*", 200, "<svg/>", "no-cache"},
		{"/assets/portico-wordmark-mono-white.svg", "*/*", 200, "<svg/>", "no-cache"},
		{"/assets/index-test.js", "*/*", 200, "export default 3", "no-cache"},
		{"/manifest.json", "*/*", 200, "{}", "no-cache"},
		{"/index.html", "text/html", 200, "<main>Portico</main>", "no-cache"},
		{"/assets/missing.js", "text/html", 404, "", ""},
		{"/assets/escape.txt", "text/html", 404, "", ""},
		{"/assets/", "text/html", 404, "", ""},
		{"/missing", "application/json", 404, "", ""},
		{"/v2/playback/missing", "text/html", 404, "", ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.path, nil)
			r.Header.Set("Accept", tc.accept)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != tc.status || (tc.body != "" && w.Body.String() != tc.body) {
				t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "PRIVATE DATA") {
				t.Fatal("escaped public root")
			}
			if tc.cache != "" && w.Header().Get("Cache-Control") != tc.cache {
				t.Fatalf("cache policy %q, want %q", w.Header().Get("Cache-Control"), tc.cache)
			}
			if tc.status == 200 && w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing content-type protection")
			}
		})
	}
}

// A file the web build copies from its public folder keeps its own name, so
// it can change without its URL changing. None may be named like a
// content-hashed file, or it would be cached for a year.
func TestUnversionedWebAssetsAreNotNamedLikeHashedOnes(t *testing.T) {
	public := filepath.Join("..", "..", "..", "web", "public")
	if _, err := os.Stat(public); err != nil {
		t.Skip("the web app's sources are not beside this module")
	}
	err := filepath.WalkDir(public, func(file string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		name, err := filepath.Rel(public, file)
		if err != nil {
			return err
		}
		if name = filepath.ToSlash(name); contentHashedWebAsset(name) {
			t.Errorf("%s would be served as immutable; rename it so its last hyphenated part is not eight characters", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWebDocumentAuthorizesOnlyItsOwnScrollLockStyles(t *testing.T) {
	root := t.TempDir()
	document := `<html><head><meta name="portico-style-nonce" content="__PORTICO_STYLE_NONCE__"></head><body>Portico</body></html>`
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte(document), 0600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Dependencies{WebDirectory: root}.installationRoutes(mux)
	seen := make(map[string]bool)
	for _, target := range []string{"/", "/settings/server-integrations", "/index.html"} {
		r := httptest.NewRequest("GET", target, nil)
		r.Header.Set("Accept", "text/html")
		r.Header.Set("If-Modified-Since", "Tue, 13 Oct 2099 12:00:00 GMT")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d", target, w.Code)
		}
		body := w.Body.String()
		start := strings.Index(body, `content="`) + len(`content="`)
		end := strings.Index(body[start:], `"`)
		nonce := body[start : start+end]
		if len(nonce) < 26 || nonce == styleNoncePlaceholder || seen[nonce] {
			t.Fatalf("nonce is missing or reused: %q", nonce)
		}
		seen[nonce] = true
		policy := w.Header().Get("Content-Security-Policy")
		if !strings.Contains(policy, "style-src 'self' 'nonce-"+nonce+"'") || strings.Contains(policy, "'unsafe-inline'") || strings.Contains(policy, "'unsafe-eval'") {
			t.Fatalf("unsafe or mismatched policy: %s", policy)
		}
		if !strings.Contains(policy, "'wasm-unsafe-eval'") || !strings.Contains(policy, "frame-ancestors 'none'") || !strings.Contains(policy, "frame-src 'self' blob:;") {
			t.Fatal("lost existing protections")
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Last-Modified") != "" {
			t.Fatal("nonce document can be reused with a different policy")
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("HEAD", "/", nil)
	mux.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") == "" {
		t.Fatal("HEAD must return headers without the document")
	}
}

func TestSubtitleCJKFontIsServedOnceAndRevalidates(t *testing.T) {
	mux := http.NewServeMux()
	Dependencies{}.installationRoutes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("GET", "/v1/subtitle-fonts/cjk", nil))
	tag := response.Header().Get("ETag")
	if response.Code != 200 || response.Header().Get("Content-Type") != "font/otf" || tag == "" || response.Body.Len() < 1<<20 || !strings.HasPrefix(response.Body.String(), "OTTO") {
		t.Fatalf("font: %d %q %q %d", response.Code, response.Header().Get("Content-Type"), tag, response.Body.Len())
	}
	again := httptest.NewRequest("GET", "/v1/subtitle-fonts/cjk", nil)
	again.Header.Set("If-None-Match", tag)
	cached := httptest.NewRecorder()
	mux.ServeHTTP(cached, again)
	if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
		t.Fatalf("revalidation: %d %d", cached.Code, cached.Body.Len())
	}
}
