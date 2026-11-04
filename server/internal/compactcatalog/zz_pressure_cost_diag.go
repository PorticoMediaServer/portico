package compactcatalog

import (
	"context"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

var pressureCostEnabled = os.Getenv("PORTICO_PRESSURE_COST") == "1"

type pressureCostKey struct{}

func activePressureCost(ctx context.Context) bool {
	active, _ := ctx.Value(pressureCostKey{}).(bool)
	return pressureCostEnabled && active
}

type pressureCost struct {
	calls, units, rows, statements, transactions, vm int64
	elapsed, max                                     time.Duration
}

var pressureCosts = struct {
	sync.Mutex
	values map[string]*pressureCost
}{values: map[string]*pressureCost{}}

func notePressureCost(key string, started time.Time, before dbwork.Cost, ctx context.Context, units int, rows int64) {
	if !activePressureCost(ctx) {
		return
	}
	elapsed, after := time.Since(started), dbwork.CostFrom(ctx)
	pressureCosts.Lock()
	defer pressureCosts.Unlock()
	item := pressureCosts.values[key]
	if item == nil {
		item = &pressureCost{}
		pressureCosts.values[key] = item
	}
	item.calls++
	item.units += int64(units)
	item.rows += rows
	item.statements += after.Statements - before.Statements
	item.transactions += after.Transactions - before.Transactions
	item.vm += after.VMSteps - before.VMSteps
	item.elapsed += elapsed
	if elapsed > item.max {
		item.max = elapsed
	}
}

func dumpPressureCosts() {
	pressureCosts.Lock()
	defer pressureCosts.Unlock()
	keys := make([]string, 0, len(pressureCosts.values))
	for key := range pressureCosts.values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		v := pressureCosts.values[key]
		log.Printf("PRESSURE-COST key=%s calls=%d units=%d rows=%d statements=%d transactions=%d vm=%d wall=%s max=%s", key, v.calls, v.units, v.rows, v.statements, v.transactions, v.vm, v.elapsed, v.max)
	}
}
