package writer

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Every retirement statement must be one bounded indexed SEARCH, before and
// after ANALYZE. This is a live native EXPLAIN, not a recorded golden: builders
// do not run tests, so no Capture artifact is invented for this commit.
func TestMGGuideReclaimNativeStatementPlans(t *testing.T) {
	defs := mgGuideReclaimDefinitions()
	if len(defs) != 14 {
		t.Fatal("closed retirement inventory", len(defs))
	}
	if _, err := stmttest.New(t, defs); err != nil {
		t.Fatal(err)
	}
	w, _ := testCanonicalExecutor(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, analyzed := range []bool{false, true} {
		if analyzed {
			if _, err := conn.ExecContext(t.Context(), `ANALYZE`); err != nil {
				t.Fatal(err)
			}
		}
		for _, d := range defs {
			if issues := plan.LintSQLOn(t.Context(), conn, d.SQL); len(issues) > 0 {
				t.Fatal(d.ID, issues)
			}
			rows, err := conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, d.Cohorts[0].Bindings...)
			if err != nil {
				t.Fatal(d.ID, err)
			}
			count := 0
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(d.ID, err)
				}
				if !strings.Contains(detail, "SEARCH ") || strings.Contains(detail, "TEMP B-TREE") {
					t.Fatal(d.ID, detail)
				}
				count++
			}
			err = rows.Err()
			rows.Close()
			if err != nil || count == 0 {
				t.Fatal(d.ID, count, err)
			}
		}
	}
}

// The closed inventory is additive and MG-owned: no shared registry list is
// appended to, and no DDL is declared by this task.
func TestMGGuideReclaimStatementInventory(t *testing.T) {
	var reads, writes int
	for _, d := range mgGuideReclaimDefinitions() {
		switch d.Kind {
		case stmt.Read:
			reads++
		case stmt.Write:
			writes++
		default:
			t.Fatal("retirement declared a non-domain statement", d.ID, d.Kind)
		}
		for _, site := range d.CallSites {
			if !strings.HasSuffix(site, "mg_guide_reclaim_native.go") {
				t.Fatal("foreign call site", d.ID, site)
			}
		}
	}
	if reads != 9 || writes != 5 {
		t.Fatal("retirement statement split", reads, writes)
	}
}
