package writer

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
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

var recordScanLifecycleHistoryPlans = flag.Bool("record-scan-lifecycle-history-plans", false, "deliberately record closed lifecycle history read plans")

// Actual W-owned receipt populations and complete ten-ID fixture registration;
// this records only the two new read points, not an issuer or fresh profile.
func TestScanLifecycleHistoryStatementPlans(t *testing.T) {
	w, _, complete := scanLifecycleProtocolFixture(t)
	if len(complete.Manifest()) != 10 {
		t.Fatal("fixture must retain complete ten-ID native Capture")
	}
	parts := []struct{ name, sha string }{
		{"scan_lifecycle_protocol_test.go", "074c14744196963202a5d841972aed0f9acf0f068bb83bb7d4536d33df1c56e6"},
		{"scan_receipts_test.go", "27c2de821f72f47109fd758a39c026b4b5d7492b2e5f0124e4b9100a13d394ae"},
	}
	hash := sha256.New()
	for _, part := range parts {
		data, err := os.ReadFile(part.name)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != part.sha {
			t.Fatal("frozen source journal changed; review before recording", part.name)
		}
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(part.name))))
		hash.Write([]byte(part.name))
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(data))))
		hash.Write(data)
	}
	fixtureSHA := hex.EncodeToString(hash.Sum(nil))
	if fixtureSHA != "ad9cb0c0c26495f374949114cc55e4d149dff57cd64432d6b8e40ebba1eef829" {
		t.Fatal("closed source journal changed")
	}
	defs := scanRunLifecycleHistoryDefinitions()
	if len(defs) != 2 {
		t.Fatal("history definition inventory changed")
	}
	for _, d := range defs {
		if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Plan.Waivers) != 0 || len(plan.LintSQL(d.SQL)) != 0 {
			t.Fatal("history must retain closed hot read grammar", d.ID)
		}
		switch d.ID {
		case scanRunHistoryOwner:
			if len(d.Params) != 1 || len(d.Cohorts) != 8 {
				t.Fatal("owner binding/cohort inventory changed")
			}
		case scanRunHistoryBody:
			if len(d.Params) != 3 || len(d.Cohorts) != 9 {
				t.Fatal("body binding/cohort inventory changed")
			}
		default:
			t.Fatal("unknown history definition", d.ID)
		}
	}
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	settings, err := sqliteconn.Assert(t.Context(), conn, w.config)
	if err != nil || settings.Synchronous != int(sqliteconn.Full) {
		t.Fatal("actual FULL startup boundary", settings, err)
	}
	t.Logf("history FULL startup Capture settings: %+v", settings)
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	golden, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: fixtureSHA, Statistics: plan.Unanalyzed,
		Implementations: "W scanLifecycleProtocolFixture actual saved create/claim/history/floor populations; FULL startup; structural two-read qualification only, not public issuance or a fresh execution/resource profile"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.New(defs, golden.Receipt()); err != nil {
		t.Fatal("actual exact two-ID Capture receipt", err)
	}
	if len(golden.Manifest) != 2 || len(golden.Plans) != 17 || golden.ForeignKeys != nil {
		t.Fatal("incomplete history Capture or unexpected DML/FK audit")
	}
	opcodes := 0
	for _, p := range golden.Plans {
		if len(p.Explain.Values) == 0 || len(p.QueryPlan.Values) != 1 || p.Insert != nil || !strings.HasPrefix(p.QueryPlan.Values[0][3].(string), "SEARCH ") {
			t.Fatal("history must remain one native indexed point", p.ID, p.Cohort)
		}
		opcodes += len(p.Explain.Values)
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || before != after {
		t.Fatal("Capture changed saved history", before, after, err)
	}
	actual, err := golden.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "scan-run-lifecycle-history.json")
	if *recordScanLifecycleHistoryPlans {
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
	t.Logf("2new read IDs/17cohorts(owner8,body9)/%dVM/17SEARCH/no DML/FK artifact; live exact receipt; no public issuance/fresh profile", opcodes)
}
