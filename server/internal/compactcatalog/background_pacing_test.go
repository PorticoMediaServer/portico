package compactcatalog_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestDerivedWorkerCompletesWithForegroundPressure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	movies := c.Library("m", "Movies", "movie", "/m")
	const count = 130 // Several real derived-data quanta, rather than one batch.
	for i := 0; i < count; i++ {
		c.Movie(movies, fmt.Sprintf("/m/%03d.mkv", i), fmt.Sprintf("Film %03d", i), 2000)
	}
	dbwork.RegisterForegroundProbe(t.Name(), func() bool { return true })
	defer dbwork.RegisterForegroundProbe(t.Name(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	started := time.Now()
	pressureBefore := dbwork.Pressure()
	defer func() {
		pressureAfter := dbwork.Pressure()
		t.Logf("worker running window=%s yield-boundaries=%d foreground-wait=%dms", time.Since(started), pressureAfter.Yields-pressureBefore.Yields, pressureAfter.YieldWaitMillis-pressureBefore.YieldWaitMillis)
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		compactcatalog.NewWorker(c.DB).Run(ctx)
	}()
	defer func() { cancel(); <-done }()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline.C:
			// Stop the worker first so the counts below are one consistent
			// snapshot rather than a queue inspected mid-quantum.
			cancel()
			<-done
			reportPressureStall(t, c.DB, count)
			t.Fatal("foreground pressure prevented derived jobs completing")
		case <-tick.C:
			var dirty, derived int
			if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_dirty`).Scan(&dirty); err != nil {
				t.Fatal(err)
			}
			if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_browse_rows WHERE kind=?`, compactcatalog.Movie).Scan(&derived); err != nil {
				t.Fatal(err)
			}
			if dirty == 0 && derived == count {
				if err := compactcatalog.CheckBrowseBlocks(context.Background(), c.DB); err != nil {
					t.Fatal(err)
				}
				return
			}
		}
	}
}

// reportPressureStall attributes a timeout to the queue the paced worker is
// still holding rather than to the budget: which domains are dirty, which
// bounded phase of the asset-source walk is outstanding, which availability
// holders are mid-pass, and whether a derivation is rebuilding or set aside.
// It only reads.
func reportPressureStall(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var dirty, derived int
	if err := db.QueryRow(`SELECT count(*) FROM catalog_dirty`).Scan(&dirty); err != nil {
		t.Logf("dirty units: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM catalog_browse_rows WHERE kind=?`, compactcatalog.Movie).Scan(&derived); err != nil {
		t.Logf("derived rows: %v", err)
	}
	t.Logf("stall: %d dirty units remain, %d/%d movie browse rows derived", dirty, derived, want)
	t.Logf("paced background quantum boundaries (yields) so far: %d", dbwork.Pressure().Yields)
	for _, check := range []struct{ label, query string }{
		{"dirty by domain", `SELECT domain||':'||count(*) FROM catalog_dirty GROUP BY domain ORDER BY domain`},
		{"asset sources by phase", `SELECT phase||':'||count(*) FROM catalog_upstream_sources WHERE domain=41 GROUP BY phase ORDER BY phase`},
		{"upstream sources by domain/phase", `SELECT domain||'/'||phase||':'||count(*) FROM catalog_upstream_sources GROUP BY domain,phase ORDER BY domain,phase`},
		{"availability holders by phase", `SELECT phase||':'||count(*) FROM catalog_availability_work GROUP BY phase ORDER BY phase`},
		{"rebuilding derivations", `SELECT domain||'@'||rebuild_cursor FROM catalog_derivations WHERE rebuilding=1 ORDER BY domain`},
		{"set aside", `SELECT domain||':'||count(*) FROM catalog_derivation_failures GROUP BY domain ORDER BY domain`},
	} {
		logCounts(t, db, check.label, check.query)
	}
}

func logCounts(t *testing.T, db *sql.DB, label, query string) {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Logf("%s: %v", label, err)
		return
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var cell string
		if err := rows.Scan(&cell); err != nil {
			t.Logf("%s: %v", label, err)
			return
		}
		out = append(out, cell)
	}
	if err := rows.Err(); err != nil {
		t.Logf("%s: %v", label, err)
		return
	}
	t.Logf("%s: %s", label, strings.Join(out, " "))
}
