package playback

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"testing"
	"time"
)

func memoryFixture(t *testing.T) *HLS {
	t.Helper()
	h := newSweepFixture(t.TempDir())
	h.ctx = context.Background()
	root, err := os.OpenRoot(h.root)
	if err != nil {
		t.Fatal(err)
	}
	h.outputRoot = root
	h.memoryCheck = func(string) error { return nil }
	h.memoryFacts = func(string) (memoryFacts, error) {
		return memoryFacts{available: 8 << 30, total: 16 << 30, free: 512 << 20, filesystemLimit: 512 << 20}, nil
	}
	if err = h.ConfigureMemory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.closeOrphanDirectory(); h.outputRoot.Close(); h.memoryOutputRoot.Close() })
	return h
}
func writeMemoryOutput(t *testing.T, h *HLS, id string) string {
	t.Helper()
	dir, err := h.createOutput(id, true)
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(h.memoryRoot, id) {
		t.Fatal("not placed in memory", dir)
	}
	if err = os.WriteFile(filepath.Join(dir, "segment-000000.ts"), []byte("retained media"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "generation"), []byte("3"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMemoryPlacementBounds(t *testing.T) {
	for _, test := range []struct {
		name    string
		facts   memoryFacts
		failure bool
		memory  bool
	}{
		{"under cap", memoryFacts{8 << 30, 16 << 30, 512 << 20, 512 << 20}, false, true},
		{"bounded volume above allowance", memoryFacts{2 << 30, 16 << 30, 1 << 30, 1 << 30}, false, false},
		{"floor", memoryFacts{(1 << 30) + (128 << 20), 4 << 30, 512 << 20, 512 << 20}, false, false},
		{"filesystem free", memoryFacts{8 << 30, 16 << 30, memoryHeadroom - 1, 512 << 20}, false, false},
		{"stock shm above cap", memoryFacts{8 << 30, 16 << 30, 8 << 30, 8 << 30}, false, false},
		{"unbounded filesystem", memoryFacts{8 << 30, 16 << 30, 8 << 30, 0}, false, false},
		{"unreadable", memoryFacts{}, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := memoryFixture(t)
			h.memoryFacts = func(string) (memoryFacts, error) {
				if test.failure {
					return memoryFacts{}, errors.New("unavailable")
				}
				return test.facts, nil
			}
			dir, err := h.createOutput("session", true)
			if err != nil {
				t.Fatal(err)
			}
			if (filepath.Dir(dir) == h.memoryRoot) != test.memory {
				t.Fatal(dir)
			}
			if h.memoryRefused.Load() != boolCount(!test.memory) {
				t.Fatal("refusal diagnostic missing")
			}
		})
	}
}
func boolCount(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

func TestMemoryPlacementExistingDiskAndFiniteStayOnDisk(t *testing.T) {
	h := memoryFixture(t)
	disk := filepath.Join(h.root, "existing")
	if err := os.Mkdir(disk, 0700); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		id        string
		streaming bool
	}{{"existing", true}, {"finite", false}} {
		dir, err := h.createOutput(test.id, test.streaming)
		if err != nil || filepath.Dir(dir) != h.root {
			t.Fatal(dir, err)
		}
	}
	// Admission reserves headroom for unmeasured concurrent sessions.
	h.memoryFacts = func(string) (memoryFacts, error) {
		return memoryFacts{available: 2 << 30, total: 4 << 30, free: 512 << 20, filesystemLimit: 512 << 20}, nil
	}
	if _, err := h.createOutput("first", true); err != nil {
		t.Fatal(err)
	}
	h.memoryFacts = func(string) (memoryFacts, error) {
		return memoryFacts{available: 1 << 30, total: 4 << 30, free: 256 << 20, filesystemLimit: 512 << 20}, nil
	}
	dir, err := h.createOutput("second", true)
	if err != nil || filepath.Dir(dir) != h.root {
		t.Fatal("unmeasured headroom was overcommitted", dir, err)
	}
}

func TestMemoryBudgetSettings(t *testing.T) {
	facts := memoryFacts{available: 7 << 30, total: 16 << 30, free: 20 << 30, filesystemLimit: 20 << 30}
	for _, test := range []struct {
		setting string
		want    int64
	}{{"quarter", 2 << 30}, {"half", 4 << 30}, {"2gb", 2 << 30}, {"4gb", 4 << 30}, {"8gb", (8 << 30) - (16<<30)/10}, {"16gb", (8 << 30) - (16<<30)/10}} {
		cap, floor := memoryBudget(test.setting, facts, 1<<30)
		if cap != test.want || floor != (16<<30)/10 {
			t.Fatal(test.setting, cap, floor)
		}
	}
	if got := (DeliveryConfiguration{MemoryCap: "invalid"}).Normalized().MemoryCap; got != "quarter" {
		t.Fatal(got)
	}
}

func TestMemorySpillIdleAndServedRoot(t *testing.T) {
	h := memoryFixture(t)
	id := "idle"
	source := writeMemoryOutput(t, h, id)
	oldPath := filepath.Join(source, "segment-000000.ts")
	opened, err := h.openOutputPath(context.Background(), oldPath, "segment-000000.ts")
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	h.spillMemory(context.Background(), id)
	if h.inMemory[id] || h.outputDir(id) != filepath.Join(h.root, id) {
		t.Fatal("placement did not flip")
	}
	if _, err = os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("memory output remains", err)
	}
	// Even a request authorized before the move follows current placement.
	for _, path := range []string{oldPath, filepath.Join(h.outputDir(id), "segment-000000.ts")} {
		f, e := h.openOutputPath(context.Background(), path, "segment-000000.ts")
		if e != nil {
			t.Fatal(e)
		}
		raw, e := io.ReadAll(f)
		f.Close()
		if e != nil || string(raw) != "retained media" {
			t.Fatal(string(raw), e)
		}
	}
	raw, err := io.ReadAll(opened)
	if err != nil || string(raw) != "retained media" {
		t.Fatal("open reader lost media", err)
	}
	if h.memorySpills.Load() != 1 {
		t.Fatal("spill diagnostic missing")
	}
}

func TestMemorySpillWaitsAndFencesStarts(t *testing.T) {
	_, h, id := conversionOutputFixture(t)
	h.ctx = context.Background()
	if err := os.RemoveAll(filepath.Join(h.root, id)); err != nil {
		t.Fatal(err)
	}
	h.memoryCheck = func(string) error { return nil }
	h.memoryFacts = func(string) (memoryFacts, error) { return memoryFacts{8 << 30, 16 << 30, 512 << 20, 512 << 20}, nil }
	if err := h.ConfigureMemory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer h.memoryOutputRoot.Close()
	source := writeMemoryOutput(t, h, id)
	cancelled := make(chan struct{})
	h.active[id] = func() {
		select {
		case <-cancelled:
		default:
			close(cancelled)
		}
	}
	finished := make(chan struct{})
	go func() { h.spillMemory(context.Background(), id); close(finished) }()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("producer not cancelled")
	}
	if err := h.start(context.Background(), id, 1); !errors.Is(err, ErrSegmentPreparing) {
		t.Fatal("restart not fenced", err)
	}
	if _, err := h.createOutput(id, true); !errors.Is(err, ErrSegmentPreparing) {
		t.Fatal("folder creation not fenced", err)
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("output copied before producer exited", err)
	}
	select {
	case <-finished:
		t.Fatal("spill did not wait")
	default:
	}
	h.mu.Lock()
	delete(h.active, id)
	h.mu.Unlock()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("spill did not finish")
	}
	if h.inMemory[id] {
		t.Fatal("placement remains memory")
	}
}

func TestMemorySpillCopyFailureDropsRAM(t *testing.T) {
	for _, failure := range []string{"symlink", "disk full"} {
		t.Run(failure, func(t *testing.T) {
			h := memoryFixture(t)
			source := writeMemoryOutput(t, h, "failed")
			if failure == "symlink" {
				if err := os.Symlink("segment-000000.ts", filepath.Join(source, "link")); err != nil {
					t.Fatal(err)
				}
			} else {
				// A nondirectory disk root makes staging creation fail, like ENOSPC.
				h.root = filepath.Join(t.TempDir(), "full")
				if err := os.WriteFile(h.root, []byte("full"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			h.spillMemory(context.Background(), "failed")
			if _, err := os.Stat(source); !os.IsNotExist(err) {
				t.Fatal("RAM retained on failed copy", err)
			}
			if h.inMemory["failed"] {
				t.Fatal("failed copy still in memory")
			}
			if failure == "symlink" {
				entries, err := os.ReadDir(h.root)
				if err != nil || len(entries) != 1 || entries[0].Name() != diskPlacementPrefix+"failed" {
					t.Fatal("partial spill retained", entries, err)
				}
			}
		})
	}
}

func TestMemoryStartupAdoptionAndDuplicateDiskWins(t *testing.T) {
	h := newSweepFixture(t.TempDir())
	memory := t.TempDir()
	owned, err := prepareMemoryOwnership(memory, h.root)
	if err != nil {
		t.Fatal(err)
	}
	owned.Close()
	writeSessionOutput(t, memory, "adopt", 1)
	if err := os.WriteFile(filepath.Join(memory, "adopt", "generation"), []byte("3"), 0600); err != nil {
		t.Fatal(err)
	}
	writeSessionOutput(t, memory, "duplicate", 1)
	writeSessionOutput(t, h.root, "duplicate", 1)
	h.memoryCheck = func(string) error { return nil }
	if err := h.ConfigureMemory(memory); err != nil {
		t.Fatal(err)
	}
	defer h.memoryOutputRoot.Close()
	if !h.inMemory["adopt"] || h.inMemory["duplicate"] {
		t.Fatal(h.inMemory)
	}
	if _, err := os.Stat(filepath.Join(memory, "duplicate")); !os.IsNotExist(err) {
		t.Fatal("duplicate retained", err)
	}
}

func TestMemoryOrphanSweepAndMovingFence(t *testing.T) {
	_, h, _ := conversionOutputFixture(t)
	h.memoryCheck = func(string) error { return nil }
	if err := h.ConfigureMemory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer h.memoryOutputRoot.Close()
	defer h.closeOrphanDirectory()
	for _, id := range []string{"orphan", "moving"} {
		dir := filepath.Join(h.memoryRoot, id)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-time.Hour)
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
		h.inMemory[id] = true
	}
	h.moving["moving"] = make(chan struct{})
	h.sweepOrphanDirectories(context.Background())
	if _, err := os.Stat(filepath.Join(h.memoryRoot, "orphan")); !os.IsNotExist(err) {
		t.Fatal("orphan retained", err)
	}
	if _, err := os.Stat(filepath.Join(h.memoryRoot, "moving")); err != nil {
		t.Fatal("moving output removed", err)
	}
}

func TestMemorySweepMeasurementPressureAndOneSpill(t *testing.T) {
	h := memoryFixture(t)
	for _, id := range []string{"oldest", "newer"} {
		writeMemoryOutput(t, h, id)
	}
	counter := newMeasureCounter()
	h.measure = counter.measure
	h.sweepMemory(context.Background())
	h.sweepMemory(context.Background())
	if counter.count(filepath.Join(h.memoryRoot, "oldest")) != 1 {
		t.Fatal("memory measured more often than 1s")
	}
	h.mu.Lock()
	h.usage["newer"].served = time.Now()
	h.usage["oldest"].measured = time.Now().Add(-time.Second)
	h.mu.Unlock()
	h.sweepMemory(context.Background())
	if counter.count(filepath.Join(h.memoryRoot, "oldest")) != 2 {
		t.Fatal("memory output was not measured again after one second")
	}
	h.memoryFacts = func(string) (memoryFacts, error) {
		return memoryFacts{available: 1 << 30, total: 16 << 30, free: 512 << 20, filesystemLimit: 512 << 20}, nil
	}
	h.sweepMemory(context.Background())
	if h.inMemory["oldest"] || !h.inMemory["newer"] || h.memorySpills.Load() != 1 {
		t.Fatal("wrong or multiple spills", h.inMemory)
	}
	// Unknown facts cannot authorize continued RAM growth.
	h.memoryFacts = func(string) (memoryFacts, error) { return memoryFacts{}, errors.New("unknown") }
	h.sweepMemory(context.Background())
	if h.inMemory["newer"] {
		t.Fatal("unknown facts retained RAM output")
	}
}

func TestMemoryDiscAndFiniteStreamingPlacement(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, kind := range []string{"finite", "disc", "private-disc"} {
		t.Run(kind, func(t *testing.T) {
			db, h, id := conversionOutputFixture(t)
			h.memoryCheck = func(string) error { return nil }
			h.memoryFacts = func(string) (memoryFacts, error) { return memoryFacts{8 << 30, 16 << 30, 512 << 20, 512 << 20}, nil }
			if err := h.ConfigureMemory(t.TempDir()); err != nil {
				t.Fatal(err)
			}
			defer h.memoryOutputRoot.Close()
			if err := os.RemoveAll(filepath.Join(h.root, id)); err != nil {
				t.Fatal(err)
			}
			plan := &DeliveryPlan{}
			switch kind {
			case "finite":
				plan.Profile = finiteDeliveryProfile
			case "disc":
				if _, err := db.Exec(`INSERT INTO playback_disc_pins(session_id,object_id,title_key,source_revision,fingerprint,stream_revision,chapter_revision) VALUES(?,'object','title','rev','fp',1,1)`, id); err != nil {
					t.Fatal(err)
				}
			case "private-disc":
				if _, err := db.Exec(`INSERT INTO playback_disc_snapshots(id,session_id,generation,input_kind) VALUES('snapshot',?,3,'captured')`, id); err != nil {
					t.Fatal(err)
				}
			}
			dir, err := h.createStreamingOutput(context.Background(), id, plan)
			if err != nil || filepath.Dir(dir) != h.root {
				t.Fatal(kind, dir, err)
			}
		})
	}
}

// Use the existing remote-input execution port, so the actual producer, segment
// request and restart paths run without a media tool or even a real media file.
type memoryWindowInput struct {
	h       *HLS
	id      string
	started chan int
}

func (m *memoryWindowInput) OpenPlaybackWindow(context.Context, string) (subtitles.WindowInput, error) {
	return m, nil
}
func (m *memoryWindowInput) Close() error { return nil }
func (m *memoryWindowInput) Probe(context.Context, []string) ([]byte, error) {
	return nil, errors.New("unexpected copy probe in a conversion fixture")
}
func (m *memoryWindowInput) Run(ctx context.Context, dir string, _ func(string) (subtitles.WindowCommand, error)) error {
	m.h.mu.Lock()
	from := m.h.windows[m.id].start
	m.h.mu.Unlock()
	if err := os.WriteFile(filepath.Join(dir, hlsSegmentFile(from)), []byte("generated segment"), 0600); err != nil {
		return err
	}
	m.started <- from
	<-ctx.Done()
	return ctx.Err()
}
func TestMemorySpillRestartsRealProducerAtRequestedSegment(t *testing.T) {
	db, h, id := conversionOutputFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h.ctx = ctx
	t.Cleanup(func() { cancel(); h.cancelAll(); h.producers.Wait() })
	h.memoryCheck = func(string) error { return nil }
	h.memoryFacts = func(string) (memoryFacts, error) { return memoryFacts{8 << 30, 16 << 30, 512 << 20, 512 << 20}, nil }
	if err := os.RemoveAll(filepath.Join(h.root, id)); err != nil {
		t.Fatal(err)
	}
	if err := h.ConfigureMemory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer h.memoryOutputRoot.Close()
	var source string
	if err := db.QueryRow(`SELECT asset_id FROM playback_sessions WHERE id=?`, id).Scan(&source); err != nil {
		t.Fatal(err)
	}
	plan := &DeliveryPlan{Profile: DeliveryProfileLegacy, SourceID: source, SourceSize: 1, SourceModifiedNS: 1, SourceContainer: "strm", Strategy: DeliveryVideoConversion, Reason: "conversion", Mode: "hls", Duration: 100, VideoAction: "convert", AudioAction: "convert", OutputContainer: "mpegts_hls", OutputVideoCodec: "h264", OutputAudioCodec: "aac", HardwareBackend: "software"}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = persistDeliveryPlan(tx, id, plan); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	grant := "memory-grant"
	if _, err = db.Exec(`UPDATE playback_sessions SET grant_hash=? WHERE id=?`, identity.Digest(grant), id); err != nil {
		t.Fatal(err)
	}
	input := &memoryWindowInput{h: h, id: id, started: make(chan int, 2)}
	h.WindowInputs = input
	if err = h.start(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	select {
	case from := <-input.started:
		if from != 0 {
			t.Fatal(from)
		}
	case <-ctx.Done():
		t.Fatal("initial producer not started", ctx.Err())
	}
	h.spillMemory(ctx, id)
	if h.inMemory[id] {
		t.Fatal("spill failed")
	}
	path, err := h.FileContext(ctx, grant, hlsSegmentFile(0))
	if err != nil || filepath.Dir(path) != filepath.Join(h.root, id) {
		t.Fatal("copied segment unavailable", path, err)
	}
	path, err = h.FileContext(ctx, grant, hlsSegmentFile(4))
	if err != nil || filepath.Dir(path) != filepath.Join(h.root, id) {
		t.Fatal("disk restart did not serve requested segment", path, err)
	}
	select {
	case from := <-input.started:
		if from != 4 {
			t.Fatal("restart did not seek to requested segment", from)
		}
	case <-ctx.Done():
		t.Fatal("disk producer not started", ctx.Err())
	}
	if !segmentReady(filepath.Join(h.root, id, hlsSegmentFile(0))) {
		t.Fatal("restart discarded copied output")
	}
}

func TestMemorySpillTimeoutAndReaderWait(t *testing.T) {
	h := memoryFixture(t)
	source := writeMemoryOutput(t, h, "busy")
	h.active["busy"] = func() {}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	h.spillMemory(ctx, "busy")
	if !h.inMemory["busy"] || h.moving["busy"] != nil {
		t.Fatal("timeout lost placement or move fence")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatal("timeout lost producer output", err)
	}
	h.moving["busy"] = make(chan struct{})
	if err := h.waitOutputMove(ctx, "busy"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("reader wait ignored request deadline", err)
	}
}

func TestMemoryAdoptedOutputCannotResumeOnOversizedFilesystem(t *testing.T) {
	h := memoryFixture(t)
	writeMemoryOutput(t, h, "adopted")
	h.memoryFacts = func(string) (memoryFacts, error) { return memoryFacts{8 << 30, 16 << 30, 8 << 30, 8 << 30}, nil }
	if _, err := h.createOutput("adopted", true); !errors.Is(err, ErrSegmentPreparing) {
		t.Fatal("oversized filesystem resumed conversion", err)
	}
	h.sweepMemory(context.Background())
	if h.inMemory["adopted"] {
		t.Fatal("oversized adopted output did not spill")
	}
}

func TestMemoryRefusedRestartAlwaysSpills(t *testing.T) {
	for _, reason := range []string{"cap", "floor", "free", "unbounded", "unreadable", "unmeasured reservation"} {
		t.Run(reason, func(t *testing.T) {
			h := memoryFixture(t)
			writeMemoryOutput(t, h, "adopted")
			h.recordUsage("adopted", 1<<30, 2, time.Now())
			facts := memoryFacts{8 << 30, 16 << 30, 512 << 20, 512 << 20}
			switch reason {
			case "cap":
				h.recordUsage("adopted", 3<<30, 2, time.Now())
			case "floor":
				facts.available = 1 << 30
			case "free":
				facts.free = memoryHeadroom - 1
			case "unbounded":
				facts.filesystemLimit = 0
			case "unmeasured reservation":
				delete(h.usage, "adopted")
				facts = memoryFacts{(1 << 30) + (128 << 20), 4 << 30, 8 << 30, 8 << 30}
			}
			h.memoryFacts = func(string) (memoryFacts, error) {
				if reason == "unreadable" {
					return memoryFacts{}, errors.New("unknown facts")
				}
				return facts, nil
			}
			if _, err := h.createOutput("adopted", true); !errors.Is(err, ErrSegmentPreparing) {
				t.Fatal("unsafe restart admitted", err)
			}
			// A later sample can recover or remain unknown. Either way a refused
			// restart must progress onto disk instead of sticking in preparing.
			h.memoryFacts = func(string) (memoryFacts, error) { return memoryFacts{}, errors.New("unknown facts") }
			h.sweepMemory(context.Background())
			if h.inMemory["adopted"] {
				t.Fatal("refused restart was not moved to disk")
			}
			if dir, err := h.createOutput("adopted", true); err != nil || filepath.Dir(dir) != h.root {
				t.Fatal("spilled restart did not resume on disk", dir, err)
			}
		})
	}
}

func TestMemoryMeasuredRestartNeedsNoNewFolderReservation(t *testing.T) {
	h := memoryFixture(t)
	writeMemoryOutput(t, h, "measured")
	h.recordUsage("measured", 128<<20, 2, time.Now())
	h.memoryFacts = func(string) (memoryFacts, error) {
		return memoryFacts{(1 << 30) + memoryHeadroom, 4 << 30, memoryHeadroom, memoryHeadroom}, nil
	}
	if _, err := h.createOutput("measured", true); err != nil {
		t.Fatal("measured restart was charged new-folder headroom", err)
	}
}

func TestMemoryFactsReadOutsideProducerMutex(t *testing.T) {
	h := memoryFixture(t)
	h.memoryFacts = func(string) (memoryFacts, error) {
		if !h.mu.TryLock() {
			t.Fatal("memory facts read under producer mutex")
		}
		h.mu.Unlock()
		return memoryFacts{8 << 30, 16 << 30, 512 << 20, 512 << 20}, nil
	}
	writeMemoryOutput(t, h, "fresh")
	if _, err := h.createOutput("fresh", true); err != nil {
		t.Fatal(err)
	}
	h.sweepMemory(context.Background())
}

func TestMemorySpillWaitsForRetentionMutation(t *testing.T) {
	h := memoryFixture(t)
	source := writeMemoryOutput(t, h, "retained")
	_, release := h.retainOutput("retained")
	if release == nil {
		t.Fatal("retention lease refused")
	}
	finished := make(chan struct{})
	go func() { h.spillMemory(context.Background(), "retained"); close(finished) }()
	deadline := time.After(time.Second)
	for {
		h.mu.Lock()
		moving := h.moving["retained"] != nil
		h.mu.Unlock()
		if moving {
			break
		}
		select {
		case <-deadline:
			t.Fatal("move did not begin")
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case <-finished:
		t.Fatal("copied output while retention could mutate it")
	default:
	}
	if err := os.WriteFile(filepath.Join(source, "segment-000000.ts"), []byte("retention settled"), 0600); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("move did not finish")
	}
	raw, err := os.ReadFile(filepath.Join(h.root, "retained", "segment-000000.ts"))
	if err != nil || string(raw) != "retention settled" {
		t.Fatal(string(raw), err)
	}
}

func TestMemorySpillChoosesLargestLiveSession(t *testing.T) {
	h := memoryFixture(t)
	for _, id := range []string{"small", "large"} {
		writeMemoryOutput(t, h, id)
		h.active[id] = func() { h.mu.Lock(); delete(h.active, id); h.mu.Unlock() }
	}
	h.recordUsage("small", 10, 1, time.Now())
	h.recordUsage("large", 20, 1, time.Now())
	h.memoryFacts = func(string) (memoryFacts, error) { return memoryFacts{1 << 30, 16 << 30, 512 << 20, 512 << 20}, nil }
	h.sweepMemory(context.Background())
	if !h.inMemory["small"] || h.inMemory["large"] || h.memorySpills.Load() != 1 {
		t.Fatal("wrong live spill order", h.inMemory)
	}
}

func TestMemoryAdmissionUsesMeasuredBytesForCap(t *testing.T) {
	h := memoryFixture(t)
	cfg := DefaultDeliveryConfiguration()
	cfg.MemoryCap = "2gb"
	h.ConfigureSettings(fixedSettings{cfg: cfg})
	writeMemoryOutput(t, h, "unmeasured")
	// Reservation is an admission cost, not already-held bytes that may be added
	// back to available memory when calculating the effective cap/floor clamp.
	h.memoryFacts = func(string) (memoryFacts, error) {
		return memoryFacts{(1 << 30) + (400 << 20), 4 << 30, 512 << 20, 512 << 20}, nil
	}
	dir, err := h.createOutput("second", true)
	if err != nil || filepath.Dir(dir) != h.root {
		t.Fatal("reservation inflated effective memory cap", dir, err)
	}
}

func TestMemoryOutputFullStaysRetryableAndSpillsWithUnknownFacts(t *testing.T) {
	db, h, id := conversionOutputFixture(t)
	h.ctx = context.Background()
	h.memoryCheck = func(string) error { return nil }
	h.memoryFacts = func(string) (memoryFacts, error) { return memoryFacts{8 << 30, 16 << 30, 512 << 20, 512 << 20}, nil }
	if err := os.RemoveAll(filepath.Join(h.root, id)); err != nil {
		t.Fatal(err)
	}
	if err := h.ConfigureMemory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer h.memoryOutputRoot.Close()
	source := writeMemoryOutput(t, h, id)
	h.failedWith(context.Background(), id, FailureOutOfSpace, "No space left on device")
	var state string
	if err := db.QueryRow(`SELECT state FROM playback_sessions WHERE id=?`, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "playing" || !h.memoryFull[id] {
		t.Fatal("tmpfs ENOSPC permanently failed session", state)
	}
	if err := h.start(context.Background(), id, 1); !errors.Is(err, ErrSegmentPreparing) {
		t.Fatal("full tmpfs restarted a producer", err)
	}
	h.memoryFacts = func(string) (memoryFacts, error) { return memoryFacts{}, errors.New("unknown host facts") }
	h.sweepMemory(context.Background())
	if h.inMemory[id] || h.memoryFull[id] {
		t.Fatal("known ENOSPC did not spill")
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("full tmpfs was retained", err)
	}
	if !segmentReady(filepath.Join(h.root, id, hlsSegmentFile(0))) {
		t.Fatal("full output not copied to disk")
	}
}

func TestMemoryFullManifestFailureDefersToDisk(t *testing.T) {
	h := memoryFixture(t)
	writeMemoryOutput(t, h, "manifest")
	h.memoryFacts = func(string) (memoryFacts, error) { return memoryFacts{8 << 30, 16 << 30, 0, 512 << 20}, nil }
	if h.failed(context.Background(), "manifest") || !h.memoryFull["manifest"] {
		t.Fatal("full tmpfs manifest failure was not deferred")
	}
}

func TestMemorySpillIncompleteOutputRegeneratesFromAbsentDiskFolder(t *testing.T) {
	h := memoryFixture(t)
	source := writeMemoryOutput(t, h, "partial")
	if err := os.Remove(filepath.Join(source, "generation")); err != nil {
		t.Fatal(err)
	}
	h.spillMemory(context.Background(), "partial")
	if h.inMemory["partial"] {
		t.Fatal("incomplete output remained in memory")
	}
	for _, dir := range []string{source, filepath.Join(h.root, "partial")} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("incomplete folder prevents next-request regeneration", dir, err)
		}
	}
}

func TestMemorySpillCancellationDoesNotFailProducer(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "producer exits", true: "producer outlives spill timeout"}[timeout], func(t *testing.T) {
			db, h, id := conversionOutputFixture(t)
			h.memoryCheck = func(string) error { return nil }
			h.memoryFacts = func(string) (memoryFacts, error) {
				return memoryFacts{8 << 30, 16 << 30, 512 << 20, 512 << 20}, nil
			}
			if err := os.RemoveAll(filepath.Join(h.root, id)); err != nil {
				t.Fatal(err)
			}
			if err := h.ConfigureMemory(t.TempDir()); err != nil {
				t.Fatal(err)
			}
			defer h.memoryOutputRoot.Close()
			writeMemoryOutput(t, h, id)
			s := New(db)
			s.ConfigureHLS(h)
			owner, err := s.RetainPreparation(context.Background(), id, 3)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			producer := withProducerGeneration(owner.Context(), 3)
			h.mu.Lock()
			h.installOccurrenceLocked(id, owner.run, owner.cancel)
			h.mu.Unlock()
			remove := func() { h.mu.Lock(); h.removeOccurrenceLocked(id, owner.run); h.mu.Unlock() }
			defer remove()
			if timeout {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
				defer cancel()
				h.spillMemory(ctx, id)
			} else {
				failed := make(chan bool, 1)
				go func() {
					<-producer.Done()
					failed <- h.failed(producer, id)
					remove() // Actual producers remove admission only after failure handling.
				}()
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				h.spillMemory(ctx, id)
				if <-failed {
					t.Fatal("spill cancelled producer failed session")
				}
			}
			if producer.Err() == nil || h.moving[id] != nil {
				t.Fatal("test did not finish a cancelled move")
			}
			// A failure after finishMove must still recognize the exact cancelled run.
			if h.failed(producer, id) {
				t.Fatal("late spill failure failed session")
			}
			var state string
			if err := db.QueryRow(`SELECT state FROM playback_sessions WHERE id=?`, id).Scan(&state); err != nil || state != "playing" {
				t.Fatal("spill cancelled producer failed session", state, err)
			}
			// A different producer's real cancellation must not inherit the marker.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if !h.failed(withProducerGeneration(ctx, 3), id) {
				t.Fatal("spill marker suppressed a different producer's failure")
			}
		})
	}
}

func TestMemorySweepSpillsForMeasuredPressure(t *testing.T) {
	for _, reason := range []string{"within bounds", "cap", "floor", "free", "unbounded"} {
		t.Run(reason, func(t *testing.T) {
			h := memoryFixture(t)
			writeMemoryOutput(t, h, "measured")
			facts := memoryFacts{8 << 30, 16 << 30, 512 << 20, 512 << 20}
			bytes := int64(128 << 20)
			switch reason {
			case "cap":
				bytes = 3 << 30
			case "floor":
				facts.available = 1 << 30
			case "free":
				facts.free = memoryHeadroom - 1
			case "unbounded":
				facts.filesystemLimit = 0
			}
			h.recordUsage("measured", bytes, 2, time.Now())
			h.memoryFacts = func(string) (memoryFacts, error) { return facts, nil }
			h.sweepMemory(context.Background())
			if h.inMemory["measured"] != (reason == "within bounds") {
				t.Fatal("wrong placement after pressure sample", reason, h.inMemory)
			}
		})
	}
}

// A small expected producer write is not a bound: reserve everything that can
// still fit on the volume. This also models a tight container allowance.
func TestMemoryVolumeReservesWorstCaseGrowth(t *testing.T) {
	for _, setting := range []string{"quarter", "half", "2gb", "4gb", "8gb", "16gb"} {
		facts := memoryFacts{available: (1 << 30) + (300 << 20), total: 4 << 30, free: 512 << 20, filesystemLimit: 512 << 20}
		if memoryVolumeFits(setting, facts, 256<<20) {
			t.Fatalf("%s allowed worst-case growth through the floor", setting)
		}
	}
	facts := memoryFacts{available: 3 << 30, total: 8 << 30, free: 512 << 20, filesystemLimit: 512 << 20}
	if !memoryVolumeFits("quarter", facts, 0) {
		t.Fatal("safe bounded volume refused")
	}
	facts.filesystemLimit = 1 << 30
	if memoryVolumeFits("quarter", facts, 0) {
		t.Fatal("oversized volume admitted based on free space rather than its ceiling")
	}
}
