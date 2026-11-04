//go:build !windows

package ingestion

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Every storage helper is a new process, which costs 0.1–0.4 s on a small VPS
// (OPS-1, 2 Nov: 31 helpers per film on a first import made a 14-library demo
// scan take 40+ minutes). Since scanner M1, local stat, list and reads run in
// process and only tools (the sandboxed ffprobe passes) start one. These
// budgets hold the processes a scan starts, using the real helper binary so the
// in-process path is the one measured. Measured on this fixture (six films):
// before OPS-1 C, 36.5 per film on a first import and 7.8 on an unchanged
// rescan; after C, 10.3 and 4.0; after M1, the two ffprobe passes per film on
// a first import and none on a rescan; after M4, none per film: probes run on
// long-lived sandboxes (sandboxesPerScan), where hosts support them.
const (
	processesPerScan            = 2
	firstImportProcessesPerFile = 0
	// unpooledProbesPerFile is a first import's processes per film where the
	// host has no decoder sandbox (no bubblewrap, as on CI runners): each of
	// the two ffprobe passes then starts its own helper, as before M4.
	unpooledProbesPerFile  = 2
	rescanProcessesPerFile = 0
	// sandboxesPerScan bounds probe sandboxes started per scan run (scanner
	// M4): one per concurrent probe, never one per file.
	sandboxesPerScan = 4
)

// processStarts is the helper processes the supervisor started: admitted
// operations minus those served in process.
func processStarts(store *storage.Client) int {
	stats := store.Supervisor.Stats()
	return int(stats.Admitted - stats.InProcess)
}

func TestScanHelperProcessBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	root := t.TempDir()
	library := filepath.Join(root, "films")
	const films = 6
	for i := 0; i < films; i++ {
		folder := filepath.Join(library, fmt.Sprintf("Film %d (20%02d)", i, i))
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=64x64:rate=12", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
			"-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", filepath.Join(folder, filepath.Base(folder)+".mp4")).CombinedOutput(); err != nil {
			t.Fatalf("fixture %v %s", err, output)
		}
	}
	db, err := persistence.Open(filepath.Join(root, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cat := catalog.New(db)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// As the server configures it at start: the limits shim and probe workers.
	if _, err = mediaexec.Configure(mediaexec.Options{StateDir: t.TempDir(), Helper: binary, SkipOrphanKill: true}); err != nil {
		t.Fatal(err)
	}
	store := storage.New(binary)
	cat.SetStorage(store)
	meta, err := localmetadata.New(filepath.Join(root, "art"), ffmpeg, store)
	if err != nil {
		t.Fatal(err)
	}
	scanner := New(db, cat, assets.Probe{Binary: decodertest.QualifiedFFprobe(t), Supervisor: store.Supervisor})
	scanner.SetStorage(store)
	scanner.LocalMetadata = meta
	lib, err := cat.Create("Films", "movie", library)
	if err != nil {
		t.Fatal(err)
	}
	scan := func(what string) {
		t.Helper()
		job, err := scanner.Queue(lib.ID)
		if err != nil {
			t.Fatal(err)
		}
		for n := 0; n < 64; n++ {
			scanner.process(context.Background(), job.ID, lib.ID)
			drainDerivedCatalogue(t, db)
			state, err := scanner.Get(job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if state.Status == "complete" || state.Status == "complete_with_warnings" {
				return
			}
			if state.Status == "failed" || n == 63 {
				t.Fatalf("%s: scan ended %s", what, state.Status)
			}
		}
	}
	before, pool := processStarts(store), mediaexec.ProbePoolSnapshot()
	scan("first import")
	firstTotal := processStarts(store) - before
	firstPool := mediaexec.ProbePoolSnapshot()
	var analysed int
	if err = db.QueryRow(`SELECT count(*) FROM inventory_objects WHERE analysis_state='complete'`).Scan(&analysed); err != nil || analysed != films {
		t.Fatalf("first import analysed %d of %d films (%v)", analysed, films, err)
	}
	scan("rescan")
	rescanTotal := processStarts(store) - firstTotal - before
	stats := store.Supervisor.Stats()
	sandboxes, probes := int(firstPool.WorkersStarted-pool.WorkersStarted), int(firstPool.Jobs-pool.Jobs)
	t.Logf("processes: first import %d (%.1f per film), %d probe sandboxes for %d probes; rescan %d; %d of %d operations in process", firstTotal, float64(firstTotal)/films, sandboxes, probes, rescanTotal, stats.InProcess, stats.Admitted)
	perFile := firstImportProcessesPerFile
	if mediaexec.ProbePoolAccepts(mediaexec.Job{Executable: decodertest.QualifiedFFprobe(t), Background: true}) {
		// Scanner M4: probes run on long-lived sandboxes, at most one per
		// concurrent probe, instead of one sandbox (and helper) per probe.
		if probes < films || sandboxes > sandboxesPerScan {
			t.Fatalf("a first import of %d films ran %d pooled probes on %d sandboxes; want every probe pooled on at most %d sandboxes", films, probes, sandboxes, sandboxesPerScan)
		}
	} else {
		// No pool on this host: nothing may be pooled, and the probes keep
		// their pre-M4 cost, still with no other process per film.
		if probes != 0 || sandboxes != 0 {
			t.Fatalf("the probe pool is unavailable here, yet ran %d probes on %d sandboxes", probes, sandboxes)
		}
		perFile = unpooledProbesPerFile
	}
	if firstTotal > processesPerScan+perFile*films {
		t.Fatalf("a first import of %d films started %d processes, over the budget of %d per scan plus %d per film", films, firstTotal, processesPerScan, perFile)
	}
	if rescanTotal > processesPerScan+rescanProcessesPerFile*films {
		t.Fatalf("a rescan of %d unchanged films started %d processes, over the budget of %d per scan plus %d per film", films, rescanTotal, processesPerScan, rescanProcessesPerFile)
	}
}
