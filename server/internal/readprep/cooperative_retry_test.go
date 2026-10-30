package readprep

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestCooperativeRetryDoesNotSpinOrOccupyWorker(t *testing.T) {
	var offset atomic.Int64
	now := time.Now()
	s := storeFor(t, Config{Workers: 1, Now: func() time.Time { return now.Add(time.Duration(offset.Load())) }})
	var calls atomic.Int64
	first := submit(t, s, "viewer", testKey, Plan{InitialBytes: 64, New: func(context.Context, *Budget) (Computation, error) {
		return &testWork{bytes: 64, step: func(context.Context, *Budget) (StepResult, error) {
			if calls.Add(1) == 1 {
				return StepResult{RetryAfter: time.Second}, nil
			}
			return StepResult{Done: true, Payload: []byte{1}}, nil
		}}, nil
	}})
	eventually(t, func() bool { return calls.Load() == 1 })
	key := testKey
	key.Canonical = "unrelated-ready"
	second := submit(t, s, "viewer", key, Plan{InitialBytes: 64, New: func(context.Context, *Budget) (Computation, error) {
		return &testWork{bytes: 64, step: func(context.Context, *Budget) (StepResult, error) {
			return StepResult{Done: true, Payload: []byte{2}}, nil
		}}, nil
	}})
	r := ready(t, s, "viewer", second)
	r.Release()
	if calls.Load() != 1 {
		t.Fatal("pending readiness spun despite unchanged clock", calls.Load())
	}
	pending, e := s.Poll(context.Background(), "viewer", first)
	if e != nil || pending.State != Preparing {
		t.Fatal(pending, e)
	}
	expiry := pending.OriginalExpiry
	pending.Release()
	offset.Store(int64(time.Second))
	s.wake()
	r = ready(t, s, "viewer", first)
	defer r.Release()
	if calls.Load() != 2 || !r.OriginalExpiry.Equal(expiry) {
		t.Fatal("retry extended lease or repeated work", calls.Load(), r.OriginalExpiry, expiry)
	}
}
