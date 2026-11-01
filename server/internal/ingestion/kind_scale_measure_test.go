package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	save := func() {
		report["seconds"] = time.Since(start).Seconds()
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
		f.s.processQuantum(context.Background(), job.ID, f.library)
		dbwork.PaceBackground(context.Background(), at)
		at = time.Now()
		if _, e := worker.Step(context.Background(), 500); e != nil {
			t.Fatal(e)
		}
		dbwork.PaceBackground(context.Background(), at)
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
		if n == 0 {
			break
		}
		dbwork.PaceBackground(context.Background(), at)
	}
	report["completed"] = true
	if got := f.count(`SELECT count(*) FROM catalog_assets`); got != n {
		t.Errorf("scanned assets=%d, expected %d", got, n)
	}
}
