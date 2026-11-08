package plan_test

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"strings"
	"testing"
)

func scBasicHeaderTestDefinition() stmt.Definition {
	return stmt.Definition{ID: "scan.basic_receipt.header_lengths", Owner: "SC Basic byte preflight", CallSites: []string{"sc_basic_octets_test.go"}, Kind: stmt.Read,
		SQL:     "SELECT octet_length(source_id),octet_length(source_incarnation),octet_length(source_revision),octet_length(next_cursor),octet_length(next_copy_key),octet_length(next_copy_key2),octet_length(next_copy_key3) FROM scan_command_receipts WHERE run_id=? AND command_incarnation=? AND command_sequence=?",
		Params:  []stmt.Param{{Name: "run_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob}, {Name: "command_sequence", Type: stmt.Integer}},
		Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"scan_command_receipts"}, Seeks: []string{"run_id=? AND command_incarnation=? AND command_sequence=?"}},
		Cohorts: []stmt.Cohort{{Name: "missing_full_key", Bindings: []any{"absent", make([]byte, 16), int64(19)}}}}
}
func TestSCBasicHeaderOctetsAreOneClosedNativePoint(t *testing.T) {
	conn, fixture, policy := issuerOctetsFixture(t)
	d := scBasicHeaderTestDefinition()
	if len(plan.LintSQL(d.SQL)) == 0 {
		t.Fatal("public lint gained a function allowlist")
	}
	golden, err := captureIssuerOctets(t, conn, fixture, policy, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.New([]stmt.Definition{d}, golden.Receipt()); err != nil {
		t.Fatal(err)
	}
	// Two realistic broadening falsifiers: another projection and a key prefix.
	for _, mutate := range []func(*stmt.Definition){
		func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "octet_length(source_id)", "octet_length(run_id)", 1)
		},
		func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, " AND command_sequence=?", "", 1)
			d.Params = d.Params[:2]
			d.Cohorts[0].Bindings = d.Cohorts[0].Bindings[:2]
		},
	} {
		changed := scBasicHeaderTestDefinition()
		mutate(&changed)
		g, err := captureIssuerOctets(t, conn, fixture, policy, changed)
		if err == nil {
			t.Fatal("broadened preflight qualified")
		}
		if _, err := stmt.New([]stmt.Definition{changed}, g.Receipt()); err == nil {
			t.Fatal("failed Capture authorized a registry")
		}
	}
}
