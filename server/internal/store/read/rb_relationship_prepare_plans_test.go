package read

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestRBRelationshipPreparePlans(t *testing.T) {
	f := newRBRelationshipSource(t)
	fixtureHash := rbRelationshipFixtureHash(t)
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	definitions := rbRelationshipPrepareDefinitions()
	catalogue, err := stmt.Describe(definitions)
	if err != nil {
		t.Fatal(err)
	}
	// 2 article-policy variants x 3 seek parts x (payload + same-snapshot byte
	// preflight) + artist albums (first/tail) + album songs (first/tail).
	if len(catalogue.Manifest()) != 18 {
		t.Fatal("review changed statement inventory")
	}
	path := "testdata/rb-relationship-prepare-plans.json.gz"
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
			t.Fatal("actual Reader Capture cannot authorize relationship definitions", err)
		}
		captures = append(captures, capture{Profile: string(stats), Golden: golden})
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
		var compressed bytes.Buffer
		z := gzip.NewWriter(&compressed)
		if _, err = z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "rb-relationship-prepare-plans.json.gz"), compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("adopt reviewed runner Capture artifact:", err)
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
		if len(capture.Golden.Manifest) != 18 || len(capture.Golden.Plans) != 36 {
			t.Fatal("incomplete statement/cohort Capture coverage")
		}
	}
}
