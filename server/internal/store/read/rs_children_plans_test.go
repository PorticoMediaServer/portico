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

var recordRSChildrenPlans = flag.Bool("record-rs-children-plans", false, "deliberately record ordered child SEARCH-only plans")

func TestRSChildrenPlans(t *testing.T) {
	f := rsChildrenSource(t)
	definitions := RSChildrenDefinitions()
	for _, d := range definitions {
		for _, c := range d.Cohorts {
			rows, err := f.conn.QueryContext(t.Context(), d.SQL, c.Bindings...)
			if err != nil {
				t.Fatalf("%s/%s: %v", d.ID, c.Name, err)
			}
			present := rows.Next()
			err = rows.Err()
			rows.Close()
			if err != nil || present != (c.Name == "present") {
				t.Fatalf("%s/%s mislabeled: present=%v err=%v", d.ID, c.Name, present, err)
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
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden, err := plan.Capture(t.Context(), conn, catalogue, browsePlanFixture(cfg, rsChildrenFixtureHash(t), stats), policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = stmt.New(definitions, golden.Receipt()); err != nil {
			t.Fatal("actual Capture receipt", err)
		}
		if len(golden.Manifest) != 28 || len(golden.Plans) != 56 {
			t.Fatalf("inventory %d/%d", len(golden.Manifest), len(golden.Plans))
		}
		captures = append(captures, capture{string(stats), golden})
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	const path = "testdata/rs-children-plans.json.gz"
	if *recordRSChildrenPlans {
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
	// Two concrete production-definition mutants: an unforced child seek and
	// display-title sorting, which requires a temp B-tree on this canonical index.
	for _, mutation := range []func(string) string{
		func(sql string) string { return strings.Replace(sql, " INDEXED BY catalog_entities_album", "", 1) },
		func(sql string) string {
			return strings.Replace(sql, "ORDER BY rs_order_first,rs_order_second,id", "ORDER BY title,id", 1)
		},
	} {
		for _, original := range definitions {
			if original.ID != readStatementID(rsChildrenName("album.first")) {
				continue
			}
			original.SQL = mutation(original.SQL)
			mutant, err := stmt.Describe([]stmt.Definition{original})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = plan.Capture(t.Context(), conn, mutant, browsePlanFixture(cfg, rsChildrenFixtureHash(t), plan.Analyzed), policy); err == nil {
				t.Fatal("real SQL mutant accepted")
			}
		}
	}
}
