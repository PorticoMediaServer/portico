package catalog

// QueryMemoryStats is the exact, read-only accounting of the process-owned
// query caches. It reports only
// counters the caches already maintain, takes each cache's existing mutex
// briefly, never scans, promotes or evicts an entry, and retains nothing.
//
// It is not wired to a route, event, DDL, mutation or persistence path, and no
// caller may use it to keep query data. Budgets are not reported here because
// they are compile-time policy, not a measurement; the harness names them.
type QueryMemoryStats struct {
	FacetBytes    int64
	FacetEntries  int64
	FacetInFlight int64
	AcrossBytes   int64
	AcrossEntries int64
	CountEntries  int64
}

func (s *Service) QueryMemoryStats() QueryMemoryStats {
	if s == nil || s.state == nil {
		return QueryMemoryStats{}
	}
	out := QueryMemoryStats{}
	if cache := s.state.facetCache; cache != nil {
		cache.mu.Lock()
		out.FacetBytes = int64(cache.used)
		out.FacetEntries = int64(cache.order.Len())
		out.FacetInFlight = int64(len(cache.flights))
		cache.mu.Unlock()
	}
	if cache := s.state.acrossOrders; cache != nil {
		cache.mu.Lock()
		out.AcrossBytes = int64(cache.used)
		out.AcrossEntries = int64(cache.order.Len())
		cache.mu.Unlock()
	}
	s.state.countMu.Lock()
	out.CountEntries = int64(len(s.state.countCache))
	s.state.countMu.Unlock()
	return out
}
