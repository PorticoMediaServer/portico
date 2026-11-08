package plan_test

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func scBasicAssetOctetTestDefinition() stmt.Definition {
	return stmt.Definition{ID: "scan.basic_projection.asset_lengths", Owner: "SC Basic asset preflight", CallSites: []string{"sc_basic_asset_octets_test.go"}, Kind: stmt.Read,
		SQL:    "SELECT octet_length(token),octet_length(container),octet_length(video_codec),octet_length(audio_codec),octet_length(playback_origin_incarnation) FROM catalog_assets WHERE id=? AND library_id=?",
		Params: []stmt.Param{{Name: "asset_id", Type: stmt.Integer}, {Name: "library_id", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}},
		Cohorts: []stmt.Cohort{{Name: "missing_native_asset", Bindings: []any{int64(41), int64(7)}}}}
}

func TestSCBasicAssetOctetsStayOneLibraryFencedPoint(t *testing.T) {
	conn, fixture, policy := issuerOctetsFixture(t)
	d := scBasicAssetOctetTestDefinition()
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
			d.SQL = strings.Replace(d.SQL, "octet_length(token)", "octet_length(path)", 1)
		},
		func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, " AND library_id=?", "", 1)
			d.Params = d.Params[:1]
			d.Cohorts[0].Bindings = d.Cohorts[0].Bindings[:1]
		},
	} {
		changed := scBasicAssetOctetTestDefinition()
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
