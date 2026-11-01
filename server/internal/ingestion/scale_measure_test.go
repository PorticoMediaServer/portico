package ingestion

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// This deliberately measures the real names-only scanner, without pretending
// fixture generation includes enumeration, source frames or inventory writes.
func TestFilesystemScaleMeasurement(t *testing.T) {
	mode := os.Getenv("PORTICO_SCALE_SCAN")
	if mode == "" {
		t.Skip("opt-in hosted scan diagnostic")
	}
	albums, tracks := 20, 10
	films, shows := 100, 20
	if mode == "million" {
		albums, tracks = 80000, 13
		films, shows = 100000, 20000
	}
	if mode != "million" && mode != "smoke" {
		t.Fatal("unknown scan mode")
	}
	report := map[string]any{"mode": mode, "go": runtime.Version(), "cpus": runtime.NumCPU(), "albums": albums, "tracks": albums * tracks, "films": films, "shows": shows, "boundary": "real names-only enumeration and catalogue publication; no probing/provider calls"}
	save := func() {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		report["heap_mib"] = float64(m.HeapAlloc) / (1 << 20)
		b, _ := os.ReadFile("/proc/self/status")
		report["proc_status"] = string(b)
		raw, e := json.MarshalIndent(report, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		dir := os.Getenv("PORTICO_SCALE_REPORT")
		if dir != "" {
			if e = os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(dir, "scan.json"), raw, 0600); e != nil {
				t.Fatal(e)
			}
		}
		t.Log(string(raw))
	}
	fixtures := []struct {
		kind  string
		count int
	}{{"music", albums * tracks}, {"movie", films}, {"tv", shows}}
	for _, part := range fixtures {
		f := newLoopFixture(t, part.kind)
		start := time.Now()
		for n := 0; n < part.count; n++ {
			var relative string
			switch part.kind {
			case "music":
				relative = fmt.Sprintf("Artist %05d/Album %05d/%02d Track.mp3", n/tracks/10, n/tracks, n%tracks+1)
			case "movie":
				relative = fmt.Sprintf("Film %06d (2000).mkv", n)
			case "tv":
				relative = fmt.Sprintf("Show %06d (2000)/Season 01/Show %06d - S01E01.mkv", n, n)
			}
			f.write(relative, "media")
		}
		report[part.kind+"_file_setup_seconds"] = time.Since(start).Seconds()
		save()
		job := f.queue()
		worker := compactcatalog.NewWorker(f.db)
		scanStart := time.Now()
		var scanTime, deriveTime time.Duration
		turns, units := 0, 0
		last := time.Now()
		for {
			at := time.Now()
			f.s.processQuantum(context.Background(), job.ID, f.library)
			scanTime += time.Since(at)
			at = time.Now()
			n, e := worker.Step(context.Background(), 500)
			deriveTime += time.Since(at)
			if e != nil {
				t.Fatal(e)
			}
			units += n
			turns++
			state, e := f.s.Get(job.ID)
			if e != nil {
				t.Fatal(e)
			}
			if time.Since(last) > 30*time.Second {
				report[part.kind+"_partial"] = map[string]any{"seconds": time.Since(scanStart).Seconds(), "scan_seconds": scanTime.Seconds(), "derive_seconds": deriveTime.Seconds(), "turns": turns, "units": units, "job": state}
				save()
				last = time.Now()
			}
			if state.Status == "failed" {
				report[part.kind+"_failed"] = state
				save()
				t.Fatal(state)
			}
			if state.Status == "complete" || state.Status == "complete_with_warnings" {
				break
			}
		}
		report[part.kind+"_scan_seconds"] = scanTime.Seconds()
		report[part.kind+"_scan_wall_seconds"] = time.Since(scanStart).Seconds()
		report[part.kind+"_interleaved_derive_seconds"] = deriveTime.Seconds()
		at := time.Now()
		if e := compactcatalog.Drain(context.Background(), f.db); e != nil {
			t.Fatal(e)
		}
		report[part.kind+"_final_derive_seconds"] = time.Since(at).Seconds()
		report[part.kind+"_entities"] = f.count(`SELECT count(*) FROM catalog_entities WHERE retired=0`)
		report[part.kind+"_assets"] = f.count(`SELECT count(*) FROM catalog_assets WHERE available=1`)
		save()
	}
}

// Fixed histograms bound the harness's own memory during the 300k scan. User
// measurements call the real catalogue service, while the separate HTTP load
// experiment includes routing, authentication and admission.
type scanCurveHistogram struct {
	Buckets              [15002]uint64
	N, Errors, Cancelled uint64
}

func (h *scanCurveHistogram) add(d time.Duration, err error, cancelled bool) {
	h.N++
	if err != nil {
		if cancelled {
			h.Cancelled++
		} else {
			h.Errors++
		}
	}
	bucket := min(15001, int(d/(2*time.Millisecond)))
	h.Buckets[bucket]++
}
func (h *scanCurveHistogram) summary() map[string]any {
	q := func(p uint64) int {
		target := (h.N*p + 99) / 100
		var sum uint64
		for i, n := range h.Buckets {
			sum += n
			if sum >= target {
				return i * 2
			}
		}
		return 30002
	}
	return map[string]any{"n": h.N, "errors": h.Errors, "harness_cancellations": h.Cancelled, "p50_ms_upper": q(50) + 2, "p95_ms_upper": q(95) + 2, "p99_ms_upper": q(99) + 2, "histogram_resolution_ms": 2}
}
func TestScanUnderUserLoadMeasurement(t *testing.T) {
	count, _ := strconv.Atoi(os.Getenv("PORTICO_SCAN_GROWTH_ITEMS"))
	if count == 0 {
		t.Skip("opt-in hosted scan growth and contention diagnostic")
	}
	dir := os.Getenv("PORTICO_SCALE_REPORT")
	if dir == "" {
		t.Fatal("explicit evidence directory required")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	f := newLoopFixture(t, "music")
	c := catalogtest.New(t, f.db)
	baseRoot := t.TempDir()
	library := c.Library("baseline", "Baseline", "movie", baseRoot)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < 1000; i++ {
			path := filepath.Join(baseRoot, fmt.Sprintf("Film %06d.mp4", i))
			id, _, e := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: path, Title: fmt.Sprintf("Film %06d", i), Added: time.Unix(1700000000+int64(i)*60, 0).UTC().Format(time.RFC3339Nano)})
			if e != nil {
				return e
			}
			asset, _, e := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Duration: 3600})
			if e != nil {
				return e
			}
			if e = compactcatalog.LinkAssetTx(ctx, tx, id, asset, compactcatalog.Link{}); e != nil {
				return e
			}
		}
		return nil
	})
	c.Drain()
	var item string
	if e := f.db.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE kind=1 LIMIT 1`).Scan(&item); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	for n := 0; n < count; n++ {
		f.write(fmt.Sprintf("Artist %06d/Album %06d/%02d Track.mp3", n/130, n/13, n%13+1), "media")
	}
	setup := time.Since(start).Seconds()
	type window struct {
		mu sync.Mutex
		h  map[string]*scanCurveHistogram
	}
	w := window{h: map[string]*scanCurveHistogram{}}
	var phase atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	production := os.Getenv("PORTICO_SCAN_PRODUCTION") == "1"
	shared := catalog.New(f.db)
	users := 32
	if count <= 300 && !production {
		users = 4
	}
	for u := 0; u < users; u++ {
		wg.Add(1)
		go func(u int) {
			defer wg.Done()
			v := catalog.Viewer{Profile: fmt.Sprintf("scan-profile-%d", u), Fence: fmt.Sprintf("scan-profile-%d", u), Libraries: []string{"baseline"}}
			s := shared.WithContext(ctx)
			for round := 0; ctx.Err() == nil; round++ {
				p := phase.Load()
				at := time.Now()
				name := ""
				var e error
				switch (round + u) % 4 {
				case 0:
					name = "home"
					_, e = s.HomeRows(catalog.HomeRequest{Viewer: v, ServerID: "s", Profile: v.Profile, ViewerFence: v.Fence, Libraries: v.Libraries, Now: time.Now()})
				case 1:
					name = "browse"
					_, e = s.BrowseEntities(v, catalog.BrowseRequest{Library: "baseline", Profile: v.Profile, ViewerFence: v.Fence, Pivot: "movies", Limit: 40})
				case 2:
					name = "search"
					_, e = s.Search(ctx, catalog.SearchRequest{Viewer: v, Profile: v.Profile, ViewerFence: v.Fence, Libraries: v.Libraries, Q: "Film", Limit: 12})
				case 3:
					name = "detail"
					_, e = s.DetailWithRelated(v, "s", item, false, false)
				}
				key := fmt.Sprintf("%d/%s", p, name)
				w.mu.Lock()
				h := w.h[key]
				if h == nil {
					h = &scanCurveHistogram{}
					w.h[key] = h
				}
				h.add(time.Since(at), e, ctx.Err() != nil)
				w.mu.Unlock()
				select {
				case <-ctx.Done():
					return
				case <-time.After(250 * time.Millisecond):
				}
			}
		}(u)
	}
	defer func() { cancel(); wg.Wait() }()
	report := map[string]any{"production_pacing": production, "shared_service": true, "items": count, "users": users, "users_per_core": float64(users) / float64(runtime.NumCPU()), "cpus": runtime.NumCPU(), "file_setup_seconds": setup, "boundary": "real scanner plus foreground catalogue service; HTTP/auth measured separately"}
	save := func() {
		w.mu.Lock()
		summary := map[string]any{}
		for key, h := range w.h {
			summary[key] = h.summary()
		}
		w.mu.Unlock()
		report["endpoints"] = summary
		raw, _ := json.MarshalIndent(report, "", "  ")
		if e := os.WriteFile(filepath.Join(dir, "scan-load.json"), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	baselineFor := 20 * time.Second
	if count <= 300 {
		baselineFor = time.Second
	}
	time.Sleep(baselineFor)
	phase.Store(1)
	dbwork.WriteGate().ResetPeak()
	report["gate_before"] = dbwork.WriteGate().Stats()
	job := f.queue()
	worker := compactcatalog.NewWorker(f.db)
	if production {
		worker.PaceToHold(compactcatalog.BatchHoldTarget)
	}
	var cpu *os.File
	if production {
		cpu, _ = os.Create(filepath.Join(dir, "scan-load.pprof"))
		if cpu != nil {
			if e := pprof.StartCPUProfile(cpu); e != nil {
				t.Fatal(e)
			}
			defer func() { pprof.StopCPUProfile(); cpu.Close() }()
		}
	}
	scanStart := time.Now()
	last := scanStart
	var scan, derive time.Duration
	for {
		at := time.Now()
		f.s.processQuantum(context.Background(), job.ID, f.library)
		scan += time.Since(at)
		if production {
			dbwork.PaceBackground(context.Background(), at)
		}
		at = time.Now()
		_, e := worker.Step(context.Background(), 500)
		derive += time.Since(at)
		if production {
			dbwork.PaceBackground(context.Background(), at)
		}
		if e != nil {
			t.Fatal(e)
		}
		state, e := f.s.Get(job.ID)
		if e != nil {
			t.Fatal(e)
		}
		if time.Since(last) > 30*time.Second {
			report["partial_job"] = state
			report["scan_wall_seconds"] = time.Since(scanStart).Seconds()
			report["peak_rss_kb"] = scanLoadPeakRSS()
			save()
			last = time.Now()
		}
		if state.Status == "failed" {
			report["failed_job"] = state
			save()
			t.Fatal(state)
		}
		if state.Status == "complete" || state.Status == "complete_with_warnings" {
			break
		}
	}
	report["scan_seconds"] = scan.Seconds()
	report["interleaved_derive_seconds"] = derive.Seconds()
	at := time.Now()
	if production {
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
	} else if e := compactcatalog.Drain(context.Background(), f.db); e != nil {
		t.Fatal(e)
	}
	report["final_derive_seconds"] = time.Since(at).Seconds()
	report["scan_wall_seconds"] = time.Since(scanStart).Seconds()
	report["peak_rss_kb"] = scanLoadPeakRSS()
	report["added_assets"] = f.count(`SELECT count(*) FROM catalog_assets a JOIN catalog_asset_links l ON l.asset_id=a.id JOIN catalog_entities e ON e.id=l.entity_id JOIN catalog_libraries cl ON cl.id=e.library_id WHERE cl.library_id=?`, f.library)
	report["gate_after"] = dbwork.WriteGate().Stats()
	save()
	phase.Store(2)
	time.Sleep(baselineFor)
	cancel()
	wg.Wait()
	save()
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

// Same scanner and catalogue calls with normal production batch holds/pacing.
func TestPacedScanUnderUserLoadMeasurement(t *testing.T) {
	raw := os.Getenv("PORTICO_PACED_SCAN_ITEMS")
	if raw == "" {
		t.Skip("opt-in production-paced control")
	}
	t.Setenv("PORTICO_SCAN_GROWTH_ITEMS", raw)
	t.Setenv("PORTICO_SCAN_PRODUCTION", "1")
	TestScanUnderUserLoadMeasurement(t)
}
