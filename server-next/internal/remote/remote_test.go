package remote

import (
	"context"
	"testing"
	"time"
)

// A source told to slow down waits as long as it was asked and then carries on, and requests
// are spaced by the source's rate.
func TestThrottleWaitsWhenToldAndKeepsTheRate(t *testing.T) {
	throttle := NewThrottle(Limits{Connections: 2, PerSecond: 100})
	start, calls := time.Now(), 0
	err := throttle.Do(context.Background(), func() error {
		if calls++; calls == 1 {
			return &SlowDown{After: 80 * time.Millisecond}
		}
		return nil
	})
	if err != nil || calls != 2 || time.Since(start) < 80*time.Millisecond {
		t.Fatalf("after a slow-down: %v, %d calls in %s", err, calls, time.Since(start))
	}
	start = time.Now()
	for range 6 {
		if err := throttle.Do(context.Background(), func() error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if took := time.Since(start); took < 40*time.Millisecond {
		t.Fatalf("six requests at 100 a second took %s", took)
	}

	// A wait ends with the work that was waiting.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := throttle.Do(ctx, func() error { return &SlowDown{After: time.Hour} }); err != context.DeadlineExceeded {
		t.Fatalf("a cancelled wait: %v", err)
	}
	if _, waiting := throttle.Waiting(); !waiting {
		t.Fatal("the source should say it is waiting")
	}
}

func TestDue(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		last  time.Time
		every time.Duration
		want  bool
	}{{time.Time{}, time.Hour, true}, {now.Add(-2 * time.Hour), time.Hour, true}, {now.Add(-time.Minute), time.Hour, false}, {time.Time{}, 0, false}} {
		if got := Due(c.last, c.every, now); got != c.want {
			t.Errorf("Due(%v, %v) = %v", c.last, c.every, got)
		}
	}
}
