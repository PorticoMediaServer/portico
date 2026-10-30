package storage

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"
)

// Fifty viewers pressing play in the same second are a burst, not an overload.
// The old supervisor answered the fifth one `busy` immediately, with no queue
// and no retry budget, and the client turned that into a visible failure.
func TestPlaybackAdmissionQueuesRatherThanRefusingABurst(t *testing.T) {
	s := &Supervisor{Limit: 2}
	release := make(chan struct{})
	var held sync.WaitGroup
	held.Add(2)
	for index := 0; index < 2; index++ {
		key := "playback:reader:" + string(rune('a'+index))
		go func() {
			_ = s.Run(context.Background(), key, exec.Command("sh", "-c", "read line"), func(r io.Reader) error {
				held.Done()
				<-release
				return nil
			})
		}()
	}
	held.Wait()
	// A third playback admission waits for a slot instead of failing.
	admitted := make(chan error, 1)
	go func() {
		admitted <- s.Run(context.Background(), "playback:reader:c", exec.Command("sh", "-c", "exit 0"), func(io.Reader) error { return nil })
	}()
	select {
	case err := <-admitted:
		t.Fatalf("a playback admission was refused immediately: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-admitted:
		if err != nil {
			t.Fatalf("the queued admission failed once a slot freed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the queued admission never ran")
	}
}

// Past the wait the answer is the same honest ErrBusy it always was, so nothing
// a client sees has changed.
func TestPlaybackAdmissionStillRefusesPastTheWait(t *testing.T) {
	previous := PlaybackAdmissionWait
	PlaybackAdmissionWait = 150 * time.Millisecond
	defer func() { PlaybackAdmissionWait = previous }()
	s := &Supervisor{Limit: 1}
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{})
	go func() {
		_ = s.Run(context.Background(), "playback:reader:held", exec.Command("sh", "-c", "read line"), func(io.Reader) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	begin := time.Now()
	err := s.Run(context.Background(), "playback:reader:next", exec.Command("sh", "-c", "exit 0"), func(io.Reader) error { return nil })
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("an over-capacity playback admission answered %v", err)
	}
	if time.Since(begin) < PlaybackAdmissionWait {
		t.Fatalf("it was refused after %s without waiting its budget", time.Since(begin))
	}
}

// Background work retains its place rather than reporting a failed probe when
// another child is using the one slot on a small server.
func TestBackgroundAdmissionWaitsForCapacity(t *testing.T) {
	s := &Supervisor{Limit: 1}
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_ = s.Run(context.Background(), "optimization:one", exec.Command("sh", "-c", "read line"), func(io.Reader) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	done := make(chan error, 1)
	go func() {
		done <- s.Run(context.Background(), "optimization:two", exec.Command("sh", "-c", "exit 0"), func(io.Reader) error { return nil })
	}()
	select {
	case err := <-done:
		t.Fatalf("background admission was refused: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queued background child did not start")
	}
}

func TestScanProbeHasAReservedLaneBesideLongBackgroundJobs(t *testing.T) {
	s := &Supervisor{Limit: 1}
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_ = s.Run(context.Background(), "optimization:held", exec.Command("sh", "-c", "read line"), func(io.Reader) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Run(ctx, "scan:new-file", exec.Command("sh", "-c", "exit 0"), func(io.Reader) error { return nil }); err != nil {
		t.Fatalf("short scan probe starved behind long job: %v", err)
	}
}

func TestBackgroundQueueIsFIFOAndCancellationReleasesPlace(t *testing.T) {
	s := &Supervisor{Limit: 1}
	p, err := s.admit(context.Background(), "optimization:held", true)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		key string
		err error
	}
	order := make(chan result, 3)
	first, cancelFirst := context.WithCancel(context.Background())
	for index, entry := range []struct {
		key string
		ctx context.Context
	}{{"optimization:first", first}, {"optimization:second", context.Background()}, {"optimization:third", context.Background()}} {
		entry := entry
		go func() {
			pool, err := s.admit(entry.ctx, entry.key, true)
			order <- result{entry.key, err}
			if err == nil {
				s.release(entry.key, pool)
			}
		}()
		deadline := time.Now().Add(time.Second)
		for {
			s.mu.Lock()
			queued := len(s.waitQueues[poolBackground])
			s.mu.Unlock()
			if queued >= index+1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("background admission was not queued")
			}
			time.Sleep(time.Millisecond)
		}
	}
	cancelFirst()
	if got := <-order; got.key != "optimization:first" || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("cancelled head: %+v", got)
	}
	s.release("optimization:held", p)
	for _, want := range []string{"optimization:second", "optimization:third"} {
		if got := <-order; got.key != want || got.err != nil {
			t.Fatalf("queue order: %+v, want %s", got, want)
		}
	}
}

// Background, foreground helpers and playback have independent capacities.
func TestAScanCannotExhaustThePlaybackPool(t *testing.T) {
	s := &Supervisor{}
	previousBackground := DefaultBackgroundLimit
	DefaultBackgroundLimit = 1
	defer func() { DefaultBackgroundLimit = previousBackground }()
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{})
	go func() {
		_ = s.Run(context.Background(), "scan:library", exec.Command("sh", "-c", "read line"), func(io.Reader) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	if err := s.Run(context.Background(), "playback:descriptor:1", exec.Command("sh", "-c", "exit 0"), func(io.Reader) error { return nil }); err != nil {
		t.Fatalf("a saturated background pool refused playback: %v", err)
	}
	if err := s.Run(context.Background(), "foreground:probe", exec.Command("sh", "-c", "exit 0"), func(io.Reader) error { return nil }); err != nil {
		t.Fatalf("a saturated background pool refused a foreground helper: %v", err)
	}
	stats := s.Stats()
	if stats.BackgroundCapacity != 1 || stats.ScanProbeCapacity != 1 || stats.HelperCapacity != DefaultHelperLimit || stats.PlaybackCapacity != DefaultPlaybackLimit {
		t.Fatalf("the pools share a capacity: %#v", stats)
	}
}

func TestOwnerBackgroundPriorityChangesCapacityWithoutStoppingActiveWork(t *testing.T) {
	s := &Supervisor{}
	if got := s.BackgroundTaskPriority(); got != "lower" {
		t.Fatal(got)
	}
	if err := s.SetBackgroundTaskPriority("normal"); err != nil {
		t.Fatal(err)
	}
	if stats := s.Stats(); stats.BackgroundCapacity != max(1, runtime.NumCPU()) || stats.ScanProbeCapacity != max(1, runtime.NumCPU()) {
		t.Fatalf("normal background capacities %#v", stats)
	}
	if err := s.SetBackgroundTaskPriority("lower"); err != nil {
		t.Fatal(err)
	}
	if stats := s.Stats(); stats.BackgroundCapacity != max(1, DefaultBackgroundLimit) || stats.ScanProbeCapacity != max(1, DefaultBackgroundLimit) {
		t.Fatalf("lower background capacities %#v", stats)
	}
	if err := s.SetBackgroundTaskPriority("paused"); err == nil || s.BackgroundTaskPriority() != "lower" {
		t.Fatal("invalid owner setting changed effective priority")
	}
}

func TestScanProbeLaneUsesCPUScaledCapacity(t *testing.T) {
	previous := DefaultBackgroundLimit
	DefaultBackgroundLimit = 2
	defer func() { DefaultBackgroundLimit = previous }()
	s := &Supervisor{}
	if got := s.Stats().ScanProbeCapacity; got != 2 {
		t.Fatalf("scan probe capacity %d", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	done := make(chan error, 2)
	for _, key := range []string{"scan:probe:one", "scan:probe:two"} {
		go func(key string) {
			done <- s.Run(ctx, key, exec.Command("sh", "-c", "sleep 0.1"), func(io.Reader) error {
				started <- struct{}{}
				<-release
				return nil
			})
		}(key)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("second scan probe was serialized")
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

// Duplicate-key fencing is a correctness rule, not a capacity one, so it still
// answers at once rather than queueing.
func TestADuplicateKeyIsStillRefusedImmediately(t *testing.T) {
	s := &Supervisor{}
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{})
	go func() {
		_ = s.Run(context.Background(), "playback:reader:same", exec.Command("sh", "-c", "read line"), func(io.Reader) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	begin := time.Now()
	if err := s.Run(context.Background(), "playback:reader:same", exec.Command("sh", "-c", "exit 0"), func(io.Reader) error { return nil }); !errors.Is(err, ErrBusy) {
		t.Fatalf("a duplicate key answered %v", err)
	}
	if time.Since(begin) > time.Second {
		t.Fatalf("a duplicate key waited %s", time.Since(begin))
	}
}
