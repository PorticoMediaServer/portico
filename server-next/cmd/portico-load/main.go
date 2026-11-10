// Command portico-load measures the database against the plan's bars: first import rate,
// unchanged rescan rate, page reads and user writes while an import runs, whether the import
// keeps moving under constant user activity, and memory and WAL size. It prints a short text
// summary and writes one JSON file. It is a diagnostic: a missed bar is a number to explain,
// not a failed build.
//
//	portico-load -mode import -db main.db -items 1000000 -out import.json
//	portico-load -mode rescan -db main.db -items 1000000 -out rescan.json
//	portico-load -mode mixed  -db main.db -items 100000  -out mixed.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

const mib = 1 << 20

// Bar is one of the plan's bars with what was measured.
type Bar struct {
	Bar    string  `json:"bar"`
	Limit  string  `json:"limit"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`
	Passed bool    `json:"passed"`
}

// Report is what one run measured.
type Report struct {
	Mode         string            `json:"mode"`
	Items        int               `json:"items"`
	Import       *seed.Totals      `json:"import,omitempty"`
	LoadedImport *seed.Totals      `json:"loadedImport,omitempty"`
	Reads        map[string]Timing `json:"reads,omitempty"`
	ReadsIdle    map[string]Timing `json:"readsIdle,omitempty"`
	WritesIdle   *Timing           `json:"writesIdle,omitempty"`
	Writes       *Timing           `json:"writes,omitempty"`
	PeakRSSBytes int64             `json:"peakRssBytes"`
	WALMaxBytes  int64             `json:"walMaxBytes"`
	WALEndBytes  int64             `json:"walBytesAMinuteAfterImport"`
	Bars         []Bar             `json:"bars"`
	// DiskSync is a bare 4 KiB append and sync beside the database, at the rate of the user
	// writes and at the same time as them: what the disk alone gave a small write.
	DiskSync     *Timing `json:"diskSync,omitempty"`
	DiskSyncIdle *Timing `json:"diskSyncIdle,omitempty"`
	dir          string
}

func (r *Report) bar(name, limit string, value float64, unit string, passed bool) {
	r.Bars = append(r.Bars, Bar{name, limit, value, unit, passed})
}

func main() {
	mode := flag.String("mode", "", "import, rescan or mixed")
	path := flag.String("db", "main.db", "database file")
	items := flag.Int("items", 20_000, "playable items: the library size for import and rescan, the size of each extra import for mixed")
	seedValue := flag.Uint64("seed", 1, "seed of the library")
	batch := flag.Int("batch", 40, "files per import transaction")
	batchTime := flag.Duration("batch-time", 0, "size import transactions to take this long instead (-batch is then where the size starts)")
	readers := flag.Int("readers", 4, "mixed: goroutines reading pages")
	writesPerSec := flag.Int("writes", 20, "mixed: user writes a second")
	settle := flag.Duration("settle", time.Minute, "how long after the import to watch the WAL")
	out := flag.String("out", "", "JSON report file")
	noSync := flag.Bool("no-sync", false, "commit without syncing to disk, to time everything but the disk")
	flag.Parse()
	log.SetFlags(log.Ltime)

	ctx := context.Background()
	d, err := db.Open(ctx, *path)
	if err != nil {
		log.Fatal(err)
	}
	defer d.Close()
	if *noSync {
		d.SkipDiskSync()
	}
	cfg := seed.Config{Seed: *seedValue, Items: *items, BatchFiles: *batch, BatchTime: *batchTime}
	report := &Report{Mode: *mode, Items: *items}
	wal := watchWAL(*path + "-wal")

	switch *mode {
	case "import":
		totals := mustImport(ctx, d, cfg)
		report.Import = &totals
		report.bar("1 first import", "at least 300 files/s", totals.FilesPerSec, "files/s", totals.FilesPerSec >= 300)
		report.WALEndBytes = wal.settle(*settle)
	case "rescan":
		totals := mustImport(ctx, d, cfg)
		report.Import = &totals
		unchanged := totals.RowsWritten == 0 && totals.ItemsNew == 0
		report.bar("2 unchanged rescan", "at least 5,000 files/s", totals.FilesPerSec, "files/s", totals.FilesPerSec >= 5000)
		report.bar("2 rows changed by a rescan", "0", float64(totals.RowsWritten), "rows", unchanged)
	case "mixed":
		report.dir = filepath.Dir(*path)
		mixed(ctx, d, cfg, *readers, *writesPerSec, *settle, wal, report)
	default:
		log.Fatal("-mode must be import, rescan or mixed")
	}

	report.PeakRSSBytes, report.WALMaxBytes = peakRSS(), wal.max.Load()
	if *mode != "rescan" {
		report.bar("6 process memory", "at most 1 GiB", float64(report.PeakRSSBytes)/mib, "MiB", report.PeakRSSBytes <= 1024*mib)
		report.bar("6 largest WAL", "at most 512 MiB", float64(report.WALMaxBytes)/mib, "MiB", report.WALMaxBytes <= 512*mib)
		report.bar("6 WAL a minute after the import", "under 64 MiB", float64(report.WALEndBytes)/mib, "MiB", report.WALEndBytes < 64*mib)
	}
	for _, b := range report.Bars {
		mark := "MISSED"
		if b.Passed {
			mark = "ok"
		}
		fmt.Printf("%-6s bar %-34s %12.1f %-8s (%s)\n", mark, b.Bar, b.Value, b.Unit, b.Limit)
	}
	if *out != "" {
		raw, _ := json.MarshalIndent(report, "", "  ")
		if err := os.WriteFile(*out, raw, 0o644); err != nil {
			log.Fatal(err)
		}
	}
}

func mustImport(ctx context.Context, d *db.DB, cfg seed.Config) seed.Totals {
	last := time.Now()
	totals, err := seed.Import(ctx, d, cfg, func(t seed.Totals) {
		if time.Since(last) > 15*time.Second {
			last = time.Now()
			log.Printf("%d files, %.0f files/s", t.Files, t.FilesPerSec)
		}
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("%d files in %s: %.0f files/s, %d rows written", totals.Files, totals.Elapsed.Round(time.Second), totals.FilesPerSec, totals.RowsWritten)
	return totals
}

// mixed imports one extra library with nobody using the server, then another while readers
// page through lists and a writer marks titles, and compares.
func mixed(ctx context.Context, d *db.DB, cfg seed.Config, readers, writesPerSec int, settle time.Duration, wal *walWatch, report *Report) {
	viewers, err := d.SeedViewers(ctx)
	if err != nil || len(viewers) == 0 {
		log.Fatalf("the database has no seeded profiles (run portico-seed -phase state first): %v", err)
	}
	w, err := newWorkload(ctx, d, viewers)
	if err != nil {
		log.Fatal(err)
	}

	// The floor under every user write: one small transaction and its disk sync, nothing else running.
	log.Print("writes with nothing else running")
	warm := w.start(ctx, 0, writesPerSec) // a few first, uncounted, so the pages they touch are in memory
	time.Sleep(3 * time.Second)
	warm()
	probe := diskProbe(report.dir, writesPerSec)
	stop := w.start(ctx, 0, writesPerSec)
	time.Sleep(30 * time.Second)
	_, idleWrites := stop()
	idleSync, _ := probe()
	report.WritesIdle, report.DiskSyncIdle = &idleWrites, &idleSync

	log.Print("reads with nothing else running")
	stop = w.start(ctx, readers, 0)
	time.Sleep(30 * time.Second)
	idleReads, _ := stop()
	report.ReadsIdle = idleReads

	log.Print("import with nobody using the server")
	cfg.Seed += 1000
	idle := mustImport(ctx, d, cfg)
	report.Import = &idle

	log.Print("import with constant reads and writes")
	cfg.Seed += 1000
	probe = diskProbe(report.dir, writesPerSec)
	stop = w.start(ctx, readers, writesPerSec)
	loaded := mustImport(ctx, d, cfg)
	report.LoadedImport = &loaded
	report.WALEndBytes = wal.settle(settle) // people keep using the server while the WAL settles
	reads, writes := stop()
	loadedSync, syncSpans := probe()
	report.DiskSync = &loadedSync
	slow, withDisk := slowTogether(w.spans, syncSpans, 50*time.Millisecond)
	report.Reads, report.Writes = reads, &writes

	all := reads["all"]
	report.bar("3 page reads during import, p99", "at most 100 ms", all.P99, "ms", all.P99 <= 100)
	report.bar("3 slowest page read during import", "at most 1,000 ms", all.Max, "ms", all.Max <= 1000)
	report.bar("4 user writes during import, p99", "at most 25 ms", writes.P99, "ms", writes.P99 <= 25)
	report.bar("4 slowest user write during import", "at most 500 ms", writes.Max, "ms", writes.Max <= 500)
	kept := loaded.FilesPerSec / idle.FilesPerSec
	report.bar("5 import rate kept under activity", "at least 0.5 of idle", kept, "of idle", kept >= 0.5)
	for _, class := range sortedKeys(reads) {
		t := reads[class]
		fmt.Printf("  reads %-24s n=%-7d p50 %7.2f ms  p99 %8.2f ms  max %8.2f ms   (idle p99 %.2f ms)\n", class, t.N, t.P50, t.P99, t.Max, idleReads[class].P99)
	}
	fmt.Printf("  writes               n=%-7d p50 %7.2f ms  p99 %8.2f ms  max %8.2f ms\n", writes.N, writes.P50, writes.P99, writes.Max)
	fmt.Printf("  writes, nothing else running: n=%d p50 %.2f ms  p90 %.2f ms  p99 %.2f ms  max %.2f ms (one commit and its disk sync)\n",
		idleWrites.N, idleWrites.P50, idleWrites.P90, idleWrites.P99, idleWrites.Max)
	fmt.Printf("  writes during import: p90 %.2f ms\n", writes.P90)
	fmt.Printf("  bare 4 KiB sync beside the database, nothing else running: p50 %.2f ms  p90 %.2f ms  p99 %.2f ms  max %.2f ms\n",
		idleSync.P50, idleSync.P90, idleSync.P99, idleSync.Max)
	fmt.Printf("  bare 4 KiB sync beside the database, during import: n=%d p50 %.2f ms  p90 %.2f ms  p99 %.2f ms  max %.2f ms\n",
		loadedSync.N, loadedSync.P50, loadedSync.P90, loadedSync.P99, loadedSync.Max)
	fmt.Printf("  writes over 50 ms during import: %d, of which %d while a bare sync also took over 50 ms\n", slow, withDisk)
	fmt.Printf("  import transactions ended at %d files each\n", loaded.LastBatchFiles)
	fmt.Printf("  import: %.0f files/s idle, %.0f files/s under activity\n", idle.FilesPerSec, loaded.FilesPerSec)
}

// walWatch samples the WAL file's size.
type walWatch struct {
	path string
	max  atomic.Int64
}

func watchWAL(path string) *walWatch {
	w := &walWatch{path: path}
	go func() {
		for range time.Tick(100 * time.Millisecond) {
			w.size()
		}
	}()
	return w
}

func (w *walWatch) size() int64 {
	info, err := os.Stat(w.path)
	if err != nil {
		return 0
	}
	for size := info.Size(); ; {
		if old := w.max.Load(); size <= old || w.max.CompareAndSwap(old, size) {
			return size
		}
	}
}

// settle waits up to the given time for the WAL to fall under 64 MiB and returns its size.
func (w *walWatch) settle(limit time.Duration) int64 {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) && w.size() >= 64*mib {
		time.Sleep(time.Second)
	}
	return w.size()
}

// peakRSS is the most memory the process has held, as the kernel counts it.
func peakRSS() int64 {
	status, _ := os.ReadFile("/proc/self/status")
	for _, line := range strings.Split(string(status), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "VmHWM:" {
			kb, _ := strconv.ParseInt(fields[1], 10, 64)
			return kb * 1024
		}
	}
	return 0
}
