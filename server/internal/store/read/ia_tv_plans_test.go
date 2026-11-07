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
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func iaTVFixtureHash(t *testing.T) string {
	t.Helper()
	data, e := os.ReadFile("ia_tv_test.go")
	if e != nil {
		t.Fatal(e)
	}
	start := bytes.Index(data, []byte("func newIATVSource("))
	end := bytes.Index(data, []byte("func newIATVFixture("))
	if start < 0 || end <= start {
		t.Fatal("fixture hash boundaries")
	}
	h := sha256.Sum256(append([]byte(browsePlanFixtureHash(t)), data[start:end]...))
	return hex.EncodeToString(h[:])
}
func TestIATVPlans(t *testing.T) {
	f := newIATVSource(t)
	defs := IATVDefinitions()
	if len(defs) != 8 {
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
		golden := captureBrowse(t, f, defs, stats, iaTVFixtureHash(t))
		if _, e := stmt.New(defs, golden.Receipt()); e != nil {
			t.Fatal(e)
		}
		if len(golden.Manifest) != 8 || len(golden.Plans) != 16 {
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
		if e = os.WriteFile(filepath.Join(dir, "ia-tv-plans.json.gz"), packed.Bytes(), 0644); e != nil {
			t.Fatal(e)
		}
	}
	expected, e := os.ReadFile("testdata/ia-tv-plans.json.gz")
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
func TestIATVCaptureRejectsWeakenedControls(t *testing.T) {
	f := newIATVSource(t)
	for _, tc := range []struct {
		name string
		edit func(*stmt.Definition)
	}{
		{"unforced-child-seek", func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, " INDEXED BY catalog_entities_parent", "", 1) }},
		{"forged-octet-point", func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "library_id=?", "library_id>=?", 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var d stmt.Definition
			for _, v := range IATVDefinitions() {
				if tc.name == "unforced-child-seek" && v.ID == readStatementID("foreground.ia.tv.children") || tc.name == "forged-octet-point" && v.ID == readStatementID("foreground.ia.tv.entity_octets") {
					d = v
				}
			}
			tc.edit(&d)
			catalogue, e := stmt.Describe([]stmt.Definition{d})
			if e != nil {
				t.Fatal(e)
			}
			// Use the native connection and exact policy/fixture used by positive
			// Capture; the altered definition must never mint an executable receipt.
			_, e = plan.Capture(t.Context(), f.conn, catalogue, browsePlanFixture(browseReaderConfig(), iaTVFixtureHash(t), plan.Unanalyzed), iaTVPolicy(t))
			if e == nil {
				t.Fatal("weakened production control minted receipt")
			}
		})
	}
}

func iaTVPolicy(t *testing.T) plan.IndexPolicy {
	t.Helper()
	data, e := schema.ReadContract("INT-INDEX-POLICY.json")
	if e != nil {
		t.Fatal(e)
	}
	p, e := plan.ReadIndexPolicy(data)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
