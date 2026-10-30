package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestAdmissionQueueBoundsBeforeRecordingInterest(t *testing.T) {
	l := newLane("test", laneSpec{capacity: 2, queueWait: time.Second})
	for i := 0; i < 2; i++ {
		if admitted, queued := l.begin("busy"); !admitted || queued {
			t.Fatal("uncontended capacity was restricted")
		}
	}
	for i := 0; i < 2; i++ {
		if admitted, queued := l.begin("busy"); admitted || !queued {
			t.Fatal("client could not reserve its queue")
		}
	}
	if admitted, queued := l.begin("busy"); admitted || queued {
		t.Fatal("client exceeded its queue bound")
	}
	for i := 0; i < 2; i++ {
		if admitted, queued := l.begin("neighbor"); admitted || !queued {
			t.Fatal("one client consumed another client's queue room")
		}
	}
	for i := 0; i < 1000; i++ {
		if admitted, queued := l.begin(fmt.Sprintf("forged-%d", i)); admitted || queued {
			t.Fatal("global queue exceeded its bound")
		}
	}
	if l.waiters != 4 || len(l.waiting) != 2 || len(l.interest) != 2 {
		t.Fatalf("unbounded accounting: waiters=%d waiting=%d interest=%d", l.waiters, len(l.waiting), len(l.interest))
	}
	for _, key := range []string{"busy", "busy", "neighbor", "neighbor"} {
		l.leaveQueue(key)
		l.leave(key)
	}
	for i := 0; i < 2; i++ {
		l.release("busy")
		l.leave("busy")
	}
	if l.waiters != 0 || len(l.waiting) != 0 || len(l.interest) != 0 || len(l.held) != 0 {
		t.Fatal("queue accounting leaked after release")
	}
	if admitted, _ := l.begin("recovered"); !admitted {
		t.Fatal("lane did not recover")
	}
	l.release("recovered")
	l.leave("recovered")
}

func waitForQueueCount(t *testing.T, l *lane, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		l.mu.Lock()
		count := l.waiters
		l.mu.Unlock()
		if count == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue count %d, wanted %d", count, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAdmissionQueueCancellationAndStormRecovery(t *testing.T) {
	a := newAdmission()
	l := newLane(laneBrowsing, laneSpec{capacity: 1, queueCapacity: 2, queuePerKey: 1, queueWait: time.Second, budget: time.Second, class: dbwork.ClassInteractive})
	a.lanes[laneBrowsing] = l
	mux := http.NewServeMux()
	entered, release := make(chan struct{}), make(chan struct{})
	var first sync.Once
	mux.HandleFunc("GET /v1/home", func(w http.ResponseWriter, r *http.Request) {
		first.Do(func() { close(entered); <-release })
		w.WriteHeader(200)
	})
	handler := a.wrap(mux, mux)
	activeDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/home", nil))
		close(activeDone)
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	queued := httptest.NewRequest("GET", "/v1/home", nil).WithContext(ctx)
	queued.Header.Set("Authorization", "Bearer waiter")
	queuedDone := make(chan struct{})
	go func() { handler.ServeHTTP(httptest.NewRecorder(), queued); close(queuedDone) }()
	waitForQueueCount(t, l, 1)
	var storm sync.WaitGroup
	for i := 0; i < 32; i++ {
		storm.Add(1)
		go func() {
			defer storm.Done()
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, queued.Clone(context.Background()))
			if w.Code != 503 || w.Header().Get("Retry-After") != "1" {
				t.Errorf("overload answer %d %q", w.Code, w.Header().Get("Retry-After"))
			}
		}()
	}
	storm.Wait()
	for _, d := range a.diagnostics() {
		if d.Lane == laneBrowsing && (d.Waiting != 1 || d.QueueCapacity != 2 || d.PerClientQueueCapacity != 1) {
			t.Errorf("queue diagnostics: %+v", d)
		}
	}
	cancel()
	select {
	case <-queuedDone:
	case <-time.After(time.Second):
		t.Fatal("cancelled queue did not exit")
	}
	waitForQueueCount(t, l, 0)
	close(release)
	<-activeDone
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/v1/home", nil))
	if w.Code != 200 {
		t.Fatalf("recovery returned %d", w.Code)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.interest) != 0 || len(l.waiting) != 0 || len(l.held) != 0 || l.waiters != 0 || l.active.Load() != 0 || l.imbalance.Load() != 0 {
		t.Fatal("admission accounting did not recover")
	}
}
