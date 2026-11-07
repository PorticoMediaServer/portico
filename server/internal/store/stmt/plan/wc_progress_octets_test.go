package plan_test

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"strings"
	"testing"
)

func wcSnapshotOctetsDefinition() stmt.Definition {
	return stmt.Definition{
		ID: "wc.progress.snapshot_octets", Owner: "WC snapshot length preflight", CallSites: []string{"wc_progress_octets_test.go"}, Kind: stmt.Read,
		SQL:    `SELECT octet_length(s.state) FROM personal_items AS i INDEXED BY personal_current_live INNER JOIN personal_snapshots AS s ON s.profile_id=i.profile_id AND s.item_id=i.item_id AND s.revision=i.revision WHERE i.profile_id=? AND i.item_id=?`,
		Params: []stmt.Param{{Name: "profile", Type: stmt.Text}, {Name: "subject", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "native_unique_points", Bindings: []any{"viewer-one", int64(11)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"personal_current_live", "sqlite_autoindex_personal_snapshots_1"}},
	}
}
func TestWCProgressSnapshotOctetPointIsClosed(t *testing.T) {
	conn, fixture, policy := issuerOctetsFixture(t)
	d := wcSnapshotOctetsDefinition()
	if len(plan.LintSQL(d.SQL)) == 0 {
		t.Fatal("generic lint admitted function")
	}
	if _, err := captureIssuerOctets(t, conn, fixture, policy, d); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*stmt.Definition){
		func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "octet_length(s.state)", "octet_length(s.created_at)", 1)
		},
		func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "i.item_id=?", "i.item_id>?", 1) },
		func(d *stmt.Definition) { d.Params[1].Type = stmt.Text },
	} {
		bad := wcSnapshotOctetsDefinition()
		change(&bad)
		if _, err := captureIssuerOctets(t, conn, fixture, policy, bad); err == nil {
			t.Fatal("borrowed closed length point", bad.SQL)
		}
	}
}
