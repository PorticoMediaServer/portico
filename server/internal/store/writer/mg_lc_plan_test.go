package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordMGLCReads = flag.Bool("record-mg-lc-read-plans", false, "record synthetic LC witness/candidate read component plans")

// Capture only the five scalar/control read definitions here. Full accepting-command
// recipes, DML/receipt/authority integration and runner qualification remain
// parent-owned. This is a tiny native fixture, never performance evidence.
func TestMGLCReadCapture(t *testing.T) {
	w, _ := mgLCFixture(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var defs []stmt.Definition
	for _, d := range mgLCDefinitions() {
		if d.ID == mgLCHead || d.ID == mgLCLibrary || d.ID == mgLCHistory || d.ID == mgLCRead || d.ID == mgLCTail {
			defs = append(defs, d)
		}
	}
	registry, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	// Private fixture policy for this hash-pinned draft's ONE new partial
	// index. Capture verifies the exact native predicate. Canonical INT policy
	// stays untouched; this is not adoption or an execution waiver.
	var contract map[string]json.RawMessage
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(contract["indexes"], &entries); err != nil {
		t.Fatal(err)
	}
	extra, err := json.Marshal(map[string]string{"index": "dvr_recordings_capture_launch", "predicate": "capture_launch_id IS NOT NULL", "choice": "repeat_exact_predicate"})
	if err != nil {
		t.Fatal(err)
	}
	entries = append(entries, extra)
	contract["indexes"], err = json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	h.Write([]byte(mgLCDraftSHA))
	for _, name := range []string{"mg_lc_protocol.go", "mg_lc_statements.go", "mg_lc_effects.go", "mg_lc_digest.go", "mg_lc_receipts.go", "mg_lc_draft_fixture_test.go", "mg_lc_effects_test.go", "mg_lc_receipts_test.go", "mg_lc_plan_test.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(data)
	}
	g, err := plan.Capture(t.Context(), conn, registry, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(h.Sum(nil)), Statistics: plan.Unanalyzed, Implementations: "Exact MG-001 owner draft 4e0751989357d1aed85467ba81f5c24ce79aa3a26f947e0fd222070891bb9262; scalar/control reads only; actual-octet preflight registry, receipt/DML accepting recipe and qualification remain parent-owned"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Plans) != 5 || g.ForeignKeys != nil {
		t.Fatal("read capture inventory", len(g.Plans))
	}
	if _, err := stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	actual, err := json.MarshalIndent(g.Plans, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "mg-lc-draft-reads.json")
	if *recordMGLCReads {
		if err := os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
}
