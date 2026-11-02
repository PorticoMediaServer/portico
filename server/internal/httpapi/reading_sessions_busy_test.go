package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

func TestReadingSessionsBusyIsRetryable503(t *testing.T) {
	w := httptest.NewRecorder()
	readingFailure(w, reading.ErrSessionsBusy)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", w.Code, w.Body.String())
	}
	if code := v1Code(w); code != "reading_sessions_busy" {
		t.Fatalf("code = %q, want reading_sessions_busy", code)
	}
	if retry := w.Header().Get("Retry-After"); retry != "2" {
		t.Fatalf("Retry-After = %q, want 2", retry)
	}
}
