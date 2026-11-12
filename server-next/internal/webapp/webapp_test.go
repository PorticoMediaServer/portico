package webapp

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The built app is served from its folder: hashed files cached for good, other files asked
// about every time, the app's own page for any address a browser opens as a page, and
// nothing outside the folder.
func TestTheWebAppIsServedFromItsFolder(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"index.html":               `<html><style nonce="__PORTICO_STYLE_NONCE__"></style>app</html>`,
		"assets/index-AbCd1234.js": "console.log(1)",
		"favicon.svg":              "<svg/>",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("not for the web"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "leak.txt")); err != nil {
		t.Fatal(err)
	}
	if Find(dir) == "" || Find(t.TempDir()) != "" {
		t.Fatal("a folder with an index.html is a web app, and one without is not")
	}
	h := Handler(dir)
	get := func(method, path, accept string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		r.Header.Set("Accept", accept)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	page := get("GET", "/", "text/html")
	body := page.Body.String()
	nonce := strings.TrimSuffix(strings.TrimPrefix(body, `<html><style nonce="`), `"></style>app</html>`)
	// The reader's fonts reach a book's pages as blob: addresses; blocked, every book shows in a fallback font.
	if page.Code != 200 || strings.Contains(body, "__PORTICO") || nonce == "" || !strings.Contains(page.Header().Get("Content-Security-Policy"), "'nonce-"+nonce+"'") ||
		!strings.Contains(page.Header().Get("Content-Security-Policy"), "font-src 'self' blob:") ||
		page.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("the page: %d %q %v", page.Code, body, page.Header())
	}
	if inside := get("GET", "/libraries/lib_1/settings", "text/html,application/xhtml+xml"); inside.Code != 200 || !strings.Contains(inside.Body.String(), "app</html>") {
		t.Fatalf("an address inside the app answered %d", inside.Code)
	}
	if asset := get("GET", "/assets/index-AbCd1234.js", "*/*"); asset.Code != 200 || asset.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		asset.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("a hashed file: %d %v", asset.Code, asset.Header())
	}
	if icon := get("GET", "/favicon.svg", "*/*"); icon.Code != 200 || icon.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("a file that may change under its name: %d %v", icon.Code, icon.Header())
	}
	for path, accept := range map[string]string{"/assets/gone-AbCd1234.js": "text/html", "/api.json": "application/json", "/leak.txt": "*/*",
		"/../secret.txt": "*/*"} {
		if w := get("GET", path, accept); w.Code != 404 {
			t.Errorf("%s answered %d; want 404", path, w.Code)
		}
	}
	if w := get("POST", "/", "text/html"); w.Code != 405 {
		t.Errorf("POST answered %d; want 405", w.Code)
	}
}
