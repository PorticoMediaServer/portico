package compactcatalog

import (
	"testing"
	"time"
)

func TestSingleHolderKeyDoesNotClampItsRawPageToTwoRows(t *testing.T) {
	w := NewWorker(nil)
	w.PaceToHold(20 * time.Millisecond)
	for _, domain := range []int{DomainBrowseRows, DomainBrowseEdges, DomainAvailability,
		DomainItemMetrics, DomainAssetMetrics, DomainLibraryAvailability,
		DomainFacetCounts, DomainContainerRefresh, DomainAssetSources,
		DomainEntitySources, DomainLibrarySources, DomainMissingSources} {
		if got := w.batchFor(domain, BatchMax); got != 1 {
			t.Fatalf("unknown domain %d started with %d, want 1", domain, got)
		}
		// The previous turn advanced one cheap holder. That describes its
		// dirty-key window, not how many raw rows its cursor may read next.
		w.observeBatch(domain, 1, 100*time.Microsecond, 1)
		if got := w.batchFor(domain, BatchMax); got < upstreamQuantum {
			t.Fatalf("domain %d raw allowance pinned to %d by one holder key", domain, got)
		}
		if got := w.batchFor(domain, 1); got != 1 {
			t.Fatalf("domain %d ignored explicit one-row caller limit: %d", domain, got)
		}
		// The measured row bound still shrinks a costly holder immediately.
		w.observeBatch(domain, 1, 100*time.Microsecond, 160)
		if got := w.batchFor(domain, BatchMax); got != 1 {
			t.Fatalf("domain %d costly holder escaped row limit: %d", domain, got)
		}
	}
	// A true multi-key drain retains the original growth guard.
	w.observeBatch(DomainSearch, 1, 100*time.Microsecond, 1)
	if got := w.batchFor(DomainSearch, BatchMax); got != 2 {
		t.Fatalf("multi-key drain growth guard changed: %d", got)
	}
}
