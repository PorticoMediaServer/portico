package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Twenty distinct clients can share the ordinary Search burst without turning
// a 1.6-second wait into a refusal or receiving a fresh five seconds afterward.
func TestSearchBurstSharesOneEntryBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	a := newAdmission()
	l := a.lanes[laneExpensive]
	if l.spec.queueCapacity != 16 || l.spec.capacity != 8 {
		t.Fatalf("search bounds changed: %+v", l.spec)
	}
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	var releaseOnce sync.Once
	var workers sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		releaseOnce.Do(func() { close(release) })
		workers.Wait()
	})
	type observation struct {
		deadline time.Time
		started  time.Time
		status   int
	}
	observed := make(chan observation, 20)
	entered := make(chan struct{}, 8)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Hold") == "true" {
			entered <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Error("search reached work without its entry deadline")
		}
		// The real Search handler's three-second cap inherits the entry bound.
		work, stop := context.WithTimeout(r.Context(), 3*time.Second)
		defer stop()
		workDeadline, _ := work.Deadline()
		if workDeadline.After(deadline) {
			t.Error("handler extended the admission deadline")
		}
		w.Header().Set("X-Test-Deadline", deadline.Format(time.RFC3339Nano))
		w.WriteHeader(http.StatusOK)
	})
	h := a.wrap(mux, mux)
	start := func(i int, hold bool) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			r := httptest.NewRequest("GET", "/v1/search?q=film", nil).WithContext(ctx)
			// Distinct actual peer keys; invented bearer strings cannot mint
			// separate shares in the canonical admission ledger.
			r.RemoteAddr = fmt.Sprintf("198.51.100.%d:12345", i+1)
			if hold {
				r.Header.Set("X-Test-Hold", "true")
			}
			w := httptest.NewRecorder()
			started := time.Now()
			h.ServeHTTP(w, r)
			deadline, _ := time.Parse(time.RFC3339Nano, w.Header().Get("X-Test-Deadline"))
			observed <- observation{deadline, started, w.Code}
		}()
	}
	for i := 0; i < 8; i++ {
		start(i, true)
	}
	for i := 0; i < 8; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("initial eight clients did not enter")
		}
	}
	for i := 8; i < 20; i++ {
		start(i, false)
	}
	waitForQueueCount(t, l, 12)
	// Deliberately beyond the previous 1.5-second admission slice.
	time.Sleep(1600 * time.Millisecond)
	releaseOnce.Do(func() { close(release) })
	for i := 0; i < 20; i++ {
		select {
		case got := <-observed:
			if got.status != http.StatusOK || got.deadline.IsZero() {
				t.Fatalf("burst request failed: %+v", got)
			}
			// Allow only local setup overhead, never another queue-sized budget.
			if got.deadline.After(got.started.Add(5*time.Second + 20*time.Millisecond)) {
				t.Fatalf("queue renewed a caller's deadline: %+v", got)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("burst did not finish within its original total budget")
		}
	}
	workers.Wait()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active.Load() != 0 || l.waiters != 0 || len(l.interest) != 0 || len(l.held) != 0 || l.imbalance.Load() != 0 || l.rejected.Load() != 0 {
		t.Fatalf("burst accounting did not balance: active=%d waiters=%d rejected=%d", l.active.Load(), l.waiters, l.rejected.Load())
	}
}

func TestSearchQueueCannotOutliveEntryOrCallerDeadline(t *testing.T) {
	for _, test := range []struct {
		name   string
		parent func() (context.Context, context.CancelFunc)
		cancel bool
	}{
		{"total budget", func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }, false},
		{"earlier caller deadline", func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 30*time.Millisecond)
		}, false},
		{"caller cancellation", func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := newAdmission()
			l := newLane(laneExpensive, laneSpec{capacity: 1, queueWait: time.Second, budget: 100 * time.Millisecond, class: dbwork.ClassInteractive})
			a.lanes[laneExpensive] = l
			if admitted, queued := l.begin("occupier"); !admitted || queued {
				t.Fatal("failed to occupy the actual lane")
			}
			defer func() { l.release("occupier"); l.leave("occupier") }()
			var called atomic.Bool
			mux := http.NewServeMux()
			mux.HandleFunc("GET /v1/search", func(w http.ResponseWriter, r *http.Request) { called.Store(true) })
			ctx, cancel := test.parent()
			defer cancel()
			r := httptest.NewRequest("GET", "/v1/search?q=film", nil).WithContext(ctx)
			r.Header.Set("Authorization", "Bearer original-waiter")
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { a.wrap(mux, mux).ServeHTTP(w, r); close(done) }()
			t.Cleanup(func() { cancel(); <-done })
			if test.cancel {
				waitForQueueCount(t, l, 1)
				cancel()
			}
			select {
			case <-done:
			case <-time.After(600 * time.Millisecond):
				t.Fatal("queue outlived its shorter original deadline")
			}
			if w.Code != http.StatusServiceUnavailable || called.Load() {
				t.Fatalf("expired waiter entered work: status=%d called=%v", w.Code, called.Load())
			}
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.waiters != 0 || len(l.waiting) != 0 || len(l.interest) != 1 || l.active.Load() != 1 || l.imbalance.Load() != 0 {
				t.Fatal("cancelled waiter retained accounting or spent another client's slot")
			}
		})
	}
}

func TestSearchWorkUsesOnlyRemainingEntryBudget(t *testing.T) {
	a := newAdmission()
	l := newLane(laneExpensive, laneSpec{capacity: 1, queueWait: time.Second, budget: 200 * time.Millisecond, class: dbwork.ClassInteractive})
	a.lanes[laneExpensive] = l
	if admitted, _ := l.begin("occupier"); !admitted {
		t.Fatal("failed to occupy lane")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var release sync.Once
	free := func() { release.Do(func() { l.release("occupier"); l.leave("occupier") }) }
	defer free()
	deadline := make(chan time.Time, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/search", func(w http.ResponseWriter, r *http.Request) {
		limit, _ := r.Context().Deadline()
		deadline <- limit
		<-r.Context().Done()
		failure(w, r.Context().Err())
	})
	r := httptest.NewRequest("GET", "/v1/search?q=film", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer waiting-client")
	w := httptest.NewRecorder()
	started := time.Now()
	done := make(chan struct{})
	go func() { a.wrap(mux, mux).ServeHTTP(w, r); close(done) }()
	t.Cleanup(func() { cancel(); free(); <-done })
	waitForQueueCount(t, l, 1)
	time.Sleep(80 * time.Millisecond)
	free()
	select {
	case limit := <-deadline:
		if limit.After(started.Add(230 * time.Millisecond)) {
			t.Fatal("accepted work received a new budget after queueing")
		}
	case <-time.After(time.Second):
		t.Fatal("queued request never reached work")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("work ignored its original deadline")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("over-budget work was not an honest timeout: %d", w.Code)
	}
}
