package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// measureCounter is the regression seam for playback-004. It counts the
// directories a sweep actually walks, so a duplicated measurement shows up as
// a number rather than as an inference. Chosen directories can be made to fail,
// which is how the "never reuse a failed count" rule is checked.
type measureCounter struct {
	mu    sync.Mutex
	calls map[string]int
	fail  map[string]error
}

func newMeasureCounter() *measureCounter {
	return &measureCounter{calls: map[string]int{}, fail: map[string]error{}}
}

func (m *measureCounter) measure(dir string) (int64, int, error) {
	m.mu.Lock()
	m.calls[dir]++
	failure := m.fail[dir]
	m.mu.Unlock()
	if failure != nil {
		return 0, 0, failure
	}
	return measureDirectory(dir)
}

func (m *measureCounter) count(dir string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[dir]
}

func newSweepFixture(root string) *HLS {
	return &HLS{
		root:      root,
		active:    map[string]context.CancelFunc{},
		windows:   map[string]*hlsWindow{},
		reclaimed: map[string]int{},
		restarts:  map[string]*restartBudget{},
		timelines: map[string]*copyTimeline{},
		usage:     map[string]*hlsUsage{},
	}
}

// writeSessionOutput writes a deterministic handful of small files, not the
// hundreds the lock-contention test uses.
func writeSessionOutput(t *testing.T, root, id string, files int) int64 {
	t.Helper()
	directory := filepath.Join(root, id)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("segment-bytes")
	for index := 0; index < files; index++ {
		if err := os.WriteFile(filepath.Join(directory, hlsSegmentFile(index)), payload, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return int64(files * len(payload))
}

// A live session measured by the per-session ceiling must not be walked again
// by the global budget in the same tick.
func TestSweepMeasuresEachFreshLiveSessionOnce(t *testing.T) {
	root := t.TempDir()
	h := newSweepFixture(root)
	for _, id := range []string{"live-1", "live-2"} {
		writeSessionOutput(t, root, id, 2)
		h.active[id] = func() {}
	}
	counter := newMeasureCounter()
	h.measure = counter.measure

	h.sweep(context.Background())

	for _, id := range []string{"live-1", "live-2"} {
		if got := counter.count(filepath.Join(root, id)); got != 1 {
			t.Fatalf("%s was measured %d times in one sweep, want 1", id, got)
		}
	}
}

// The budget still measures what the per-session pass did not: retained
// directories and anything that appeared between phases. The next due pass
// measures each of them once, and no pass walks a fresh live directory twice.
func TestSweepStillMeasuresRetainedDirectoriesAndTheNextDuePass(t *testing.T) {
	root := t.TempDir()
	h := newSweepFixture(root)
	liveBytes := writeSessionOutput(t, root, "live-1", 2)
	retainedBytes := writeSessionOutput(t, root, "retained-1", 1)
	h.active["live-1"] = func() {}
	counter := newMeasureCounter()
	h.measure = counter.measure

	h.sweep(context.Background())

	if got := counter.count(filepath.Join(root, "live-1")); got != 1 {
		t.Fatalf("live session measured %d times, want 1", got)
	}
	if got := counter.count(filepath.Join(root, "retained-1")); got != 1 {
		t.Fatalf("retained directory measured %d times, want 1", got)
	}
	h.mu.Lock()
	liveUsage := h.usage["live-1"]
	retained := h.usage["retained-1"]
	h.mu.Unlock()
	if liveUsage == nil || liveUsage.bytes != liveBytes {
		t.Fatalf("live usage was not recorded from the same measurement: %+v", liveUsage)
	}
	if retained == nil || retained.bytes != retainedBytes {
		t.Fatalf("retained directory was not accounted: %+v", retained)
	}

	// A tick inside the interval does no filesystem work at all.
	h.sweep(context.Background())
	if got := counter.count(filepath.Join(root, "live-1")); got != 1 {
		t.Fatalf("a tick inside the interval re-measured the live session: %d", got)
	}
	if got := counter.count(filepath.Join(root, "retained-1")); got != 1 {
		t.Fatalf("a tick inside the interval re-measured the retained directory: %d", got)
	}

	// Age the interval so both passes are due again.
	h.mu.Lock()
	h.budgetSwept = time.Now().Add(-2 * hlsUsageInterval)
	if usage := h.usage["live-1"]; usage != nil {
		usage.measured = time.Now().Add(-2 * hlsUsageInterval)
	}
	h.mu.Unlock()
	h.sweep(context.Background())

	if got := counter.count(filepath.Join(root, "live-1")); got != 2 {
		t.Fatalf("live session measured %d times over two due passes, want 2", got)
	}
	if got := counter.count(filepath.Join(root, "retained-1")); got != 2 {
		t.Fatalf("retained directory measured %d times over two due passes, want 2", got)
	}
}

// A failed per-session measurement is not a zero. The budget must take its own
// reading rather than account the directory as empty, and nothing is recorded.
func TestSweepDoesNotReuseAFailedMeasurement(t *testing.T) {
	root := t.TempDir()
	h := newSweepFixture(root)
	writeSessionOutput(t, root, "live-1", 1)
	cancelled := false
	h.active["live-1"] = func() { cancelled = true }
	directory := filepath.Join(root, "live-1")
	counter := newMeasureCounter()
	counter.fail[directory] = errors.New("unreadable conversion output")
	h.measure = counter.measure

	h.sweep(context.Background())

	if !cancelled {
		t.Fatal("a live session whose output cannot be measured was not stopped")
	}
	if got := counter.count(directory); got != 2 {
		t.Fatalf("a failed measurement was reused: measured %d times, want 2", got)
	}
	h.mu.Lock()
	_, recorded := h.usage["live-1"]
	h.mu.Unlock()
	if recorded {
		t.Fatal("a failed measurement was recorded as usage")
	}
}

// The budget's own decisions are unchanged: an over-budget finished session is
// evicted, while a live producer and a recently served session keep their bytes,
// and per-session maps whose directories are gone retire.
func TestSweepEvictsOverBudgetFinishedSessionAndRetiresMaps(t *testing.T) {
	root := t.TempDir()
	h := newSweepFixture(root)
	previous := hlsGlobalByteBudgetForTest(0)
	defer hlsGlobalByteBudgetForTest(previous)
	writeSessionOutput(t, root, "active-1", 1)
	writeSessionOutput(t, root, "recent-1", 1)
	writeSessionOutput(t, root, "finished-1", 1)
	h.active["active-1"] = func() {}
	h.noteSessionServed("recent-1")
	h.mu.Lock()
	h.usage["ghost"] = &hlsUsage{bytes: 1, files: 1}
	h.timelines["ghost"] = &copyTimeline{}
	h.reclaimed["ghost"] = 1
	h.restarts["ghost"] = &restartBudget{}
	h.mu.Unlock()
	counter := newMeasureCounter()
	h.measure = counter.measure

	h.sweep(context.Background())

	for _, id := range []string{"active-1", "recent-1"} {
		if _, err := os.Stat(filepath.Join(root, id)); err != nil {
			t.Fatalf("the budget evicted %s, which is live or recently served: %v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "finished-1")); !os.IsNotExist(err) {
		t.Fatalf("the over-budget finished session survived: %v", err)
	}
	if got := counter.count(filepath.Join(root, "active-1")); got != 1 {
		t.Fatalf("live session measured %d times, want 1", got)
	}
	if got := counter.count(filepath.Join(root, "finished-1")); got != 1 {
		t.Fatalf("finished session measured %d times, want 1", got)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.usage["ghost"]; ok {
		t.Fatal("usage entry for a directory that is gone survived the sweep")
	}
	if _, ok := h.timelines["ghost"]; ok {
		t.Fatal("timeline for a directory that is gone survived the sweep")
	}
	if _, ok := h.reclaimed["ghost"]; ok {
		t.Fatal("reclaim floor for a directory that is gone survived the sweep")
	}
	if _, ok := h.restarts["ghost"]; ok {
		t.Fatal("restart budget for a directory that is gone survived the sweep")
	}
}
