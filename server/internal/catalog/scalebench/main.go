// Command scalebench builds an isolated catalog and measures real catalog reads.
// Run from server: go run ./internal/catalog/scalebench -dir ../../.scratch/scale-1m -items 1000000
package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime/pprof"
	"sort"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

type report struct {
	Items               int           `json:"items"`
	SeedResumedFrom     int           `json:"seedResumedFrom,omitempty"`
	SeedSeconds         float64       `json:"seedSeconds"`
	BytesPerItem        float64       `json:"bytesPerItem"`
	FirstBatchMSPerItem float64       `json:"firstBatchMsPerItem"`
	LastBatchMSPerItem  float64       `json:"lastBatchMsPerItem"`
	VisibilityBuildSec  float64       `json:"visibilityBuildSeconds"`
	Routes              []routeResult `json:"routes"`
	// This process starts catalog services only. Full-server idle must be measured
	// by the server integration harness; do not confuse an unopened worker with idle.
	IdleScope               string `json:"idleScope"`
	IdleStatementsPerMinute uint64 `json:"idleStatementsPerMinute"`
}
type routeResult struct {
	Route         string  `json:"route"`
	Samples       int     `json:"samples"`
	P50MS         float64 `json:"p50Ms"`
	P95MS         float64 `json:"p95Ms"`
	MaxStatements uint64  `json:"maxStatements"`
	MaxVMSteps    int64   `json:"maxVmSteps,omitempty"`
	Error         string  `json:"error,omitempty"`
	RSSKB         int64   `json:"rssKb"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() (runErr error) {
	dir := flag.String("dir", "", "new isolated directory under .scratch")
	n := flag.Int("items", 1000000, "playable item count (1000000 or 5000000 for acceptance)")
	samples := flag.Int("samples", 20, "requests per route")
	reuse := flag.Bool("reuse", false, "measure an existing fixture without reseeding")
	resumeSeed := flag.Bool("resume-seed", false, "continue a previously interrupted seed from its last committed item")
	idle := flag.Duration("idle", time.Minute, "catalog-only idle observation interval")
	seedOnly := flag.Bool("seed-only", false, "seed and analyze, leaving reads to the interleaved comparison harness")
	profile := flag.Bool("profile", false, "print each route's three costliest statement shapes")
	explain := flag.String("explain", "", "print the scans and sorts in the plans of the routes whose name contains this")
	scenariosOnly := flag.Bool("scenarios-only", false, "measure search and write/rebuild scenarios without the browse matrix")
	matrixOnly := flag.Bool("matrix-only", false, "measure browse vocabulary without rebuild/write scenarios")
	copyFacts := flag.String("copy-facts", "", "copy only synthetic reference facts into a fresh baseline")
	narrowFacts := flag.Bool("narrow-facts", false, "use one genre per title and sparse credits for the bounded-disk mixed fixture")
	bulkFacts := flag.Bool("bulk-facts", false, "seed reference facts with set-based inserts, production triggers enabled")
	factsOnly := flag.Bool("facts-only", false, "seed facts without deriving; use -reuse -exercise to measure the empty rebuild")
	exercise := flag.Bool("exercise", false, "measure browse vocabulary and isolated write/rebuild scenarios")
	resumeRemoval := flag.Bool("resume-removal", false, "finish removal of the already retired synthetic Movies library")
	ingest := flag.Bool("ingest", false, "with -reuse: measure adding, changing and removing items on the fixture")
	ingestRemove := flag.Bool("ingest-removal", false, "with -ingest: also remove the Movies library")
	focus := flag.Int("focus-samples", 0, "repeat main browse and typing reads before write scenarios")
	only := flag.String("only", "", "measure only the routes whose name matches this regular expression")
	skipClasses := flag.Bool("skip-class-build", false, "reuse the restricted visibility classes already published in a reused fixture")
	cpuProfile := flag.String("cpuprofile", "", "write a CPU profile of the run to this file")
	flag.IntVar(&curveWindow, "curve-window", 0, "report seeding cost per item for every window of this many items")
	flag.IntVar(&curveShapes, "curve-shapes", 0, "with -curve-window, also report this many costliest statement shapes per window")
	flag.BoolVar(&interleaving, "interleave", false, "seed films, episodes and songs in a scattered order")
	seedStop := flag.Int("seed-stop", 0, "stop seeding after this many items (the shape stays that of -items); implies -seed-only")
	deriveOnly := flag.Bool("derive-only", false, "drain the derived worker from the fixture's current state, report its rate and stop")
	flag.DurationVar(&deriveBudget, "derive-for", 0, "stop a derive-only drain after this long (0: run to completion)")
	flag.BoolVar(&deriveNoProbes, "no-probes", false, "drain without concurrent browse/search probes")
	classes := flag.Bool("classes", false, "after a derive-only drain, build a PG-13 restriction class for each library and time it")
	bump := flag.Bool("bump", false, "after a derive-only drain, rebuild browse rows and search as a derivation version bump does, and drain again")
	trickle := flag.Int("trickle", 0, "after a derive-only drain, apply this many single-title metadata updates per kind, draining after each")
	writerCache := flag.Int("writer-cache-mib", 0, "experiment: give the writer connection this page cache (0: the production policy)")
	playlist := flag.Bool("playlist", false, "with -reuse: seed/reuse the large-playlist fixture and measure its display-order pages (G03)")
	playlistEntries := flag.Int("playlist-entries", 100000, "playlist members; >=100000 is the G03 acceptance fixture")
	playlistLimit := flag.Int("playlist-limit", 100, "playlist page size (1..100)")
	playlistBudget := flag.Int("playlist-page-budget", 0, "stop a page walk after this many pages (0 walks to the last page)")
	playlistReorders := flag.Int("playlist-reorders", 20, "concurrent-reorder writes to apply while readers page")
	playlistReaders := flag.Int("playlist-readers", 2, "concurrent readers during reorder")
	queryMemory := flag.Bool("query-memory", false, "with -reuse: run the query-memory/byte-accounting churn (G04)")
	queryPrepared := flag.Int("query-prepared", 5, "distinct prepared selection queries to churn")
	queryExpiry := flag.Duration("query-expiry-wait", 0, "wait this long to observe the facet TTL (>=1m); 0 records that it was not waited")
	flag.Parse()
	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			return err
		}
		if err = pprof.StartCPUProfile(f); err != nil {
			return err
		}
		defer func() { pprof.StopCPUProfile(); f.Close() }()
	}
	if *only != "" {
		pattern, err := regexp.Compile(*only)
		if err != nil {
			return fmt.Errorf("only: %w", err)
		}
		routeFilter = pattern
	}
	profileRoutes = *profile
	focusedSamples = *focus
	playlistExercise = playlistOptions{entries: *playlistEntries, limit: *playlistLimit, pageBudget: *playlistBudget, reorders: *playlistReorders, readers: *playlistReaders}
	queryMemoryExercise = queryMemoryOptions{preparedQueries: *queryPrepared, expiryWait: *queryExpiry}
	if focusedSamples < 0 || focusedSamples > 20 {
		return errors.New("focus-samples must be 0..20")
	}
	abs, err := filepath.Abs(*dir)
	if err != nil || *dir == "" || !strings.Contains(abs+string(os.PathSeparator), string(os.PathSeparator)+".scratch"+string(os.PathSeparator)) || *n < 100 || *samples < 1 || *idle <= 0 {
		return errors.New("invalid arguments: use an explicit .scratch directory, at least 100 items and positive samples/idle")
	}
	scratchDirectory = strings.SplitN(abs+string(os.PathSeparator), string(os.PathSeparator)+".scratch"+string(os.PathSeparator), 2)[0]
	if err = os.MkdirAll(abs, 0700); err != nil {
		return err
	}
	path := filepath.Join(abs, "catalog.sqlite")
	_, statErr := os.Stat(path)
	if interleaving && *resumeSeed {
		return errors.New("interleave cannot resume a seed")
	}
	if *reuse && *resumeSeed {
		return errors.New("reuse and resume-seed are mutually exclusive")
	}
	if !*reuse && !*resumeSeed && !errors.Is(statErr, os.ErrNotExist) {
		return errors.New("fixture already exists; use -reuse to measure it")
	}
	if (*reuse || *resumeSeed) && statErr != nil {
		return statErr
	}
	db, err := persistence.Open(path)
	if err != nil {
		return err
	}
	defer func() { db.Close() }()
	r := report{Items: *n, IdleScope: "catalog service only; full-server workers not started"}
	defer func() {
		if runErr != nil {
			raw, e := json.MarshalIndent(r, "", "  ")
			if e == nil {
				_ = os.WriteFile(filepath.Join(abs, "partial-report.json"), append(raw, '\n'), 0600)
			}
		}
	}()
	if !*reuse {
		if err = checkScratchBudget(); err != nil {
			return err
		}
		startItem := 0
		if *resumeSeed {
			if err = db.QueryRow(`SELECT count(*) FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1`).Scan(&startItem); err != nil {
				return err
			}
			if startItem < 0 || startItem > *n {
				return errors.New("resume fixture has no committed item batch or exceeds requested size")
			}
			fmt.Fprintf(os.Stderr, "resume seed %d/%d\n", startItem, *n)
			r.SeedResumedFrom = startItem
		}
		start := time.Now()
		if *copyFacts != "" {
			if *resumeSeed || !*factsOnly {
				return errors.New("copy-facts requires a new facts-only fixture")
			}
			r.FirstBatchMSPerItem, r.LastBatchMSPerItem, err = copyReferenceFacts(db, *copyFacts)
		} else if *bulkFacts {
			if *resumeSeed || !*factsOnly {
				return errors.New("bulk-facts requires a new facts-only fixture")
			}
			r.FirstBatchMSPerItem, r.LastBatchMSPerItem, err = seedBulkFactsDensity(db, *n, *narrowFacts)
		} else {
			stop := *n
			if *seedStop > 0 {
				stop = min(*n, *seedStop)
				*seedOnly = true
			}
			r.FirstBatchMSPerItem, r.LastBatchMSPerItem, err = seedReference(db, *n, startItem, stop, 32, !*factsOnly)
		}
		if err != nil {
			return err
		}
		r.SeedSeconds = time.Since(start).Seconds()
		if _, err = db.Exec("ANALYZE; PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			return err
		}
	}
	var pages, size int64
	if err = db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		return err
	}
	if err = db.QueryRow("PRAGMA page_size").Scan(&size); err != nil {
		return err
	}
	r.BytesPerItem = float64(pages*size) / float64(*n)
	if *seedOnly || *factsOnly {
		raw, e := json.MarshalIndent(r, "", "  ")
		if e != nil {
			return e
		}
		return os.WriteFile(filepath.Join(abs, "seed-report.json"), append(raw, '\n'), 0600)
	}
	s := catalog.New(db)
	if *ingest {
		if !*reuse {
			return errors.New("ingest requires reuse")
		}
		return ingestScenarios(db, s, *ingestRemove)
	}
	if *writerCache > 0 {
		if err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
			_, err := tx.Exec(fmt.Sprintf("PRAGMA cache_size=-%d", *writerCache*1024))
			return err
		}); err != nil {
			return err
		}
	}
	if *deriveOnly {
		rr, err := exerciseDrain(db, s, "derive-only")
		r.Routes = append(r.Routes, rr)
		if err == nil && *bump {
			if _, err = db.Exec(`UPDATE catalog_derivations SET version=version-1 WHERE domain IN(17,18)`); err != nil {
				return err
			}
			rr, err = exerciseDrain(db, s, "derive-version-bump")
			r.Routes = append(r.Routes, rr)
		}
		if err == nil && *classes {
			err = exerciseClasses(db, s)
		}
		if err == nil && *trickle > 0 {
			err = exerciseTrickle(db, *trickle)
		}
		return err
	}
	if *resumeRemoval {
		if !*reuse {
			return errors.New("resume-removal requires reuse")
		}
		removed, err := exerciseRemoval(db, s)
		r.Routes = append(r.Routes, removed)
		if err != nil {
			return err
		}
		raw, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(abs, "removal-report.json"), append(raw, '\n'), 0600)
	}
	if *exercise || *matrixOnly || *scenariosOnly || *playlist || *queryMemory {
		var rows, dirty int
		if err = db.QueryRow(`SELECT count(*) FROM catalog_browse_rows`).Scan(&rows); err != nil {
			return err
		}
		if err = db.QueryRow(`SELECT count(*) FROM catalog_dirty`).Scan(&dirty); err != nil {
			return err
		}
		if rows == 0 || dirty > 0 {
			rr, err := exerciseDrain(db, s, "derive-empty")
			r.Routes = append(r.Routes, rr)
			if err != nil {
				return err
			}
		}
	}
	if *playlist || *queryMemory {
		// G03/G04 runs are measured on their own so a small rehearsal does not
		// also pay for the whole browse matrix. Require a reused fixture.
		if !*reuse {
			return errors.New("playlist and query-memory exercises require -reuse")
		}
		if *playlist {
			report, err := exercisePlaylist(context.Background(), db, s, abs)
			if err != nil {
				return err
			}
			summary, _ := json.Marshal(report)
			fmt.Fprintln(os.Stderr, "PLAYLIST "+string(summary))
		}
		if *queryMemory {
			report, err := exerciseQueryMemory(context.Background(), db, s, abs)
			fmt.Fprintln(os.Stderr, "QUERY_MEMORY "+queryMemorySummary(report))
			if err != nil {
				return err
			}
		}
		raw, _ := json.MarshalIndent(r, "", "  ")
		return os.WriteFile(filepath.Join(abs, "report.json"), append(raw, '\n'), 0600)
	}
	if _, err = s.ClassifyPendingRatings(context.Background()); err != nil {
		return err
	}
	var rated int
	if err = db.QueryRow(`SELECT count(*) FROM catalog_item_attribute_edges x JOIN catalog_attribute_terms t ON t.id=x.term_id JOIN catalog_attribute_fields f ON f.id=t.field_id AND f.field='contentRating'`).Scan(&rated); err != nil {
		return err
	}
	if rated == 0 {
		return errors.New("scale fixture has no content ratings; reseed before measuring a restricted viewer")
	}
	movieLibrary, musicLibrary := "movies", "music"
	libraries := []string{"movies", "tv", "music"}
	pg13 := 13
	viewers := []struct {
		name string
		v    catalog.Viewer
	}{
		{"open", catalog.Viewer{Profile: "viewer", Fence: "scale-open", Libraries: libraries}},
		{"pg13", catalog.Viewer{Profile: "viewer-pg13", Fence: "scale-pg13", Libraries: libraries, Restrictions: identity.ContentRestrictions{MaximumAge: &pg13, BlockUnrated: true}}},
	}
	if _, err = db.Exec("ANALYZE"); err != nil {
		return err
	}
	stats, err := db.Query(`SELECT tbl,COALESCE(idx,''),stat FROM sqlite_stat1 WHERE tbl LIKE 'catalog_%' OR tbl LIKE 'compact_%' ORDER BY tbl,idx`)
	if err != nil {
		return err
	}
	for stats.Next() {
		var table, index, stat string
		if err = stats.Scan(&table, &index, &stat); err != nil {
			stats.Close()
			return err
		}
		fmt.Fprintf(os.Stderr, "STAT1 %s %s %s\n", table, index, stat)
	}
	err = stats.Err()
	stats.Close()
	if err != nil {
		return err
	}
	buildStart := time.Now()
	dbwork.WriteGate().ResetPeak()
	for _, library := range libraries {
		if *skipClasses {
			break
		}
		if err = s.RebuildVisibilityClass(context.Background(), library, viewers[1].v.EffectiveRestrictions()); err != nil {
			return fmt.Errorf("build %s visibility class: %w", library, err)
		}
	}
	r.VisibilityBuildSec = time.Since(buildStart).Seconds()
	fmt.Fprintf(os.Stderr, "VISIBILITY_BUILD seconds=%.3f max_gate_held_ms=%d\n", r.VisibilityBuildSec, dbwork.WriteGate().Stats().MaxHeldMilli)
	if err = db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		return err
	}
	r.BytesPerItem = float64(pages*size) / float64(*n)
	cases := []struct {
		name string
		read func(*catalog.Service) error
	}{}
	for _, entry := range viewers {
		viewer := entry.v
		prefix := entry.name + "/"
		cases = append(cases, struct {
			name string
			read func(*catalog.Service) error
		}{prefix + "home-manifest", func(s *catalog.Service) error {
			page, err := s.HomeManifest(catalog.HomeRequest{Viewer: viewer, Limit: 12})
			if err == nil && len(page.Rows) == 0 {
				return errors.New("empty home manifest")
			}
			return err
		}})
		cases = append(cases,
			struct {
				name string
				read func(*catalog.Service) error
			}{prefix + "home", func(s *catalog.Service) error {
				page, e := s.HomeRows(catalog.HomeRequest{Viewer: viewer, Limit: 12})
				if e == nil && len(page.Rows) == 0 {
					return errors.New("empty home projection")
				}
				return e
			}},
			struct {
				name string
				read func(*catalog.Service) error
			}{prefix + "home-topshelf", func(s *catalog.Service) error {
				// The Top Shelf feed: Home composed only until four rows have entries.
				page, e := s.HomeRows(catalog.HomeRequest{Viewer: viewer, Limit: 10, Rows: 4})
				if e == nil && len(page.Rows) == 0 {
					return errors.New("empty top shelf")
				}
				return e
			}},
			struct {
				name string
				read func(*catalog.Service) error
			}{prefix + "movies", func(s *catalog.Service) error {
				page, e := s.Content(catalog.ContentRequest{Viewer: viewer, Library: movieLibrary, View: "browse", Sort: "title", Limit: 40})
				if e == nil && (len(page.Sections) == 0 || len(page.Sections[0].Entries) == 0) {
					return errors.New("empty movie projection")
				}
				return e
			}},
			struct {
				name string
				read func(*catalog.Service) error
			}{prefix + "songs", func(s *catalog.Service) error {
				page, e := s.Content(catalog.ContentRequest{Viewer: viewer, Library: musicLibrary, View: "browse", Sort: "title", Limit: 40})
				if e == nil && (len(page.Sections) == 0 || len(page.Sections[0].Entries) == 0) {
					return errors.New("empty song projection")
				}
				return e
			}},
		)
		// Each Home row on its own, so a slow Home names its row.
		catalogue, e := s.HomeRowCatalogue(catalog.HomeRequest{Viewer: viewer, Limit: 12})
		if e != nil {
			return fmt.Errorf("home row catalogue: %w", e)
		}
		for _, row := range catalogue {
			id := row.ID
			cases = append(cases, struct {
				name string
				read func(*catalog.Service) error
			}{prefix + "home-row/" + id, func(s *catalog.Service) error {
				_, e := s.HomeSingleRow(catalog.HomeRequest{Viewer: viewer, Limit: 12}, id, catalog.HomeRowPage{Limit: 12})
				if errors.Is(e, catalog.ErrHomeRowUnknown) {
					return nil // listed for layout, not served to this viewer
				}
				return e
			}})
		}
		for _, mode := range []string{"ordered", "mix"} {
			mode := mode
			cases = append(cases, struct {
				name string
				read func(*catalog.Service) error
			}{prefix + "listening-" + mode, func(s *catalog.Service) error {
				selection, e := s.ListeningSelection(catalog.ContentRequest{Viewer: viewer, Library: musicLibrary, Limit: 40}, catalog.ListeningTarget{Kind: "library", ID: musicLibrary, LibraryID: musicLibrary}, mode, "fixture", false, "")
				if e == nil && len(selection.Entries) == 0 {
					return errors.New("empty listening projection")
				}
				return e
			}})
		}
	}
	for _, c := range cases {
		if !routeSelected(c.name) {
			continue
		}
		if *explain != "" && strings.Contains(c.name, *explain) {
			explainRoute(db, c.name, c.read, s)
		}
		rr := routeResult{Route: c.name}
		dbwork.ProfileStatements(*profile)
		var durations []float64
		for i := 0; i < *samples; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			ctx, cost := dbwork.Measure(ctx)
			before := dbwork.Reads().Statements
			start := time.Now()
			e := dbwork.WithReadSnapshot(ctx, db, func(ctx context.Context) error { return c.read(catalog.New(db).WithContext(ctx)) })
			elapsed := float64(time.Since(start)) / float64(time.Millisecond)
			cancel()
			rr.MaxVMSteps = max(rr.MaxVMSteps, cost().VMSteps)
			rr.MaxStatements = max(rr.MaxStatements, dbwork.Reads().Statements-before)
			durations = append(durations, elapsed)
			if e != nil {
				rr.Error = e.Error()
				break
			}
		}
		sort.Float64s(durations)
		rr.RSSKB = processRSSKB()
		rr.Samples = len(durations)
		if len(durations) > 0 {
			rr.P50MS = durations[(len(durations)-1)/2]
			rr.P95MS = durations[int(math.Ceil(.95*float64(len(durations))))-1]
		}
		r.Routes = append(r.Routes, rr)
		b, _ := json.Marshal(rr)
		fmt.Fprintln(os.Stderr, string(b))
		if *profile {
			for i, entry := range dbwork.StatementProfile() {
				if i == 3 {
					break
				}
				fmt.Fprintf(os.Stderr, "  %8.1f ms %5d× %s\n", entry.Millis(), entry.Count, entry.Shape)
			}
		}
	}
	if *exercise || *matrixOnly || *scenariosOnly {
		measured, err := exerciseCatalog(db, s, *samples, !*scenariosOnly, !*matrixOnly, *explain)
		r.Routes = append(r.Routes, measured...)
		if err != nil {
			return err
		}
	}
	before := dbwork.Reads().Statements
	time.Sleep(*idle)
	r.IdleStatementsPerMinute = uint64(float64(dbwork.Reads().Statements-before) * float64(time.Minute) / float64(*idle))
	raw, _ := json.MarshalIndent(r, "", "  ")
	fmt.Println(string(raw))
	return os.WriteFile(filepath.Join(abs, "report.json"), append(raw, '\n'), 0600)
}

func seed(db *sql.DB, n, startItem, stopItem int) (first, last float64, err error) {
	return seedReference(db, n, startItem, stopItem, 32, true)
}

// explainRoute runs one read of a route under a statement trace and prints
// the plan lines of each statement that scan a table or sort, with every
// parameter NULL (a plan, not a measurement).
func explainRoute(db *sql.DB, name string, read func(*catalog.Service) error, s *catalog.Service) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx, statements := dbwork.TraceQueryPlans(ctx)
	_ = dbwork.WithReadSnapshot(ctx, db, func(ctx context.Context) error { return read(s.WithContext(ctx)) })
	conn, err := db.Conn(context.Background())
	if err != nil {
		return
	}
	defer conn.Close()
	for _, text := range statements() {
		var lines []string
		_ = conn.Raw(func(raw any) error {
			prepared, err := raw.(driver.Conn).Prepare("EXPLAIN QUERY PLAN " + text)
			if err != nil {
				return err
			}
			defer prepared.Close()
			count := prepared.NumInput()
			if count < 0 {
				count = strings.Count(text, "?")
			}
			args := make([]driver.NamedValue, count)
			for i := range args {
				args[i] = driver.NamedValue{Ordinal: i + 1}
			}
			rows, err := prepared.(driver.StmtQueryContext).QueryContext(context.Background(), args)
			if err != nil {
				return err
			}
			defer rows.Close()
			values := make([]driver.Value, len(rows.Columns()))
			for rows.Next(values) == nil {
				if detail, ok := values[len(values)-1].(string); ok {
					lines = append(lines, detail)
				}
			}
			return nil
		})
		if len(lines) > 0 {
			fmt.Fprintf(os.Stderr, "EXPLAIN %s: %.300s\n  %s\n", name, strings.Join(strings.Fields(text), " "), strings.Join(lines, " | "))
		}
	}
}

// routeFilter limits a run to the named routes; profileRoutes prints each
// measured route's costliest statement shapes.
var (
	routeFilter   *regexp.Regexp
	profileRoutes bool
)

func routeSelected(name string) bool { return routeFilter == nil || routeFilter.MatchString(name) }

// exerciseClasses times a first restriction-class build per library, the
// work a restricted profile's first visit starts.
func exerciseClasses(db *sql.DB, s *catalog.Service) error {
	ctx := context.Background()
	if _, err := s.ClassifyPendingRatings(ctx); err != nil {
		return err
	}
	pg13 := 13
	r := identity.ContentRestrictions{MaximumAge: &pg13, BlockUnrated: true}
	// The server's maintenance loop keeps copying the log while a build writes.
	schedule, err := dbwork.NewCheckpointSchedule(ctx, db)
	if err != nil {
		return err
	}
	stop, done := make(chan struct{}), make(chan struct{})
	supervise.Go("scalebench log copies", func() {
		defer close(done)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				schedule.Tick(ctx)
			}
		}
	})
	defer func() { close(stop); <-done }()
	for _, library := range []string{"movies", "tv", "music"} {
		var rows int
		if err := db.QueryRow(`SELECT count(*) FROM catalog_browse_rows WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?)`, library).Scan(&rows); err != nil {
			return err
		}
		dbwork.WriteGate().ResetPeak()
		start := time.Now()
		if err := s.RebuildVisibilityClass(ctx, library, r); err != nil {
			return fmt.Errorf("build %s class: %w", library, err)
		}
		took := time.Since(start)
		fmt.Fprintf(os.Stderr, "CLASS_BUILD library=%s rows=%d seconds=%.3f rows_per_s=%.0f max_gate_held_ms=%d\n", library, rows, took.Seconds(), float64(rows)/took.Seconds(), dbwork.WriteGate().Stats().MaxHeldMilli)
	}
	return nil
}
