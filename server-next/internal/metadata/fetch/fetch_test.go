package fetch

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
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

// A provider's X-RateLimit headers are read: with little of a bucket's allowance left the
// requests are spread out, and a used-up allowance is not asked again until its reset, while
// another bucket goes on.
func TestRateLimitHeadersSlowRequestsBeforeARefusal(t *testing.T) {
	var asked atomic.Int64
	left := map[string]int{"search": 2, "lookup": 0}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		bucket := r.URL.Path[1:]
		w.Header().Set("X-RateLimit-Limit", "400")
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(left[bucket]))
		reset := "1" // a second from now
		if bucket == "lookup" {
			reset = strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
		}
		w.Header().Set("X-RateLimit-Reset", reset)
		w.Write([]byte("ok"))
	}))
	defer server.Close()
	c := New("test", 0, 5*time.Second)
	c.Bucket = func(r *http.Request) string { return r.URL.Path[1:] }

	// The lookup bucket is used up for an hour: the next lookup is not sent.
	if _, err := get(t, c, server.URL+"/lookup"); err != nil {
		t.Fatal(err)
	}
	var slow *SlowDown
	if _, err := get(t, c, server.URL+"/lookup"); !errors.As(err, &slow) || asked.Load() != 1 {
		t.Fatalf("a used-up allowance was asked again: %v, %d requests", err, asked.Load())
	}
	// Two searches of 400 left, resetting in a second: they are spread over that second.
	start := time.Now()
	for range 3 {
		if _, err := get(t, c, server.URL+"/search"); err != nil {
			t.Fatal(err)
		}
	}
	if took := time.Since(start); took < 500*time.Millisecond {
		t.Errorf("three searches with two of 400 left took %v", took)
	}
}
