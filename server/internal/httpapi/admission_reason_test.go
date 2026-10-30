package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func occupiedAdmission(t *testing.T, spec laneSpec) (*admission, *lane, http.Handler) {
	t.Helper()
	a := newAdmission()
	l := newLane(laneBrowsing, spec)
	a.lanes[laneBrowsing] = l
	mux := http.NewServeMux()
	entered, release, completed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var first, closeRelease sync.Once
	mux.HandleFunc("GET /v1/home", func(w http.ResponseWriter, r *http.Request) {
		first.Do(func() { close(entered); <-release })
		w.WriteHeader(http.StatusOK)
	})
	handler := a.wrap(mux, mux)
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/home", nil))
		close(completed)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("holder did not enter")
	}
	t.Cleanup(func() {
		closeRelease.Do(func() { close(release) })
		select {
		case <-completed:
		case <-time.After(time.Second):
			t.Error("active request did not recover")
		}
	})
	return a, l, handler
}

func assertAdmissionRefusalReasons(t *testing.T, a *admission, want LaneDiagnostics) {
	t.Helper()
	var got LaneDiagnostics
	for _, entry := range a.diagnostics() {
		if entry.Lane == laneBrowsing {
			got = entry
			break
		}
	}
	sum := got.NoQueueRefusals + got.QueueFullRefusals + got.ClientQueueFullRefusals + got.QueueTimeoutRefusals + got.QueueCancellationRefusals
	if sum != got.Rejected || got.NoQueueRefusals != want.NoQueueRefusals || got.QueueFullRefusals != want.QueueFullRefusals || got.ClientQueueFullRefusals != want.ClientQueueFullRefusals || got.QueueTimeoutRefusals != want.QueueTimeoutRefusals || got.QueueCancellationRefusals != want.QueueCancellationRefusals {
		t.Fatalf("refusal reasons do not partition rejected requests: got%+v want%+v", got, want)
	}
}

func TestAdmissionRefusalReasonsDistinguishBoundsAndCancellation(t *testing.T) {
	a, l, handler := occupiedAdmission(t, laneSpec{capacity: 1, queueCapacity: 2, queuePerKey: 1, queueWait: time.Second})
	queue := func(peer string) (context.CancelFunc, <-chan struct{}) {
		ctx, cancel := context.WithCancel(context.Background())
		r := httptest.NewRequest("GET", "/v1/home", nil).WithContext(ctx)
		r.RemoteAddr = peer
		done := make(chan struct{})
		go func() {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 503 {
				t.Errorf("cancelled queued status%d", w.Code)
			}
			close(done)
		}()
		return cancel, done
	}
	cancelOne, one := queue("192.0.2.1:1")
	defer cancelOne()
	waitForQueueCount(t, l, 1)
	refuse := func(peer string) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/v1/home", nil)
		r.RemoteAddr = peer
		handler.ServeHTTP(w, r)
		if w.Code != 503 || w.Header().Get("Retry-After") != "1" {
			t.Fatal("refusal contract changed")
		}
	}
	refuse("192.0.2.1:2")
	cancelTwo, two := queue("192.0.2.2:1")
	defer cancelTwo()
	waitForQueueCount(t, l, 2)
	refuse("192.0.2.3:1")
	cancelOne()
	cancelTwo()
	for _, done := range []<-chan struct{}{one, two} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("cancelled queue failed to exit")
		}
	}
	assertAdmissionRefusalReasons(t, a, LaneDiagnostics{ClientQueueFullRefusals: 1, QueueFullRefusals: 1, QueueCancellationRefusals: 2})
	waitForQueueCount(t, l, 0)
}

func TestAdmissionRefusalReasonsDistinguishQueueAndRequestDeadlines(t *testing.T) {
	for _, requestDeadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "queue-deadline", true: "request-deadline"}[requestDeadline], func(t *testing.T) {
			wait := 10 * time.Millisecond
			if requestDeadline {
				wait = time.Second
			}
			a, l, handler := occupiedAdmission(t, laneSpec{capacity: 1, queueWait: wait})
			r := httptest.NewRequest("GET", "/v1/home", nil)
			if requestDeadline {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
				defer cancel()
				r = r.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 503 {
				t.Fatalf("deadline status%d", w.Code)
			}
			want := LaneDiagnostics{QueueTimeoutRefusals: 1}
			if requestDeadline {
				want = LaneDiagnostics{QueueCancellationRefusals: 1}
			}
			assertAdmissionRefusalReasons(t, a, want)
			waitForQueueCount(t, l, 0)
		})
	}
}

func TestAdmissionRefusalReasonsForLanesWithoutAQueue(t *testing.T) {
	a, _, handler := occupiedAdmission(t, laneSpec{capacity: 1})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/v1/home", nil))
	if w.Code != 503 {
		t.Fatalf("refusal status%d", w.Code)
	}
	assertAdmissionRefusalReasons(t, a, LaneDiagnostics{NoQueueRefusals: 1})
}
