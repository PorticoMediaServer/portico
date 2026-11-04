package compactcatalog

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

var counterTraceEnabled = os.Getenv("PORTICO_COUNTER_TRACE") == "1"

type counterTraceKey struct{}

func MarkCounterTrace(ctx context.Context) context.Context {
	return context.WithValue(ctx, counterTraceKey{}, true)
}
func counterTraceMarked(ctx context.Context) bool {
	marked, _ := ctx.Value(counterTraceKey{}).(bool)
	return marked
}

var counterTraceTotals struct {
	sync.Mutex
	captured, expected, meters, probes int64
}

func counterTraceStep(ctx context.Context) (context.Context, func()) {
	before := dbwork.CostFrom(ctx).Statements
	ctx, read := dbwork.TraceQueryPlans(ctx)
	return ctx, func() {
		queries := read()
		var meters, probes int64
		for _, q := range queries {
			if strings.Contains(q, "compact_visibility_source_jobs_turn") {
				probes++
			}
			if strings.Contains(strings.ToLower(q), "total_changes(") {
				meters++
			}
		}
		after := dbwork.CostFrom(ctx).Statements
		counterTraceTotals.Lock()
		counterTraceTotals.captured += int64(len(queries))
		counterTraceTotals.expected += after - before
		counterTraceTotals.meters += meters
		counterTraceTotals.probes += probes
		counterTraceTotals.Unlock()
	}
}
func CounterTraceSummary() (int64, int64, int64, int64) {
	counterTraceTotals.Lock()
	defer counterTraceTotals.Unlock()
	return counterTraceTotals.captured, counterTraceTotals.expected, counterTraceTotals.meters, counterTraceTotals.probes
}
