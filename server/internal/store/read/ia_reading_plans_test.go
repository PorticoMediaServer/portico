package read

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func iaReadingFixtureHash(t *testing.T) string {
	t.Helper()
	data, e := os.ReadFile("ia_reading_test.go")
	if e != nil {
		t.Fatal(e)
	}
	start := bytes.Index(data, []byte("func newIAReadingSource("))
	end := bytes.Index(data, []byte("func newIAReadingFixture("))
	if start < 0 || end <= start {
		t.Fatal("fixture hash boundaries")
	}
	h := sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[start:end]...))
	return hex.EncodeToString(h[:])
}
func TestIAReadingPlans(t *testing.T) {
	f := newIAReadingSource(t)
	defs := IAReadingDefinitions()
	if len(defs) != 14 {
		t.Fatal("review statement inventory")
	}
	for _, d := range defs {
		for _, c := range d.Cohorts {
			rows, e := f.conn.QueryContext(t.Context(), d.SQL, c.Bindings...)
			if e != nil {
				t.Fatal(d.ID, e)
			}
			present := rows.Next()
			e = rows.Err()
			rows.Close()
			if e != nil || present != (c.Name == "present") {
				t.Fatalf("mislabeled %s/%s: %v", d.ID, c.Name, e)
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
		golden := captureBrowse(t, f, defs, stats, iaReadingFixtureHash(t))
		if _, e := stmt.New(defs, golden.Receipt()); e != nil {
			t.Fatal(e)
		}
		if len(golden.Manifest) != 14 || len(golden.Plans) != 28 {
			t.Fatal("incomplete native Capture")
		}
		captures = append(captures, capture{string(stats), golden})
	}
	actual, e := json.MarshalIndent(captures, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	var packed bytes.Buffer
	z := gzip.NewWriter(&packed)
	if _, e = z.Write(actual); e != nil {
		t.Fatal(e)
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	// lane-focused ordinary tests preserve this new domain's capture without a
	// shared tooling edit. A missing baseline fails until the owner adopts it.
	if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
		if e = os.MkdirAll(dir, 0755); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, "ia-reading-plans.json.gz"), packed.Bytes(), 0644); e != nil {
			t.Fatal(e)
		}
	}
	expected, e := os.ReadFile("testdata/ia-reading-plans.json.gz")
	if e != nil {
		t.Fatal("capture requires owner adoption", e)
	}
	reader, e := gzip.NewReader(bytes.NewReader(expected))
	if e != nil {
		t.Fatal(e)
	}
	data, e := io.ReadAll(reader)
	reader.Close()
	if e != nil {
		t.Fatal(e)
	}
	if e = plan.CompareJSON(data, actual); e != nil {
		t.Fatal(e)
	}
}
