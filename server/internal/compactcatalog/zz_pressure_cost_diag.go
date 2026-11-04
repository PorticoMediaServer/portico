package compactcatalog

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	sqlite "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

var pressureCostEnabled = os.Getenv("PORTICO_PRESSURE_COST") == "1"
var pressureNativeEnabled = os.Getenv("PORTICO_PRESSURE_NATIVE") == "1"

func pressureDomain(ctx context.Context, domain string) context.Context {
	if !pressureNativeEnabled || !activePressureCost(ctx) {
		return ctx
	}
	return sqlite.WithPressureDomain(ctx, domain)
}

type pressureSQLKey struct{ Domain, Kind, Query string }

var pressureSQL = struct {
	sync.Mutex
	counts             map[pressureSQLKey]int64
	expected, captured int64
}{counts: map[pressureSQLKey]int64{}}

func notePressureSQL(domain string, trace func() []string, expected int64) {
	if trace == nil {
		return
	}
	lines := trace()
	pressureSQL.Lock()
	defer pressureSQL.Unlock()
	pressureSQL.expected += expected
	pressureSQL.captured += int64(len(lines))
	for _, query := range lines {
		kind := "empty"
		if fields := strings.Fields(query); len(fields) != 0 {
			kind = strings.ToUpper(fields[0])
		}
		pressureSQL.counts[pressureSQLKey{domain, kind, query}]++
	}
}

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
	if pressureNativeEnabled {
		sqlite.DumpPressureNativeTrace()
		pressureSQL.Lock()
		keys := make([]pressureSQLKey, 0, len(pressureSQL.counts))
		for key := range pressureSQL.counts {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i].Domain+keys[i].Query < keys[j].Domain+keys[j].Query })
		for _, key := range keys {
			entry, _ := json.Marshal(struct {
				pressureSQLKey
				Count int64
			}{key, pressureSQL.counts[key]})
			log.Printf("PRESSURE-SQL %s", entry)
		}
		log.Printf("PRESSURE-SQL-COVERAGE expected=%d captured=%d", pressureSQL.expected, pressureSQL.captured)
		pressureSQL.Unlock()
	}
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
