package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestScanDirectoryStatementPlans(t *testing.T) {
	e, _, _ := testExecutor(t)
	seedScanDirectory(t, e)
	defs := scanDirectoryOwnDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("scan_directory_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start, end := bytes.Index(source, []byte("func seedScanDirectory(")), bytes.Index(source, []byte("func directoryFixture("))
	if start < 0 || end <= start {
		t.Fatal("missing seed journal")
	}
	hash := sha256.Sum256(source[start:end])
	fixtureSHA := hex.EncodeToString(hash[:])
	if fixtureSHA != "59d1dddb0a2b6b468b47ecaf77b359ea457626a3f4bf0478bb6867bf2ac5f28e" {
		t.Fatal("frozen directory seed changed; review before recording", fixtureSHA)
	}
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || (len(plan.LintSQL(d.SQL)) != 0 && d.ID != scanDirectoryRunLengths && d.ID != scanDirectoryJobLengths && d.ID != scanDirectoryRootLengths && d.ID != scanDirectoryReceiptLengths) {
			t.Fatal("directory statement grammar", d.ID, plan.LintSQL(d.SQL))
		}
	}
	conn, err := e.db.Conn(t.Context())
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
	golden, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: e.config, SHA256: fixtureSHA, Statistics: plan.Unanalyzed, Implementations: "directory discovery/EOF native SQL-fact fixture; complete indexed scalar receipt programs and INSERT destination audit; no issuer or production resource qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.New(defs, golden.Receipt()); err != nil {
		t.Fatal(err)
	}
	if golden.ForeignKeys == nil || len(golden.ForeignKeys.Plans) != 671 {
		t.Fatal("canonical671-FK audit missing")
	}
	golden.ForeignKeys = nil // same canonical graph; no schema change in this PR
	for _, p := range golden.Plans {
		for _, row := range p.QueryPlan.Values {
			if !strings.HasPrefix(row[3].(string), "SEARCH ") {
				t.Fatal("nonindexed directory point", p.ID, row)
			}
		}
		if p.ID == scanDirectoryInsert || p.ID == scanDirectoryReceiptInsert {
			if p.Insert == nil || p.Insert.Conflict != nil {
				t.Fatal("strict INSERT destination missing", p.ID)
			}
		}
	}
	actual, err := golden.JSON()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join("testdata", "scan-directory.json")
	expected, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		if evidence := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); evidence != "" {
			if err := os.WriteFile(filepath.Join(evidence, "scan-directory.json"), append(actual, '\n'), 0644); err != nil {
				t.Fatal(err)
			}
		}
		t.Fatal("new directory capture candidate requires explicit review; written to runner evidence")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := golden.Compare(expected); err != nil {
		t.Fatal(err)
	}
}
