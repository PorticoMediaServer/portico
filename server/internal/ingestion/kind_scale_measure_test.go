package ingestion

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

// Per-kind RSS controls complement the active-user music scan. Each process
// starts fresh, scans real files, and settles the normal derived catalogue.
// file_list_only deliberately excludes ffprobe, providers and content decoding.
func TestKindScanMemoryMeasurement(t *testing.T) {
	kind := os.Getenv("PORTICO_KIND_SCAN")
	if kind == "" {
		t.Skip("opt-in hosted per-kind scan memory control")
	}
	n, _ := strconv.Atoi(os.Getenv("PORTICO_KIND_SCAN_ITEMS"))
	dir := os.Getenv("PORTICO_SCALE_REPORT")
	if n <= 0 || dir == "" {
		t.Fatal("positive item count and evidence directory required")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	f := newLoopFixture(t, kind)
	stopCheckpoint := scanMeasurementCheckpointer(t, f.db)
	defer stopCheckpoint()
	report := map[string]any{"kind": kind, "items": n, "cpus": runtime.NumCPU(), "go": runtime.Version(), "boundary": "real names-only filesystem scan plus settled derivation; no users, ffprobe, providers or document decoding", "rss_before_kb": scanLoadPeakRSS()}
	start := time.Now()
	var body = "media"
	if kind == "book" {
		body = string(readingtest.EPUB())
	} else if kind == "comic" {
		body = string(readingtest.CBZ())
	}
	for i := 0; i < n; i++ {
		var path string
		switch kind {
		case "movie":
			path = fmt.Sprintf("Film %06d (2000).mkv", i)
		case "tv":
			path = fmt.Sprintf("Show %06d/Season %02d/Show - S%02dE%02d.mkv", i/250, (i%250)/25+1, (i%250)/25+1, i%25+1)
		case "music":
			path = fmt.Sprintf("Artist %06d/Album %06d/%02d Track.mp3", i/130, i/13, i%13+1)
		case "audiobook":
			path = fmt.Sprintf("Author %06d/Book %06d/Part %02d.m4b", i/80, i/4, i%4+1)
		case "book":
			path = fmt.Sprintf("Author %06d/Series %06d/%04d - Book.epub", i/20, i/5, i)
		case "comic":
			path = fmt.Sprintf("Series %06d/%04d - Comic.cbz", i/20, i)
		default:
			t.Fatal("unsupported kind", kind)
		}
		f.write(path, body)
	}
	report["file_setup_seconds"] = time.Since(start).Seconds()
	job := f.queue()
	worker := compactcatalog.NewWorker(f.db)
	worker.PaceToHold(compactcatalog.BatchHoldTarget)
	report["gate_before"] = dbwork.WriteGate().Stats()
	dbwork.WriteGate().ResetPeak()
	start = time.Now()
	last := start
	phaseNS := map[string]int64{}
	var workerNS, pacingNS int64
	save := func() {
		report["seconds"] = time.Since(start).Seconds()
		report["phase_nanoseconds"], report["worker_nanoseconds"], report["pacing_nanoseconds"] = phaseNS, workerNS, pacingNS
		report["peak_rss_kb"] = scanLoadPeakRSS()
		report["gate_after"] = dbwork.WriteGate().Stats()
		report["write_plans"] = dbwork.Plans()
		report["assets"] = f.count(`SELECT count(*) FROM catalog_assets`)
		report["entities"] = f.count(`SELECT count(*) FROM catalog_entities`)
		report["publications"] = f.count(`SELECT count(*) FROM reading_publications`)
		state, e := f.s.Get(job.ID)
		report["job"], report["job_error"] = state, fmt.Sprint(e)
		b, _ := json.MarshalIndent(report, "", "  ")
		if e := os.WriteFile(filepath.Join(dir, "kind-scan.json"), b, 0600); e != nil {
			t.Error(e)
		}
	}
	defer func() {
		r := recover()
		if r != nil {
			report["panic"] = fmt.Sprint(r)
		}
		save()
		if r != nil {
			panic(r)
		}
	}()
	for {
		at := time.Now()
		var phase string
		if err := f.db.QueryRow(`SELECT phase FROM inventory_runs WHERE job_id=?`, job.ID).Scan(&phase); err != nil {
			t.Fatal(err)
		}
		f.s.processQuantum(context.Background(), job.ID, f.library)
		phaseNS[phase] += time.Since(at).Nanoseconds()
		paceStart := time.Now()
		dbwork.PaceBackground(context.Background(), at)
		pacingNS += time.Since(paceStart).Nanoseconds()
		at = time.Now()
		if _, e := worker.Step(context.Background(), 500); e != nil {
			t.Fatal(e)
		}
		workerNS += time.Since(at).Nanoseconds()
		paceStart = time.Now()
		dbwork.PaceBackground(context.Background(), at)
		pacingNS += time.Since(paceStart).Nanoseconds()
		state, e := f.s.Get(job.ID)
		if e != nil || state.Status == "failed" {
			t.Fatalf("scan state: %+v (%v)", state, e)
		}
		if time.Since(last) > 30*time.Second {
			save()
			last = time.Now()
		}
		if state.Status == "complete" || state.Status == "complete_with_warnings" {
			break
		}
	}
	report["scan_and_interleaved_derive_seconds"] = time.Since(start).Seconds()
	for {
		at := time.Now()
		n, e := worker.Step(context.Background(), 500)
		if e != nil {
			t.Fatal(e)
		}
		workerNS += time.Since(at).Nanoseconds()
		if n == 0 {
			break
		}
		paceStart := time.Now()
		dbwork.PaceBackground(context.Background(), at)
	}
	report["completed"] = true
	if got := f.count(`SELECT count(*) FROM catalog_assets`); got != n {
		t.Errorf("scanned assets=%d, expected %d", got, n)
	}
}

func scanLoadPeakRSS() int64 {
	b, _ := os.ReadFile("/proc/self/status")
	for _, line := range strings.Split(string(b), "\n") {
		var kb int64
		if _, e := fmt.Sscanf(line, "VmHWM: %d kB", &kb); e == nil {
			return kb
		}
	}
	return 0
}

func scanMeasurementCheckpointer(t *testing.T, db *sql.DB) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	schedule, e := dbwork.NewCheckpointSchedule(ctx, db)
	if e != nil {
		cancel()
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				schedule.Tick(ctx)
			}
		}
	}()
	return func() { cancel(); <-done }
}
