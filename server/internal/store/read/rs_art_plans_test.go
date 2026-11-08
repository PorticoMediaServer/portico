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

var recordRSArtPlans = flag.Bool("record-rs-art-plans", false, "deliberately record reviewed native browse plans")

func TestRSArtPlans(t *testing.T) {
	f := newRSArtSource(t)
	for _, d := range ArtworkDefinitions() {
		for _, c := range d.Cohorts {
			rows, e := f.conn.QueryContext(t.Context(), d.SQL, c.Bindings...)
			if e != nil {
				t.Fatal(e)
			}
			present := rows.Next()
			if e = rows.Err(); e != nil {
				t.Fatal(e)
			}
			rows.Close()
			if present != (c.Name == "present") {
				t.Fatalf("%s/%s mislabeled population", d.ID, c.Name)
			}
		}
	}

	fixtureHash := rsArtHash(t)
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	definitions := ArtworkDefinitions()
	catalogue, err := stmt.Describe(definitions)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogue.Manifest()) != 5 {
		t.Fatal("review changed statement inventory")
	}
	path := "testdata/rs-art-plans.json.gz"
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
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden, err := plan.Capture(t.Context(), conn, catalogue, browsePlanFixture(cfg, fixtureHash, stats), policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stmt.New(definitions, golden.Receipt()); err != nil {
			t.Fatal("actual Reader Capture cannot authorize exact definitions", err)
		}
		captures = append(captures, capture{Profile: string(stats), Golden: golden})
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir+"/rs-art-plans.json", actual, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if *recordRSArtPlans {
		var compressed bytes.Buffer
		z := gzip.NewWriter(&compressed)
		if _, err = z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
	for _, capture := range captures {
		if len(capture.Golden.Manifest) != 5 || len(capture.Golden.Plans) != 10 {
			t.Fatal("incomplete statement/cohort Capture coverage")
		}
	}
}
