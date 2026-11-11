package webapp

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The web folder is handed to anyone who can reach the server, signed in or not. A hidden
// file that happens to be in it is not served, and a folder that holds the server's data
// folder is not a web folder at all.
func TestTheWebFolderGivesAwayNothingButTheApp(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"index.html": "app", ".env": "SECRET=1", ".git/config": "[core]", "data/portico.db": "the database"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h := Handler(dir)
	for _, path := range []string{"/.env", "/.git/config"} {
		for _, accept := range []string{"*/*", "text/html"} {
			r := httptest.NewRequest("GET", path, nil)
			r.Header.Set("Accept", accept)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 404 {
				t.Errorf("%s (Accept %s) answered %d %q; want 404", path, accept, w.Code, w.Body.String())
			}
		}
	}
	link := filepath.Join(t.TempDir(), "web")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	for _, web := range []string{dir, link} {
		if !Holds(web, filepath.Join(dir, "data")) || !Holds(web, dir) {
			t.Errorf("%s holds the data folder, and that was not noticed", web)
		}
	}
	if Holds(filepath.Join(dir, "data"), dir) || Holds(dir, t.TempDir()) || Holds(dir, dir+"-data") {
		t.Error("a data folder beside or above the web folder is fine")
	}
}
