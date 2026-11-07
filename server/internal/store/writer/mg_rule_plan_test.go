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

var recordMGRulePlans = flag.Bool("record-mg-rule-plans", false, "deliberately record private sparse personal command plans")

func TestMGRuleNativeStatementPlans(t *testing.T) {
	defs := mgRuleDefinitions()
	if len(defs) != 8 {
		t.Fatal("closed guide inventory", len(defs))
	}
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := mgRuleFixture(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	fixtureSource, err := os.ReadFile("mg_rule_effects_test.go")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(fixtureSource)
	var before, after int64
	if err := conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	g, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(digest[:]), Statistics: plan.Unanalyzed, Implementations: "Canonical schema scalar rule/work point statements; sparse fixture VM and FK audits; no qualification or public command integration"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Manifest) != 8 || len(g.Plans) != 8 || g.ForeignKeys == nil {
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
	path := "testdata/mg-rule-plans.json.gz"
	if *recordMGRulePlans {
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
