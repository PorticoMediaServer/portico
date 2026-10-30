package catalog

import (
	"sync"
	"testing"
	"time"
)

// QueryMemoryStats must report the caches' own charged bytes and counts, must
// not promote, scan or retain an entry, and must not leave a cache lock held.
func TestQueryMemoryStatsChargedBytesEvictionsAndLock(t *testing.T) {
	s := New(nil)
	if stats := s.QueryMemoryStats(); stats != (QueryMemoryStats{}) {
		t.Fatalf("empty service reported %+v", stats)
	}
	restore := facetCacheBytes
	facetCacheBytes = 1000
	defer func() { facetCacheBytes = restore }()
	page := func(label string, values int) FacetPage {
		out := FacetPage{Field: label, Values: []FacetValue{}}
		for index := 0; index < values; index++ {
			out.Values = append(out.Values, FacetValue{Value: label, Label: label, Count: 1})
		}
		return out
	}
	small := page("a", 1) // 128 + (64+1+1) = 194 bytes, under the 250-byte quarter
	cache := s.state.facetCache
	cache.put("a", facetCacheEntry{page: small, expires: time.Now().Add(time.Minute)})
	cache.put("b", facetCacheEntry{page: page("b", 1), expires: time.Now().Add(time.Minute)})
	stats := s.QueryMemoryStats()
	if want := int64(2 * facetPageCost(small)); stats.FacetBytes != want || stats.FacetEntries != 2 || stats.FacetInFlight != 0 {
		t.Fatalf("two cached pages: %+v, want %d bytes", stats, want)
	}
	// Reading stats must not promote the least recently used entry.
	if back := cache.order.Back(); back == nil || back.Value.(*facetNode).key != "a" {
		t.Fatalf("stats promoted an entry: %v", back)
	}
	// Filling past the byte budget evicts oldest-first, and the reported bytes
	// follow the evictions exactly: five 194-byte pages fit in 1000, not six.
	for _, key := range []string{"c", "d", "e", "f"} {
		cache.put(key, facetCacheEntry{page: page(key, 1), expires: time.Now().Add(time.Minute)})
	}
	stats = s.QueryMemoryStats()
	if stats.FacetEntries != 5 || stats.FacetBytes != int64(5*facetPageCost(small)) {
		t.Fatalf("after eviction: %+v", stats)
	}
	if back := cache.order.Back(); back == nil || back.Value.(*facetNode).key != "b" {
		t.Fatalf("eviction did not remove oldest-first: %v", back)
	}
	// A single list over a quarter of the budget is not retained at all.
	oversized := page("big", 4)
	if cost := facetPageCost(oversized); cost <= int(facetCacheBytes)/4 {
		t.Fatalf("oversized fixture costs %d, not over %d", cost, facetCacheBytes/4)
	}
	before := s.QueryMemoryStats()
	cache.put("big", facetCacheEntry{page: oversized, expires: time.Now().Add(time.Minute)})
	if after := s.QueryMemoryStats(); after.FacetBytes != before.FacetBytes || after.FacetEntries != before.FacetEntries {
		t.Fatalf("an oversized list was retained: %+v -> %+v", before, after)
	}
	// Across-order bytes are the same declared costs the cache charges.
	across := s.state.acrossOrders
	across.put("one", &acrossOrdered{ids: []int64{1, 2}, keys: []string{"k1", "k2"}, letters: []string{"a", "b"}}, time.Minute)
	across.put("two", &acrossOrdered{ids: []int64{3}, keys: []string{"k3"}, letters: []string{"c"}}, time.Minute)
	stats = s.QueryMemoryStats()
	if stats.AcrossEntries != 2 || stats.AcrossBytes != int64(across.used) || stats.AcrossBytes <= 0 {
		t.Fatalf("across cache: %+v used=%d", stats, across.used)
	}
	// The maintained count history is bounded by its own capacity.
	s.storeCount(countKey{library: "a", profile: "p", mode: "m"}, 3)
	s.storeCount(countKey{library: "b", profile: "p", mode: "m"}, 4)
	if stats = s.QueryMemoryStats(); stats.CountEntries != 2 {
		t.Fatalf("count entries: %+v", stats)
	}
	// The stats call releases every lock it took.
	released := make(chan struct{})
	go func() {
		cache.mu.Lock()
		cache.mu.Unlock()
		across.mu.Lock()
		across.mu.Unlock()
		s.state.countMu.Lock()
		s.state.countMu.Unlock()
		close(released)
	}()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("QueryMemoryStats left a cache lock held")
	}
	// Concurrent writers and stats readers must not race or change accounting.
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		for index := 0; index < 200; index++ {
			s.QueryMemoryStats()
		}
	}()
	go func() {
		defer wait.Done()
		for index := 0; index < 200; index++ {
			cache.put("churn", facetCacheEntry{page: small, expires: time.Now().Add(time.Minute)})
		}
	}()
	wait.Wait()
	if stats = s.QueryMemoryStats(); stats.FacetBytes > int64(facetCacheBytes) {
		t.Fatalf("facet bytes over budget after churn: %+v", stats)
	}
}
