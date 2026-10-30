package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// G04 query-memory accounting. Nothing here claims cache bytes from RSS: the
// prepared-read store reports its exact charged bytes, and the facet,
// across-order and count caches report theirs through the approved read-only
// catalog.Service.QueryMemoryStats. The remaining evidence is named for what it
// is (per-page query cost, heap snapshots, database inventory).

// QueryMemoryStats is the approved exact read-only observer on catalog.Service;
// these are the compile-time budgets it is measured against, never a substitute
// for the measurement.
const (
	facetBudgetBytes       = int64(64 << 20)
	facetSingleEntryBytes  = int64(64 << 20 / 4)
	acrossOrderBudgetBytes = int64(16 << 20)
	acrossOrderEntryBytes  = int64(16 << 20 / 4)
	facetCapacityEntries   = int64(512)
	acrossCapacityEntries  = int64(64)
)

type cacheBudgets struct {
	FacetBytes       int64 `json:"facetBudgetBytes"`
	FacetSingleEntry int64 `json:"facetSingleEntryBytes"`
	FacetCapacity    int64 `json:"facetCapacity"`
	AcrossBytes      int64 `json:"acrossBudgetBytes"`
	AcrossSingle     int64 `json:"acrossSingleEntryBytes"`
	AcrossCapacity   int64 `json:"acrossCapacity"`
}

type queryMemoryOptions struct {
	preparedQueries int
	expiryWait      time.Duration
	heapSamples     int
}

var queryMemoryExercise queryMemoryOptions

type preparedProbe struct {
	BudgetBytes     int64    `json:"budgetBytes"`
	MaxChargedBytes int64    `json:"maxChargedBytes"`
	MaxEntries      int      `json:"maxEntries"`
	MaxHandles      int      `json:"maxHandles"`
	Deliveries      int      `json:"maxConcurrentDeliveries"`
	Submitted       int      `json:"submitted"`
	SharedBuilds    int      `json:"sharedEquivalentBuilds"`
	PayloadBytes    int64    `json:"largestPayloadBytes"`
	RestartBytes    int64    `json:"bytesAfterRestart"`
	RestartEntries  int      `json:"entriesAfterRestart"`
	Exact           bool     `json:"exact"`
	Errors          []string `json:"errors,omitempty"`
}

type facetProbe struct {
	Library        string  `json:"library"`
	Field          string  `json:"field"`
	Total          int     `json:"total"`
	Returned       int     `json:"returned"`
	ColdMS         float64 `json:"coldMs"`
	WarmMS         float64 `json:"warmMs"`
	ColdVMSteps    int64   `json:"coldVmSteps"`
	WarmVMSteps    int64   `json:"warmVmSteps"`
	ColdStatements uint64  `json:"coldStatements"`
	WarmStatements uint64  `json:"warmStatements"`
	// RetainedBytes is exact only when the whole value list was returned
	// (total <= 200). Otherwise it is a lower bound and Truncated says so.
	RetainedBytes int64  `json:"retainedBytes"`
	Truncated     bool   `json:"truncated"`
	Error         string `json:"error,omitempty"`
}

type acrossProbe struct {
	Route          string  `json:"route"`
	Total          int     `json:"total"`
	Entries        int     `json:"entries"`
	ColdMS         float64 `json:"coldMs"`
	WarmMS         float64 `json:"warmMs"`
	ColdVMSteps    int64   `json:"coldVmSteps"`
	WarmVMSteps    int64   `json:"warmVmSteps"`
	ColdStatements uint64  `json:"coldStatements"`
	WarmStatements uint64  `json:"warmStatements"`
	Capped         bool    `json:"capped"`
	Error          string  `json:"error,omitempty"`
}

type countProbe struct {
	Route          string  `json:"route"`
	ColdMS         float64 `json:"coldMs"`
	WarmMS         float64 `json:"warmMs"`
	ColdVMSteps    int64   `json:"coldVmSteps"`
	WarmVMSteps    int64   `json:"warmVmSteps"`
	ColdStatements uint64  `json:"coldStatements"`
	WarmStatements uint64  `json:"warmStatements"`
	Entries        int     `json:"countCacheEntries"`
	Error          string  `json:"error,omitempty"`
}

type heapSnapshot struct {
	At        string `json:"at"`
	HeapAlloc int64  `json:"heapAllocBytes"`
	HeapInuse int64  `json:"heapInuseBytes"`
	HeapSys   int64  `json:"heapSysBytes"`
	NumGC     uint32 `json:"numGc"`
}

type inventory struct {
	Objects     map[string]string `json:"objects"`
	Counts      map[string]int64  `json:"counts"`
	Files       map[string]int64  `json:"files"`
	PageCount   int64             `json:"pageCount"`
	PageSize    int64             `json:"pageSize"`
	FreePages   int64             `json:"freelistPages"`
	JournalMode string            `json:"journalMode"`
}

type inventoryDiff struct {
	SchemaHashBefore string           `json:"schemaHashBefore"`
	SchemaHashAfter  string           `json:"schemaHashAfter"`
	SchemaEqual      bool             `json:"schemaEqual"`
	AddedObjects     []string         `json:"addedObjects"`
	RemovedObjects   []string         `json:"removedObjects"`
	ChangedObjects   []string         `json:"changedObjects"`
	CountDeltas      map[string]int64 `json:"countDeltas"`
	FileDeltas       map[string]int64 `json:"fileDeltas"`
	PageDelta        int64            `json:"pageCountDelta"`
	StoredQueryState bool             `json:"noStoredQueryState"`
}

type expiryProbe struct {
	Waited          string `json:"waited"`
	FacetExpirySeen bool   `json:"facetRecomputedAfterTtl"`
	PreparedRestart bool   `json:"preparedHandlesLostOnRestart"`
	Note            string `json:"note,omitempty"`
}

type queryMemoryReport struct {
	Prepared         preparedProbe             `json:"preparedReadBytes"`
	Facets           []facetProbe              `json:"facetPages"`
	Across           acrossProbe               `json:"acrossOrder"`
	Counts           countProbe                `json:"maintainedCount"`
	Budgets          cacheBudgets              `json:"cacheBudgets"`
	CacheStatsBefore catalog.QueryMemoryStats  `json:"cacheStatsBefore"`
	CacheStatsAfter  catalog.QueryMemoryStats  `json:"cacheStatsAfter"`
	CachePeak        catalog.QueryMemoryStats  `json:"cacheStatsPeak"`
	CacheStatsExact  bool                      `json:"cacheCountersExact"`
	Inventory        inventoryDiff             `json:"inventory"`
	Heap             []heapSnapshot            `json:"heapSnapshots"`
	Expiry           expiryProbe               `json:"expiryRestart"`
	Notes            []string                  `json:"notes"`
}

// exerciseQueryMemory runs a real read-only query-churn pass and measures what
// can be measured exactly. It never writes a query result, count, selection or
// scratch row: the before/after inventory proves that.
func exerciseQueryMemory(ctx context.Context, db *sql.DB, s *catalog.Service, dir string) (queryMemoryReport, error) {
	report := queryMemoryReport{Notes: []string{}, CacheStatsExact: true}
	report.Budgets = cacheBudgets{FacetBytes: facetBudgetBytes, FacetSingleEntry: facetSingleEntryBytes,
		FacetCapacity: facetCapacityEntries, AcrossBytes: acrossOrderBudgetBytes,
		AcrossSingle: acrossOrderEntryBytes, AcrossCapacity: acrossCapacityEntries}
	before, err := captureInventory(ctx, db, dir)
	if err != nil {
		return report, err
	}
	if s == nil {
		return report, errors.New("query memory needs a catalogue service")
	}
	report.CacheStatsBefore = s.QueryMemoryStats()
	report.CachePeak = report.CacheStatsBefore
	report.Heap = append(report.Heap, takeHeap("before"))

	store, err := readprep.New(readprep.DefaultConfig())
	if err != nil {
		return report, err
	}
	s.UseReadPreparations(store)
	report.Prepared, err = preparedReadProbe(ctx, db, s, store)
	if err != nil {
		report.Prepared.Errors = append(report.Prepared.Errors, err.Error())
	}
	report.Heap = append(report.Heap, takeHeap("prepared"))

	report.Facets, err = facetProbeRun(ctx, db, s)
	if err != nil {
		report.Notes = append(report.Notes, "facet probes: "+err.Error())
	}
	noteCachePeak(&report.CachePeak, s.QueryMemoryStats())
	report.Heap = append(report.Heap, takeHeap("facets"))

	report.Across = acrossProbeRun(ctx, db, s)
	noteCachePeak(&report.CachePeak, s.QueryMemoryStats())
	report.Heap = append(report.Heap, takeHeap("across"))

	report.Counts = countProbeRun(ctx, db, s)
	report.Heap = append(report.Heap, takeHeap("counts"))

	// Restart semantics: closing the process-owned store drops every completed
	// result and private handle; only user data may survive.
	if err = store.Close(); err != nil {
		report.Notes = append(report.Notes, "close prepared store: "+err.Error())
	}
	restarted, err := readprep.New(readprep.DefaultConfig())
	if err != nil {
		return report, err
	}
	afterStats := restarted.Stats()
	s.UseReadPreparations(restarted)
	report.Prepared.RestartBytes = afterStats.Bytes
	report.Prepared.RestartEntries = afterStats.Entries
	report.Expiry.PreparedRestart = afterStats.Bytes == 0 && afterStats.Entries == 0
	report.Expiry.Note = "A restart loses only transient prepared handles; no database row is written for them."

	if queryMemoryExercise.expiryWait > 0 {
		report.Expiry.Waited = queryMemoryExercise.expiryWait.String()
		if _, findErr := facetColdWarm(ctx, db, s, "movies", "genre"); findErr == nil {
			time.Sleep(queryMemoryExercise.expiryWait)
			after, _ := facetColdWarm(ctx, db, s, "movies", "genre")
			report.Expiry.FacetExpirySeen = after.ColdStatements > 0
		}
	} else {
		report.Expiry.Waited = "not waited (set -query-expiry-wait above 1m to observe the facet TTL)"
	}

	report.CacheStatsAfter = s.QueryMemoryStats()
	noteCachePeak(&report.CachePeak, report.CacheStatsAfter)
	after, err := captureInventory(ctx, db, dir)
	if err != nil {
		return report, err
	}
	report.Inventory = diffInventories(before, after)
	if err = restarted.Close(); err != nil {
		report.Notes = append(report.Notes, "close restarted store: "+err.Error())
	}
	peak := report.CachePeak
	if peak.FacetBytes > facetBudgetBytes {
		return report, fmt.Errorf("facet cache peaked at %d bytes over the %d-byte budget", peak.FacetBytes, facetBudgetBytes)
	}
	if peak.AcrossBytes > acrossOrderBudgetBytes {
		return report, fmt.Errorf("across-order cache peaked at %d bytes over the %d-byte budget", peak.AcrossBytes, acrossOrderBudgetBytes)
	}
	if peak.FacetEntries > facetCapacityEntries || peak.AcrossEntries > acrossCapacityEntries {
		return report, fmt.Errorf("cache entry counts over capacity: facet=%d across=%d", peak.FacetEntries, peak.AcrossEntries)
	}
	if err = writeJSONFile(filepath.Join(dir, "query-memory-report.json"), report); err != nil {
		return report, err
	}
	if !report.Inventory.SchemaEqual || len(report.Inventory.CountDeltas) > 0 {
		return report, errors.New("query churn changed the database schema or stored rows")
	}
	return report, nil
}

// noteCachePeak keeps the largest exact cache charge seen during the churn, so
// a transient peak is not hidden by the quieter end state.
func noteCachePeak(peak *catalog.QueryMemoryStats, stats catalog.QueryMemoryStats) {
	peak.FacetBytes = max(peak.FacetBytes, stats.FacetBytes)
	peak.FacetEntries = max(peak.FacetEntries, stats.FacetEntries)
	peak.FacetInFlight = max(peak.FacetInFlight, stats.FacetInFlight)
	peak.AcrossBytes = max(peak.AcrossBytes, stats.AcrossBytes)
	peak.AcrossEntries = max(peak.AcrossEntries, stats.AcrossEntries)
	peak.CountEntries = max(peak.CountEntries, stats.CountEntries)
}

// preparedReadProbe churns the real selection-preparation store: distinct
// filters, an equivalent query shared by two callers, and the exact charged
// bytes the store reports.
func preparedReadProbe(ctx context.Context, db *sql.DB, s *catalog.Service, store *readprep.Store) (preparedProbe, error) {
	out := preparedProbe{BudgetBytes: store.MaxBytes(), Exact: true}
	viewer := catalog.Viewer{Profile: "viewer", Fence: "scale-open", Libraries: []string{"movies", "tv", "music"}}
	queries := []string{"Title", "Title 0000", "Title 0001", "0001", "Title 0002"}
	samples := max(1, min(queryMemoryExercise.preparedQueries, len(queries)))
	var firstHandle string
	for index := 0; index < samples; index++ {
		query := queries[index%len(queries)]
		pending, err := submitSelection(ctx, db, s, viewer, query, "scalebench-prepared-"+strconv.Itoa(index))
		if err != nil {
			out.Errors = append(out.Errors, query+": "+err.Error())
			continue
		}
		out.Submitted++
		result, err := awaitPrepared(ctx, store, readprep.Handle(pending.QueryID), "scalebench-prepared-"+strconv.Itoa(index))
		if err != nil {
			out.Errors = append(out.Errors, query+": "+err.Error())
			continue
		}
		out.MaxChargedBytes = max(out.MaxChargedBytes, store.Stats().Bytes)
		out.MaxEntries = max(out.MaxEntries, store.Stats().Entries)
		out.MaxHandles = max(out.MaxHandles, store.Stats().Handles)
		out.Deliveries = max(out.Deliveries, store.Stats().Deliveries)
		out.PayloadBytes = max(out.PayloadBytes, int64(len(result.Payload)))
		if index == 0 {
			firstHandle = pending.QueryID
		}
		result.Release()
	}
	// An equivalent content query under a second caller shares one computation.
	if firstHandle != "" {
		query := queries[0]
		if _, err := submitSelection(ctx, db, s, viewer, query, "scalebench-shared-a"); err == nil {
			second, err := submitSelection(ctx, db, s, viewer, query, "scalebench-shared-b")
			if err == nil && second.QueryID == "" {
				out.Errors = append(out.Errors, "equivalent preparation returned no handle")
			}
			if err == nil && store.Stats().Entries == 1 {
				out.SharedBuilds = 1
			}
		}
	}
	if out.MaxChargedBytes > out.BudgetBytes {
		return out, fmt.Errorf("prepared store charged %d bytes over its %d-byte budget", out.MaxChargedBytes, out.BudgetBytes)
	}
	return out, nil
}

func submitSelection(ctx context.Context, db *sql.DB, s *catalog.Service, viewer catalog.Viewer, query, caller string) (catalog.BrowseSelectionPreparing, error) {
	var pending catalog.BrowseSelectionPreparing
	node := catalog.BrowseNode{Field: "title", Operator: "contains", Value: query}
	err := dbwork.WithReadSnapshot(ctx, db, func(inner context.Context) error {
		read := s.WithContext(inner)
		request := catalog.BrowseRequest{Library: "movies", Pivot: "movies", Query: &node, Limit: 1}
		page, err := read.BrowseEntities(viewer, request)
		if err != nil {
			return err
		}
		if page.Bound == nil {
			return errors.New("browse page published no selection bound")
		}
		access := catalog.SelectionPreparationAccess{Caller: readprep.Caller("browse-selection/scalebench/" + caller), Check: func(*catalog.Service) error { return nil }, CapturedBytes: 1024}
		pending, err = read.SubmitBrowseSelectionPreparation(request, viewer, *page.Bound, access)
		return err
	})
	return pending, err
}

func awaitPrepared(ctx context.Context, store *readprep.Store, handle readprep.Handle, caller string) (readprep.Result, error) {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		result, err := store.Poll(ctx, readprep.Caller("browse-selection/scalebench/"+caller), handle)
		if err != nil {
			return readprep.Result{}, err
		}
		if result.State == readprep.Ready {
			return result, nil
		}
		result.Release()
		time.Sleep(2 * time.Millisecond)
	}
	return readprep.Result{}, errors.New("preparation did not become ready inside 20s")
}

// facetColdWarm reads one facet twice. The second read must not issue the
// grouped pass again; the statement/VM-step delta is the cache evidence. The
// retained-byte figure is exact for a whole list (total <= the published page
// limit) and a lower bound otherwise.
func facetColdWarm(ctx context.Context, db *sql.DB, s *catalog.Service, library, field string) (facetProbe, error) {
	probe := facetProbe{Library: library, Field: field}
	viewer := catalog.Viewer{Profile: "viewer", Fence: "scale-open", Libraries: []string{"movies", "tv", "music"}}
	read := func() (catalog.FacetPage, float64, int64, uint64, error) {
		var page catalog.FacetPage
		var ms float64
		var vm int64
		var statements uint64
		err := dbwork.WithReadSnapshot(ctx, db, func(inner context.Context) error {
			measured, cost := dbwork.Measure(inner)
			before := dbwork.Reads().Statements
			start := time.Now()
			var readErr error
			page, readErr = s.WithContext(measured).Facets(viewer, catalog.FacetRequest{Library: library, Field: field, Limit: 200})
			ms = float64(time.Since(start)) / float64(time.Millisecond)
			vm = cost().VMSteps
			statements = dbwork.Reads().Statements - before
			return readErr
		})
		return page, ms, vm, statements, err
	}
	page, coldMS, coldVM, coldStatements, err := read()
	if err != nil {
		return probe, err
	}
	probe.ColdMS, probe.ColdVMSteps, probe.ColdStatements = coldMS, coldVM, coldStatements
	probe.Total, probe.Returned = page.Total, len(page.Values)
	probe.Truncated = page.Total > len(page.Values)
	probe.RetainedBytes = facetCost(page.Values)
	if probe.RetainedBytes > facetSingleEntryBytes {
		return probe, fmt.Errorf("one facet list costs %d bytes, over the %d-byte single-entry limit", probe.RetainedBytes, facetSingleEntryBytes)
	}
	_, warmMS, warmVM, warmStatements, err := read()
	if err != nil {
		return probe, err
	}
	probe.WarmMS, probe.WarmVMSteps, probe.WarmStatements = warmMS, warmVM, warmStatements
	return probe, nil
}

// facetCost mirrors facetPageCost in caches.go. It is applied to values the
// public response returned; when the response window truncates a larger list,
// this is a lower bound and the probe says so.
func facetCost(values []catalog.FacetValue) int64 {
	cost := int64(128)
	for _, value := range values {
		cost += 64 + int64(len(value.Value)+len(value.Label))
	}
	return cost
}

func facetProbeRun(ctx context.Context, db *sql.DB, s *catalog.Service) ([]facetProbe, error) {
	out := []facetProbe{}
	fields := []string{"genre", "year", "decade", "contentRating", "resolution", "studio", "tag"}
	for _, library := range []string{"movies", "tv", "music"} {
		for _, field := range fields {
			probe, err := facetColdWarm(ctx, db, s, library, field)
			if err != nil {
				if probe.RetainedBytes > facetSingleEntryBytes {
					return out, err
				}
				probe.Error = err.Error()
			}
			out = append(out, probe)
		}
	}
	return out, nil
}

func acrossProbeRun(ctx context.Context, db *sql.DB, s *catalog.Service) acrossProbe {
	out := acrossProbe{Route: "across/songs/title/asc"}
	viewer := catalog.Viewer{Profile: "viewer", Fence: "scale-open", Libraries: []string{"movies", "tv", "music"}}
	read := func() (catalog.BrowseResult, float64, int64, uint64, error) {
		var page catalog.BrowseResult
		var ms float64
		var vm int64
		var statements uint64
		err := dbwork.WithReadSnapshot(ctx, db, func(inner context.Context) error {
			measured, cost := dbwork.Measure(inner)
			before := dbwork.Reads().Statements
			start := time.Now()
			var readErr error
			page, readErr = s.WithContext(measured).BrowseAcross(viewer, catalog.AcrossRequest{
				Pivot: "songs", Limit: 40,
				Sort: []catalog.BrowseSortSelection{{Field: "title", Direction: "asc"}},
			})
			ms = float64(time.Since(start)) / float64(time.Millisecond)
			vm = cost().VMSteps
			statements = dbwork.Reads().Statements - before
			return readErr
		})
		return page, ms, vm, statements, err
	}
	page, coldMS, coldVM, coldStatements, err := read()
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Total, out.Entries, out.Capped = page.PageInfo.Total, len(page.Entries), page.PageInfo.TotalIsLowerBound
	out.ColdMS, out.ColdVMSteps, out.ColdStatements = coldMS, coldVM, coldStatements
	_, warmMS, warmVM, warmStatements, err := read()
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.WarmMS, out.WarmVMSteps, out.WarmStatements = warmMS, warmVM, warmStatements
	return out
}

// countProbeRun exercises the maintained count the Home rows read, twice: a
// warmed exact count must not repeat its COUNT pass.
func countProbeRun(ctx context.Context, db *sql.DB, s *catalog.Service) countProbe {
	out := countProbe{Route: "home/rows"}
	viewer := catalog.Viewer{Profile: "viewer", Fence: "scale-open", Libraries: []string{"movies", "tv", "music"}}
	read := func() (float64, int64, uint64, error) {
		var ms float64
		var vm int64
		var statements uint64
		err := dbwork.WithReadSnapshot(ctx, db, func(inner context.Context) error {
			measured, cost := dbwork.Measure(inner)
			before := dbwork.Reads().Statements
			start := time.Now()
			_, readErr := s.WithContext(measured).HomeRows(catalog.HomeRequest{Viewer: viewer, Limit: 12})
			ms = float64(time.Since(start)) / float64(time.Millisecond)
			vm = cost().VMSteps
			statements = dbwork.Reads().Statements - before
			return readErr
		})
		return ms, vm, statements, err
	}
	coldMS, coldVM, coldStatements, err := read()
	out.ColdMS, out.ColdVMSteps, out.ColdStatements = coldMS, coldVM, coldStatements
	if err != nil {
		out.Error = err.Error()
		return out
	}
	warmMS, warmVM, warmStatements, err := read()
	out.WarmMS, out.WarmVMSteps, out.WarmStatements = warmMS, warmVM, warmStatements
	if err != nil {
		out.Error = err.Error()
	}
	out.Entries = int(s.QueryMemoryStats().CountEntries)
	return out
}

func takeHeap(at string) heapSnapshot {
	debug.FreeOSMemory()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return heapSnapshot{At: at, HeapAlloc: int64(stats.HeapAlloc), HeapInuse: int64(stats.HeapInuse), HeapSys: int64(stats.HeapSys), NumGC: stats.NumGC}
}

// captureInventory records schema objects, maintained-table row counts and the
// fixture directory's files. Only names are stored, never query results.
func captureInventory(ctx context.Context, db *sql.DB, dir string) (inventory, error) {
	out := inventory{Objects: map[string]string{}, Counts: map[string]int64{}, Files: map[string]int64{}}
	rows, err := db.QueryContext(ctx, `SELECT type,name,COALESCE(tbl_name,''),COALESCE(sql,'') FROM sqlite_master ORDER BY type,name`)
	if err != nil {
		return out, err
	}
	tables := []string{}
	for rows.Next() {
		var kind, name, table, text string
		if err = rows.Scan(&kind, &name, &table, &text); err != nil {
			rows.Close()
			return out, err
		}
		sum := sha256.Sum256([]byte(text))
		out.Objects[kind+"/"+name] = hex.EncodeToString(sum[:])
		if kind == "table" && (strings.HasPrefix(name, "catalog_") || strings.HasPrefix(name, "compact_") ||
			strings.HasPrefix(name, "playlist_") || strings.HasPrefix(name, "saved_") ||
			name == "progress" || name == "progress_activity" || name == "libraries") {
			tables = append(tables, name)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	sort.Strings(tables)
	for _, table := range tables {
		var count int64
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM "`+strings.ReplaceAll(table, `"`, `""`)+`"`).Scan(&count); err != nil {
			return out, err
		}
		out.Counts[table] = count
	}
	for _, pragma := range []struct {
		query string
		dest  *int64
	}{{"PRAGMA page_count", &out.PageCount}, {"PRAGMA page_size", &out.PageSize}, {"PRAGMA freelist_count", &out.FreePages}} {
		if err = db.QueryRowContext(ctx, pragma.query).Scan(pragma.dest); err != nil {
			return out, err
		}
	}
	if err = db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&out.JournalMode); err != nil {
		return out, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out, err
	}
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		out.Files[entry.Name()] = info.Size()
	}
	return out, nil
}

func diffInventories(before, after inventory) inventoryDiff {
	out := inventoryDiff{
		SchemaHashBefore: hashInventory(before.Objects),
		SchemaHashAfter:  hashInventory(after.Objects),
		CountDeltas:      map[string]int64{},
		FileDeltas:       map[string]int64{},
	}
	out.SchemaEqual = out.SchemaHashBefore == out.SchemaHashAfter
	for name, hash := range before.Objects {
		other, ok := after.Objects[name]
		if !ok {
			out.RemovedObjects = append(out.RemovedObjects, name)
		} else if other != hash {
			out.ChangedObjects = append(out.ChangedObjects, name)
		}
	}
	for name := range after.Objects {
		if _, ok := before.Objects[name]; !ok {
			out.AddedObjects = append(out.AddedObjects, name)
		}
	}
	for name, count := range after.Counts {
		if delta := count - before.Counts[name]; delta != 0 {
			out.CountDeltas[name] = delta
		}
	}
	for name, count := range before.Counts {
		if _, ok := after.Counts[name]; !ok {
			out.CountDeltas[name] = -count
		}
	}
	for name, size := range after.Files {
		if delta := size - before.Files[name]; delta != 0 {
			out.FileDeltas[name] = delta
		}
	}
	out.PageDelta = after.PageCount - before.PageCount
	out.StoredQueryState = out.SchemaEqual && len(out.AddedObjects) == 0 && len(out.RemovedObjects) == 0 &&
		len(out.ChangedObjects) == 0 && len(out.CountDeltas) == 0
	return out
}

func hashInventory(objects map[string]string) string {
	keys := make([]string, 0, len(objects))
	for name := range objects {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	sum := sha256.New()
	for _, name := range keys {
		sum.Write([]byte(name))
		sum.Write([]byte{0})
		sum.Write([]byte(objects[name]))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func queryMemorySummary(report queryMemoryReport) string {
	raw, _ := json.Marshal(struct {
		Exact     bool  `json:"cacheCountersExact"`
		Prepared  int64 `json:"preparedMaxBytes"`
		Budget    int64 `json:"preparedBudgetBytes"`
		MaxFacet  int64 `json:"largestFacetBytes"`
		FacetCut  bool  `json:"anyFacetTruncated"`
		Inventory bool  `json:"noStoredQueryState"`
	}{report.CacheStatsExact, report.Prepared.MaxChargedBytes, report.Prepared.BudgetBytes,
		largestFacet(report.Facets), anyFacetTruncated(report.Facets), report.Inventory.StoredQueryState})
	return string(raw)
}

func largestFacet(probes []facetProbe) int64 {
	var largest int64
	for _, probe := range probes {
		largest = max(largest, probe.RetainedBytes)
	}
	return largest
}

func anyFacetTruncated(probes []facetProbe) bool {
	for _, probe := range probes {
		if probe.Truncated {
			return true
		}
	}
	return false
}
