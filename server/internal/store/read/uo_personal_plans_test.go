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

var recordUOPersonalPlans = flag.Bool("record-uo-personal-plans", false, "record UO personal point native plan golden")

func TestUOPersonalPlans(t *testing.T) {
	f := newForegroundSource(t)
	definitions := UOPersonalDefinitions()
	if len(definitions) != 1 {
		t.Fatal("unexpected UO statement inventory")
	}
	for _, cohort := range definitions[0].Cohorts {
		rows, err := f.conn.QueryContext(t.Context(), definitions[0].SQL, cohort.Bindings...)
		if err != nil {
			t.Fatal(err)
		}
		present := rows.Next()
		err = rows.Err()
		rows.Close()
		if err != nil || present != (cohort.Name == "present") {
			t.Fatal("mislabeled cohort", cohort.Name, err)
		}
	}
	var captures []plan.Golden
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden := captureBrowse(t, f, definitions, stats, foregroundFixtureHash(t))
		if _, err := stmt.New(definitions, golden.Receipt()); err != nil {
			t.Fatal("native Capture failed to authorize exact definitions", err)
		}
		captures = append(captures, golden)
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := "testdata/uo-personal-plans.json.gz"
	if *recordUOPersonalPlans {
		var compressed bytes.Buffer
		z := gzip.NewWriter(&compressed)
		if _, err := z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, compressed.Bytes(), 0644); err != nil {
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
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
}

func TestUOPersonalPlanRejectsUnforcedState(t *testing.T) {
	f := newForegroundSource(t)
	d := UOPersonalDefinitions()[0]
	d.SQL = strings.Replace(d.SQL, " INDEXED BY personal_current_live", "", 1)
	// Use the native Capture path without the test helper's fatal-on-error.
	if err := uoPersonalCaptureError(t, f, d); err == nil || !strings.Contains(err.Error(), "not forced in SQL") {
		t.Fatal("missing forced-index negative control", err)
	}
}

func uoPersonalCaptureError(t *testing.T, f *browseFixture, d stmt.Definition) error {
	t.Helper()
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
	catalogue, err := stmt.Describe([]stmt.Definition{d})
	if err != nil {
		return err
	}
	_, err = plan.Capture(t.Context(), conn, catalogue, browsePlanFixture(cfg, foregroundFixtureHash(t), plan.Unanalyzed), policy)
	return err
}
