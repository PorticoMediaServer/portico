package read

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRBGroupPlans(t *testing.T) {
	f := rbGroupSource(t)
	definitions := RBGroupDefinitions()
	if len(definitions) != 56 {
		t.Fatal("review changed group inventory", len(definitions))
	}
	for _, d := range definitions {
		for _, c := range d.Cohorts {
			rows, e := f.conn.QueryContext(t.Context(), d.SQL, c.Bindings...)
			if e != nil {
				t.Fatal(d.ID, e)
			}
			present := rows.Next()
			err := rows.Err()
			rows.Close()
			if err != nil {
				t.Fatal(err)
			}
			if present != (c.Name == "present") {
				t.Fatalf("%s/%s mislabeled population", d.ID, c.Name)
			}
		}
	}
	cat, e := stmt.Describe(definitions)
	if e != nil {
		t.Fatal(e)
	}
	contract, e := schema.ReadContract("INT-INDEX-POLICY.json")
	if e != nil {
		t.Fatal(e)
	}
	policy, e := plan.ReadIndexPolicy(contract)
	if e != nil {
		t.Fatal(e)
	}
	cfg := browseReaderConfig()
	db, _, e := sqliteconn.Open(t.Context(), f.path, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	conn, e := db.Conn(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	type capture struct {
		Profile string
		Golden  plan.Golden
	}
	var captures []capture
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden, e := plan.Capture(t.Context(), conn, cat, browsePlanFixture(cfg, rbGroupFixtureHash(), stats), policy)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = stmt.New(definitions, golden.Receipt()); e != nil {
			t.Fatal(e)
		}
		captures = append(captures, capture{string(stats), golden})
	}
	actual, e := json.MarshalIndent(captures, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	if _, e = z.Write(actual); e != nil {
		t.Fatal(e)
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
		if e = os.WriteFile(filepath.Join(dir, "rb-group-plans.json.gz"), compressed.Bytes(), 0644); e != nil {
			t.Fatal(e)
		}
	}
	baseline, e := os.ReadFile("testdata/rb-group-plans.json.gz")
	if e != nil {
		t.Fatal("adopt runner Capture artifact after review:", e)
	}
	reader, e := gzip.NewReader(bytes.NewReader(baseline))
	if e != nil {
		t.Fatal(e)
	}
	expected, e := io.ReadAll(reader)
	reader.Close()
	if e != nil {
		t.Fatal(e)
	}
	if e = plan.CompareJSON(expected, actual); e != nil {
		t.Fatal(e)
	}
}

func TestRBGroupDefinitionLint(t *testing.T) {
	for _, d := range RBGroupDefinitions() {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatalf("%s: %+v\n%s", d.ID, issues, d.SQL)
		}
	}
}
