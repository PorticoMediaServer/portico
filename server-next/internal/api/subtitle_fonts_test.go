package api

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
)

func TestSubtitleCJKFallbackCanBeFetchedWithoutSignIn(t *testing.T) {
	s := newTestServer(t)
	font, tag := mediaexec.CJKFont()
	for _, cached := range []bool{false, true} {
		r := httptest.NewRequest("GET", "/v1/subtitle-fonts/cjk", nil)
		if cached {
			r.Header.Set("If-None-Match", tag)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Header().Get("ETag") != tag || w.Header().Get("Cache-Control") != "public, max-age=86400" {
			t.Fatal("the shared font cannot be cached by its content")
		}
		if cached {
			if w.Code != 304 || w.Body.Len() != 0 {
				t.Fatalf("cached font answered %d with %d bytes", w.Code, w.Body.Len())
			}
		} else if w.Code != 200 || w.Header().Get("Content-Type") != "font/otf" || !bytes.Equal(w.Body.Bytes(), font) {
			t.Fatalf("font answered %d, %s, %d bytes", w.Code, w.Header().Get("Content-Type"), w.Body.Len())
		}
	}
}
