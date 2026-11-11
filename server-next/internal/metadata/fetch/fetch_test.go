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

// Under go test a request to anything but this machine is refused before it is sent, naming
// the rule; a stand-in on 127.0.0.1 or localhost is asked as usual.
func TestATestCannotReachALiveProvider(t *testing.T) {
	c := New("tmdb", 0, 0)
	var sent atomic.Int64
	c.http.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
		sent.Add(1)
		return nil, errors.New("sent")
	})
	for _, address := range []string{"https://api.themoviedb.org/3/movie/603", "https://coverartarchive.org/release/x",
		"http://10.0.0.1/", "http://[2606:4700::1]/", "http://127.0.0.1.example.com/"} {
		if _, err := get(t, c, address); !errors.Is(err, ErrLiveCallInTest) {
			t.Errorf("%s: %v, want refused as a live call", address, err)
		}
	}
	if sent.Load() != 0 {
		t.Fatalf("%d refused requests were sent", sent.Load())
	}
	for _, address := range []string{"http://127.0.0.1:1/", "http://127.8.0.1/", "http://localhost/", "http://[::1]/"} {
		if _, err := get(t, c, address); errors.Is(err, ErrLiveCallInTest) {
			t.Errorf("%s: refused, want asked", address)
		}
	}
	if sent.Load() != 4 {
		t.Fatalf("%d of 4 requests to this machine were sent", sent.Load())
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
