package httpapi

import (
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

type lockedLogBuffer struct {
	mu  sync.Mutex
	buf []string
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, line := range strings.Split(string(p), "\n") {
		if line != "" {
			b.buf = append(b.buf, line)
		}
	}
	return len(p), nil
}

func (b *lockedLogBuffer) warned() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []string{}
	for _, line := range b.buf {
		// The log package prefixes every line with a timestamp.
		if strings.Contains(line, "[http] warn:") {
			out = append(out, line)
		}
	}
	return out
}

func TestClientClosedFailureIsNotAServerFault(t *testing.T) {
	capture := &lockedLogBuffer{}
	log.SetOutput(capture)
	defer log.SetOutput(os.Stderr)
	m := operations.NewMeasurements(nil, "")
	for _, reply := range []func(http.ResponseWriter, *http.Request){
		func(w http.ResponseWriter, r *http.Request) { failure(w, r.Context().Err()) },
		func(w http.ResponseWriter, r *http.Request) {
			policyErrors.Write(w, "cancelled-request", &apierror.Error{Code: "internal", Cause: r.Context().Err()})
		},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		handler := m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cancel()
			reply(w, r)
		}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/probe", nil).WithContext(ctx))
		if rec.Code != 499 || rec.Body.Len() != 0 {
			t.Fatalf("closed client received a server failure: %d %s", rec.Code, rec.Body.String())
		}
	}
	for _, line := range capture.buf {
		if strings.Contains(line, "request failed") || strings.Contains(line, "warn:") || strings.Contains(line, "503") || strings.Contains(line, "500") {
			t.Fatal("closed client was reported as a server fault", line)
		}
	}
	// A canceled authority/operation under a still-live request remains a
	// genuine server failure; checking the error alone would hide it.
	rec := httptest.NewRecorder()
	m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failure(w, context.Canceled)
	})).ServeHTTP(rec, httptest.NewRequest("GET", "/v1/live-request", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("live-request cancellation was hidden: %d", rec.Code)
	}
}

// Item 1: a route whose handler fails with a server fault answers 503 and the
// wrapper logs one line naming the registered pattern (never the concrete
// path) and the code. Ten more within the minute log nothing more; the
// post-window "N more" half is covered in operations with a fake clock.
func TestFiveHundredLogsRoutePattern(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/loggingtest/{id}", func(w http.ResponseWriter, r *http.Request) {
		failure(w, context.DeadlineExceeded)
	})
	m := operations.NewMeasurements(nil, "")
	m.Pattern = func(r *http.Request) string {
		_, pattern := mux.Handler(r)
		return pattern
	}
	handler := m.Wrap(mux)

	capture := &lockedLogBuffer{}
	log.SetOutput(capture)
	defer log.SetOutput(os.Stderr)

	serve := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/loggingtest/concrete-id-123", nil))
		return rec
	}

	first := serve()
	if first.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", first.Code)
	}
	if !strings.Contains(first.Body.String(), `"code":"timeout"`) {
		t.Fatalf("body lacks the timeout code: %s", first.Body.String())
	}
	lines := capture.warned()
	if len(lines) != 1 {
		t.Fatalf("wrapper lines=%d, want 1: %v", len(lines), lines)
	}
	line := lines[0]
	for _, want := range []string{"GET /v1/loggingtest/{id}", "503", "timeout"} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line lacks %q: %s", want, line)
		}
	}
	if strings.Contains(line, "concrete-id-123") {
		t.Fatalf("log line carries the concrete path: %s", line)
	}
	for i := 0; i < 10; i++ {
		if rec := serve(); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("repeat %d: status=%d, want 503", i, rec.Code)
		}
	}
	if lines := capture.warned(); len(lines) != 1 {
		t.Fatalf("after 10 more: wrapper lines=%d, want 1: %v", len(lines), lines)
	}
}
