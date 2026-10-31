package httpapi

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestReadingInitialReadyHasNoPollDelayOrHiddenHandle(t *testing.T) {
	if testing.Short() {
		t.Skip("isolated cold fixture measured 3.12s; merge-tier coverage")
	}
	// The process-global write gate replaces its wake channel during a write.
	// A channel created inside this bubble cannot later be closed by real-clock
	// tests. Keep the entire cold fixture in its own test process.
	const child = "PORTICO_TEST_READING_INITIAL_READY_SYNCTEST"
	if os.Getenv(child) != "1" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 150*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, executable, "-test.run=^TestReadingInitialReadyHasNoPollDelayOrHiddenHandle$", "-test.count=1", "-test.timeout=120s")
		cmd.Env = append(os.Environ(), child+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated initial readiness: %v\n%s", err, output)
		}
		return
	}
	// Keep cold SQL and the real worker runnable without spending the virtual
	// initial grace on host scheduling. All resources belong to this bubble.
	synctest.Test(t, func(t *testing.T) {
		f := newReadingFixture(t, "book", "pdf")
		readingSeriesOwnerField(t, f, f.item, "author", "Tiny author")
		readingSeriesDrain(t, f)
		store, _ := readingFacetTestStore(t, f, false)
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/libraries/{id}/browse", f.d.readingBrowsePreparedHandler)
		mux.Handle("GET /v1/libraries/{id}/facets", readingFacetTestHandler(t, f))
		base := "/v1/libraries/" + f.library
		for _, path := range []string{"browse", "facets"} {
			method := http.MethodGet
			if path == "browse" {
				method = http.MethodPost
			}
			w := preparedReadingHTTP(t, mux, method, base+"/"+path, f.f.owner.AccessToken, map[string]any{"limit": 1}, nil)
			if w.Code != 200 || w.Header().Get("Location") != "" || w.Header().Get("Retry-After") != "" || strings.Contains(w.Body.String(), `"state":"preparing"`) {
				t.Fatalf("initial %s: %d %s %v", path, w.Code, w.Body.String(), w.Header())
			}
			if stats := store.Stats(); stats.Handles != 0 || stats.Deliveries != 0 || stats.DeliveryBytes != 0 {
				t.Fatalf("initial %s leaked private demand/delivery: %+v", path, stats)
			}
		}
	})
}

func TestReadingInitialFacetRetryBacksOffAfterFirstPoll(t *testing.T) {
	f := newReadingFixture(t, "book", "pdf")
	readingSeriesOwnerField(t, f, f.item, "author", "Mid-length author")
	readingSeriesDrain(t, f)
	store, release := readingFacetTestStore(t, f, true)
	h := readingFacetTestHandler(t, f)
	_, path := readingFacetStart(t, f, h) // Initial response retains the 250ms floor.
	w := readingHTTP(t, h, "GET", path, f.f.owner.AccessToken, "", nil)
	if w.Code != 202 || w.Header().Get("Retry-After") != "1" || !strings.Contains(w.Body.String(), `"retryAfterMs":1000`) {
		t.Fatalf("poll did not restore ordinary pacing: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	if stats := store.Stats(); stats.Deliveries != 0 || stats.DeliveryBytes != 0 {
		t.Fatalf("pending poll leaked delivery: %+v", stats)
	}
	release()
	readingFacetReady(t, f, h, path)
}
