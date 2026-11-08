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
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordScanObjectPlans = flag.Bool("record-scan-object-plans", false, "deliberately record reviewed inventory object statement plans")

// These are native plan/constraint captures of the private compiled definitions,
// not an InventoryAccept handler, admission recipe, Applied result or hold test.
func TestScanObjectStatementPlans(t *testing.T) {
	journal, err := os.ReadFile("scan_object_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start := bytes.Index(journal, []byte("func seedScanObject("))
	end := bytes.Index(journal, []byte("func scanObjectTestTurn("))
	if start < 0 || end <= start {
		t.Fatal("missing immutable scan fixture journal")
	}
	digest := sha256.Sum256(journal[start:end])
	fixtureSHA := hex.EncodeToString(digest[:])
	if fixtureSHA != "557e5568239517d272fc642bd0333603aaf91048c91e0b763198b197440048df" {
		t.Fatal("scan fixture journal changed; review source/populations before recording plans")
	}
	definitions := scanObjectDefinitions()
	if len(definitions) != 1 {
		t.Fatalf("scan definition inventory %d, want1", len(definitions))
	}
	for _, d := range definitions {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 {
			t.Fatalf("scan %s must be hot with no waivers", d.ID)
		}
	}
	if definitions[0].Kind != stmt.Write || len(definitions[0].Params) != 16 || len(definitions[0].Cohorts) != 1 {
		t.Fatal("inventory object compiled shape changed")
	}
	_, returned, ok := strings.Cut(definitions[0].SQL, "\nRETURNING ")
	if !ok || len(strings.Split(returned, ",")) != 17 {
		t.Fatal("inventory object RETURNING inventory changed")
	}
	w, _, _ := testExecutor(t)
	startupConn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	startup, assertErr := sqliteconn.Assert(t.Context(), startupConn, w.config)
	closeErr := startupConn.Close()
	if assertErr != nil || closeErr != nil {
		t.Fatalf("startup assertion=%v close=%v", assertErr, closeErr)
	}
	t.Logf("scan fixture startup physical settings: %+v", startup)
	seedScanObject(t, w)
	// Reuse the existing private synthetic fixture turn to select Inventory's
	// NORMAL durability. Its virtual clock is not a command latency result.
	turn := scanObjectTestTurn(t, w, definitions)
	tx := turn.transaction
	policy := w.config
	policy.Synchronous = sqliteconn.Normal
	settings, err := sqliteconn.Assert(tx.ctx, tx.conn, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("scan capture acquired physical settings: %+v", settings)
	var before int64
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
	golden, err := cachedCapture(tx.ctx, tx.conn, turn.registry, plan.Fixture{
		ConnectionPolicy: policy, SHA256: fixtureSHA, Statistics: plan.Unanalyzed,
		Implementations: "bundled SQLite3.53.4; builtin functions/collations; seedScanObject SQL facts, synthetic revision/JSON; compile only, not source preparation or command authority",
	}, indexPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if len(golden.Plans) != 1 || golden.ForeignKeys == nil || plan.CheckSchemaForeignKeys(golden.ForeignKeys) != nil {
		t.Fatalf("incomplete scan evidence: cohorts=%d FK=%v", len(golden.Plans), golden.ForeignKeys != nil)
	}
	inserts := 0
	for _, p := range golden.Plans {
		if len(p.Explain.Values) == 0 {
			t.Fatalf("empty native VM for %s/%s", p.ID, p.Cohort)
		}
		if p.Insert != nil {
			if p.Insert.Table != "inventory_objects" {
				t.Fatalf("wrong native destination %s", p.Insert.Table)
			}
			inserts++
		}
	}
	if inserts != 1 {
		t.Fatalf("destination audits=%d, want1", inserts)
	}
	var after int64
	if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("plan capture mutated fixture: total_changes %d -> %d", before, after)
	}
	// Keep the full global FK audit in a separate compressed artifact, and
	// compare it on every run. The statement artifact retains every INSERT's
	// own destination columns/indexes and every cohort's canonical VM/EQP.
	fkJSON, err := json.MarshalIndent(golden.ForeignKeys, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	golden.ForeignKeys = nil
	actual, err := golden.JSON()
	if err != nil {
		t.Fatal(err)
	}
	statementPath := filepath.Join("testdata", "scan-inventory-object.json")
	fkPath := filepath.Join("testdata", "scan-inventory-object-fk.json.gz")
	if *recordScanObjectPlans {
		if err := os.WriteFile(statementPath, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
		var compressed bytes.Buffer
		gz := gzip.NewWriter(&compressed)
		if _, err := gz.Write(fkJSON); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
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
	expectedFK, err := os.ReadFile(fkPath)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(expectedFK))
	if err != nil {
		t.Fatal(err)
	}
	expectedFK, err = io.ReadAll(gz)
	closeErr = gz.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("FK golden read=%v close=%v", err, closeErr)
	}
	if err := plan.CompareExactJSON(expectedFK, fkJSON); err != nil {
		t.Fatal(err)
	}
	t.Logf("1compiled definition/1cohort/1destination audit/16bindings+17RETURNING fields/shared-schema FK audit; fixture journal%s; no accepting SQL executed", fixtureSHA)
}
