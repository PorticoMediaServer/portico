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

var recordRSSavedPages = flag.Bool("record-rs-saved-pages", false, "deliberately record saved collection reader native plans")

func TestRSSavedPagesPlans(t *testing.T) {
	f := rsSavedPagesSource(t)
	definitions := SavedCollectionHarnessDefinitions()
	var captures []plan.Golden
	for _, profile := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if profile == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden := captureBrowse(t, f, definitions, profile, rsSavedPageHash(t))
		if _, e := stmt.New(definitions, golden.Receipt()); e != nil {
			t.Fatal(e)
		}
		captures = append(captures, golden)
	}
	actual, e := json.Marshal(captures)
	if e != nil {
		t.Fatal(e)
	}
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	if _, e = z.Write(actual); e != nil {
		t.Fatal(e)
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	path := "testdata/rs-saved-page-plans.json.gz"
	if *recordRSSavedPages {
		if e = os.WriteFile(path, compressed.Bytes(), 0644); e != nil {
			t.Fatal(e)
		}
	}
	if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
		if e = os.MkdirAll(dir, 0755); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, "rs-saved-page-plans.json.gz"), compressed.Bytes(), 0644); e != nil {
			t.Fatal(e)
		}
	}
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	reader, e := gzip.NewReader(bytes.NewReader(data))
	if e != nil {
		t.Fatal(e)
	}
	expected, e := io.ReadAll(reader)
	if e != nil {
		t.Fatal(e)
	}
	if e = reader.Close(); e != nil {
		t.Fatal(e)
	}
	if e = plan.CompareJSON(expected, actual); e != nil {
		t.Fatal(e)
	}
}
