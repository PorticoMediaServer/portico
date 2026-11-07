package read

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordRSCollectionPlans = flag.Bool("record-rs-collection-plans", false, "deliberately record reviewed native browse plans")

func TestRSCollectionPlans(t *testing.T) {
	f := newRSCollectionSource(t)
	for _, d := range CollectionDefinitions() {
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

	fixtureHash := rsCollectionHash(t)
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	definitions := CollectionDefinitions()
	catalogue, err := stmt.Describe(definitions)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogue.Manifest()) != 4 {
		t.Fatal("review changed statement inventory")
	}
	path := "testdata/rs-collection-plans.json.gz"
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
	if *recordRSCollectionPlans {
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
		if len(capture.Golden.Manifest) != 4 || len(capture.Golden.Plans) != 8 {
			t.Fatal("incomplete statement/cohort Capture coverage")
		}
	}
}

func TestRSCollectionCaptureRejectsUnboundedSeeks(t *testing.T) {
	f := newRSCollectionSource(t)
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
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	original := CollectionDefinitions()[2]
	for _, mutation := range []struct{ old, new string }{
		{" INDEXED BY catalog_collection_members_page", ""},
		{"m.item_id>?", "m.item_id+0>?"},
	} {
		mutated := original
		mutated.SQL = strings.Replace(original.SQL, mutation.old, mutation.new, 1)
		definitions, err := stmt.Describe([]stmt.Definition{mutated})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = plan.Capture(t.Context(), conn, definitions, browsePlanFixture(cfg, rsCollectionHash(t), plan.Unanalyzed), policy); err == nil {
			t.Fatal("lost indexed range was accepted", mutation.old)
		}
	}
}
