package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordMGCapturePlans = flag.Bool("record-mg-capture-plans", false, "deliberately record private MG capture component plans")

// Compile on the owned native connection, preserving full VM/EQP and binding
// manifests. This is tiny fixture/plan evidence, never performance qualification.
func TestMGCaptureStatementPlans(t *testing.T) {
	defs := mgCaptureDefinitions()
	if len(defs) != 7 {
		t.Fatal("statement inventory", len(defs))
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 {
			t.Fatal("unbounded capture plan", d.ID)
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("mg_capture_effects_test.go")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(source)
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	indexPolicy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := mgCaptureFixture(t)
	turn := mgCaptureTestTurn(t, w, defs)
	policy := w.config
	policy.Synchronous = sqliteconn.Normal
	var before, after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	golden, err := plan.Capture(t.Context(), turn.transaction.conn, catalogue, plan.Fixture{
		ConnectionPolicy: policy, SHA256: hex.EncodeToString(digest[:]), Statistics: plan.Unanalyzed,
		Implementations: "MG folded recording/allocation scalar fixture; absent-key compile cohorts; no authority, receipt, physical exit or qualification",
	}, indexPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.New(defs, golden.Receipt()); err != nil {
		t.Fatal(err)
	}
	if len(golden.Manifest) != 7 || len(golden.Plans) != 7 {
		t.Fatal("incomplete capture")
	}
	for _, p := range golden.Plans {
		if len(p.Explain.Values) == 0 {
			t.Fatal("empty VM", p.ID)
		}
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatal("capture changed native rows", before, after, err)
	}
	actual, err := golden.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := "testdata/mg-capture-plans.json"
	if *recordMGCapturePlans {
		if err := os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := golden.Compare(expected); err != nil {
		t.Fatal(err)
	}
	t.Log("7 finite definitions/cohorts; native SEARCH plans and full VM; no effects executed by Capture")
}
