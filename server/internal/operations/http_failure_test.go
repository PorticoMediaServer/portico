package operations

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClientClosedRequestsDoNotBecomeTimeoutAlerts(t *testing.T) {
	m := NewMeasurements(nil, "")
	var lines []string
	m.failLog = func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	for i := 0; i < 25; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		rec := httptest.NewRecorder()
		m.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cancel() // The incoming transport, not an operation's child lease.
			w.WriteHeader(http.StatusServiceUnavailable)
			if n, err := w.Write([]byte(`{"error":{"code":"timeout"}}`)); n != 0 || err != context.Canceled {
				t.Fatalf("closed client was sent a body: %d %v", n, err)
			}
		})).ServeHTTP(rec, httptest.NewRequest("GET", "/v1/probe", nil).WithContext(ctx))
		if rec.Code != 499 {
			t.Fatalf("client cancellation recorded as %d, want499", rec.Code)
		}
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "client closed (499)") || strings.Contains(lines[0], "warn") {
		t.Fatal("client closes polluted failure logs", lines)
	}
	if len(m.timeouts) != 0 || m.timeoutOpen || m.Inflight.Load() != 0 || m.Sent.Load() != 0 {
		t.Fatal("client closes polluted timeout/overload accounting")
	}
}

// Item 1: the wrapper's 5xx report is rate-limited by (pattern, status,
// code) — the first at once, then at most one line per minute with the
// suppressed count.
func TestWrapLogsFiveHundredOncePerMinute(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/widgets/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"code":"widget_broken","message":"Nope.","retryable":false}}`))
	})
	m := NewMeasurements(nil, "")
	now := time.Now()
	m.failNow = func() time.Time { return now }
	var mu sync.Mutex
	var lines []string
	m.failLog = func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, fmt.Sprintf(format, args...))
	}
	m.Pattern = func(r *http.Request) string {
		_, pattern := mux.Handler(r)
		return pattern
	}
	handler := m.Wrap(mux)

	serve := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/widgets/concrete-9", nil))
		return rec
	}

	if rec := serve(); rec.Code != http.StatusBadGateway {
		t.Fatalf("status=%d, want 502", rec.Code)
	}
	mu.Lock()
	if len(lines) != 1 {
		mu.Unlock()
		t.Fatalf("lines=%d, want 1: %v", len(lines), lines)
	}
	first := lines[0]
	mu.Unlock()
	for _, want := range []string{"GET", "GET /v1/widgets/{id}", "502", "widget_broken"} {
		if !strings.Contains(first, want) {
			t.Fatalf("log line lacks %q: %s", want, first)
		}
	}
	if strings.Contains(first, "concrete-9") {
		t.Fatalf("log line carries the concrete path: %s", first)
	}
	for i := 0; i < 10; i++ {
		serve()
	}
	mu.Lock()
	if len(lines) != 1 {
		mu.Unlock()
		t.Fatalf("after 10 more: lines=%d, want 1", len(lines))
	}
	mu.Unlock()
	now = now.Add(61 * time.Second)
	serve()
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 2 {
		t.Fatalf("after the window: lines=%d, want 2: %v", len(lines), lines)
	}
	if !strings.Contains(lines[1], "10 more") {
		t.Fatalf("second line lacks the suppressed count: %s", lines[1])
	}
}

// Item 2: a timed-out request's WARN names the registered route pattern, and
// falls back to the bare path (never the query) when no resolver is set.
func TestWrapTimeoutWarnNamesRoutePattern(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/slow/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"timeout","message":"Slow.","retryable":true}}`))
	})
	capture := &lockedLogCapture{}
	log.SetOutput(capture)
	defer log.SetOutput(os.Stderr)

	m := NewMeasurements(nil, "")
	m.Pattern = func(r *http.Request) string {
		_, pattern := mux.Handler(r)
		return pattern
	}
	handler := m.Wrap(mux)
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/slow/concrete-7", nil))
	lines := capture.matching("request timed out")
	if len(lines) != 1 {
		t.Fatalf("timeout lines=%d, want 1: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "(GET /v1/slow/{id})") || strings.Contains(lines[0], "GET GET") {
		t.Fatalf("timeout line should name the route pattern once: %s", lines[0])
	}
	if strings.Contains(lines[0], "concrete-7") {
		t.Fatalf("timeout line carries the concrete path: %s", lines[0])
	}

	// A request no route matched is named by its method only, never its path.
	unmatched := NewMeasurements(nil, "")
	unmatched.Pattern = func(*http.Request) string { return "" }
	unmatched.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"timeout","message":"Slow.","retryable":true}}`))
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/unrouted/concrete-9", nil))
	lines = capture.matching("request timed out")
	if last := lines[len(lines)-1]; !strings.Contains(last, "(GET (unmatched))") || strings.Contains(last, "concrete-9") {
		t.Fatalf("an unmatched request is named by its path: %s", last)
	}

	plain := NewMeasurements(nil, "")
	plainHandler := plain.Wrap(mux)
	plainHandler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/slow/concrete-8?token=secret", nil))
	lines = capture.matching("request timed out")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "(GET /v1/slow/concrete-8)") {
		t.Fatalf("fallback line lacks the bare path: %s", last)
	}
	if strings.Contains(last, "token=secret") {
		t.Fatalf("fallback line carries the query: %s", last)
	}
}

type lockedLogCapture struct {
	mu  sync.Mutex
	buf []string
}

func (b *lockedLogCapture) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, line := range strings.Split(string(p), "\n") {
		if line != "" {
			b.buf = append(b.buf, line)
		}
	}
	return len(p), nil
}

func (b *lockedLogCapture) matching(substr string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []string{}
	for _, line := range b.buf {
		if strings.Contains(line, substr) {
			out = append(out, line)
		}
	}
	return out
}
