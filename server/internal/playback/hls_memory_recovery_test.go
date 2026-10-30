package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"testing"
	"time"
)

func TestMemoryOwnershipRefusesUnownedMatchingDirectories(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "no disk duplicate", true: "disk duplicate"}[duplicate], func(t *testing.T) {
			h := newSweepFixture(t.TempDir())
			h.memoryCheck = func(string) error { return nil }
			memory := t.TempDir()
			writeSessionOutput(t, memory, "customer-files", 1)
			if duplicate {
				writeSessionOutput(t, h.root, "customer-files", 1)
			}
			if err := h.ConfigureMemory(memory); err == nil {
				t.Fatal("adopted an unowned tree")
			}
			if !segmentReady(filepath.Join(memory, "customer-files", hlsSegmentFile(0))) {
				t.Fatal("deleted unrelated matching directory")
			}
			if _, err := os.Lstat(filepath.Join(memory, memoryOwnershipMarker)); !os.IsNotExist(err) {
				t.Fatal("minted ownership over existing content", err)
			}
			if h.memoryOutputRoot != nil || len(h.inMemory) > 0 {
				t.Fatal("installed unowned root")
			}
		})
	}
}

func TestMemoryOwnershipRefusesOtherInstallationAndLinkedParents(t *testing.T) {
	h := memoryFixture(t)
	source := writeMemoryOutput(t, h, "owned")
	other := newSweepFixture(t.TempDir())
	other.memoryCheck = func(string) error { return nil }
	if err := other.ConfigureMemory(h.memoryRoot); err == nil {
		t.Fatal("adopted another installation's output")
	}
	if !segmentReady(filepath.Join(source, hlsSegmentFile(0))) {
		t.Fatal("changed another installation's output")
	}
	linked := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(h.memoryRoot, linked); err != nil {
		t.Fatal(err)
	}
	if err := other.ConfigureMemory(filepath.Join(linked, "child")); err == nil {
		t.Fatal("accepted linked parent")
	}
	if _, err := os.Lstat(filepath.Join(h.memoryRoot, "child")); !os.IsNotExist(err) {
		t.Fatal("created output through link", err)
	}
}

func TestMemoryOwnershipRefusesSharedFilesystemRoot(t *testing.T) {
	if _, err := os.Stat("/dev/shm"); err != nil {
		t.Skip("Linux shared memory unavailable")
	}
	h := newSweepFixture(t.TempDir())
	h.memoryCheck = func(string) error { return nil }
	if err := h.ConfigureMemory("/dev/shm"); err == nil {
		t.Fatal("accepted shared filesystem root")
	}
}

func TestMemoryFailedSpillKeepsDiskPlacementAcrossRestart(t *testing.T) {
	for _, stamp := range []string{"", "invalid", "0", "4\n"} {
		t.Run("generation="+stamp, func(t *testing.T) {
			h := memoryFixture(t)
			source := writeMemoryOutput(t, h, "partial")
			if err := os.WriteFile(filepath.Join(source, "generation"), []byte(stamp), 0600); err != nil {
				t.Fatal(err)
			}
			h.spillMemory(context.Background(), "partial")
			if _, err := os.Stat(filepath.Join(h.root, "partial")); !os.IsNotExist(err) {
				t.Fatal("incomplete tree retained", err)
			}
			restarted := newSweepFixture(h.root)
			restarted.memoryCheck, restarted.memoryFacts = h.memoryCheck, h.memoryFacts
			if err := restarted.ConfigureMemory(h.memoryRoot); err != nil {
				t.Fatal(err)
			}
			defer restarted.memoryOutputRoot.Close()
			for _, instance := range []*HLS{h, restarted} {
				dir, err := instance.createOutput("partial", true)
				if err != nil || dir != filepath.Join(h.root, "partial") {
					t.Fatal("returned to RAM after failed spill", dir, err)
				}
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestMemorySpillPreservesRecentlyServedAgainstGlobalEviction(t *testing.T) {
	h := memoryFixture(t)
	writeMemoryOutput(t, h, "watching")
	h.recordUsage("watching", 123, 2, time.Now())
	h.noteSessionServed("watching")
	served := h.usage["watching"].served
	h.spillMemory(context.Background(), "watching")
	if u := h.usage["watching"]; u == nil || u.served != served || !u.measured.IsZero() {
		t.Fatal("spill lost recency or kept stale measurement", u)
	}
	h.measure = func(string) (int64, int, error) { return hlsGlobalByteBudget + 1, 2, nil }
	h.enforceGlobalBudget(context.Background())
	if !segmentReady(filepath.Join(h.root, "watching", hlsSegmentFile(0))) {
		t.Fatal("global budget evicted recently served spill")
	}
}

func TestMemoryDiskPolicyPlacesNewAndSpillsExistingOutput(t *testing.T) {
	h := memoryFixture(t)
	writeMemoryOutput(t, h, "existing")
	cfg := DefaultDeliveryConfiguration()
	cfg.TranscodeLocation = "disk"
	h.ConfigureSettings(fixedSettings{cfg: cfg})
	if dir, err := h.createOutput("new", true); err != nil || dir != filepath.Join(h.root, "new") {
		t.Fatal("disk preference ignored", dir, err)
	}
	if _, err := h.createOutput("existing", true); !errors.Is(err, ErrSegmentPreparing) {
		t.Fatal("RAM producer resumed after policy switch", err)
	}
	h.sweepMemory(context.Background())
	if h.inMemory["existing"] || !segmentReady(filepath.Join(h.root, "existing", hlsSegmentFile(0))) {
		t.Fatal("policy change did not spill retained output")
	}
}

func TestMemorySpillTimeoutKeepsStartsFenced(t *testing.T) {
	h := memoryFixture(t)
	writeMemoryOutput(t, h, "busy")
	h.active["busy"] = func() {}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	h.spillMemory(ctx, "busy")
	if !h.memoryFull["busy"] {
		t.Fatal("timeout did not preserve pressure fence")
	}
	if _, err := h.createOutput("busy", true); !errors.Is(err, ErrSegmentPreparing) {
		t.Fatal("timed-out producer admitted a new writer", err)
	}
	delete(h.active, "busy")
	h.sweepMemory(context.Background())
	if h.inMemory["busy"] || h.memoryFull["busy"] {
		t.Fatal("later sweep did not complete forced relocation")
	}
}

func TestMemoryOpenLeaseAllowsOtherSessionsAndFencesSpill(t *testing.T) {
	h := memoryFixture(t)
	dir := writeMemoryOutput(t, h, "reader")
	entered, release, opened := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	h.outputOpen = func(root *os.Root, id, name string) (*os.File, error) {
		close(entered)
		<-release
		return openHLSOutput(root, id, name)
	}
	go func() {
		f, err := h.openOutputPath(context.Background(), filepath.Join(dir, hlsSegmentFile(0)), hlsSegmentFile(0))
		if f != nil {
			f.Close()
		}
		opened <- err
	}()
	<-entered
	if !h.mu.TryLock() {
		close(release)
		t.Fatal("artifact open holds global producer mutex")
	}
	h.mu.Unlock()
	if _, err := h.createOutput("other", true); err != nil {
		close(release)
		t.Fatal("unrelated placement blocked", err)
	}
	moved := make(chan struct{})
	go func() { h.spillMemory(context.Background(), "reader"); close(moved) }()
	deadline := time.After(time.Second)
	for {
		h.mu.Lock()
		moving := h.moving["reader"] != nil
		h.mu.Unlock()
		if moving {
			break
		}
		select {
		case <-deadline:
			close(release)
			t.Fatal("spill did not begin")
		case <-time.After(time.Millisecond):
		}
	}
	select {
	case <-moved:
		close(release)
		t.Fatal("spill moved directory during open")
	default:
	}
	close(release)
	if err := <-opened; err != nil {
		t.Fatal(err)
	}
	select {
	case <-moved:
	case <-time.After(time.Second):
		t.Fatal("spill did not finish after open")
	}
}

func TestMemoryRenameLeaseAllowsOtherSessions(t *testing.T) {
	h := memoryFixture(t)
	writeMemoryOutput(t, h, "moving")
	calls := 0
	h.outputRename = func(from, to string) error {
		if !h.mu.TryLock() {
			t.Error("rename holds global producer mutex")
			return errors.New("locked")
		}
		h.mu.Unlock()
		calls++
		if _, err := h.createOutput("other", true); err != nil {
			t.Error("unrelated placement blocked", err)
		}
		if _, err := h.createOutput("moving", true); !errors.Is(err, ErrSegmentPreparing) {
			t.Error("moving session not fenced", err)
		}
		return os.Rename(from, to)
	}
	h.spillMemory(context.Background(), "moving")
	if calls != 2 || h.inMemory["moving"] {
		t.Fatal("spill did not publish and retire", calls)
	}
}

func TestMemoryStartupIncompleteOutputRestartsAtRequestedSegment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, placement := range []string{"memory", "disk"} {
		t.Run(placement, func(t *testing.T) {
			db, old, id := conversionOutputFixture(t)
			if err := os.RemoveAll(filepath.Join(old.root, id)); err != nil {
				t.Fatal(err)
			}
			old.memoryCheck = func(string) error { return nil }
			old.memoryFacts = func(string) (memoryFacts, error) { return memoryFacts{8 << 30, 16 << 30, 512 << 20, 512 << 20}, nil }
			memory := t.TempDir()
			if err := old.ConfigureMemory(memory); err != nil {
				t.Fatal(err)
			}
			dir, err := old.createOutput(id, placement == "memory")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "master.m3u8"), []byte("partial"), 0600); err != nil {
				t.Fatal(err)
			}
			old.memoryOutputRoot.Close()
			h := newSweepFixture(old.root)
			h.db = db
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			h.ctx = ctx
			t.Cleanup(func() { cancel(); h.cancelAll(); h.producers.Wait() })
			h.memoryCheck, h.memoryFacts = old.memoryCheck, old.memoryFacts
			if err := h.ConfigureMemory(memory); err != nil {
				t.Fatal(err)
			}
			defer h.memoryOutputRoot.Close()
			grant, input := prepareMemoryProducer(t, h, id)
			if _, err = h.FileContext(ctx, grant, hlsSegmentFile(4)); !errors.Is(err, ErrSegmentPreparing) {
				t.Fatal("initial request must start regeneration", err)
			}
			select {
			case from := <-input.started:
				if from != 4 {
					t.Fatal("regeneration ignored demand", from)
				}
			case <-ctx.Done():
				t.Fatal("incomplete output stayed preparing", ctx.Err())
			}
			path, err := h.FileContext(ctx, grant, hlsSegmentFile(4))
			if err != nil || filepath.Dir(path) != filepath.Join(h.root, id) || !segmentReady(path) {
				t.Fatal("restarted output not served on disk", path, err)
			}
		})
	}
}

func TestMemoryIncompleteRecoveryKeepsValidDifferentGenerationAndActivePublication(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "different generation", true: "active initial publication"}[active], func(t *testing.T) {
			h := memoryFixture(t)
			dir, err := h.createOutput("generation", false)
			if err != nil {
				t.Fatal(err)
			}
			if active {
				h.active["generation"] = func() {}
			} else if err := os.WriteFile(filepath.Join(dir, "generation"), []byte("2"), 0600); err != nil {
				t.Fatal(err)
			}
			h.retireIncompleteOutput("generation")
			if _, err := os.Stat(dir); err != nil {
				t.Fatal("retired output that must stay fenced", err)
			}
		})
	}
}

func TestMemoryCleanupRenamesOutsideGlobalMutex(t *testing.T) {
	for _, mode := range []string{"orphan", "budget"} {
		t.Run(mode, func(t *testing.T) {
			_, h, _ := conversionOutputFixture(t)
			writeSessionOutput(t, h.root, "orphan", 1)
			old := time.Now().Add(-time.Hour)
			if err := os.Chtimes(filepath.Join(h.root, "orphan"), old, old); err != nil {
				t.Fatal(err)
			}
			calls := 0
			h.outputRename = func(from, to string) error {
				calls++
				if !h.mu.TryLock() {
					t.Error("cleanup rename holds global mutex")
					return errors.New("locked")
				}
				if !h.cleaning["orphan"] {
					t.Error("cleanup did not fence the session")
				}
				h.mu.Unlock()
				if _, err := h.createOutput("orphan", false); !errors.Is(err, ErrSegmentPreparing) {
					t.Error("cleanup admitted a writer", err)
				}
				return os.Rename(from, to)
			}
			if mode == "orphan" {
				h.sweepOrphanDirectories(context.Background())
			} else {
				h.db = nil // The global budget, rather than orphan removal, retires the tree.
				h.measure = func(dir string) (int64, int, error) {
					if filepath.Base(dir) == "orphan" {
						return hlsGlobalByteBudget + 1, 1, nil
					}
					return 0, 0, nil
				}
				h.enforceGlobalBudget(context.Background())
			}
			if calls != 1 {
				t.Fatal("cleanup did not retire output", calls)
			}
		})
	}
}

func TestMemoryVolumeAllowanceIncludesMetadataAndUnmeasuredWrites(t *testing.T) {
	facts := memoryFacts{available: 8 << 30, total: 16 << 30, free: (512 << 20) - 4096, filesystemLimit: 512 << 20}
	if !memoryVolumeFits("quarter", facts, 0) {
		t.Fatal("ownership metadata made bounded volume unavailable")
	}
	facts.free = 128 << 20
	if !memoryVolumeFits("quarter", facts, 0) {
		t.Fatal("unmeasured writes changed volume admission ceiling")
	}
	facts.filesystemLimit = 8 << 30
	if memoryVolumeFits("quarter", facts, 0) {
		t.Fatal("accepted volume above configured allowance")
	}
}

func TestMemoryInitialDirectoryFailureFallsBackAndPinsDisk(t *testing.T) {
	h := memoryFixture(t)
	conflict := filepath.Join(h.memoryRoot, "conflict")
	if err := os.WriteFile(conflict, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		dir, err := h.createOutput("conflict", true)
		if err != nil || dir != filepath.Join(h.root, "conflict") {
			t.Fatal("RAM setup failure did not fall back to disk", dir, err)
		}
		if h.inMemory["conflict"] {
			t.Fatal("failed RAM placement retained")
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	if raw, err := os.ReadFile(conflict); err != nil || string(raw) != "untouched" {
		t.Fatal("changed conflicting RAM entry", err)
	}
}

func TestMemoryInitialDirectoryFailurePreservesDiskError(t *testing.T) {
	h := memoryFixture(t)
	if err := os.WriteFile(filepath.Join(h.memoryRoot, "conflict"), []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	h.root = filepath.Join(t.TempDir(), "disk-is-not-directory")
	if err := os.WriteFile(h.root, []byte("disk unavailable"), 0600); err != nil {
		t.Fatal(err)
	}
	dir, err := h.createOutput("conflict", true)
	if err == nil || dir != filepath.Join(h.root, "conflict") || h.inMemory["conflict"] {
		t.Fatal("disk failure masked or returned to RAM", dir, err)
	}
}

func prepareMemoryProducer(t *testing.T, h *HLS, id string) (string, *memoryWindowInput) {
	t.Helper()
	db := h.db
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
	grant := "restart-grant"
	if _, err = db.Exec(`UPDATE playback_sessions SET grant_hash=? WHERE id=?`, identity.Digest(grant), id); err != nil {
		t.Fatal(err)
	}
	input := &memoryWindowInput{h: h, id: id, started: make(chan int, 1)}
	h.WindowInputs = input
	return grant, input
}

func TestMemoryInitialGenerationWriteFailureMovesToDisk(t *testing.T) {
	db, h, id := conversionOutputFixture(t)
	if err := os.RemoveAll(filepath.Join(h.root, id)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	h.ctx = ctx
	t.Cleanup(func() { cancel(); h.cancelAll(); h.producers.Wait() })
	h.memoryCheck = func(string) error { return nil }
	h.memoryFacts = func(string) (memoryFacts, error) { return memoryFacts{8 << 30, 16 << 30, 512 << 20, 512 << 20}, nil }
	if err := h.ConfigureMemory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer h.memoryOutputRoot.Close()
	dir, err := h.createOutput(id, true)
	if err != nil {
		t.Fatal(err)
	}
	// The capacity facts remain healthy, but manifest publication cannot create
	// its temporary generation file. This must be a disk retry, not a failure.
	if err := os.Mkdir(filepath.Join(dir, "generation.tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	grant, input := prepareMemoryProducer(t, h, id)
	if err := h.start(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	h.producers.Wait()
	var state string
	if err := db.QueryRow(`SELECT state FROM playback_sessions WHERE id=?`, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "playing" || !h.memoryFull[id] {
		t.Fatal("RAM metadata failure did not remain retryable", state)
	}
	h.sweepMemory(ctx)
	if _, err := h.FileContext(ctx, grant, hlsSegmentFile(4)); !errors.Is(err, ErrSegmentPreparing) {
		t.Fatal(err)
	}
	select {
	case from := <-input.started:
		if from != 4 {
			t.Fatal(from)
		}
	case <-ctx.Done():
		t.Fatal("metadata failure did not regenerate on disk", ctx.Err())
	}
	if path, err := h.FileContext(ctx, grant, hlsSegmentFile(4)); err != nil || filepath.Dir(path) != filepath.Join(h.root, id) {
		t.Fatal(path, err)
	}
}
