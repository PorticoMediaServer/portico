package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordInventoryDisclosurePlans = flag.Bool("record-inventory-disclosure-plans", false, "deliberately record Inventory disclosure point-read plans")

// These captures compile W's private native relationship reads. They do not
// issue a principal, authorize disclosure or qualify a composed command.
func TestInventoryDisclosurePlanGoldens(t *testing.T) {
	journal, err := os.ReadFile("scan_receipts_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start, end := bytes.Index(journal, []byte("type scanNativePopulation")), bytes.Index(journal, []byte("func scanFixtureTurn"))
	if start < 0 || end <= start {
		t.Fatal("missing immutable SC fixture journal")
	}
	journal = journal[start:end]
	digest := sha256.Sum256(journal)
	if hex.EncodeToString(digest[:]) != "498990719bdefd61b423a99961f74de664c09ff4f7f040af946b79dec53b6723" {
		t.Fatal("SC fixture journal changed; review populations before recording")
	}
	fixture, err := os.ReadFile("inventory_disclosure_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start, end = bytes.Index(fixture, []byte("func inventoryDisclosureFixture(")), bytes.Index(fixture, []byte("func inventoryDisclosureTurn("))
	if start < 0 || end <= start {
		t.Fatal("missing immutable W disclosure fixture")
	}
	fixture = fixture[start:end]
	digest = sha256.Sum256(fixture)
	if hex.EncodeToString(digest[:]) != "b396c7822c2424fabc2741e7fadd9816d624b83b039621a3e2ec73f8e5a565ba" {
		t.Fatal("W disclosure fixture changed; review populations before recording")
	}
	fixtureBytes := append(append([]byte(nil), journal...), fixture...)
	digest = sha256.Sum256(fixtureBytes)
	fixtureSHA := hex.EncodeToString(digest[:])
	definitions := inventoryDisclosureDefinitions()
	if len(definitions) != 2 {
		t.Fatalf("disclosure definitions=%d, want2", len(definitions))
	}
	for _, d := range definitions {
		if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(d.Params) != 2 || len(d.Cohorts) != 3 {
			t.Fatalf("%s must retain its closed hot read/two bindings/three cohorts/no-waiver contract", d.ID)
		}
	}
	catalogue, err := stmt.Describe(definitions)
	if err != nil {
		t.Fatal(err)
	}
	w := inventoryDisclosureFixture(t)
	startupConn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	startup, assertErr := sqliteconn.Assert(t.Context(), startupConn, w.config)
	closeErr := startupConn.Close()
	if assertErr != nil || closeErr != nil {
		t.Fatalf("startup assertion=%v close=%v", assertErr, closeErr)
	}
	t.Logf("disclosure fixture FULL startup settings: %+v", startup)
	registry := nativeInventoryFixtureRegistry(t, w, definitions, "inventory_disclosure_test.go", "scan_receipts_test.go")
	_, tx := inventoryDisclosureTurn(t, w, registry)
	policy := w.config
	policy.Synchronous = sqliteconn.Normal
	settings, err := sqliteconn.Assert(tx.ctx, tx.conn, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("disclosure Inventory NORMAL acquired settings: %+v", settings)
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
	golden, err := cachedCapture(tx.ctx, tx.conn, catalogue, plan.Fixture{
		ConnectionPolicy: policy, SHA256: fixtureSHA, Statistics: plan.Unanalyzed,
		Implementations: "bundled SQLite3.53.4; builtin functions/collations; immutable SC seedScanReceiptCohorts plus W inventoryDisclosureFixture library8; native relationship facts only; compile only",
	}, indexPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.New(definitions, golden.Receipt()); err != nil {
		t.Fatal("complete native Capture did not qualify the exact set", err)
	}
	if len(golden.Plans) != 6 || golden.ForeignKeys != nil {
		t.Fatalf("disclosure evidence cohorts=%d, unexpected INSERT FK audit=%v", len(golden.Plans), golden.ForeignKeys != nil)
	}
	for _, p := range golden.Plans {
		if len(p.Explain.Values) == 0 || len(p.QueryPlan.Values) != 1 || p.Insert != nil {
			t.Fatalf("%s/%s must retain a nonempty VM and one indexed read SEARCH with no INSERT destination", p.ID, p.Cohort)
		}
	}
	var after int64
	if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("capture mutated fixture: total_changes %d -> %d", before, after)
	}
	actual, err := golden.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "scan-inventory-disclosure.json")
	if *recordInventoryDisclosurePlans {
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
	t.Logf("2compiled read IDs/6cohorts/one SEARCH each/0INSERT destinations; fixture%s; no permission or accepting-command qualification", fixtureSHA)
}
