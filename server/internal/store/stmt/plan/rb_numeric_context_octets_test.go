package plan

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func rbNumericContextOctetControl() stmt.Definition {
	return stmt.Definition{ID: stmt.ID("read.browse.rb.numeric_context.scalar_octets"), SQL: rbNumericContextScalarOctetsSQL, Kind: stmt.Read, Owner: "store/read", Params: []stmt.Param{{Name: "entity", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}, Seeks: []string{"rowid=?"}}}
}

func TestRBNumericContextOctetPoint(t *testing.T) {
	d := rbNumericContextOctetControl()
	if !closedOctetDefinition(d) || checkSyntax(d) != nil || len(LintSQL(d.SQL)) == 0 {
		t.Fatal("closed numeric-context point rejected")
	}
	mutations := []func(*stmt.Definition){
		func(d *stmt.Definition) { d.ID += ".foreign-purpose" },
		func(d *stmt.Definition) { d.SQL += " " },
		func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "e.id=?", "e.id>?", 1) },
		func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "octet_length(e.title)", "length(e.title)", 1)
		},
		func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "octet_length(e.release_date)", "octet_length(e.added_at)", 1)
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
		changed := rbNumericContextOctetControl()
		mutate(&changed)
		if closedOctetDefinition(changed) || checkSyntax(changed) == nil {
			t.Fatal("octet authority borrowed", n, changed)
		}
	}
}
