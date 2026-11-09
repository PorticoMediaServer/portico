package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
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

var recordScanFreshPlans = flag.Bool("record-scan-fresh-plans", false, "deliberately record frozen fresh Inventory statement plans")

// These are native plan/constraint captures of the private compiled definitions,
// not an InventoryAccept handler, admission recipe, Applied result or hold test.
func TestScanFreshStatementPlans(t *testing.T) {
	// Pin the complete native seed dependency journal, not unrelated test bodies.
	// These SQL-fact populations are not a valid command manifest/FS authority.
	parts := []struct{ file, start, end, sha string }{
		{"scan_types_test.go", "func inventoryRequestFixture(", "func inventoryTestClone(", "79ed441e7172714ff02ca6a9fdbd025a367d1a84ad7241a0b08f9a0ac45e3404"},
		{"scan_fresh_test.go", "func inventoryFreshRequest(", "func inventoryFreshFixture(", "605a8d82bb789b5491e18661cfcd3be3ad18e32ac1cbf71bb3db8f50dfdaa380"},
		{"scan_fresh_plan_facts_test.go", "func seedInventoryFreshPlanFacts(", "func TestInventoryFreshNativeCompiledCohortPopulations(", "95d1b95d4e527ced085861bd66d89cf851779170bc911f892b134586ae975f9c"},
	}
	hash := sha256.New()
	for _, part := range parts {
		data, err := os.ReadFile(part.file)
		if err != nil {
			t.Fatal(err)
		}
		start, end := bytes.Index(data, []byte(part.start)), bytes.Index(data, []byte(part.end))
		if start < 0 || end <= start {
			t.Fatal("missing native seed journal", part.file)
		}
		data = data[start:end]
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != part.sha {
			t.Fatal("native seed changed; review populations before recording", part.file)
		}
		name := part.file + ":" + part.start
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(name))))
		hash.Write([]byte(name))
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(data))))
		hash.Write(data)
	}
	fixtureSHA := hex.EncodeToString(hash.Sum(nil))
	if fixtureSHA != "dbc6b53fc422f9f6a3f4e9ead9975e4a0ea6ceb2dea7a881c8c1ffd43f43605c" || schema.SHA256 != "49b3df34c79bd2d524860cea42909dd0d818d785b999bbe4221045e2b2ad4fe0" {
		t.Fatal("frozen fixture/schema changed; review before recording")
	}
	definitions := scanFreshDefinitions()
	if len(definitions) != 24 {
		t.Fatalf("scan definition inventory %d, want24", len(definitions))
	}
	expectedInsert := map[stmt.ID]string{
		scanFreshScalarInsert:   "catalog_scalar_contributions",
		scanFreshPhysicalInsert: "catalog_asset_contributions",
		scanFreshAgeInsert:      "catalog_age_item_counts",
	}
	cohorts, reads, writes := 0, 0, 0
	for _, d := range definitions {
		if !d.Plan.Hot || len(d.Plan.Waivers) != 0 {
			t.Fatalf("scan %s must be hot with no waivers", d.ID)
		}
		cohorts += len(d.Cohorts)
		switch d.Kind {
		case stmt.Read:
			reads++
		case stmt.Write:
			writes++
		default:
			t.Fatal("unexpected statement kind", d.ID, d.Kind)
		}
		if expectedInsert[d.ID] != "" && (len(d.Plan.Indexes) != 0 || len(d.Plan.PrimaryKeys) != 0 || len(d.Plan.Seeks) != 0) {
			t.Fatal("INSERT destination must not invent a seek", d.ID)
		}
	}
	if cohorts != 44 || reads != 13 || writes != 11 {
		t.Fatal("frozen statement population changed", cohorts, reads, writes)
	}
	registry, err := stmt.Describe(definitions)
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
		t.Fatalf("startup assertion=%v close=%v", assertErr, closeErr)
	}
	t.Logf("scan fixture startup physical settings: %+v", startup)
	seedInventoryFreshPlanFacts(t, w)
	// Capture only: use W's private frame to select Inventory NORMAL. This
	// testCustody does not qualify an accepting command or an admission recipe.
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.rollback(nil) })
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
	golden, err := cachedCapture(tx.ctx, tx.conn, registry, plan.Fixture{
		ConnectionPolicy: policy, SHA256: fixtureSHA, Statistics: plan.Unanalyzed,
		Implementations: "bundled SQLite3.53.4; builtin functions/collations; seedInventoryFreshPlanFacts + inventoryFreshRequest/seedInventoryFresh native pre/post-allocation SQL facts; not valid command manifest or FS authority; compile only",
	}, indexPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if len(golden.Plans) != 44 || golden.ForeignKeys == nil || plan.CheckSchemaForeignKeys(golden.ForeignKeys) != nil {
		t.Fatalf("incomplete scan evidence: cohorts=%d FK=%v", len(golden.Plans), golden.ForeignKeys != nil)
	}
	if len(golden.Manifest) != 24 {
		t.Fatal("incomplete new-only manifest", len(golden.Manifest))
	}
	// The new-only catalogue must receive a real exact-set receipt. W later
	// qualifies its complete43-ID effects set; this receipt cannot replace it.
	if _, err := stmt.New(definitions, golden.Receipt()); err != nil {
		t.Fatal("actual Capture did not qualify the exact new set", err)
	}
	inserts, opcodes, searches := 0, 0, 0
	for _, p := range golden.Plans {
		if len(p.Explain.Values) == 0 {
			t.Fatalf("empty native VM for %s/%s", p.ID, p.Cohort)
		}
		opcodes += len(p.Explain.Values)
		if table := expectedInsert[p.ID]; table != "" {
			if p.Insert == nil || p.Insert.Table != table || p.Insert.Conflict != nil {
				t.Fatal("wrong/missing native INSERT destination", p.ID, p.Cohort, table)
			}
			inserts++
		} else if p.Insert != nil || len(p.QueryPlan.Values) == 0 {
			t.Fatal("point/CAS lacks its real native seek", p.ID, p.Cohort)
		}
		for _, row := range p.QueryPlan.Values {
			if len(row) != 4 || !strings.HasPrefix(strings.ToUpper(row[3].(string)), "SEARCH ") {
				t.Fatal("unapproved plan row", p.ID, p.Cohort, row)
			}
			searches++
		}
	}
	if inserts != 3 {
		t.Fatalf("destination audits=%d, want3", inserts)
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
	// The complete global FK image must match the same adopted010a schema
	// captured by the receipt harness before any new artifact is written.
	baselineFK := readScanFreshFK(t, filepath.Join("testdata", "scan-inventory-accept-fk.json.gz"))
	if err := plan.CompareExactJSON(baselineFK, fkJSON); err != nil {
		t.Fatal("STOP: new fixture changed existing686-FK/2058-probe image", err)
	}
	golden.ForeignKeys = nil
	actual, err := golden.JSON()
	if err != nil {
		t.Fatal(err)
	}
	statementPath := filepath.Join("testdata", "scan-inventory-fresh.json")
	fkPath := filepath.Join("testdata", "scan-inventory-fresh-fk.json.gz")
	if *recordScanFreshPlans {
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
	expectedFK := readScanFreshFK(t, fkPath)
	if err := plan.CompareExactJSON(expectedFK, fkJSON); err != nil {
		t.Fatal(err)
	}

	t.Logf("24new compiled definitions/44cohorts/3destination audits/%dVM opcodes/%dSEARCH rows/shared-schema FK audit; fixture journal%s; no accepting SQL executed or full43-ID command qualified", opcodes, searches, fixtureSHA)
}

func readScanFreshFK(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(gz)
	closeErr := gz.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("FK golden read=%v close=%v", err, closeErr)
	}
	return data
}
