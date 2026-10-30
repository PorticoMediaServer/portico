package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestWebFallbackComposesWithProtocolRoutes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("Portico shell"), 0600); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/dlna/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	Dependencies{WebDirectory: dir}.installationRoutes(mux)
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "/", 200}, {"HEAD", "/", 200}, {"POST", "/", 405}, {"SUBSCRIBE", "/dlna/events", 204}, {"GET", "/v1/missing", 404}} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.status {
			t.Fatalf("%s %s: got %d want %d", tc.method, tc.path, w.Code, tc.status)
		}
	}
}
