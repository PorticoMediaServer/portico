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
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordUORootPlans = flag.Bool("record-uo-root-plans", false, "record UO shared lifetime-root component native plans")

// UO-01's own plan capture only: the shared root statements, no shared baseline
// and no other task's golden is re-recorded here. Followers add their own file.
func TestUORootPlans(t *testing.T) {
	w, _ := uoRootFixture(t)
	defs := uoRootDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	// The exact fixture constructor is the source journal; unrelated test edits
	// do not change it.
	data, err := os.ReadFile("uo_root_schema_test.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	start, end := strings.Index(source, "func uoRootFixture("), strings.Index(source, "func TestUORootDraft")
	if start < 0 || end <= start {
		t.Fatal("fixture constructor journal missing")
	}
	hash := sha256.Sum256([]byte(source[start:end] + "\x00INSERT INTO user_mutation_global(singleton) VALUES(1) ON CONFLICT(singleton) DO NOTHING" +
		"\x00INSERT INTO user_mutation_roots(id,installation_incarnation,account_id,profile_id,account_incarnation,profile_incarnation,family_id,registration_revision,state) VALUES(?,?,?,?,?,?,?,?,?)" +
		"\x00INSERT INTO user_mutation_command_owners(root_id,command_incarnation) VALUES(?,?)"))
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var captures []plan.Golden
	// Follow the merged WC full-writer Capture under its declared unanalyzed
	// statistics. Analyzed full-schema FK plans belong on QS's representative
	// corpus; tiny unrelated child tables legitimately choose scans after ANALYZE.
	for _, stats := range []plan.Statistics{plan.Unanalyzed} {
		golden, err := cachedCapture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(hash[:]), Statistics: stats, Implementations: "UO shared lifetime-root component native fixture; no command registration, authority or resource qualification"}, policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stmt.New(defs, golden.Receipt()); err != nil {
			t.Fatal(err)
		}
		captures = append(captures, golden)
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := "testdata/uo-root-plans.json.gz"
	if *recordUORootPlans {
		var compressed bytes.Buffer
		z := gzip.NewWriter(&compressed)
		if _, err := z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
}
