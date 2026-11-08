package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"io"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordMGDVRPolicyPlans = flag.Bool("record-mg-dvr-policy-plans", false, "record canonical DVR policy scalar point plans")

func TestMGDVRPolicyNativeStatementPlans(t *testing.T) {
	defs := mgDVRPolicyDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	w, _, _ := mgDVRPolicyFixture(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	for _, name := range []string{"mg_dvr_policy_types.go", "mg_dvr_policy_statements.go", "mg_dvr_policy_effects.go", "mg_dvr_policy_fixture_test.go", "mg_dvr_policy_effects_test.go", "mg_dvr_policy_plan_test.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(data)
	}
	var before, after int64
	if err := conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	g, err := cachedCapture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(h.Sum(nil)), Statistics: plan.Unanalyzed, Implementations: "Canonical DVR scalar policies only; four closed point/readback/CAS definitions with exact/missing/stale cohorts; no authority, scheduler/cleanup activation, admission, receipt or performance qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Manifest) != 4 || len(g.Plans) != 8 || g.ForeignKeys != nil {
		t.Fatal("capture inventory", len(g.Manifest), len(g.Plans), g.ForeignKeys)
	}
	if _, err := stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != before {
		t.Fatal("Capture executed effects", before, after, err)
	}
	actual, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := "testdata/mg-dvr-policy-plans.json.gz"
	if *recordMGDVRPolicyPlans {
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		if _, err := gz.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b.Bytes(), 0644); err != nil {
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
	if err := g.Compare(expected); err != nil {
		t.Fatal(err)
	}
}
