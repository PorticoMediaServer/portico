package main

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// Crowd is what the crowd run measured.
type Crowd struct {
	People         int     `json:"people"`
	Streams        int     `json:"streams"`
	Seconds        float64 `json:"seconds"`
	Progress       Timing  `json:"progressWrites"`
	Failures       int     `json:"failedRequests"`
	CPUCores       float64 `json:"cpuCoresUsedOnAverage"`
	ImportedFiles  int     `json:"filesImportedMeanwhile"`
	ImportPerSec   float64 `json:"importFilesPerSecond"`
	StreamedMiB    int64   `json:"mibReadByStreams"`
	RequestsPerSec float64 `json:"readsPerSecond"`
}

// crowd is an ordinary busy evening: people streaming by direct play, each sending its
// position every ten seconds and, if a file is given, reading a mebibyte of it a second, and
// people browsing with a second or two between requests, half of them as a restricted
// profile. An import runs through the middle half. All of them share the seeded profiles.
func crowd(ctx context.Context, d *db.DB, cfg seed.Config, people, streams int, length time.Duration, streamFile string, report *Report) {
	viewers, err := d.SeedViewers(ctx)
	if err != nil || len(viewers) < 4 {
		log.Fatalf("the database has no seeded profiles (run portico-seed -phase state first): %v", err)
	}
	w, err := newWorkload(ctx, d, viewers)
	if err != nil {
		log.Fatal(err)
	}
	w.countFailures = true
	w.reads = map[string][]time.Duration{}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	var streamed int64
	var streamedMu sync.Mutex
	open, restricted := len(viewers)-3, 3
	for n := range people - streams {
		wg.Go(func() {
			r := rand.New(rand.NewPCG(uint64(n), 7))
			profile := r.IntN(open)
			if n%2 == 1 {
				profile = open + r.IntN(restricted)
			}
			time.Sleep(time.Duration(r.Int64N(int64(2 * time.Second)))) // nobody arrives at the same instant
			w.browse(ctx, r, profile, func() time.Duration { return time.Second + time.Duration(r.Int64N(int64(time.Second))) })
		})
	}
	for n := range streams {
		wg.Go(func() {
			r := rand.New(rand.NewPCG(uint64(n), 8))
			v, item, play := viewers[r.IntN(len(viewers))], pick(r, w.leaves), fmt.Sprint("crowd-", n, "-", time.Now().UnixNano())
			var file *os.File
			if streamFile != "" {
				if file, err = os.Open(streamFile); err != nil {
					log.Fatal(err)
				}
				defer file.Close()
			}
			size, block := int64(0), make([]byte, 1<<20)
			if file != nil {
				info, _ := file.Stat()
				size = info.Size()
			}
			offset := r.Int64N(max(size, 1))
			time.Sleep(time.Duration(r.Int64N(int64(10 * time.Second))))
			started := time.Now()
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for second := 1; ; second++ {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
				}
				if file != nil {
					if offset+int64(len(block)) > size {
						offset = 0
					}
					read, _ := file.ReadAt(block, offset)
					offset += int64(read)
					streamedMu.Lock()
					streamed += int64(read)
					streamedMu.Unlock()
				}
				if second%10 != 0 {
					continue
				}
				// Ten minutes in already, so the position is past where a title counts as
				// started and every report is a real write.
				watched := time.Since(started)
				at := time.Now()
				_, err := d.SaveProgress(ctx, db.User, []db.Progress{{Viewer: v, ItemID: item, PlayID: play,
					PositionMS: (10*time.Minute + watched).Milliseconds(), At: at, WatchedMS: watched.Milliseconds()}})
				w.mu.Lock()
				if err != nil && ctx.Err() == nil {
					w.failures++
					println("progress failed:", err.Error())
				}
				w.writes = append(w.writes, time.Since(at))
				w.mu.Unlock()
			}
		})
	}

	begun, cpu := time.Now(), cpuTime()
	var imported seed.Totals
	time.Sleep(length / 4)
	log.Print("import begins")
	importCtx, stopImport := context.WithTimeout(ctx, length/2)
	cfg.Seed += 5000
	imported, _ = seed.Import(importCtx, d, cfg, nil)
	stopImport()
	log.Printf("import ended: %d files", imported.Files)
	if rest := length - time.Since(begun); rest > 0 {
		time.Sleep(rest)
	}
	elapsed, used := time.Since(begun), cpuTime()-cpu
	cancel()
	wg.Wait()

	reads, _ := summariseReads(w.reads)
	report.Reads = reads
	c := &Crowd{People: people, Streams: streams, Seconds: elapsed.Seconds(), Progress: summarise(w.writes), Failures: w.failures,
		CPUCores: used.Seconds() / elapsed.Seconds(), ImportedFiles: imported.Files, ImportPerSec: imported.FilesPerSec,
		StreamedMiB: streamed >> 20, RequestsPerSec: float64(reads["all"].N) / elapsed.Seconds()}
	report.Crowd = c
	for _, class := range sortedKeys(reads) {
		t := reads[class]
		fmt.Printf("  reads %-30s n=%-7d p50 %7.2f ms  p99 %8.2f ms  max %8.2f ms\n", class, t.N, t.P50, t.P99, t.Max)
	}
	fmt.Printf("  %d people for %.0f s: %d streaming, %d browsing; %.0f reads a second; %d failed requests\n",
		people, c.Seconds, streams, people-streams, c.RequestsPerSec, c.Failures)
	fmt.Printf("  progress writes: n=%d p50 %.2f ms  p99 %.2f ms  max %.2f ms\n", c.Progress.N, c.Progress.P50, c.Progress.P99, c.Progress.Max)
	fmt.Printf("  CPU: %.2f cores on average; streams read %d MiB; import meanwhile %d files at %.0f files/s\n",
		c.CPUCores, c.StreamedMiB, c.ImportedFiles, c.ImportPerSec)
	for _, who := range []string{" (unrestricted)", " (label)", " (library)", " (rating)"} {
		pages := reads["pages"+who]
		report.bar("crowd: pages"+who+", p99", "at most 100 ms", pages.P99, "ms", pages.P99 <= 100)
	}
	for _, who := range []string{" (label)", " (rating)"} {
		counts := reads["counts"+who]
		report.bar("crowd: counts"+who+", p99", "at most 300 ms", counts.P99, "ms", counts.P99 <= 300)
	}
	report.bar("crowd: progress writes, p99", "at most 25 ms", c.Progress.P99, "ms", c.Progress.P99 <= 25)
	report.bar("crowd: failed requests", "0", float64(c.Failures), "", c.Failures == 0)
}

// cpuTime is the processor time this process has used.
func cpuTime() time.Duration {
	var usage syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}
