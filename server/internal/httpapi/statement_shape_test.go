package httpapi

import (
	"bytes"
	"net/http/httptest"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/httpapi/fixture"
)

// A statement count says a route is expensive. It does not say which statement
// is repeated, and that is the only thing that tells you what to fix. This
// reports the shape: how many times each distinct statement ran for one request.
//
// It is a report rather than a gate — the gates live in the load test — and it
// is how both of the N+1s this workstream removed were found rather than
// guessed at: `Get` called once per related title on the detail page, and the
// per-kind enrichment queries inside `mediaPage`.
func TestStatementShapeOfCompositeReads(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	tier := performanceTier{name: "shape", shape: fixture.Tiny(), concurrentViewers: 1, iterations: 1, maximumP95: time.Minute, maximumP99: time.Minute, allowBoundedOverload: true}
	f := newLoadFixture(t, tier)
	token := f.viewers[0].AccessToken
	type probe struct{ method, path, body, pattern string }
	for _, request := range []probe{
		{"GET", "/v1/home", "", "GET /v1/home"},
		{"GET", "/v1/items/" + f.items[0] + "/detail", "", "GET /v1/items/{id}/detail"},
		{"GET", "/v1/search?q=Word&limit=10", "", "GET /v1/search"},
		{"POST", "/v1/libraries/" + f.library + "/browse", `{"pivot":"movies","limit":40}`, "POST /v1/libraries/{id}/browse"},
		{"GET", "/v1/libraries/" + f.library + "/facets?field=genre", "", "GET /v1/libraries/{id}/facets"},
		{"GET", "/v1/people?q=Person&limit=20", "", "GET /v1/people"},
	} {
		path := request.path
		// Warm the authority caches first, so the shape is the shape of the read
		// rather than of the sign-in in front of it.
		f.callTimed(token, request.method, path, nil)
		r := httptest.NewRequest(request.method, path, bytes.NewBufferString(request.body))
		r.Header.Set("Authorization", "Bearer "+token)
		ResetRouteCosts()
		ctx, collect := dbwork.TraceStatements(r.Context())
		f.handler.ServeHTTP(httptest.NewRecorder(), r.WithContext(ctx))
		counts := map[string]int{}
		total := 0
		for _, statement := range collect() {
			counts[statement]++
			total++
		}
		type entry struct {
			statement string
			count     int
		}
		rows := make([]entry, 0, len(counts))
		for statement, count := range counts {
			rows = append(rows, entry{statement, count})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].count != rows[j].count {
				return rows[i].count > rows[j].count
			}
			return rows[i].statement < rows[j].statement
		})
		cost := RouteCostFor(request.pattern)
		t.Logf("%s: %d statements, %d distinct, %d pooled acquisitions, %d transactions",
			path, total, len(rows), cost.Acquisitions, cost.Transactions)
		limit := 8
		if os.Getenv("PORTICO_SHAPE_ALL") == "1" {
			limit = len(rows)
		}
		for index, row := range rows {
			if index == limit || (row.count < 2 && os.Getenv("PORTICO_SHAPE_ALL") != "1") {
				break
			}
			t.Logf("    %3d x  %s", row.count, row.statement)
		}
	}
}
