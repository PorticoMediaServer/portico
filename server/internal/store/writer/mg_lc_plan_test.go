package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordMGLCReads = flag.Bool("record-mg-lc-read-plans", false, "record closed canonical LC native plans")

// Complete closed native Capture on adopted canonical 012+013.
// This structural fixture supplies no runtime or publication evidence.
func TestMGLCReadCapture(t *testing.T) {
	synctest.Test(t, mgLCCapture)
}

// The old selected-draft capture remains in historical commit445ef3c44.
// This canonical Capture has no selected-schema fallback.
func mgLCCapture(t *testing.T) {
	w, _ := mgLCFixture(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defs := mgLCDefinitions()
	registry, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	h.Write([]byte("canonical012+013:9d9aa39dfcdb99d4943f61b91ce7307f17026bf5ff7afed36a248f6b55b21f05"))
	for _, name := range []string{"mg_lc_protocol.go", "mg_lc_statements.go", "mg_lc_effects.go", "mg_lc_digest.go", "mg_lc_receipts.go", "mg_lc_receipt_debt.go", "mg_lc_draft_fixture_test.go", "mg_lc_effects_test.go", "mg_lc_receipts_test.go", "mg_lc_plan_test.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(data)
	}
	g, err := plan.Capture(t.Context(), conn, registry, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(h.Sum(nil)), Statistics: plan.Unanalyzed, Implementations: "Canonical012+013 closed PRIVATE LC structural capture; historical selected-MG-001 results are old-head evidence; authority, receipt reclaim/control retirement and publication remain parent-owned"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	wantPlans := 0
	for _, d := range defs {
		wantPlans += len(d.Cohorts)
	}
	if len(g.Manifest) != len(defs) || len(g.Plans) != wantPlans || g.ForeignKeys == nil {
		t.Fatal("read capture inventory", len(g.Plans))
	}
	if _, err := stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	actual, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "mg-lc-native.json.gz")
	if *recordMGLCReads {
		var encoded bytes.Buffer
		z := gzip.NewWriter(&encoded)
		if _, err := z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, encoded.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(expected))
	if err != nil {
		t.Fatal(err)
	}
	expected, err = io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := g.Compare(expected); err != nil {
		t.Fatal(err)
	}
}
