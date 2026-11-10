package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// SearchReport is what the search mode measured. Times are milliseconds unless they say otherwise.
type SearchReport struct {
	FirstBuildSeconds     float64           `json:"firstBuildSeconds"`
	IndexBytes            int64             `json:"indexBytes"`
	SearchesIdle          Timing            `json:"searchesIdle"`
	Searchable            Timing            `json:"searchableAfterImportBatch"`
	SearchesDuringImport  Timing            `json:"searchesDuringImport"`
	ReadsDuringImport     map[string]Timing `json:"readsDuringImport"`
	WritesDuringImport    Timing            `json:"writesDuringImport"`
	RebuildSeconds        float64           `json:"rebuildSeconds"`
	SearchesDuringRebuild Timing            `json:"searchesDuringRebuild"`
	ReadsDuringRebuild    map[string]Timing `json:"readsDuringRebuild"`
	WritesDuringRebuild   Timing            `json:"writesDuringRebuild"`
	FailedSearches        int               `json:"failedSearches"`
}

// searchLoad is people searching: mostly every kind at once as they type, sometimes one kind,
// sometimes the suggestions under the field.
type searchLoad struct {
	d       *db.DB
	viewers []db.Viewer
	words   []string
	mu      sync.Mutex
	samples []time.Duration
	failed  int
}

// start runs n searchers until the returned function is called, which returns how long the
// searches took and how many failed.
func (s *searchLoad) start(ctx context.Context, n int) func() (Timing, int) {
	ctx, cancel := context.WithCancel(ctx)
	s.samples, s.failed = nil, 0
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			r := rand.New(rand.NewPCG(uint64(i), 77))
			for ctx.Err() == nil {
				word := []rune(pick(r, s.words))
				text := string(word[:min(len(word), 2+r.IntN(len(word)))])
				if r.IntN(4) == 0 {
					text += " " + pick(r, s.words)
				}
				v := pick(r, s.viewers)
				started := time.Now()
				var err error
				switch pick := r.IntN(10); {
				case pick < 7:
					_, err = s.d.Search(ctx, v, db.SearchQuery{Text: text})
				case pick < 9:
					_, err = s.d.Search(ctx, v, db.SearchQuery{Text: text, Kind: []string{"movie", "episode", "track", "person"}[r.IntN(4)]})
				default:
					_, err = s.d.SearchSuggestions(ctx, v, text, 8)
				}
				s.mu.Lock()
				if err != nil && ctx.Err() == nil {
					s.failed++
					println("search failed:", text, err.Error())
				}
				s.samples = append(s.samples, time.Since(started))
				s.mu.Unlock()
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
	return func() (Timing, int) {
		cancel()
		wg.Wait()
		return summarise(s.samples), s.failed
	}
}

// searchMode builds the index of the library from nothing, then imports more with search
// following (bar 7), then rebuilds the index while people read, write and search (bar 8).
func searchMode(ctx context.Context, d *db.DB, path string, cfg seed.Config, readers, writesPerSec int, report *Report) {
	viewers, err := d.SeedViewers(ctx)
	if err != nil || len(viewers) == 0 {
		log.Fatalf("the database has no seeded profiles (run portico-seed -phase state first): %v", err)
	}
	w, err := newWorkload(ctx, d, viewers)
	if err != nil {
		log.Fatal(err)
	}
	s := &searchLoad{d: d, viewers: viewers}
	for _, lib := range w.libs {
		for _, sort := range []string{db.SortTitle, db.SortAdded} {
			page, err := d.Items(ctx, viewers[0], db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{seed.LeafKind(lib.Kind)}, Sort: sort, Limit: 200})
			if err != nil {
				log.Fatal(err)
			}
			for _, item := range page.Items {
				s.words = append(s.words, strings.Fields(strings.ToLower(item.Title))...)
			}
		}
	}
	out := &SearchReport{}
	report.Search = out

	log.Print("building the search index from nothing")
	index := filepath.Join(filepath.Dir(path), "search.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		os.Remove(index + suffix)
	}
	started := time.Now()
	d.StartSearch(index, slog.Default())
	if err := d.SearchCaughtUp(ctx); err != nil {
		log.Fatal(err)
	}
	out.FirstBuildSeconds = time.Since(started).Seconds()
	if info, err := os.Stat(index); err == nil {
		out.IndexBytes = info.Size()
	}

	log.Print("searches with nothing else running")
	stopSearches := s.start(ctx, 2)
	time.Sleep(20 * time.Second)
	out.SearchesIdle, _ = stopSearches()

	log.Print("import with constant reads, writes and searches; how long until each batch is searchable")
	type mark struct {
		change int64
		at     time.Time
	}
	marks := make(chan mark, 1<<20)
	var lags []time.Duration
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		for m := range marks {
			if err := d.SearchReflects(ctx, m.change); err != nil {
				log.Fatal(err)
			}
			lags = append(lags, time.Since(m.at))
		}
	}()
	stop := w.start(ctx, readers, writesPerSec)
	stopSearches = s.start(ctx, 2)
	cfg.Seed += 3000
	last := time.Now()
	totals, err := seed.Import(ctx, d, cfg, func(t seed.Totals) {
		// The batch has just been committed: the clock starts here.
		at := time.Now()
		if head, err := d.ChangesHead(ctx); err == nil {
			marks <- mark{head, at}
		}
		if time.Since(last) > 15*time.Second {
			last = time.Now()
			log.Printf("%d files, %.0f files/s", t.Files, t.FilesPerSec)
		}
	})
	if err != nil {
		log.Fatal(err)
	}
	close(marks)
	<-watched
	out.ReadsDuringImport, out.WritesDuringImport = stop()
	var failed int
	out.SearchesDuringImport, failed = stopSearches()
	out.FailedSearches += failed
	out.Searchable = summarise(lags)
	// The last thing imported really is found, not only counted as indexed.
	lib := w.libs[len(w.libs)-1]
	newest, err := d.Items(ctx, viewers[0], db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{seed.LeafKind(lib.Kind)}, Sort: db.SortAdded, Desc: true, Limit: 1})
	if err != nil || len(newest.Items) == 0 {
		log.Fatalf("reading the newest title: %v", err)
	}
	found, err := d.Search(ctx, viewers[0], db.SearchQuery{Text: newest.Items[0].Title, Kind: seed.LeafKind(lib.Kind).String(), Limit: 200})
	if err != nil || !strings.Contains(fmt.Sprint(found), newest.Items[0].PublicID) {
		log.Fatalf("the newest title %q is not found by search: %v", newest.Items[0].Title, err)
	}

	log.Print("rebuilding the search index with constant reads, writes and searches")
	stop = w.start(ctx, readers, writesPerSec)
	stopSearches = s.start(ctx, 2)
	started = time.Now()
	if err := d.RebuildSearch(ctx); err != nil {
		log.Fatal(err)
	}
	out.RebuildSeconds = time.Since(started).Seconds()
	out.ReadsDuringRebuild, out.WritesDuringRebuild = stop()
	out.SearchesDuringRebuild, failed = stopSearches()
	out.FailedSearches += failed

	reads, writes := out.ReadsDuringRebuild["all"], out.WritesDuringRebuild
	report.bar("7 new title searchable, p99", "at most 5,000 ms", out.Searchable.P99, "ms", out.Searchable.P99 <= 5000)
	report.bar("8 page reads during a rebuild, p99", "at most 100 ms", reads.P99, "ms", reads.P99 <= 100)
	report.bar("8 slowest page read in a rebuild", "at most 1,000 ms", reads.Max, "ms", reads.Max <= 1000)
	report.bar("8 user writes during a rebuild, p99", "at most 25 ms", writes.P99, "ms", writes.P99 <= 25)
	report.bar("8 slowest user write in a rebuild", "at most 500 ms", writes.Max, "ms", writes.Max <= 500)
	report.bar("8 searches that failed", "0", float64(out.FailedSearches), "", out.FailedSearches == 0)
	line := func(name string, t Timing) {
		fmt.Printf("  %-34s n=%-7d p50 %8.2f ms  p99 %8.2f ms  max %8.2f ms\n", name, t.N, t.P50, t.P99, t.Max)
	}
	fmt.Printf("  first build of the index: %.1f s, %.0f MiB; %d files imported at %.0f files/s with search following; rebuild under use: %.1f s\n",
		out.FirstBuildSeconds, float64(out.IndexBytes)/mib, totals.Files, totals.FilesPerSec, out.RebuildSeconds)
	line("searchable after an import batch", out.Searchable)
	line("searches, nothing else running", out.SearchesIdle)
	line("searches during the import", out.SearchesDuringImport)
	line("searches during the rebuild", out.SearchesDuringRebuild)
	line("page reads during the import", out.ReadsDuringImport["all"])
	line("user writes during the import", out.WritesDuringImport)
	line("page reads during the rebuild", reads)
	line("user writes during the rebuild", writes)
}
