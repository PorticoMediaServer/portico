package plan_test

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func basicResumeOctetTestDefinition() stmt.Definition {
	return stmt.Definition{ID: "scan.basic_stage.resume_lengths", Owner: "Basic resume byte preflight", CallSites: []string{"basic_resume_octets_test.go"}, Kind: stmt.Read,
		SQL:    "SELECT octet_length(copy_key),octet_length(copy_key2),octet_length(copy_key3),octet_length(fault) FROM catalog_info_changes WHERE id=? AND job_revision=?",
		Params: []stmt.Param{{Name: "change_id", Type: stmt.Integer}, {Name: "revision", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}},
		Cohorts: []stmt.Cohort{{Name: "missing_native_asset", Bindings: []any{int64(41), int64(7)}}}}
}

func TestBasicResumeOctetsStayOneRevisionFencedPoint(t *testing.T) {
	conn, fixture, policy := issuerOctetsFixture(t)
	d := basicResumeOctetTestDefinition()
	if len(plan.LintSQL(d.SQL)) == 0 {
		t.Fatal("public function syntax broadened")
	}
	g, err := captureIssuerOctets(t, conn, fixture, policy, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stmt.New([]stmt.Definition{d}, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*stmt.Definition){
		func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "octet_length(copy_key)", "octet_length(asset_token)", 1)
		},
		func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, " AND job_revision=?", "", 1)
			d.Params = d.Params[:1]
			d.Cohorts[0].Bindings = d.Cohorts[0].Bindings[:1]
		},
	} {
		changed := basicResumeOctetTestDefinition()
		mutate(&changed)
		g, err := captureIssuerOctets(t, conn, fixture, policy, changed)
		if err == nil {
			t.Fatal("broadened point admitted")
		}
		if _, err := stmt.New([]stmt.Definition{changed}, g.Receipt()); err == nil {
			t.Fatal("failed Capture admitted registry")
		}
	}
}
