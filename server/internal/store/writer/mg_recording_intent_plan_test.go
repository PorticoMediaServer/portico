package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordMGRecordingIntentPlans = flag.Bool("record-mg-recording-intent-plans", false, "deliberately record private recording intent draft plans")

// Native compile/Capture smoke against the exact draft declarations. This does
// not execute an endpoint, issue authority or qualify a production profile.
func TestMGRecordingIntentStatementPlans(t *testing.T) {
	defs := mgRecordingIntentDefinitions()
	if len(defs) != 15 {
		t.Fatal("finite inventory", len(defs))
	}
	for _, d := range defs {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	for _, name := range []string{"mg_recording_intent_test.go", "mg_recording_intent_draft_test.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		hash.Write([]byte(name))
		hash.Write([]byte{0})
		hash.Write(data)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	var policyContract struct {
		Indexes []map[string]any `json:"indexes"`
	}
	if err := json.Unmarshal(contract, &policyContract); err != nil {
		t.Fatal(err)
	}
	// Exact owner-draft predicate only, local to this disposable native fixture.
	policyContract.Indexes = append(policyContract.Indexes, map[string]any{"index": "dvr_recordings_capture_launch", "predicate": "capture_launch_id IS NOT NULL", "choice": "repeat_exact_predicate"})
	draftPolicy, err := json.Marshal(policyContract)
	if err != nil {
		t.Fatal(err)
	}
	indexPolicy, err := plan.ReadIndexPolicy(draftPolicy)
	if err != nil {
		t.Fatal(err)
	}
	w, _, _, _, _ := mgRecordingFixture(t)
	turn := mgRecordingTestTurn(t, w)
	policy := w.config
	policy.Synchronous = sqliteconn.Normal
	var before, after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	golden, err := plan.Capture(t.Context(), turn.transaction.conn, catalogue, plan.Fixture{ConnectionPolicy: policy, SHA256: hex.EncodeToString(hash.Sum(nil)), Statistics: plan.Unanalyzed, Implementations: "MG recording intent; exact capture/floor declarations selected from candidate 4e075198; frozen full programme fixture; absent-key compile cohorts, no authority or qualification"}, indexPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.New(defs, golden.Receipt()); err != nil {
		t.Fatal(err)
	}
	if len(golden.Manifest) != 15 || len(golden.Plans) != 15 || golden.ForeignKeys == nil {
		t.Fatal("incomplete native capture")
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || before != after {
		t.Fatal("Capture executed accepting effects", before, after, err)
	}
	actual, err := golden.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := "testdata/mg-recording-intent-plans.json.gz"
	if *recordMGRecordingIntentPlans {
		var compressed bytes.Buffer
		gz := gzip.NewWriter(&compressed)
		if _, err := gz.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(expected))
	if err != nil {
		t.Fatal(err)
	}
	expected, err = io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := golden.Compare(expected); err != nil {
		t.Fatal(err)
	}
	t.Log("15 finite definitions/cohorts; indexed point plans, INSERT/FK audit and full VM; Capture made zero changes")
}
