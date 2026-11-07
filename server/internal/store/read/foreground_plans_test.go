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

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordForegroundPlans = flag.Bool("record-foreground-plans", false, "deliberately record reviewed native browse plans")

func TestForegroundPlans(t *testing.T) {
	f := newForegroundSource(t)
	for _, d := range foregroundDefinitions() {
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

	fixtureHash := foregroundFixtureHash(t)
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	definitions := foregroundDefinitions()
	catalogue, err := stmt.Describe(definitions)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogue.Manifest()) != 17 {
		t.Fatal("review changed statement inventory")
	}
	path := "testdata/foreground-plans.json.gz"
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
	if *recordForegroundPlans {
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
		if len(capture.Golden.Manifest) != 17 || len(capture.Golden.Plans) != 34 {
			t.Fatal("incomplete statement/cohort Capture coverage")
		}
	}
}

func foregroundFixtureHash(t *testing.T) string { return readfixture.ForegroundHash(t) }

func TestForegroundCaptureRejectsUnforcedSeek(t *testing.T) {
	f := newForegroundSource(t)
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
	var original stmt.Definition
	for _, d := range foregroundDefinitions() {
		if d.ID == readStatementID("foreground.continue.tail") {
			original = d
		}
	}
	capture := func(d stmt.Definition) error {
		inert, e := stmt.Describe([]stmt.Definition{d})
		if e != nil {
			return e
		}
		_, e = plan.Capture(t.Context(), conn, inert, browsePlanFixture(cfg, foregroundFixtureHash(t), plan.Unanalyzed), policy)
		return e
	}
	if err := capture(original); err != nil {
		t.Fatal("positive seek", err)
	}
	original.SQL = strings.Replace(original.SQL, " INDEXED BY personal_current_activity", "", 1)
	if err := capture(original); err == nil || !strings.Contains(err.Error(), "not forced in SQL") {
		t.Fatal("unforced seek did not fail the intended check", err)
	}
}
