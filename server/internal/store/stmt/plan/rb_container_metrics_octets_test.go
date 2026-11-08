package plan

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func rbContainerMetricsOctetControl() stmt.Definition {
	spec := rbContainerMetricsOctetSources()[0]
	params := append(append([]stmt.Param(nil), spec.params...), stmt.Param{Name: "key0", Type: stmt.Integer}, stmt.Param{Name: "limit", Type: stmt.Integer})
	return stmt.Definition{
		ID:     stmt.ID("read.browse.rb.container_metrics.children.show.tail0.bytes"),
		SQL:    rbContainerMetricsOctetSelect(spec, []string{"e.id>?"}),
		Kind:   stmt.Read,
		Owner:  "store/read",
		Params: params,
		Plan:   stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_show_fk", "personal_current_live"}, Seeks: []string{"show_id=?", "profile_id=? AND item_id=?"}},
	}
}

func TestRBContainerMetricsOctetRange(t *testing.T) {
	d := rbContainerMetricsOctetControl()
	if !closedOctetDefinition(d) || checkSyntax(d) != nil {
		t.Fatal("closed container-child byte range rejected")
	}
	mutations := []func(*stmt.Definition){
		func(d *stmt.Definition) { d.ID += ".foreign-purpose" },
		func(d *stmt.Definition) { d.SQL += " " },
		func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "e.id>?", "e.id<?", 1) },
		func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "octet_length(e.latest_aired_key)", "length(e.latest_aired_key)", 1)
		},
		func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "catalog_entities_show_fk", "catalog_entities_parent", 1)
		},
		func(d *stmt.Definition) { d.Owner = "foreign-owner" },
		func(d *stmt.Definition) { d.Params[0].Type = stmt.Text },
		func(d *stmt.Definition) { d.Params[3].Nullable = true },
		func(d *stmt.Definition) { d.Plan.Hot = false },
		func(d *stmt.Definition) { d.Plan.AllowedScans = []string{"e"} },
		func(d *stmt.Definition) { d.Plan.Seeks = nil },
		func(d *stmt.Definition) { d.Plan.Indexes = nil },
	}
	for n, mutate := range mutations {
		changed := rbContainerMetricsOctetControl()
		mutate(&changed)
		if closedOctetDefinition(changed) || checkSyntax(changed) == nil {
			t.Fatal("octet authority borrowed", n, changed)
		}
	}
}
