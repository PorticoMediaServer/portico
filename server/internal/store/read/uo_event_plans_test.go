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

// The UO event page/feed recipes are captured live. Stored JSON goldens are a
// comparison baseline, never registration authority: the test proves the exact
// definitions pass HN Capture (keyed SEARCH only, exact octet preflights and
// EQP) on the browse fixture and then compares the capture with the golden.
var recordUOEventPlans = flag.Bool("record-uo-event-plans", false, "record UO event native plan golden")

func TestUOEventPlans(t *testing.T) {
	f := newBrowseSource(t)
	definitions := UOEventDefinitions()
	if len(definitions) != 8 {
		t.Fatal("unexpected UO event statement inventory", len(definitions))
	}
	for _, definition := range definitions {
		if len(definition.Cohorts) == 0 {
			t.Fatalf("statement %s has no capture cohort", definition.ID)
		}
		for _, cohort := range definition.Cohorts {
			rows, err := f.conn.QueryContext(t.Context(), definition.SQL, cohort.Bindings...)
			if err != nil {
				t.Fatal(definition.ID, cohort.Name, err)
			}
			rows.Close()
		}
	}
	var captures []plan.Golden
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden := captureBrowse(t, f, definitions, stats, browsePlanFixtureHash(t))
		if _, err := stmt.New(definitions, golden.Receipt()); err != nil {
			t.Fatal("native Capture failed to authorize exact event definitions", err)
		}
		captures = append(captures, golden)
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := "testdata/uo-event-plans.json.gz"
	if *recordUOEventPlans {
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

func TestUOEventPlanRejectsUnusableKeysets(t *testing.T) {
	f := newBrowseSource(t)
	original := uoActivityDefinition(t, UOEventDefinitions(), "read.uo.events.next")
	if err := uoEventCaptureError(t, f, original); err != nil {
		t.Fatal("positive audience keyset", err)
	}
	for name, mutate := range map[string]func(*stmt.Definition){
		"unforced-audience-index": func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, " INDEXED BY api_events_audience", "", 1)
		},
		"temporary-sort": func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "ORDER BY id LIMIT", "ORDER BY revision LIMIT", 1)
		},
	} {
		changed := original
		mutate(&changed)
		if err := uoEventCaptureError(t, f, changed); err == nil {
			t.Fatal("unusable event keyset was authorized:", name)
		}
	}
}

func uoEventCaptureError(t *testing.T, f *browseFixture, d stmt.Definition) error {
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
	_, err = plan.Capture(t.Context(), conn, catalogue, browsePlanFixture(cfg, browsePlanFixtureHash(t), plan.Unanalyzed), policy)
	return err
}
