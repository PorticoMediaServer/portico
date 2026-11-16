package processpool

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCancellationKillsOwnedChildAndAdmissionRemainsBounded(t *testing.T) {
	s := &Supervisor{Limit: 1}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, release, retired := make(chan struct{}), make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	result := make(chan error, 1)
	go func() {
		result <- s.RunOwnedCompletion(ctx, "root", exec.Command("sh", "-c", "sleep 30"), func(r io.Reader) error {
			close(started)
			<-release
			_, err := io.Copy(io.Discard, r)
			return err
		}, nil, func() { close(retired) })
	}()
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("the owned child did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("cancellation waited for the held consumer")
	}
	unblock()
	select {
	case <-retired:
	case <-time.After(30 * time.Second):
		t.Fatal("the owned child was not reaped")
	}
	if s.Active() != 0 {
		t.Fatal("reaped child retained slot")
	}
	var e error
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
