package read

import (
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"testing"
)

func TestIAExtraPlans(t *testing.T) {
	f := newIAExtraSource(t)
	defs := IAExtraHarnessDefinitions()
	for _, d := range defs {
		for _, c := range d.Cohorts {
			rows, e := f.conn.QueryContext(t.Context(), d.SQL, c.Bindings...)
			if e != nil {
				t.Fatal(e)
			}
			has := rows.Next()
			e = rows.Err()
			rows.Close()
			if e != nil || has != (c.Name == "present") {
				t.Fatalf("%s/%s population: %v", d.ID, c.Name, e)
			}
		}
	}
	type capture struct {
		Profile string
		Golden  plan.Golden
	}
	var captures []capture
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		g := captureBrowse(t, f, defs, stats, iaAudioHash(t))
		if _, e := stmt.New(defs, g.Receipt()); e != nil {
			t.Fatal(e)
		}
		captures = append(captures, capture{string(stats), g})
	}
	actual, e := json.MarshalIndent(captures, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	iaAudioCompareGolden(t, "testdata/ia-extra-plans.json.gz", actual)
}
