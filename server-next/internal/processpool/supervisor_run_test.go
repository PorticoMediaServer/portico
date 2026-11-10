package processpool

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

func TestCancellationKillsOwnedChildAndAdmissionRemainsBounded(t *testing.T) {
	s := &Supervisor{Limit: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	e := s.Run(ctx, "root", exec.Command("sh", "-c", "sleep 30"), func(r io.Reader) error { _, e := io.Copy(io.Discard, r); return e })
	if !errors.Is(e, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("cancellation %v after %v", e, time.Since(started))
	}
	for n := 0; n < 100 && s.Active() != 0; n++ {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Active() != 0 {
		t.Fatal("reaped child retained slot")
	}
	s.mu.Lock()
	s.active["wedged-root"] = true
	s.counts[poolHelper] = 1
	s.mu.Unlock()
	e = s.Run(context.Background(), "another-root", exec.Command("sh", "-c", "exit 0"), func(io.Reader) error { return nil })
	if !errors.Is(e, ErrBusy) {
		t.Fatal("quarantine did not cap new admissions", e)
	}
}

func TestObservedSupervisorOwnedNoStart(t *testing.T) {
	for _, cancelFirst := range []bool{false, true} {
		s := &Supervisor{}
		ctx, cancel := context.WithCancel(context.Background())
		if cancelFirst {
			cancel()
		}
		var calls atomic.Int32
		err := s.RunOwned(ctx, "playback:no-start", exec.Command("/portico-no-such-owned-helper"), func(io.Reader) error { t.Error("failed start consumed output"); return nil }, func() { calls.Add(1) })
		cancel()
		if err == nil || calls.Load() != 1 || s.Active() != 0 {
			t.Fatal("no-start cleanup not exact")
		}
	}
}
