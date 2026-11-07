package read

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// Actual native Reader Capture, both statistics populations. This is a NEW
// source-bound artifact, never a refresh of frozen Home/manifest goldens.
func TestRBHomeFixedPlans(t *testing.T) {
	f := homeFixedSeed(t, 1)
	homeFixedAssetSeed(t, f, 1)
	homeFixedStateSeed(t, f, 1, 5000, "in_progress")
	defs := append(rbHomeFixedDefinitions(), rbHomeFixedBindingByteDefinitions()...)
	defs = append(defs, rbHomeBindingDefinitions()[1:]...)
	captures := []plan.Golden{}
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden := captureBrowse(t, f, defs, stats, homeFixedHash(t))
		if _, e := stmt.New(defs, golden.Receipt()); e != nil {
			t.Fatal("actual Reader receipt refused exact definitions", e)
		}
		cohorts := 0
		for _, d := range defs {
			cohorts += len(d.Cohorts)
		}
		if len(golden.Manifest) != len(defs) || len(golden.Plans) != cohorts {
			t.Fatal("incomplete new-family cohort coverage")
		}
		for _, p := range golden.Plans {
			for _, v := range p.QueryPlan.Values {
				if len(v) != 4 || !bytes.HasPrefix([]byte(v[3].(string)), []byte("SEARCH ")) {
					t.Fatal("new fixed recipe is not SEARCH-only", p.ID, v)
				}
			}
		}
		captures = append(captures, golden)
	}
	data, e := json.MarshalIndent(captures, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
		var compressed bytes.Buffer
		z := gzip.NewWriter(&compressed)
		if _, e = z.Write(data); e != nil {
			t.Fatal(e)
		}
		if e = z.Close(); e != nil {
			t.Fatal(e)
		}
		if e = os.MkdirAll(dir, 0755); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, "rb-home-fixed-plans.json.gz"), compressed.Bytes(), 0644); e != nil {
			t.Fatal(e)
		}
	}
}
