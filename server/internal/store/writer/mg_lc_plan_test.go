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

// Capture only the six bounded read definitions here. Full accepting-command
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
		if d.Kind == stmt.Read {
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
	policy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	for _, name := range []string{"mg_lc_protocol.go", "mg_lc_statements.go", "mg_lc_effects.go", "mg_lc_effects_test.go", "mg_lc_plan_test.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(data)
	}
	g, err := plan.Capture(t.Context(), conn, registry, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(h.Sum(nil)), Statistics: plan.Unanalyzed, Implementations: "Retained LC source/head/config/base/lease witnesses and immutable candidate point reads; tiny missing-key fixture; no authority, DML acceptance or performance qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Plans) != 6 || g.ForeignKeys != nil {
		t.Fatal("read capture inventory", len(g.Plans))
	}
	if _, err := stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	actual, err := json.MarshalIndent(g.Plans, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "mg-lc-reads.json")
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
