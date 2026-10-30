package receiver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedCastCatalogueIsServedAsLocalScript(t *testing.T) {
	// Use the same embedded filesystem and mount as the production receiver route.
	handler := http.StripPrefix("/receiver/", http.FileServerFS(Files()))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/receiver/cast/receiver-text.js", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "javascript") || !strings.Contains(response.Body.String(), "window.porticoCastText") {
		t.Fatalf("catalogue asset: status=%d type=%q", response.Code, response.Header().Get("Content-Type"))
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/receiver/generate-cast-text.mjs", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("build-time producer must not be embedded: %d", response.Code)
	}
}

// The standalone presentation must work from the same embedded filesystem used
// by the real receiver route, including its local font and licence payload.
func TestEmbeddedCastPresentationResources(t *testing.T) {
	handler := http.StripPrefix("/receiver/", http.FileServerFS(Files()))
	for _, tc := range []struct{ path, contentType string }{
		{"/receiver/cast/", "text/html"},
		{"/receiver/cast/receiver.css", "text/css"},
		{"/receiver/cast/view.js", "javascript"},
		{"/receiver/cast/receiver.js", "javascript"},
		{"/receiver/cast/receiver-text.js", "javascript"},
		{"/receiver/cast/assets/manrope-var-latin.woff2", "font/woff2"},
		{"/receiver/cast/assets/OFL.txt", "text/plain"},
		{"/receiver/cast/assets/SOURCES.md", "text/"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", tc.path, nil))
			if response.Code != http.StatusOK || response.Body.Len() == 0 || !strings.Contains(response.Header().Get("Content-Type"), tc.contentType) {
				t.Fatalf("embedded resource %s: status=%d type=%q bytes=%d", tc.path, response.Code, response.Header().Get("Content-Type"), response.Body.Len())
			}
		})
	}
}
