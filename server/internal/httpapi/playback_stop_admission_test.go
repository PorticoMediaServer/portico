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

func TestPlaybackStopStormHasItsOwnBoundedIngress(t *testing.T) {
	for _, n := range []int{200, 350} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			a := newAdmissionForMemory(8 << 30)
			var counts atomic.Int64
			a.livePlaybackSessions = func(context.Context) (int, error) { counts.Add(1); return n, nil }
			// Occupied revocation slots must not take capacity from Stops.
			fence := a.lanes[laneSecurityFence]
			for i := 0; i < fence.spec.capacity; i++ {
				if ok, _ := fence.begin("revocation"); !ok {
					t.Fatal("fence control")
				}
			}
			defer func() {
				for i := 0; i < fence.spec.capacity; i++ {
					fence.release("revocation")
					fence.leave("revocation")
				}
			}()
			release := make(chan struct{})
			var once sync.Once
			finish := func() { once.Do(func() { close(release) }) }
			defer finish()
			var badClass atomic.Int64
			mux := http.NewServeMux()
			mux.HandleFunc("DELETE /v1/playback/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
				if dbwork.ClassFrom(r.Context(), dbwork.ClassInteractive) != dbwork.ClassSecurityFence {
					badClass.Add(1)
				}
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > 10*time.Second {
					badClass.Add(1)
				}
				<-release
				w.WriteHeader(http.StatusNoContent)
			})
			gate := a.wrap(mux, mux)
			var wg sync.WaitGroup
			responses := make(chan int, n)
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					r := httptest.NewRequest("DELETE", fmt.Sprintf("/v1/playback/sessions/session-%d", i), nil)
					w := httptest.NewRecorder()
					gate.ServeHTTP(w, r)
					responses <- w.Code
				}(i)
			}
			stop := a.lanes[lanePlaybackStop]
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				stop.mu.Lock()
				waiting := stop.waiters
				stop.mu.Unlock()
				if stop.active.Load()+int64(waiting)+int64(stop.rejected.Load()) == int64(n) {
					break
				}
				time.Sleep(time.Millisecond)
			}
			stop.mu.Lock()
			waiting := stop.waiters
			bound := stop.queueCapacity
			stop.mu.Unlock()
			active := stop.active.Load()
			finish()
			wg.Wait()
			close(responses)
			for status := range responses {
				if status != 204 {
					t.Errorf("Stop status %d", status)
				}
			}
			if active != 16 || waiting != n-16 || stop.rejected.Load() != 0 || badClass.Load() != 0 {
				t.Fatalf("active=%d waiting=%d rejected=%d bad class/deadline=%d", active, waiting, stop.rejected.Load(), badClass.Load())
			}
			if bound != max(200, n) {
				t.Fatalf("queue bound%d", bound)
			}
			if n == 200 && counts.Load() != 0 {
				t.Fatalf("ordinary target storm queried sessions%d times", counts.Load())
			}
			if n > 200 && counts.Load() == 0 {
				t.Fatal("larger live population was not checked")
			}
			if stop.active.Load() != 0 || len(stop.tokens) != 16 || stop.imbalance.Load() != 0 {
				t.Fatal("Stop lane did not drain")
			}
			stop.mu.Lock()
			left := stop.waiters + len(stop.waiting) + len(stop.interest) + len(stop.held)
			stop.mu.Unlock()
			if left != 0 {
				t.Fatalf("Stop ownership retained %d", left)
			}
			if fence.active.Load() != 16 || fence.spec.queueCapacity != 32 {
				t.Fatal("revocation lane changed")
			}
			t.Logf("%d simultaneous Stops: %d active, %d queued, zero refusals; live count reads%d", n, active, waiting, counts.Load())
		})
	}
}
