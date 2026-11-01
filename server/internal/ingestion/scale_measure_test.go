package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
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
