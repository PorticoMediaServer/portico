package fetch

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func get(t *testing.T, c *Client, url string) ([]byte, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c.Do(req)
}

// A provider that asks for a long pause is not asked again until it is over.
func TestLongPauseIsHonoured(t *testing.T) {
	var asked atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	c := New("test", 0, 5*time.Second)
	for range 3 {
		var slow *SlowDown
		if _, err := get(t, c, server.URL); !errors.As(err, &slow) || time.Until(slow.Until) < 100*time.Second {
			t.Fatalf("want a pause of about two minutes, got %v", err)
		}
	}
	if asked.Load() != 1 {
		t.Errorf("the provider was asked %d times during its pause", asked.Load())
	}
}

// A short pause is waited out and the request sent again; requests are spaced out.
func TestShortPauseAndSpacing(t *testing.T) {
	var asked atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if asked.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer server.Close()
	c := New("test", 30*time.Millisecond, time.Minute)
	start := time.Now()
	for range 3 {
		if body, err := get(t, c, server.URL); err != nil || string(body) != "ok" {
			t.Fatalf("got %q, %v", body, err)
		}
	}
	if asked.Load() != 4 {
		t.Errorf("want the first request sent twice, %d requests", asked.Load())
	}
	if took := time.Since(start); took < 90*time.Millisecond {
		t.Errorf("four requests at least 30 ms apart took %v", took)
	}
}
