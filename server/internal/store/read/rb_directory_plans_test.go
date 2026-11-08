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

type rbDirectoryCapture struct {
	Profile string
	Golden  plan.Golden
}

// captureRBPlans records the analyzed/unanalyzed Capture evidence for one owned
// definition inventory. The evidence artifact is written only when the desk's
// runner supplies PORTICO_LANE_EVIDENCE_DIR; the builder records no golden.
func captureRBPlans(t *testing.T, definitions []stmt.Definition, path string) {
	t.Helper()
	f := newRBDirectorySource(t)
	fixtureHash := rbDirectoryFixtureHash(t)
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
	var captures []rbDirectoryCapture
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden, err := plan.Capture(t.Context(), conn, catalogue, browsePlanFixture(cfg, fixtureHash, stats), policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stmt.New(definitions, golden.Receipt()); err != nil {
			t.Fatal("actual Reader Capture cannot authorize definitions", err)
		}
		captures = append(captures, rbDirectoryCapture{Profile: string(stats), Golden: golden})
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
		if err = os.WriteFile(filepath.Join(dir, filepath.Base(path)), compressed.Bytes(), 0644); err != nil {
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
}

func TestRBDirectoryPlans(t *testing.T) {
	definitions := rbDirectoryDefinitions()
	if len(definitions) != 1 {
		t.Fatal("review changed directory statement inventory", len(definitions))
	}
	captureRBPlans(t, definitions, "testdata/rb-directory-plans.json.gz")
}

func TestRBAcrossPlans(t *testing.T) {
	definitions := rbAcrossDefinitions()
	// 12 title + 20 accepted-date/content + 10 scalar x 2 directions x 5
	// legs = 132 exact same-sort legs.
	if len(definitions) != 132 {
		t.Fatal("review changed cross statement inventory", len(definitions))
	}
	captureRBPlans(t, definitions, "testdata/rb-across-plans.json.gz")
}
