package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The tiny query-memory pass must be exact where the store reports bytes, must
// stay inside its budget, and must leave the database byte-for-byte unchanged
// in schema and stored rows. Facet/cross exact counters depend on the
// lead-gated read-only observer; without it the report says exact=false.
func TestQueryMemoryChurnLeavesNoStoredQueryState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	dir, db, service := tinyPlaylistFixture(t)
	queryMemoryExercise = queryMemoryOptions{preparedQueries: 3}
	report, err := exerciseQueryMemory(context.Background(), db, service, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Inventory.StoredQueryState {
		t.Fatalf("query churn changed stored state: %+v", report.Inventory)
	}
	if report.Prepared.BudgetBytes <= 0 {
		t.Fatal("prepared store reported no budget")
	}
	if report.Prepared.MaxChargedBytes > report.Prepared.BudgetBytes {
		t.Fatalf("prepared read bytes %d over budget %d", report.Prepared.MaxChargedBytes, report.Prepared.BudgetBytes)
	}
	if !report.Prepared.Exact || !report.Expiry.PreparedRestart {
		t.Fatalf("prepared read accounting or restart semantics: %+v", report.Prepared)
	}
	if report.Prepared.RestartBytes != 0 || report.Prepared.RestartEntries != 0 {
		t.Fatalf("restart kept prepared handles: %+v", report.Prepared)
	}
	if len(report.Facets) == 0 {
		t.Fatal("no facet page was probed")
	}
	if len(report.Heap) < 4 {
		t.Fatalf("heap snapshots: %d", len(report.Heap))
	}
	if report.Inventory.SchemaHashBefore != report.Inventory.SchemaHashAfter {
		t.Fatalf("schema changed: %+v", report.Inventory)
	}
	if !report.CacheStatsExact {
		t.Fatal("exact cache counters were not reported")
	}
	if report.CachePeak.FacetBytes > report.Budgets.FacetBytes || report.CachePeak.AcrossBytes > report.Budgets.AcrossBytes {
		t.Fatalf("cache peak over budget: %+v", report.CachePeak)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "query-memory-report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("empty query-memory report")
	}
}
