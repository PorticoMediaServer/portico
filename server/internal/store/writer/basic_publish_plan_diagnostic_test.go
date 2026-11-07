package writer

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// TestBasicPublishPlanDiagnostic reports native compilation facts only.
// It deliberately bypasses Capture and does not execute the published statements.
func TestBasicPublishPlanDiagnostic(t *testing.T) {
	w, _ := basicActivationFixture(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sqliteconn.Assert(t.Context(), conn, w.config); err != nil {
		t.Fatal(err)
	}
	defs := basicPublishLeafDefinitions()
	if len(defs) != 4 {
		t.Fatalf("definitions=%d, want 4", len(defs))
	}
	for _, d := range defs {
		for _, cohort := range d.Cohorts {
			t.Logf("EQP_BEGIN id=%s cohort=%s bindings=%#v", d.ID, cohort.Name, cohort.Bindings)
			rows, err := conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, cohort.Bindings...)
			if err != nil {
				t.Fatalf("%s: %v", d.ID, err)
			}
			columns, err := rows.Columns()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			t.Logf("EQP_COLUMNS id=%s columns=%q", d.ID, columns)
			for rows.Next() {
				var node, parent, unused int64
				var detail string
				if err := rows.Scan(&node, &parent, &unused, &detail); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				t.Logf("EQP_NODE id=%s node=%d parent=%d unused=%d detail=%q", d.ID, node, parent, unused, detail)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			t.Logf("EQP_END id=%s cohort=%s", d.ID, cohort.Name)
		}
	}
}
