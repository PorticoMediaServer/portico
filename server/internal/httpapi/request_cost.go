package httpapi

import (
	"sort"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// What a request costs the database is a property of the route, not of the
// machine it ran on. A latency budget can be met by a fast host hiding a query
// that scans the library; a statement count cannot be, which is why the load
// test gates on this table and not only on percentiles.
//
// The registry is process-wide on purpose. One process serves one database, the
// diagnostics endpoint reads it, and a test resets it before the one request it
// wants to measure.

// RouteCost is one route pattern's accumulated database cost.
type RouteCost struct {
	Route           string `json:"route"`
	Lane            string `json:"lane"`
	Requests        int64  `json:"requests"`
	Statements      int64  `json:"statements"`
	Transactions    int64  `json:"transactions"`
	Acquisitions    int64  `json:"acquisitions"`
	MaxStatements   int64  `json:"maxStatements"`
	MaxTransactions int64  `json:"maxTransactions"`
	MaxAcquisitions int64  `json:"maxAcquisitions"`
	MaxReadMillis   int64  `json:"maxReadMillis"`
}

// Mean statements per request, which is the number the budgets are written
// against. A route never served reports zero rather than dividing by it.
func (c RouteCost) MeanStatements() float64 {
	if c.Requests == 0 {
		return 0
	}
	return float64(c.Statements) / float64(c.Requests)
}

// MeanAcquisitions is the mean number of pooled connections a request took.
func (c RouteCost) MeanAcquisitions() float64 {
	if c.Requests == 0 {
		return 0
	}
	return float64(c.Acquisitions) / float64(c.Requests)
}

// routeCostCeiling bounds the table so an unmatched-pattern storm cannot grow it
// without limit. Real servers publish a few hundred routes.
const routeCostCeiling = 1024

var routeCostRegistry = struct {
	mu    sync.Mutex
	table map[string]*RouteCost
}{table: map[string]*RouteCost{}}

func recordRouteCost(pattern, lane string, cost dbwork.Cost) {
	if pattern == "" {
		pattern = "(unmatched)"
	}
	routeCostRegistry.mu.Lock()
	defer routeCostRegistry.mu.Unlock()
	entry := routeCostRegistry.table[pattern]
	if entry == nil {
		if len(routeCostRegistry.table) >= routeCostCeiling {
			return
		}
		entry = &RouteCost{Route: pattern, Lane: lane}
		routeCostRegistry.table[pattern] = entry
	}
	entry.Requests++
	entry.Statements += cost.Statements
	entry.Transactions += cost.Transactions
	entry.Acquisitions += cost.Acquisitions
	if cost.Statements > entry.MaxStatements {
		entry.MaxStatements = cost.Statements
	}
	if cost.Transactions > entry.MaxTransactions {
		entry.MaxTransactions = cost.Transactions
	}
	if cost.Acquisitions > entry.MaxAcquisitions {
		entry.MaxAcquisitions = cost.Acquisitions
	}
	if cost.ReadMillis > entry.MaxReadMillis {
		entry.MaxReadMillis = cost.ReadMillis
	}
}

// RouteCosts reports every route that has been served, ordered by pattern.
func RouteCosts() []RouteCost {
	routeCostRegistry.mu.Lock()
	defer routeCostRegistry.mu.Unlock()
	out := make([]RouteCost, 0, len(routeCostRegistry.table))
	for _, entry := range routeCostRegistry.table {
		out = append(out, *entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Route < out[j].Route })
	return out
}

// RouteCostFor reports one route pattern's accumulated cost. The pattern is the
// one `http.ServeMux` matched, for example "GET /v1/home".
func RouteCostFor(pattern string) RouteCost {
	routeCostRegistry.mu.Lock()
	defer routeCostRegistry.mu.Unlock()
	if entry := routeCostRegistry.table[pattern]; entry != nil {
		return *entry
	}
	return RouteCost{Route: pattern}
}

// ResetRouteCosts empties the table. A test that wants the cost of exactly one
// request resets, issues it, and reads RouteCostFor.
func ResetRouteCosts() {
	routeCostRegistry.mu.Lock()
	defer routeCostRegistry.mu.Unlock()
	routeCostRegistry.table = map[string]*RouteCost{}
}
