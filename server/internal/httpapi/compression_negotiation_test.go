package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGzipQualityAndRepresentationTags(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   bool
	}{
		{"gzip", true}, {"GZIP;q=1", true}, {"gzip;q=0", false}, {"gzip;q=0.0, *;q=1", false}, {"br, *;q=0.5", true}, {"gzip;q=NaN", false}, {"gzip;q=2", false}, {"gzip;q=bad", false},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Encoding", tc.header)
		if got := acceptsGzip(r); got != tc.want {
			t.Fatalf("%s: %v", tc.header, got)
		}
	}
	w := httptest.NewRecorder()
	c := &compressingWriter{ResponseWriter: w}
	c.Header().Set("Content-Type", "application/json")
	c.Header().Set("ETag", `"body"`)
	_, _ = c.Write(bytes.Repeat([]byte("x"), 2048))
	c.close()
	if w.Header().Get("ETag") != `W/"body"` || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("%v", w.Header())
	}
	w = httptest.NewRecorder()
	c = &compressingWriter{ResponseWriter: w}
	c.Header().Set("ETag", `"body"`)
	c.WriteHeader(http.StatusNotModified)
	if w.Header().Get("ETag") != `W/"body"` {
		t.Fatal("304 strengthened a gzip validator")
	}
}
