package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A viewer whose network drops leaves a converter running for somebody who is
// not there. Reclaiming it must free the processor without ending the session
// or discarding what has already been converted.
func TestAConversionNobodyIsFetchingFromIsReclaimed(t *testing.T) {
	restore := hlsAbandonedAfterForTest(50 * time.Millisecond)
	defer hlsAbandonedAfterForTest(restore)
	h := &HLS{active: map[string]context.CancelFunc{}, windows: map[string]*hlsWindow{}, usage: map[string]*hlsUsage{}}
	var cancelled atomic.Bool
	h.active["gone"] = func() { cancelled.Store(true) }
	h.windows["gone"] = &hlsWindow{}
	h.usage["gone"] = &hlsUsage{served: time.Now().Add(-time.Second)}
	// A session being fetched from right now is untouched.
	var live atomic.Bool
	h.active["watching"] = func() { live.Store(true) }
	h.windows["watching"] = &hlsWindow{}
	h.usage["watching"] = &hlsUsage{served: time.Now()}
	h.reclaimAbandoned()
	if !cancelled.Load() {
		t.Fatal("a conversion nobody had fetched from for a full threshold was left running")
	}
	if live.Load() {
		t.Fatal("a conversion someone is watching was reclaimed")
	}
	if n := h.abandoned.Load(); n != 1 {
		t.Fatalf("%d reclaims counted", n)
	}
}

// A planned audio session produces everything in one run with no segment
// requests in between, so silence there means working, not gone.
func TestAPlannedAudioConversionIsNeverReclaimedForSilence(t *testing.T) {
	restore := hlsAbandonedAfterForTest(time.Millisecond)
	defer hlsAbandonedAfterForTest(restore)
	h := &HLS{active: map[string]context.CancelFunc{}, windows: map[string]*hlsWindow{}, usage: map[string]*hlsUsage{}}
	var cancelled atomic.Bool
	h.active["audio"] = func() { cancelled.Store(true) }
	h.usage["audio"] = &hlsUsage{served: time.Now().Add(-time.Hour)}
	h.reclaimAbandoned()
	if cancelled.Load() {
		t.Fatal("a planned audio conversion was reclaimed for not serving segments it never serves")
	}
}

// Scrubbing is a person dragging a bar; a reconnect loop is a client asking
// hundreds of times a second. The budget has to let the first through and stop
// the second.
func TestProducerRestartsAreBudgetedPerSession(t *testing.T) {
	h := &HLS{}
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := 0; i < hlsRestartBudget; i++ {
		if !h.restartAllowedLocked("one") {
			t.Fatalf("restart %d was refused inside the budget", i+1)
		}
	}
	if h.restartAllowedLocked("one") {
		t.Fatal("the budget did not apply")
	}
	// Another session has its own budget: one client cannot spend another's.
	if !h.restartAllowedLocked("two") {
		t.Fatal("a second session was refused because the first had spent its budget")
	}
	// The window refills.
	h.restarts["one"].window = time.Now().Add(-hlsRestartWindow - time.Second)
	if !h.restartAllowedLocked("one") {
		t.Fatal("the budget never refilled")
	}
}

// Ten clients reconnecting onto the same segment must cost one wait, not ten.
func TestManyRequestsForOneSegmentShareOneWait(t *testing.T) {
	h := &HLS{}
	root := t.TempDir()
	path := filepath.Join(root, "segment-000003.ts")
	release := make(chan struct{})
	var polls atomic.Int64
	poll := func(ctx context.Context) (string, error) {
		polls.Add(1)
		select {
		case <-release:
			return path, nil
		case <-ctx.Done():
			return "", ErrSegmentPreparing
		}
	}
	var callers sync.WaitGroup
	results := make(chan string, 10)
	for i := 0; i < 10; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			got, err := h.awaitSegment(context.Background(), "session", 3, poll)
			if err != nil {
				results <- "error: " + err.Error()
				return
			}
			results <- got
		}()
	}
	// Give the followers time to arrive behind the leader.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && h.waitsInFlight() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if n := h.waitsInFlight(); n != 1 {
		t.Fatalf("%d waits were open for one segment", n)
	}
	close(release)
	callers.Wait()
	close(results)
	for got := range results {
		if got != path {
			t.Fatalf("a caller got %q", got)
		}
	}
	if n := polls.Load(); n != 1 {
		t.Fatalf("%d poll loops ran for one segment", n)
	}
	if n := h.waitsInFlight(); n != 0 {
		t.Fatalf("%d waits were left behind", n)
	}
}

// A follower keeps its own deadline: a slow leader must not hold a client past
// the budget that client was admitted under.
func TestAFollowerKeepsItsOwnDeadline(t *testing.T) {
	h := &HLS{}
	leaderRunning := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	go func() {
		_, _ = h.awaitSegment(context.Background(), "session", 1, func(ctx context.Context) (string, error) {
			close(leaderRunning)
			<-release
			return "", nil
		})
	}()
	<-leaderRunning
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := h.awaitSegment(ctx, "session", 1, func(context.Context) (string, error) {
		t.Error("a follower ran its own poll")
		return "", nil
	})
	if !errors.Is(err, ErrSegmentPreparing) {
		t.Fatalf("a follower past its deadline got %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("a follower waited %s for a leader that had not finished", elapsed)
	}
}

func TestSegmentReadyRejectsAnEmptyFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "segment-000000.ts")
	if segmentReady(path) {
		t.Fatal("a missing segment reported ready")
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if segmentReady(path) {
		t.Fatal("an empty segment reported ready")
	}
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if !segmentReady(path) {
		t.Fatal("a written segment reported not ready")
	}
}
