package read

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"io"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordRSTechnicalPlans = flag.Bool("record-rs-technical-plans", false, "deliberately record selected technical SEARCH-only plans")

// TestRSTechnicalPlans records the actual unanalyzed and analyzed plans of the
// inert technical inventory against the exact fixture the reader composes. The
// golden's fixture hash covers rs_technical_test.go and
// rs_technical_fixture_test.go, so any change to the fixture or its controls
// requires a deliberate re-record.
func TestRSTechnicalPlans(t *testing.T) {
	f := rsTechnicalSource(t)
	definitions := TechnicalFactsDefinitions()
	for _, definition := range definitions {
		for _, cohort := range definition.Cohorts {
			rows, err := f.conn.QueryContext(t.Context(), definition.SQL, cohort.Bindings...)
			if err != nil {
				t.Fatalf("%s/%s: %v", definition.ID, cohort.Name, err)
			}
			present := rows.Next()
			err = rows.Err()
			rows.Close()
			if err != nil || present != (cohort.Name == "present") {
				t.Fatalf("%s/%s mislabeled: present=%v err=%v", definition.ID, cohort.Name, present, err)
			}
		}
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	catalogue, err := stmt.Describe(definitions)
	if err != nil {
		t.Fatal(err)
	}
	cfg := browseReaderConfig()
	db, _, err := sqliteconn.Open(t.Context(), f.path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	type capture struct {
		Profile string
		Golden  plan.Golden
	}
	var captures []capture
	for _, statistics := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if statistics == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden, err := plan.Capture(t.Context(), conn, catalogue, browsePlanFixture(cfg, rsTechnicalHash(t), statistics), policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = stmt.New(definitions, golden.Receipt()); err != nil {
			t.Fatal("actual Capture receipt", err)
		}
		if len(golden.Manifest) != len(definitions) || len(golden.Plans) != 2*len(definitions) {
			t.Fatalf("inventory %d/%d want %d/%d", len(golden.Manifest), len(golden.Plans), len(definitions), 2*len(definitions))
		}
		captures = append(captures, capture{string(statistics), golden})
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	const path = "testdata/rs-technical-plans.json.gz"
	if *recordRSTechnicalPlans {
		var buf bytes.Buffer
		z := gzip.NewWriter(&buf)
		if _, err = z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, buf.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(expected))
	if err != nil {
		t.Fatal(err)
	}
	expected, err = io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
}
