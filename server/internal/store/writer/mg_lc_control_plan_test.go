package writer

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Closed structural inventory for the four channel-control purposes. It runs
// native EXPLAIN on the embedded G/013 tables and adds no golden: the desk
// records the family Capture at train level (this builder does not run it).
func TestMGLCControlStatementDefinitions(t *testing.T) {
	defs := mgLCControlDefinitions()
	if len(defs) != 11 {
		t.Fatal("finite control statement inventory", len(defs))
	}
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	w, _ := testCanonicalExecutor(t)
	for _, d := range defs {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
		if len(d.Cohorts) == 0 {
			t.Fatal("missing cohort", d.ID)
		}
		rows, err := w.db.QueryContext(t.Context(), "EXPLAIN "+d.SQL, d.Cohorts[0].Bindings...)
		if err != nil {
			t.Fatal(d.ID, err)
		}
		n := 0
		for rows.Next() {
			n++
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil || n == 0 {
			t.Fatal("native compile", d.ID, n, err)
		}
	}
	ds := mgLCControlDescriptors()
	if len(ds) != 4 {
		t.Fatal(ds)
	}
	if _, err := buildCommandDescriptors(commandRegistration{owner: "MG", descriptors: ds}); err != nil {
		t.Fatal(err)
	}
}
