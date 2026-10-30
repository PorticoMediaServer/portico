package metadata

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// Every metadata and artwork queue claim runs in a write transaction. Its plan
// must read one row per state from an index, never sort or scan the backlog:
// a claim that sorted every pending row held the write gate (and so the
// scanner) for as long as the backlog was long, once per provider request.
func TestQueueClaimsReadOneIndexedRowPerState(t *testing.T) {
	_, db, _, _ := integrationScreen(t)
	stamp := "2026-10-14T00:00:00Z"
	claims := map[string]string{
		"screen":            screenClaimSQL,
		"screen recover":    screenRecoverSQL,
		"artwork discovery": artworkDiscoveryClaimSQL,
		"artwork job":       artworkJobClaimSQL,
	}
	for name, query := range claims {
		rows, err := db.Query(`EXPLAIN QUERY PLAN `+query, stamp)
		if err != nil {
			t.Fatal(name, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		sorts := 0
		for _, line := range plan {
			if strings.HasPrefix(line, "SCAN ") && !strings.HasPrefix(line, "SCAN (subquery") {
				t.Fatalf("%s claim scans a table: %q in %v", name, line, plan)
			}
			if strings.Contains(line, "TEMP B-TREE") {
				sorts++
			}
		}
		// The outer merge sorts the handful of probe rows; the artwork job
		// claim also orders expired leases, which are bounded by its workers.
		allowed := 1
		if name == "artwork job" {
			allowed = 2
		}
		if sorts > allowed {
			t.Fatalf("%s claim sorts its backlog: %v", name, plan)
		}
	}
}

// The newest title is matched first within a class: what was just added is
// what a person is looking at on Recently Added, and it should not wait
// behind an old backlog. Shows still come before titles.
func TestScreenClaimTakesTheNewestTitleFirst(t *testing.T) {
	s, db, _, _ := integrationScreen(t)
	ctx := context.Background()
	c := catalogtest.New(t, db)
	library := c.Handle("lib")
	var newest catalogtest.Item
	for n := 0; n < 5; n++ {
		newest = c.Movie(library, "/local/order-"+strconv.Itoa(n)+".mkv", "Order "+strconv.Itoa(n), 2000+n)
	}
	var older int
	if err := db.QueryRow(`SELECT count(*) FROM screen_metadata_work WHERE status='pending' AND target_kind='item' AND target_id<?`, newest.ID).Scan(&older); err != nil || older < 4 {
		t.Fatal("fixture has no older backlog", older, err)
	}
	claim, err := s.claimScreen(ctx)
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	if claim.Base.TargetID != newest.Public {
		var title string
		_ = db.QueryRow(`SELECT title FROM catalog_entities WHERE public_id=pid_blob(?)`, claim.Base.TargetID).Scan(&title)
		t.Fatalf("the worker took %q before the newest title", title)
	}
}

// The claim's cost does not grow with the backlog: timed at three backlog
// sizes inside a write transaction, as the worker runs it.
func TestScreenClaimCostIsFlatAcrossBacklogSizes(t *testing.T) {
	requireScaleTier(t)
	_, db, _, _ := integrationScreen(t)
	var handle int64
	if err := db.QueryRow(`SELECT id FROM catalog_libraries ORDER BY id LIMIT 1`).Scan(&handle); err != nil {
		t.Fatal(err)
	}
	stamp := "2026-10-14T00:00:00Z"
	added := 0
	var costs []float64
	for _, size := range []int{2_000, 20_000, 200_000} {
		// Films and shows inserted as rows: their own triggers queue them for
		// matching, as a scan's writes do. A third are already matched.
		if _, err := db.Exec(`WITH RECURSIVE n(i) AS (SELECT ?1 UNION ALL SELECT i+1 FROM n WHERE i<?2)
 INSERT INTO catalog_entities(public_id,library_id,kind,title,sort_key,sort_head) SELECT randomblob(16),?3,CASE WHEN i%250=0 THEN 2 ELSE 1 END,'Backlog '||i,'backlog '||i,'b' FROM n`, added+1, size, handle); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE screen_metadata_work SET status='accepted' WHERE target_id%3=0 AND status='pending'`); err != nil {
			t.Fatal(err)
		}
		added = size
		if _, err := db.Exec(`ANALYZE screen_metadata_work`); err != nil {
			t.Fatal(err)
		}
		best := time.Duration(1 << 62)
		for n := 0; n < 20; n++ {
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			var kind string
			var target int64
			err = tx.QueryRow(screenClaimSQL, stamp).Scan(&kind, &target)
			best = min(best, time.Since(start))
			tx.Rollback()
			if err != nil {
				t.Fatal(err)
			}
		}
		costs = append(costs, float64(best.Microseconds()))
		t.Logf("backlog %d: claim %v", size, best)
	}
	if costs[2] > 5*costs[0]+1000 {
		t.Fatalf("claim cost grows with the backlog: %v microseconds", costs)
	}
}
