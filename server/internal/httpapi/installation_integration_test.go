package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSPARejectsEveryAPIVersionAndPreservesReadiness(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("TEST-ONLY SPA"), 0600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(Dependencies{WebDirectory: root}).installationRoutes(mux)
	for _, path := range []string{"/v1", "/v1/missing", "/v2", "/v2/not-implemented", "/v99/future", "/health/unknown"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 404 || strings.Contains(w.Body.String(), "TEST-ONLY SPA") {
			t.Fatal("API fell through to HTML", path, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "/settings/backups", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "TEST-ONLY SPA") {
		t.Fatal("deep link did not reach SPA", w.Code)
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/health/ready", nil))
	if w.Code != 503 || strings.Contains(w.Body.String(), "TEST-ONLY SPA") {
		t.Fatal("missing database was ready", w.Code, w.Body.String())
	}
}
