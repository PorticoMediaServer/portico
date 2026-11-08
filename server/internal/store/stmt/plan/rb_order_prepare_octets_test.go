package plan

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func rbOrderPrepareOctetControl() stmt.Definition {
	return stmt.Definition{ID: stmt.ID("read.browse.rb.order_prepare.point_octets"), SQL: rbOrderPreparePointOctetsSQL, Kind: stmt.Read, Owner: "store/read", Params: []stmt.Param{{Name: "entity", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}, Seeks: []string{"rowid=?"}}}
}

func TestRBOrderPrepareOctetPoint(t *testing.T) {
	d := rbOrderPrepareOctetControl()
	if !closedOctetDefinition(d) || checkSyntax(d) != nil || len(LintSQL(d.SQL)) == 0 {
		t.Fatal("closed ordered-ID point rejected")
	}
	mutations := []func(*stmt.Definition){
		func(d *stmt.Definition) { d.ID += ".foreign-purpose" },
		func(d *stmt.Definition) { d.SQL += " " },
		func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "e.id=?", "e.id>?", 1) },
		func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "octet_length(e.title)", "length(e.title)", 1)
		},
		func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "octet_length(e.sort_key)", "octet_length(e.sort_key_keep_articles)", 1)
		},
		func(d *stmt.Definition) { d.Owner = "foreign-owner" },
		func(d *stmt.Definition) { d.Params[0].Type = stmt.Text },
		func(d *stmt.Definition) { d.Params[0].Nullable = true },
		func(d *stmt.Definition) { d.Plan.Hot = false },
		func(d *stmt.Definition) { d.Plan.AllowedScans = []string{"e"} },
		func(d *stmt.Definition) { d.Plan.Seeks = nil },
		func(d *stmt.Definition) { d.Plan.PrimaryKeys = nil },
	}
	for n, mutate := range mutations {
		changed := rbOrderPrepareOctetControl()
		mutate(&changed)
		if closedOctetDefinition(changed) || checkSyntax(changed) == nil {
			t.Fatal("octet authority borrowed", n, changed)
		}
	}
}
