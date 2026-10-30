package readprep

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestReuseHandleKeepsPrivateIdentityAndPollDeadline(t *testing.T) {
	var instant atomic.Int64
	instant.Store(time.Now().UnixNano())
	s := storeFor(t, Config{Now: func() time.Time { return time.Unix(0, instant.Load()) }})
	p := simplePlan("ready")
	p.ColdSearch, p.ReuseHandle = true, true
	p.ShouldYield = func() bool { return true }
	h := submit(t, s, "caller", coldKey("retry"), p)
	for i := 0; i < 40; i++ {
		next := submit(t, s, "caller", coldKey("retry"), p)
		if next != h {
			t.Fatal("same private command gained another handle")
		}
	}
	other := submit(t, s, "different", coldKey("retry"), p)
	if other == h || s.Stats().Handles != 2 {
		t.Fatal("private caller identity collapsed")
	}
	instant.Add(int64(29 * time.Second))
	if next := submit(t, s, "caller", coldKey("retry"), p); next != h {
		t.Fatal("premature expiry")
	}
	instant.Add(int64(2 * time.Second))
	if _, err := s.Poll(context.Background(), "caller", h); !errors.Is(err, ErrStale) {
		t.Fatalf("Submit renewed abandonment: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for s.Stats().Running != 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if stats := s.Stats(); stats.Entries != 0 || stats.Handles != 0 || stats.Bytes != 0 {
		t.Fatalf("abandoned demand retained %+v", stats)
	}
}
