package metadataprovider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureTransport(t *testing.T, handler http.HandlerFunc) *transport {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	tr := newTransport("fixture", server.URL, "Portico/test (https://getportico.tv)")
	tr.interval = 0
	t.Cleanup(tr.client.CloseIdleConnections)
	return tr
}
func errorCode(t *testing.T, e error, code string) {
	t.Helper()
	var p *Error
	if !errors.As(e, &p) || p.Code != code {
		t.Fatalf("want %s, got %v", code, e)
	}
}
func TestTransportRateAndCancellation(t *testing.T) {
	var mu sync.Mutex
	starts := []time.Time{}
	tr := fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		w.Write([]byte(`{}`))
	})
	tr.interval = 35 * time.Millisecond
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out any
			errs <- tr.request(context.Background(), "GET", "/data", "", nil, &out)
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	for i := 1; i < len(starts); i++ {
		if starts[i].Sub(starts[i-1]) < 25*time.Millisecond {
			t.Fatal("rate starts overlapped", starts)
		}
	}
	tr.mu.Lock()
	tr.next = time.Now().Add(time.Hour)
	tr.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	var out any
	if e := tr.request(ctx, "GET", "/data", "", nil, &out); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if e := tr.acquire(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func TestBackoffBoundsAndNoAutomaticRetry(t *testing.T) {
	var calls atomic.Int32
	tr := fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
	})
	var out any
	e := tr.request(context.Background(), "GET", "/", "", nil, &out)
	var p *Error
	if !errors.As(e, &p) || !p.Retryable() || p.RetryAfter != time.Hour {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if e = tr.request(ctx, "GET", "/", "", nil, &out); !errors.Is(e, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatal(e, calls.Load())
	}
	now := time.Now()
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{{"99999999", 24 * time.Hour}, {"-1", time.Second}, {"garbage", time.Second}, {now.Add(-time.Hour).UTC().Format(http.TimeFormat), time.Second}} {
		if got := retryDelay(tc.value, now); got != tc.want {
			t.Fatal(tc, got)
		}
	}
}
func TestTransportSizeRedirectAndSecrets(t *testing.T) {
	var destination atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destination.Add(1) }))
	defer other.Close()
	tr := fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" || r.Header.Get("User-Agent") == "" {
			t.Error("missing request headers")
		}
		switch r.URL.Path {
		case "/redirect":
			w.Header().Set("Location", other.URL+"/secret-location")
			w.WriteHeader(302)
			w.Write([]byte("secret-body"))
		case "/size":
			w.Header().Set("Content-Length", "4194305")
		case "/chunk":
			w.(http.Flusher).Flush()
			w.Write([]byte(strings.Repeat("x", (4<<20)+1)))
		case "/bad":
			w.Write([]byte(`{"secret":"private"} trailing`))
		default:
			w.WriteHeader(500)
			w.Write([]byte("secret-body"))
		}
	})
	var out any
	for _, tc := range []struct{ route, code string }{{"/redirect", "request_rejected"}, {"/size", "response_too_large"}, {"/chunk", "response_too_large"}, {"/bad", "invalid_response"}, {"/failure", "request_rejected"}} {
		e := tr.request(context.Background(), "GET", tc.route, "secret-token", nil, &out)
		errorCode(t, e, tc.code)
		if strings.Contains(e.Error(), "secret") || strings.Contains(e.Error(), "private") {
			t.Fatal("secret leaked", e)
		}
	}
	if destination.Load() != 0 {
		t.Fatal("redirect followed")
	}
}
func TestCancellationWhileReadingBody(t *testing.T) {
	tr := fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	var out any
	if e := tr.request(ctx, "GET", "/", "", nil, &out); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
}
func TestAlreadyCancelledNeverReservesRateSlot(t *testing.T) {
	tr := newTransport("fixture", "http://127.0.0.1", "test")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 100; i++ {
		if e := tr.acquire(ctx); !errors.Is(e, context.Canceled) {
			t.Fatal(e)
		}
	}
	if !tr.next.IsZero() {
		t.Fatal("cancelled caller consumed admission")
	}
}
func TestTransport503HTTPDateBackoff(t *testing.T) {
	tr := fixtureTransport(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", time.Now().Add(10*time.Second).UTC().Format(http.TimeFormat))
		w.WriteHeader(503)
	})
	var out any
	e := tr.request(context.Background(), "GET", "/", "", nil, &out)
	var problem *Error
	if !errors.As(e, &problem) || problem.RetryAfter < 8*time.Second || problem.RetryAfter > 11*time.Second {
		t.Fatal(e)
	}
}
