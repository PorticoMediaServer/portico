package streaming

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// h.mu is taken by start, noteServed and produce's cleanup, which puts it on the
// path of every segment request. The old sweep held it across a ReadDir and a
// stat per file, per session, plus a pooled database query per session, every
// second. A segment request must be able to take the lock while disk work waits.
func TestTheSweepDoesNotHoldTheLockAcrossTheFilesystem(t *testing.T) {
	root := t.TempDir()
	const sessions, files = 2, 2
	h := &HLS{root: root, active: map[string]context.CancelFunc{}, windows: map[string]*hlsWindow{}, freed: map[string]int{}, usage: map[string]*hlsUsage{}}
	for index := range sessions {
		id := "session-" + strconv.Itoa(index)
		directory := filepath.Join(root, id)
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		for file := range files {
			if err := os.WriteFile(filepath.Join(directory, hlsSegmentFile(file)), []byte("segment"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		h.active[id] = func() {}
	}
	// Keep the filesystem blocked until a segment request takes the lock.
	// This checks independence from disk speed, including an unresponsive disk.
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	var once sync.Once
	h.measure = func(dir string) (int64, int, error) {
		once.Do(func() { close(entered); <-release })
		return measureDirectory(dir)
	}
	done := make(chan struct{})
	go func() { defer close(done); h.sweep(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the sweep did not reach the filesystem")
	}
	served := make(chan struct{})
	go func() { h.noteSessionServed("session-0"); close(served) }()
	select {
	case <-served:
	case <-time.After(30 * time.Second):
		t.Fatal("a segment request waited for the held filesystem measurement")
	}
	unblock()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the sweep did not finish after the filesystem answered")
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
	h := &HLS{root: root, active: map[string]context.CancelFunc{}, windows: map[string]*hlsWindow{}, freed: map[string]int{}, usage: map[string]*hlsUsage{}}
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
