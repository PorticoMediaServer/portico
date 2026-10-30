package httpapi

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/hostlimits"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestBrowsingQueueMemoryBudget(t *testing.T) {
	for _, tc := range []struct {
		memory int64
		want   int
	}{
		{-1, 48}, {0, 48}, {64 << 20, 48}, {256 << 20, 64},
		{512 << 20, 128}, {1 << 30, 256}, {2 << 30, 512},
		{32 << 30, 512}, {math.MaxInt64, 512},
	} {
		a := newAdmissionForMemory(tc.memory)
		l := a.lanes[laneBrowsing]
		if l.spec.queueCapacity != tc.want || l.spec.capacity != 24 || l.spec.queuePerKey != 24 || l.spec.queueWait != 3*time.Second || l.spec.budget != 5*time.Second {
			t.Fatalf("memory %d: policy %+v, want %d waiting places", tc.memory, l.spec, tc.want)
		}
		for name, spec := range laneSpecs {
			if name != laneBrowsing && a.lanes[name].spec != newLane(name, spec).spec {
				t.Fatalf("browsing policy changed %s", name)
			}
		}
	}
	// The production constructor must use the minimum physical/cgroup budget,
	// rather than a separate physical-memory probe that ignores containers.
	if got := newAdmission().lanes[laneBrowsing].spec.queueCapacity; got != browsingQueueCapacity(hostlimits.EffectiveMemoryBytes()) {
		t.Fatalf("production queue ignores effective host budget: %d", got)
	}
}

func TestBrowsingQueueServesTwoHundredDeviceBurst(t *testing.T) {
	a := newAdmissionForMemory(1 << 30)
	l := a.lanes[laneBrowsing]
	mux := http.NewServeMux()
	release := make(chan struct{})
	var closeOnce sync.Once
	defer closeOnce.Do(func() { close(release) })
	mux.HandleFunc("GET /v1/home", func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	})
	handler := a.wrap(mux, mux)
	const count = 200
	// Learn accounting keys as successful authentication would. All devices
	// share one NAT and account, so arbitrary peer variation cannot pass this.
	for i := 0; i < count; i++ {
		a.rememberCredential(fmt.Sprint("device-token-", i), identity.Principal{
			Viewer:   identity.Viewer{ServerID: "server", Authority: "local", AccountID: "household"},
			DeviceID: fmt.Sprint("device-", i),
		})
	}
	start := make(chan struct{})
	results := make(chan int, count)
	for i := 0; i < count; i++ {
		go func(i int) {
			<-start
			r := httptest.NewRequest("GET", "/v1/home", nil)
			r.RemoteAddr = "198.51.100.42:12345"
			r.Header.Set("Authorization", "Bearer "+fmt.Sprint("device-token-", i))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			results <- w.Code
		}(i)
	}
	close(start)
	waitForQueueCount(t, l, count-l.spec.capacity)
	if l.active.Load() != 24 {
		t.Fatalf("queue policy increased active work to %d", l.active.Load())
	}
	closeOnce.Do(func() { close(release) })
	for i := 0; i < count; i++ {
		select {
		case code := <-results:
			if code != http.StatusOK {
				t.Fatalf("normal burst returned %d", code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("normal burst did not drain")
		}
	}
	assertEmptyAdmissionLane(t, l)
	if l.rejected.Load() != 0 {
		t.Fatalf("normal burst refused %d requests", l.rejected.Load())
	}
}

func assertEmptyAdmissionLane(t *testing.T, l *lane) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.waiters != 0 || len(l.held) != 0 || len(l.waiting) != 0 || len(l.interest) != 0 || l.active.Load() != 0 || l.imbalance.Load() != 0 {
		t.Fatalf("lane accounting did not drain: waiters=%d active=%d", l.waiters, l.active.Load())
	}
}

func TestBrowsingQueueExpansionPreservesDeviceAndInventedPeerBounds(t *testing.T) {
	a := newAdmissionForMemory(32 << 30)
	l := a.lanes[laneBrowsing]
	// Occupy the unchanged active capacity with legitimate independent viewers.
	for i := 0; i < l.spec.capacity; i++ {
		if admitted, _ := l.begin(fmt.Sprint("active-", i)); !admitted {
			t.Fatal("active capacity restricted")
		}
	}
	principal := identity.Principal{Viewer: identity.Viewer{ServerID: "server", Authority: "local", AccountID: "account"}, DeviceID: "one-device"}
	for _, verified := range []bool{false, true} {
		var key string
		for i := 0; i < 200; i++ {
			token := fmt.Sprintf("token-%t-%d", verified, i)
			if verified {
				a.rememberCredential(token, principal)
			}
			r := httptest.NewRequest("GET", "/v1/home", nil)
			r.RemoteAddr = "198.51.100.42:12345"
			r.Header.Set("Authorization", "Bearer "+token)
			current := a.clientKey(r)
			if i == 0 {
				key = current
			} else if current != key {
				t.Fatal("token rotation minted queue shares")
			}
			admitted, queued := l.begin(current)
			if admitted || queued != (i < 24) {
				t.Fatalf("one client queue bound changed at %d: %t/%t", i, admitted, queued)
			}
		}
		for i := 0; i < 24; i++ {
			l.leaveQueue(key)
			l.leave(key)
		}
	}
	for i := 0; i < l.spec.capacity; i++ {
		key := fmt.Sprint("active-", i)
		l.release(key)
		l.leave(key)
	}
	assertEmptyAdmissionLane(t, l)
}

func TestBrowsingQueueRejectsOnlyBeyondHostWaitingBudget(t *testing.T) {
	for _, memory := range []int64{0, 256 << 20, 1 << 30, 32 << 30} {
		l := newAdmissionForMemory(memory).lanes[laneBrowsing]
		for i := 0; i < l.spec.capacity; i++ {
			if admitted, _ := l.begin(fmt.Sprint("active-", i)); !admitted {
				t.Fatal("active capacity restricted")
			}
		}
		for i := 0; i < l.spec.queueCapacity; i++ {
			if admitted, queued := l.begin(fmt.Sprint("waiter-", i)); admitted || !queued {
				t.Fatalf("memory%d refuses bounded waiter%d", memory, i)
			}
		}
		for i := 0; i < 1000; i++ {
			if admitted, queued := l.begin(fmt.Sprint("overflow-", i)); admitted || queued {
				t.Fatal("expanded queue exceeded host bound")
			}
		}
		if l.waiters != l.spec.queueCapacity || len(l.interest) != l.spec.capacity+l.spec.queueCapacity {
			t.Fatal("overflow retained accounting or queue grew")
		}
		for i := 0; i < l.spec.queueCapacity; i++ {
			key := fmt.Sprint("waiter-", i)
			l.leaveQueue(key)
			l.leave(key)
		}
		for i := 0; i < l.spec.capacity; i++ {
			key := fmt.Sprint("active-", i)
			l.release(key)
			l.leave(key)
		}
		assertEmptyAdmissionLane(t, l)
	}
}

func TestBrowsingQueueExpandedWaitHonorsCallerCancellation(t *testing.T) {
	a := newAdmissionForMemory(1 << 30)
	l := a.lanes[laneBrowsing]
	for i := 0; i < l.spec.capacity; i++ {
		if ok, _ := l.begin(fmt.Sprint("active-", i)); !ok {
			t.Fatal("active capacity restricted")
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/home", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	handler := a.wrap(mux, mux)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/v1/home", nil).WithContext(ctx))
		done <- w.Code
	}()
	waitForQueueCount(t, l, 1)
	cancel()
	select {
	case code := <-done:
		if code != 503 || l.queueCancelledRejected.Load() != 1 {
			t.Fatalf("cancel response %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("larger queue ignored caller cancellation")
	}
	for i := 0; i < l.spec.capacity; i++ {
		key := fmt.Sprint("active-", i)
		l.release(key)
		l.leave(key)
	}
	assertEmptyAdmissionLane(t, l)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/v1/home", nil))
	if w.Code != 200 {
		t.Fatalf("queue did not recover: %d", w.Code)
	}
}
