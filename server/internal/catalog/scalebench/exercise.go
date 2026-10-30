package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/eventfeed"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

func processRSSKB() int64 {
	raw, _ := os.ReadFile("/proc/self/status")
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			n, _ := strconv.ParseInt(fields[1], 10, 64)
			return n
		}
	}
	return 0
}

func logMeasurement(r routeResult) { raw, _ := json.Marshal(r); fmt.Fprintln(os.Stderr, string(raw)) }

func exerciseRead(db *sql.DB, s *catalog.Service, name string, samples int, read func(context.Context, *catalog.Service) error) (routeResult, error) {
	r := routeResult{Route: name}
	durations := make([]float64, 0, samples)
	dbwork.ProfileStatements(profileRoutes)
	defer printRouteProfile()
	for i := 0; i < samples; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		ctx, cost := dbwork.Measure(ctx)
		before := dbwork.Reads().Statements
		start := time.Now()
		err := dbwork.WithReadSnapshot(ctx, db, func(ctx context.Context) error { return read(ctx, s.WithContext(ctx)) })
		cancel()
		r.MaxVMSteps = max(r.MaxVMSteps, cost().VMSteps)
		durations = append(durations, float64(time.Since(start))/float64(time.Millisecond))
		r.RSSKB = max(r.RSSKB, processRSSKB())
		r.MaxStatements = max(r.MaxStatements, dbwork.Reads().Statements-before)
		if err != nil {
			r.Error = err.Error()
			break
		}
	}
	sort.Float64s(durations)
	r.Samples = len(durations)
	r.P50MS = durations[(len(durations)-1)/2]
	r.P95MS = durations[(len(durations)*95+99)/100-1]
	logMeasurement(r)
	return r, nil
}

// Reads are probed at frequent commit checkpoints. The worker uses the server's
// batch size; this tool intentionally measures catalogue services, not HTTP.
// deriveBudget stops a derive-only drain early; the rate up to then is the
// measurement. deriveNoProbes leaves out the concurrent read probes.
var (
	deriveBudget   time.Duration
	deriveNoProbes bool
)

// pendingByDomain is the queued work per derived domain and job table, for
// the drain log: where a rebuild's remaining work is.
func pendingByDomain(db *sql.DB) string {
	rows, err := db.Query(`SELECT domain,count(*) FROM catalog_dirty GROUP BY domain`)
	if err != nil {
		return err.Error()
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var domain, n int
		if rows.Scan(&domain, &n) == nil {
			parts = append(parts, fmt.Sprintf("%d:%d", domain, n))
		}
	}
	return strings.Join(parts, ",")
}

func exerciseDrain(db *sql.DB, s *catalog.Service, name string, queries ...string) (routeResult, error) {
	ctx := context.Background()
	w := compactcatalog.NewWorker(db)
	w.PaceToHold(compactcatalog.BatchHoldTarget) // as the server's worker runs
	dbwork.ProfileStatements(true)
	defer dbwork.ProfileStatements(false)
	r := routeResult{Route: name}
	start := time.Now()
	logAt := time.Now()
	schedule, err := dbwork.NewCheckpointSchedule(ctx, db)
	if err != nil {
		return r, err
	}
	v := catalog.Viewer{Profile: "viewer", Fence: "scale-open", Libraries: []string{"movies", "tv", "music"}}
	var probes, failedProbes int
	firstVisible := time.Duration(-1)
	var maxRead time.Duration
	stop, done := make(chan struct{}), make(chan struct{})
	// Requests run alongside the worker, rather than pausing derivation for
	// every checkpoint. They use the real read snapshot/admission machinery.
	supervise.Go("scalebench read probes", func() {
		defer close(done)
		for !deriveNoProbes {
			select {
			case <-stop:
				return
			default:
			}
			readStart := time.Now()
			probe, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := dbwork.WithReadSnapshot(probe, db, func(ctx context.Context) error {
				service := s.WithContext(ctx)
				if _, err := service.BrowseEntities(v, catalog.BrowseRequest{Library: "movies", Pivot: "movies", Limit: 40}); err != nil {
					return err
				}
				query := "Title 000000"
				if len(queries) > 0 {
					query = queries[0]
				}
				page, err := service.Search(ctx, catalog.SearchRequest{Groups: []string{"movies", "shows", "episodes", "artists", "albums", "songs", "books", "collections", "authors", "people"}, Viewer: v, Q: query, Limit: 12})
				if err != nil {
					return err
				}
				for _, group := range page.Groups {
					if group.ErrorCode != "" {
						failedProbes++
						fmt.Fprintf(os.Stderr, "PROBE_GROUP_ERROR %s group=%s code=%s\n", name, group.ID, group.ErrorCode)
						continue
					}
					if name == "derive-added-1000" && firstVisible < 0 && group.ID == "movies" && len(group.Items) > 0 {
						firstVisible = time.Since(start)
					}
				}
				return nil
			})
			cancel()
			if err != nil {
				failedProbes++
				fmt.Fprintf(os.Stderr, "PROBE_ERROR %s: %v\n", name, err)
			}
			maxRead = max(maxRead, time.Since(readStart))
			probes++
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-stop:
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	})
	dbwork.WriteGate().ResetPeak()
	var stepUnits int
	var stepMillis []float64
	domainUnits, domainHeld := map[int]int{}, map[int]time.Duration{}
	domainSummary := func() string {
		keys := make([]int, 0, len(domainUnits))
		for d := range domainUnits {
			keys = append(keys, d)
		}
		sort.Ints(keys)
		parts := make([]string, 0, len(keys))
		for _, d := range keys {
			parts = append(parts, fmt.Sprintf("%d:%d/%.1fs", d, domainUnits[d], domainHeld[d].Seconds()))
		}
		return strings.Join(parts, ",")
	}
	lastUnits, lastLog := 0, time.Now()
	for {
		schedule.Tick(ctx)
		stepStart := time.Now()
		n, err := w.Step(ctx, compactcatalog.BatchMax)
		stepMillis = append(stepMillis, float64(time.Since(stepStart))/float64(time.Millisecond))
		if since := time.Since(stepStart); since > 250*time.Millisecond {
			domain, units, took := w.LastBatch()
			fmt.Fprintf(os.Stderr, "SLOW_STEP %s ms=%d domain=%d units=%d held_ms=%d\n", name, since.Milliseconds(), domain, units, took.Milliseconds())
		}
		stepUnits += n
		if domain, units, took := w.LastBatch(); err == nil && n > 0 {
			domainUnits[domain] += units
			domainHeld[domain] += took
		}
		if err != nil {
			close(stop)
			<-done
			return r, err
		}
		r.RSSKB = max(r.RSSKB, processRSSKB())
		if time.Since(logAt) > 10*time.Second {
			var pending int
			db.QueryRow(`SELECT count(*) FROM catalog_dirty`).Scan(&pending)
			cache := dbwork.Reads().StatementCache
			fmt.Fprintf(os.Stderr, "DRAIN %s elapsed=%s pending=%d units=%d units_per_s=%.1f rss_kb=%d gate_max_ms=%d cache_hits=%d misses=%d evictions=%d domains=%s\n", name, time.Since(start).Round(time.Second), pending, stepUnits, float64(stepUnits-lastUnits)/time.Since(lastLog).Seconds(), r.RSSKB, dbwork.WriteGate().Stats().MaxHeldMilli, cache.Hits, cache.Misses, cache.Evictions, pendingByDomain(db))
			fmt.Fprintf(os.Stderr, "DRAIN_DOMAINS %s %s\n", name, domainSummary())
			lastUnits, lastLog = stepUnits, time.Now()
			logAt = time.Now()
			if deriveBudget > 0 && time.Since(start) > deriveBudget {
				break
			}
			if err := checkScratchBudget(); err != nil {
				close(stop)
				<-done
				r.Samples = 1
				r.P50MS = float64(time.Since(start)) / float64(time.Millisecond)
				r.P95MS = r.P50MS
				r.Error = err.Error()
				logMeasurement(r)
				return r, err
			}
		}
		if n == 0 {
			break
		}
	}
	close(stop)
	<-done
	r.Samples = 1
	r.P50MS = float64(time.Since(start)) / float64(time.Millisecond)
	r.P95MS = r.P50MS
	logMeasurement(r)
	if len(stepMillis) > 0 {
		sort.Float64s(stepMillis)
		at := func(q float64) float64 { return stepMillis[min(len(stepMillis)-1, int(q*float64(len(stepMillis))))] }
		fmt.Fprintf(os.Stderr, "STEPS %s steps=%d units=%d p50_ms=%.2f p95_ms=%.2f p99_ms=%.2f max_ms=%.2f\n", name, len(stepMillis), stepUnits, at(.5), at(.95), at(.99), stepMillis[len(stepMillis)-1])
	}
	fmt.Fprintf(os.Stderr, "DERIVE_DOMAINS %s %s\n", name, domainSummary())
	fmt.Fprintf(os.Stderr, "DERIVE %s probes=%d max_browse_search_ms=%.3f max_gate_held_ms=%d failed_probes=%d\n", name, probes, float64(maxRead)/float64(time.Millisecond), dbwork.WriteGate().Stats().MaxHeldMilli, failedProbes)
	for i, entry := range dbwork.StatementProfile() {
		if i == 40 {
			break
		}
		fmt.Fprintf(os.Stderr, "DERIVE_COST %s ms=%.3f calls=%d %s\n", name, entry.Millis(), entry.Count, entry.Shape)
	}
	if firstVisible >= 0 {
		fmt.Fprintf(os.Stderr, "FIRST_VISIBLE drain_ms=%.3f\n", float64(firstVisible)/float64(time.Millisecond))
	}
	return r, nil
}

func exerciseCatalog(db *sql.DB, s *catalog.Service, samples int, matrix, writes bool, planFilter string) ([]routeResult, error) {
	var results []routeResult
	add := func(name string, read func(context.Context, *catalog.Service) error) error {
		if !routeSelected(name) {
			return nil
		}
		if planFilter != "" && strings.Contains(name, planFilter) {
			explainRoute(db, name, func(service *catalog.Service) error { return read(service.Context(), service) }, s)
		}
		r, err := exerciseRead(db, s, name, samples, read)
		results = append(results, r)
		return err
	}
	v := catalog.Viewer{Profile: "viewer", Fence: "scale-open", Libraries: []string{"movies", "tv", "music"}}
	if matrix {
		for _, lib := range v.Libraries {
			caps, err := s.BrowseCapabilitiesFor(lib, "")
			if err != nil {
				return results, err
			}
			for _, pivot := range caps.Pivots {
				if !pivot.Browsable && len(pivot.Sorts) == 0 {
					continue
				}
				scoped, err := s.BrowseCapabilitiesFor(lib, pivot.ID)
				if err != nil {
					return results, err
				}
				measure := func(name string, request catalog.BrowseRequest) error {
					for _, deep := range []bool{false, true} {
						suffix := "/first"
						if deep {
							suffix = "/deep"
							request.Range = &catalog.BrowseRange{Start: 50000}
						}
						if err := add(lib+"/"+pivot.ID+"/"+name+suffix, func(_ context.Context, s *catalog.Service) error { _, err := s.BrowseEntities(v, request); return err }); err != nil {
							return err
						}
					}
					return nil
				}
				base := catalog.BrowseRequest{Library: lib, Pivot: pivot.ID, Limit: 40}
				for _, definition := range scoped.Sorts {
					for _, direction := range definition.Directions {
						request := base
						request.Sort = []catalog.BrowseSortSelection{{Field: definition.ID, Direction: direction}}
						if err := measure("sort/"+definition.ID+"/"+direction, request); err != nil {
							return results, err
						}
					}
				}
				for _, field := range scoped.Fields {
					for _, node := range fieldOperands(field) {
						raw, _ := json.Marshal(node)
						parsed, err := catalog.ParseBrowseQuery(raw, "query")
						if err != nil {
							return results, fmt.Errorf("%s/%s: %w", field.ID, node.Operator, err)
						}
						request := base
						request.Query = parsed
						if err := measure("filter/"+field.ID+"/"+node.Operator, request); err != nil {
							return results, err
						}
					}
				}
			}
		}
	}

	if matrix {
		if err := exerciseGroupedVocabulary(db, s, v, add); err != nil {
			return results, err
		}
	}

	for _, query := range []string{"Ti", "Tit", "Title", "Title 000000", "Show", "Album", "Artist", "Person"} {
		if err := add("search/"+query, func(ctx context.Context, s *catalog.Service) error {
			page, err := s.Search(ctx, catalog.SearchRequest{Groups: []string{"movies", "shows", "episodes", "artists", "albums", "songs", "books", "collections", "authors", "people"}, Viewer: v, Profile: "viewer", ViewerFence: "scale-open", Libraries: v.Libraries, Q: query, Limit: 12})
			if err != nil {
				return err
			}
			for _, group := range page.Groups {
				if group.ErrorCode != "" {
					return fmt.Errorf("search group %s: %v", group.ID, group.ErrorCode)
				}
			}
			return nil
		}); err != nil {
			return results, err
		}
	}
	// A title condition matching few titles (a rare run of digits, none)
	// next to a broad one, on a film and a song list, both viewers.
	pg13 := v
	age := 13
	pg13.Profile, pg13.Fence, pg13.Restrictions = "viewer-pg13", "scale-pg13", identity.ContentRestrictions{MaximumAge: &age, BlockUnrated: true}
	for _, viewer := range []struct {
		name   string
		viewer catalog.Viewer
	}{{"open", v}, {"pg13", pg13}} {
		for _, list := range [][2]string{{"movies", "movies"}, {"music", "songs"}} {
			for _, text := range []string{"Title", "0001234", "zzz"} {
				viewer, list, text := viewer, list, text
				node := catalog.BrowseNode{Field: "title", Operator: "contains", Value: text}
				if err := add("title-filter/"+viewer.name+"/"+list[0]+"/"+list[1]+"/"+text, func(_ context.Context, s *catalog.Service) error {
					_, err := s.BrowseEntities(viewer.viewer, catalog.BrowseRequest{Library: list[0], Pivot: list[1], Query: &node, Limit: 40})
					return err
				}); err != nil {
					return results, err
				}
			}
		}
	}
	// Non-relevance search sorts rank every match of the typed words.
	for _, sortID := range []string{"title", "releaseYear", "dateAdded"} {
		for _, query := range []string{"Ti", "Title", "Title 000000"} {
			sortID, query := sortID, query
			if err := add("search-sort/"+sortID+"/"+query, func(ctx context.Context, s *catalog.Service) error {
				page, err := s.Search(ctx, catalog.SearchRequest{Groups: []string{"movies", "shows", "episodes", "artists", "albums", "songs"}, Viewer: v, Profile: "viewer", ViewerFence: "scale-open", Libraries: v.Libraries, Q: query, Sort: sortID, Limit: 12})
				if err != nil {
					return err
				}
				for _, group := range page.Groups {
					if group.ErrorCode != "" {
						return fmt.Errorf("search group %s: %v", group.ID, group.ErrorCode)
					}
				}
				return nil
			}); err != nil {
				return results, err
			}
		}
	}
	var rr routeResult
	if routeSelected("missing") || routeSelected("needs-attention") {
		if err := add("missing", func(ctx context.Context, s *catalog.Service) error {
			_, err := s.MissingTitles(ctx, "", "", 40)
			return err
		}); err != nil {
			return results, err
		}
		m := metadata.New(db, "")
		// Attention owns its own read snapshot; do not nest it in the measured one.
		start := time.Now()
		page, err := m.Attention(context.Background(), "", "", 50)
		if err != nil {
			return results, err
		}
		rr = routeResult{Route: "needs-attention", Samples: 1, P50MS: float64(time.Since(start)) / float64(time.Millisecond), RSSKB: processRSSKB()}
		rr.P95MS = rr.P50MS
		logMeasurement(rr)
		results = append(results, rr)
		fmt.Fprintf(os.Stderr, "ATTENTION entries=%d total=%d\n", len(page.Entries), page.Total)
	}
	if focusedSamples > 0 {
		focused, err := focusedReads(db, s)
		results = append(results, focused...)
		if err != nil {
			return results, err
		}
	}
	if !writes {
		return results, nil
	}
	if _, err := db.Exec(`UPDATE catalog_derivations SET version=version-1 WHERE domain IN(17,18)`); err != nil {
		return results, err
	}
	rr, err := exerciseDrain(db, s, "derive-version-bump")
	results = append(results, rr)
	if err != nil {
		return results, err
	}
	rr, err = exerciseInsert(db, s)
	results = append(results, rr)
	if err != nil {
		return results, err
	}
	rr, err = exerciseEvents(db)
	results = append(results, rr)
	if err != nil {
		return results, err
	}
	marked, err := exerciseContainer(db, s)
	results = append(results, marked...)
	if err != nil {
		return results, err
	}
	rr, err = exerciseRemoval(db, s)
	results = append(results, rr)
	return results, err
}

func exerciseInsert(db *sql.DB, s *catalog.Service) (routeResult, error) {
	r := routeResult{Route: "add-1000-films"}
	namespace := strconv.FormatInt(time.Now().UnixNano(), 10)
	searchQuery := "Incremental " + namespace
	ctx := context.Background()
	start := time.Now()
	before, err := writerChangeCount(ctx, db)
	if err != nil {
		return r, err
	}
	for offset := 0; offset < 1000; offset += 16 {
		err := dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			library, err := compactcatalog.LibraryTx(ctx, tx, "movies")
			if err != nil {
				return err
			}
			for i := offset; i < min(1000, offset+16); i++ {
				path := fmt.Sprintf("/fixture/movies/incremental-%s-%04d.mp4", namespace, i)
				item, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Movie, Key: compactcatalog.ItemKey("/fixture/movies", path, 0), Title: fmt.Sprintf("%s %04d", searchQuery, i), Year: 2000 + i%25, Added: "2026-10-13T00:00:00.000Z"})
				if err != nil {
					return err
				}
				for version := 0; version < 1+i%3; version++ {
					asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: fmt.Sprintf("%s.%d", path, version), Size: 1000000, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 5400})
					if err != nil {
						return err
					}
					if err = compactcatalog.LinkAssetTx(ctx, tx, item, asset, compactcatalog.Link{}); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return r, err
		}
	}
	syncMS := float64(time.Since(start)) / float64(time.Millisecond)
	drained, err := exerciseDrain(db, s, "derive-added-1000", searchQuery)
	if err != nil {
		return r, err
	}
	view := catalog.Viewer{Profile: "viewer", Fence: "scale-open", Libraries: []string{"movies", "tv", "music"}}
	page, err := s.Search(ctx, catalog.SearchRequest{Viewer: view, Q: searchQuery, Group: "movies", Limit: 40})
	if err != nil || len(page.Groups) != 1 || page.Groups[0].TotalCount != 1000 {
		return r, fmt.Errorf("added films were not published: %+v %v", page.Groups, err)
	}
	after, err := writerChangeCount(ctx, db)
	if err != nil {
		return r, err
	}
	r.Samples = 1
	r.P50MS = float64(time.Since(start)) / float64(time.Millisecond)
	r.P95MS = r.P50MS
	r.RSSKB = drained.RSSKB
	logMeasurement(r)
	fmt.Fprintf(os.Stderr, "INSERT sync_ms=%.3f changes=%d changes_per_title=%.3f\n", syncMS, after-before, float64(after-before)/1000)
	return r, nil
}

// exerciseEvents writes a 10,000-event burst in commits of 32. A reader that
// keeps up (one read per commit, as the feed's wakes give it) receives every
// event in bounded pages; one that slept through the whole burst is answered
// with a single resync, not a 10,000-event replay.
func exerciseEvents(db *sql.DB) (routeResult, error) {
	r := routeResult{Route: "events-10000"}
	ctx := context.Background()
	start := time.Now()
	hub := eventfeed.New(db)
	reader := identity.Principal{}
	first, err := hub.Read(ctx, reader, false, "")
	if err != nil {
		return r, err
	}
	asleep, cursor := first.NextAfter, first.NextAfter
	received, pages := 0, 0
	for offset := 0; offset < 10000; offset += 32 {
		err := dbwork.WithWriteTx(ctx, db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
			for i := offset; i < min(10000, offset+32); i++ {
				if err := apievents.Append(tx, "server", "catalog.changed", "item", strconv.Itoa(i), "1", nil); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return r, err
		}
		for {
			page, err := hub.Read(ctx, reader, false, cursor)
			if err != nil {
				return r, err
			}
			for _, event := range page.Events {
				if event.Type == "stream.resync" {
					return r, fmt.Errorf("a reader keeping up was resynced after %d events", received)
				}
			}
			received += len(page.Events)
			pages++
			cursor = page.NextAfter
			if !page.More {
				break
			}
		}
	}
	if received != 10000 {
		return r, fmt.Errorf("event burst delivered %d of 10000", received)
	}
	late, err := hub.Read(ctx, reader, false, asleep)
	if err != nil {
		return r, err
	}
	if len(late.Events) != 1 || late.Events[0].Type != "stream.resync" {
		return r, fmt.Errorf("a reader 10000 events behind got %d events, want one resync", len(late.Events))
	}
	r.Samples = 1
	r.P50MS = float64(time.Since(start)) / float64(time.Millisecond)
	r.P95MS = r.P50MS
	r.RSSKB = processRSSKB()
	logMeasurement(r)
	fmt.Fprintf(os.Stderr, "EVENTS received=%d pages=%d late=resync\n", received, pages)
	return r, nil
}

func exerciseContainer(db *sql.DB, s *catalog.Service) ([]routeResult, error) {
	ctx := context.Background()
	var show int64
	seasons := make([]int64, 10)
	err := dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		library, err := compactcatalog.LibraryTx(ctx, tx, "tv")
		if err != nil {
			return err
		}
		show, _, err = compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Show, Key: compactcatalog.ShowKey("scale-500"), Title: "Five Hundred", Year: 2000})
		if err != nil {
			return err
		}
		if err = compactcatalog.SetFactsTx(ctx, tx, show, map[string]any{"local_key": "scale-500"}); err != nil {
			return err
		}
		for i := range seasons {
			seasons[i], _, err = compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Season, Parent: show, Key: compactcatalog.SeasonKey("scale-500", i+1), Title: fmt.Sprintf("Season %d", i+1)})
			if err != nil {
				return err
			}
			if err = compactcatalog.SetFactsTx(ctx, tx, seasons[i], map[string]any{"show_id": show, "number": i + 1}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for offset := 0; offset < 500; offset += 16 {
		err = dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			library, err := compactcatalog.LibraryTx(ctx, tx, "tv")
			if err != nil {
				return err
			}
			for i := offset; i < min(500, offset+16); i++ {
				season, number := i/50+1, i%50+1
				path := fmt.Sprintf("/fixture/tv/scale500/S%02dE%02d.mp4", season, number)
				item, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: library, Kind: compactcatalog.Episode, Parent: seasons[season-1], Key: compactcatalog.EpisodeKey("scale-500", "seasonal", season, number), Title: fmt.Sprintf("Episode %d", number), Year: 2000, Added: "2026-10-13T00:00:00.000Z"})
				if err != nil {
					return err
				}
				if err = compactcatalog.SetFactsTx(ctx, tx, item, map[string]any{"show_id": show, "season_id": seasons[season-1], "numbering": "seasonal", "number": number}); err != nil {
					return err
				}
				asset, _, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1000000, ModifiedNS: 1, Container: "mp4", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 2400})
				if err != nil {
					return err
				}
				if err = compactcatalog.LinkAssetTx(ctx, tx, item, asset, compactcatalog.Link{}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if err = compactcatalog.Drain(ctx, db); err != nil {
		return nil, err
	}
	p := identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "fixture-owner", ProfileID: "fixture-viewer", Role: "owner"}}
	// This isolated owner's grant is the synthetic TV library. HTTP principal
	// resolution is outside this catalogue-service measurement.
	access := func(_ context.Context, _ *sql.Tx, _ identity.Principal, column string) (string, []any, error) {
		// Per item, as the server's own predicate is: a list of every entity of
		// the library would make the action's cost follow the library's size.
		return `EXISTS(SELECT 1 FROM catalog_entities granted JOIN catalog_libraries granted_library ON granted_library.id=granted.library_id WHERE granted.id=` + column + ` AND granted_library.library_id='tv')`, nil, nil
	}
	var public string
	if err = db.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE id=?`, show).Scan(&public); err != nil {
		return nil, err
	}
	yes := true
	if containerProfile {
		dbwork.ProfileStatements(true)
	}
	start := time.Now()
	dbwork.WriteGate().ResetPeak()
	marked, err := s.SetContainerPersonal(ctx, p, "show", public, catalog.ContainerPersonalMutation{Watched: &yes}, nil, access)
	if err != nil {
		return nil, err
	}
	r := routeResult{Route: "mark-500-episodes", Samples: 1, P50MS: float64(time.Since(start)) / float64(time.Millisecond), RSSKB: processRSSKB()}
	r.P95MS = r.P50MS
	logMeasurement(r)
	fmt.Fprintf(os.Stderr, "CONTAINER mark_gate_ms=%d\n", dbwork.WriteGate().Stats().MaxHeldMilli)
	if containerProfile {
		profileReport("mark-500", 1, 8)
	}
	viewer := catalog.Viewer{Profile: identity.PersonalKey(p.Viewer), Fence: "scale-500", Libraries: []string{"tv"}}
	counts, err := s.ContainerPersonalCounts(viewer, "show", public)
	if err != nil || counts.WatchedCount != 500 {
		return []routeResult{r}, fmt.Errorf("marked counts=%+v: %v", counts, err)
	}
	if containerProfile {
		dbwork.ProfileStatements(true)
	}
	start = time.Now()
	dbwork.WriteGate().ResetPeak()
	if _, err = s.UndoContainerPersonal(ctx, p, "show", public, catalog.ContainerUndoMutation{ExpectedRevision: marked.Revision, UndoToken: marked.UndoToken}, access); err != nil {
		return []routeResult{r}, err
	}
	undo := routeResult{Route: "undo-500-episodes", Samples: 1, P50MS: float64(time.Since(start)) / float64(time.Millisecond), RSSKB: processRSSKB()}
	undo.P95MS = undo.P50MS
	logMeasurement(undo)
	fmt.Fprintf(os.Stderr, "CONTAINER undo_gate_ms=%d\n", dbwork.WriteGate().Stats().MaxHeldMilli)
	if containerProfile {
		profileReport("undo-500", 1, 8)
	}
	counts, err = s.ContainerPersonalCounts(viewer, "show", public)
	if err != nil || counts.WatchedCount != 0 {
		return []routeResult{r, undo}, fmt.Errorf("undo counts=%+v: %v", counts, err)
	}
	return []routeResult{r, undo}, nil
}

func exerciseRemoval(db *sql.DB, s *catalog.Service) (routeResult, error) {
	r := routeResult{Route: "remove-movies-library"}
	ctx := context.Background()
	var before, free, size, revision int64
	if err := db.QueryRow(`PRAGMA page_count`).Scan(&before); err != nil {
		return r, err
	}
	if err := db.QueryRow(`PRAGMA page_size`).Scan(&size); err != nil {
		return r, err
	}
	if err := db.QueryRow(`SELECT revision FROM library_configuration WHERE library_id='movies'`).Scan(&revision); err != nil {
		return r, err
	}
	adapter := s.LibraryRemovalAdapter()
	start := time.Now()
	dbwork.WriteGate().ResetPeak()
	// The operation receipt is supplied by the real scheduler; its worker is
	// advanced synchronously so timing includes only this operation.
	scheduler := operations.NewScheduler(operations.New(db))
	if err := scheduler.Register(adapter); err != nil {
		return r, err
	}
	var retired bool
	err := db.QueryRow(`SELECT retired FROM catalog_libraries WHERE library_id='movies'`).Scan(&retired)
	if err != nil {
		return r, err
	}
	if !retired {
		err = s.RemoveLibrary(ctx, "movies", "Movies", revision, func(*sql.Tx) error { return nil }, func(ctx context.Context, tx *sql.Tx, kind, resource string) error {
			_, err := scheduler.EnqueueDomainTx(ctx, tx, kind, resource, "fixture-owner")
			return err
		})
		if err != nil {
			return r, err
		}
	}
	for {
		step, cancel := context.WithTimeout(ctx, time.Second)
		observation, err := adapter.Step(step, "movies")
		cancel()
		if err != nil {
			return r, err
		}
		r.RSSKB = max(r.RSSKB, processRSSKB())
		if observation.State == "succeeded" {
			break
		}
	}
	r.Samples = 1
	r.P50MS = float64(time.Since(start)) / float64(time.Millisecond)
	r.P95MS = r.P50MS
	logMeasurement(r)
	fmt.Fprintf(os.Stderr, "REMOVAL max_gate_ms=%d\n", dbwork.WriteGate().Stats().MaxHeldMilli)
	if err = compactcatalog.Drain(ctx, db); err != nil {
		return r, err
	}
	if err = db.QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil {
		return r, err
	}
	fmt.Fprintf(os.Stderr, "RECLAIM before_bytes=%d reusable_bytes=%d\n", before*size, free*size)
	scratchBytes, budgetErr := scratchUsage()
	if budgetErr != nil {
		return r, budgetErr
	}
	if scratchBytes+before*size*2 > scratchLimit {
		fmt.Fprintf(os.Stderr, "VACUUM_SKIPPED scratch_budget estimated_peak_bytes=%d\n", before*size*2)
		return r, nil
	}
	start = time.Now()
	connCtx, conn, release, err := dbwork.ReserveWriterConn(ctx, db, dbwork.ClassMaintenance)
	if err != nil {
		return r, err
	}
	_, err = conn.ExecContext(connCtx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	if err == nil {
		_, err = conn.ExecContext(connCtx, `VACUUM`)
	}
	release()
	if err != nil {
		return r, err
	}
	var after int64
	if err = db.QueryRow(`PRAGMA page_count`).Scan(&after); err != nil {
		return r, err
	}
	fmt.Fprintf(os.Stderr, "VACUUM elapsed_ms=%.3f after_bytes=%d reclaimed_bytes=%d rss_kb=%d\n", float64(time.Since(start))/float64(time.Millisecond), after*size, (before-after)*size, processRSSKB())
	return r, nil
}

// Raw handle reads use the reader pool, whose total_changes counter stays at
// zero. Read this connection-local counter on the sole writer instead.
func writerChangeCount(ctx context.Context, db *sql.DB) (int64, error) {
	var count int64
	err := dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error { return tx.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&count) })
	return count, err
}

func printRouteProfile() {
	if !profileRoutes {
		return
	}
	for i, entry := range dbwork.StatementProfile() {
		if i == 3 {
			break
		}
		fmt.Fprintf(os.Stderr, "  %8.1f ms %5d× %s\n", entry.Millis(), entry.Count, entry.Shape)
	}
	dbwork.ProfileStatements(false)
}
