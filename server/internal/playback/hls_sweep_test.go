package playback

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// h.mu is taken by start, noteServed and produce's cleanup, which puts it on the
// path of every segment request. The old sweep held it across a ReadDir and a
// stat per file, per session, plus a pooled database query per session, every
// second. This measures the thing that matters: how long a segment request would
// have to wait for the lock while a sweep runs.
func TestTheSweepDoesNotHoldTheLockAcrossTheFilesystem(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	root := t.TempDir()
	const sessions, files = 60, 400
	h := &HLS{root: root, active: map[string]context.CancelFunc{}, windows: map[string]*hlsWindow{}, reclaimed: map[string]int{}, usage: map[string]*hlsUsage{}}
	payload := make([]byte, 4096)
	for index := 0; index < sessions; index++ {
		id := "session-" + strconv.Itoa(index)
		directory := filepath.Join(root, id)
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		for file := 0; file < files; file++ {
			if err := os.WriteFile(filepath.Join(directory, hlsSegmentFile(file)), payload, 0600); err != nil {
				t.Fatal(err)
			}
		}
		h.active[id] = func() {}
	}

	// A stand-in for a segment request: take the lock, let go, and remember the
	// worst wait.
	var worst atomic.Int64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			start := time.Now()
			h.mu.Lock()
			elapsed := time.Since(start)
			h.mu.Unlock()
			for {
				current := worst.Load()
				if int64(elapsed) <= current || worst.CompareAndSwap(current, int64(elapsed)) {
					break
				}
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// h.db is nil, so the batched state query declines and the sweep does the
	// filesystem work, which is the part under test.
	h.sweep(context.Background())
	close(stop)
	<-done

	longest := time.Duration(worst.Load())
	t.Logf("%d sessions x %d files: longest wait for the HLS lock during a sweep was %s", sessions, files, longest)
	if longest > 50*time.Millisecond {
		t.Fatalf("a segment request waited %s for the HLS lock during a sweep; the filesystem work is inside the lock", longest)
	}
	// The sweep measured what it walked, which is what makes the next tick cheap.
	h.mu.Lock()
	measured := len(h.usage)
	h.mu.Unlock()
	if measured != sessions {
		t.Fatalf("the sweep recorded %d of %d sessions", measured, sessions)
	}
}

// The eviction policy has one rule it must never break: it may not take bytes a
// viewer might seek back to.
func TestTheGlobalBudgetNeverEvictsWhatALiveViewerMaySeekBackTo(t *testing.T) {
	root := t.TempDir()
	h := &HLS{root: root, active: map[string]context.CancelFunc{}, windows: map[string]*hlsWindow{}, reclaimed: map[string]int{}, usage: map[string]*hlsUsage{}}
	previous := hlsGlobalByteBudgetForTest(0)
	defer hlsGlobalByteBudgetForTest(previous)

	write := func(id string) {
		directory := filepath.Join(root, id)
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, hlsSegmentFile(0)), make([]byte, 8192), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("watching-now")
	write("watched-long-ago")
	write("never-watched")
	// A live producer owns its output whatever the budget says.
	write("still-producing")
	h.active["still-producing"] = func() {}

	h.noteSessionServed("watching-now")
	h.mu.Lock()
	h.usage["watched-long-ago"] = &hlsUsage{served: time.Now().Add(-2 * time.Hour)}
	h.mu.Unlock()

	h.enforceGlobalBudget(context.Background())

	for _, id := range []string{"watching-now", "still-producing"} {
		if _, err := os.Stat(filepath.Join(root, id)); err != nil {
			t.Fatalf("the budget evicted %s, which a viewer may still be reading: %v", id, err)
		}
	}
	for _, id := range []string{"watched-long-ago", "never-watched"} {
		if _, err := os.Stat(filepath.Join(root, id)); err == nil {
			t.Fatalf("the budget was over and %s survived", id)
		}
	}
}

// A volume with no room must refuse a producer rather than let the database be
// the one to discover it.
func TestAProducerWillNotStartWithoutRoom(t *testing.T) {
	h := &HLS{root: t.TempDir()}
	if !h.roomToProduce() {
		t.Skip("this volume is already below the free-space floor")
	}
	previous := hlsFreeSpaceFloorForTest(1 << 62)
	defer hlsFreeSpaceFloorForTest(previous)
	if h.roomToProduce() {
		t.Fatal("a producer was cleared to start on a volume below the floor")
	}
}

// Load and CPU count never impose an implicit admission limit.
func TestConversionDiagnosticsExcludeRemuxAndAudio(t *testing.T) {
	h := &HLS{active: map[string]context.CancelFunc{}, windows: map[string]*hlsWindow{}}
	for _, id := range []string{"remux1", "remux2", "audio", "video"} {
		h.active[id] = func() {}
		h.windows[id] = &hlsWindow{videoConversion: id == "video"}
	}
	if report := h.Conversions(); report.Active != 1 || report.Capacity != 0 {
		t.Fatal(report)
	}
}
