package read

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordRSRelatedPlans = flag.Bool("record-rs-related-plans", false, "record actual related/similar Reader Capture")

func TestRSRelatedPlans(t *testing.T) {
	f := newBrowseSource(t)
	seedRSRelated(t, f)
	defs := RSRelatedDefinitions()
	for _, d := range defs {
		for _, c := range d.Cohorts {
			rows, err := f.conn.QueryContext(t.Context(), d.SQL, c.Bindings...)
			if err != nil {
				t.Fatal(d.ID, c.Name, err)
			}
			present := rows.Next()
			err = rows.Close()
			if err != nil || present != (c.Name == "present") {
				t.Fatal("mislabeled cohort", d.ID, c.Name, present, err)
			}
		}
	}
	type capture struct {
		Profile string
		Golden  plan.Golden
	}
	var captures []capture
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		g := captureBrowse(t, f, defs, stats, rsRelatedHash(t))
		if _, err := stmt.New(defs, g.Receipt()); err != nil {
			t.Fatal(err)
		}
		captures = append(captures, capture{string(stats), g})
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	if _, err = z.Write(actual); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	path := "testdata/rs-related-plans.json.gz"
	if *recordRSRelatedPlans {
		if err = os.WriteFile(path, compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	// Runner capture can be collected using ordinary lane-focused tests, without
	// widening its capture-target allowlist or writing into another lane's file.
	if evidence := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); evidence != "" {
		if err = os.WriteFile(filepath.Join(evidence, "rs-related-plans.json.gz"), compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	want, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = plan.CompareJSON(want, actual); err != nil {
		t.Fatal(err)
	}
}
