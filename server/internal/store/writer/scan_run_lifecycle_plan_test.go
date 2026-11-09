package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordScanRunLifecyclePlans = flag.Bool("record-scan-run-lifecycle-plans", false, "deliberately record reviewed scan run lifecycle statement plans")

// Compiles SC's frozen SQL-fact populations. This is not a production lifecycle
// purpose/profile, source/birth permit, authority or command effect proof.
func TestScanRunLifecycleStatementPlans(t *testing.T) {
	data, err := os.ReadFile("scan_run_lifecycle_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start, end := bytes.Index(data, []byte("func seedScanLifecycle(")), bytes.Index(data, []byte("func scanLifecycleFixture("))
	if start < 0 || end <= start {
		t.Fatal("missing frozen lifecycle seed")
	}
	hash := sha256.Sum256(data[start:end])
	fixtureSHA := hex.EncodeToString(hash[:])
	if fixtureSHA != "f96f8813659786b14397a566f6475d834aec9173ad4e3ba2b4f877e07d5a4af0" || schema.SHA256 != "49b3df34c79bd2d524860cea42909dd0d818d785b999bbe4221045e2b2ad4fe0" {
		t.Fatal("frozen seed/schema changed; review before recording")
	}
	defs := scanRunLifecycleDefinitions()
	inserts := map[stmt.ID]string{
		scanLifecycleInsertJob: "jobs", scanLifecycleInsertRun: "inventory_runs",
		scanLifecycleInsertRoot: "inventory_directories", scanLifecycleInsertActive: "inventory_source_active",
		scanLifecycleReceiptInsert: "scan_command_receipts",
	}
	cohorts, reads, writes := 0, 0, 0
	for _, d := range defs {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(plan.LintSQL(d.SQL)) != 0 {
			t.Fatal("unqualified lifecycle definition", d.ID)
		}
		cohorts += len(d.Cohorts)
		switch d.Kind {
		case stmt.Read:
			reads++
		case stmt.Write:
			writes++
		}
		if inserts[d.ID] != "" && (len(d.Plan.Seeks) != 0 || len(d.Plan.Indexes) != 0 || len(d.Plan.PrimaryKeys) != 0) {
			t.Fatal("INSERT must not invent a destination seek", d.ID)
		}
	}
	if len(defs) != 12 || cohorts != 19 || reads != 4 || writes != 8 {
		t.Fatal("lifecycle closed inventory changed", len(defs), cohorts, reads, writes)
	}
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	w, _, _ := testExecutor(t)
	startupConn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	startup, assertErr := sqliteconn.Assert(t.Context(), startupConn, w.config)
	closeErr := startupConn.Close()
	if assertErr != nil || closeErr != nil {
		t.Fatal("startup boundary", assertErr, closeErr)
	}
	t.Logf("lifecycle fixture FULL startup settings: %+v", startup)
	seedScanLifecycle(t, w)
	// Existing W frame chooses NORMAL for this synthetic Inventory fixture.
	// No domain helper or accepting command is executed by the plan harness.
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	policy := w.config
	policy.Synchronous = sqliteconn.Normal
	settings, err := sqliteconn.Assert(tx.ctx, tx.conn, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("lifecycle acquired NORMAL capture settings: %+v", settings)
	var before, after int64
	if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	indexPolicy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := cachedCapture(tx.ctx, tx.conn, catalogue, plan.Fixture{ConnectionPolicy: policy, SHA256: fixtureSHA, Statistics: plan.Unanalyzed,
		Implementations: "bundled SQLite3.53.4; seedScanLifecycle native SQL facts before lifecycle effects; compile only, not source/birth authority or production resource/command qualification"}, indexPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.New(defs, golden.Receipt()); err != nil {
		t.Fatal("exact full-set Capture receipt refused", err)
	}
	if len(golden.Manifest) != 12 || len(golden.Plans) != 19 || golden.ForeignKeys == nil || plan.CheckSchemaForeignKeys(golden.ForeignKeys) != nil {
		t.Fatal("incomplete lifecycle Capture")
	}
	audits, opcodes, searches := 0, 0, 0
	for _, p := range golden.Plans {
		if len(p.Explain.Values) == 0 {
			t.Fatal("missing VM", p.ID, p.Cohort)
		}
		opcodes += len(p.Explain.Values)
		if table := inserts[p.ID]; table != "" {
			if p.Insert == nil || p.Insert.Table != table || p.Insert.Conflict != nil {
				t.Fatal("wrong native INSERT audit", p.ID, p.Cohort)
			}
			audits++
		} else if p.Insert != nil || len(p.QueryPlan.Values) == 0 {
			t.Fatal("missing native point/CAS lookup", p.ID, p.Cohort)
		}
		for _, row := range p.QueryPlan.Values {
			if len(row) != 4 || !strings.HasPrefix(row[3].(string), "SEARCH ") {
				t.Fatal("non-SEARCH lifecycle plan", p.ID, p.Cohort, row)
			}
			searches++
		}
	}
	if audits != 6 {
		t.Fatal("destination audit inventory", audits)
	}
	if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&after); err != nil || before != after {
		t.Fatal("EXPLAIN mutated seed", before, after, err)
	}
	fkJSON, err := json.MarshalIndent(golden.ForeignKeys, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareExactJSON(readScanFreshFK(t, filepath.Join("testdata", "scan-inventory-accept-fk.json.gz")), fkJSON); err != nil {
		t.Fatal("STOP: lifecycle changed existing full671-FK image", err)
	}
	golden.ForeignKeys = nil
	actual, err := golden.JSON()
	if err != nil {
		t.Fatal(err)
	}
	statementPath := filepath.Join("testdata", "scan-run-lifecycle.json")
	fkPath := filepath.Join("testdata", "scan-run-lifecycle-fk.json.gz")
	if *recordScanRunLifecyclePlans {
		if err := os.WriteFile(statementPath, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
		var compressed bytes.Buffer
		z := gzip.NewWriter(&compressed)
		if _, err := z.Write(fkJSON); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fkPath, compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(statementPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := golden.Compare(expected); err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareExactJSON(readScanFreshFK(t, fkPath), fkJSON); err != nil {
		t.Fatal(err)
	}
	t.Logf("12definitions/19cohorts/6INSERTaudits/%dVM/%dSEARCH/shared-schema FK audit; same frozen pre-effect seed, no production profile/authority/enable", opcodes, searches)
}
