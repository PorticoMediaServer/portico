package plan

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func rbPersonalPrepareRecentOctetControl(part string) stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	sqlText := rbPersonalPrepareRecentFirstSQL
	params := []stmt.Param{s("profile"), i("library"), i("kind"), i("limit")}
	if part == "tail" {
		sqlText = rbPersonalPrepareRecentTailSQL
		params = []stmt.Param{s("profile"), i("library"), i("kind"), i("after_activity"), s("after_text"), b("public_id"), i("limit")}
	}
	return stmt.Definition{ID: stmt.ID("read.browse.rb.personal_prepare.recent." + part + ".bytes"), SQL: sqlText, Kind: stmt.Read, Owner: "store/read", Params: params, Plan: stmt.Plan{Hot: true, Indexes: []string{"personal_current_recent"}, Seeks: []string{"profile_id=?", "rowid=?"}}}
}

func TestRBPersonalPrepareOctetRanges(t *testing.T) {
	for _, part := range []string{"first", "tail"} {
		d := rbPersonalPrepareRecentOctetControl(part)
		if !closedOctetDefinition(d) || checkSyntax(d) != nil || len(LintSQL(d.SQL)) == 0 {
			t.Fatal("closed personal-order recent range rejected", part)
		}
		mutations := []func(*stmt.Definition){
			func(d *stmt.Definition) { d.ID += ".foreign-purpose" },
			func(d *stmt.Definition) { d.SQL += " " },
			func(d *stmt.Definition) {
				d.SQL = strings.Replace(d.SQL, "octet_length(p.last_played_at)", "length(p.last_played_at)", 1)
			},
			func(d *stmt.Definition) {
				d.SQL = strings.Replace(d.SQL, "p.last_played_at>''", "p.last_played_at>=''", 1)
			},
			func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "p.activity_visible ASC,", "", 1) },
			func(d *stmt.Definition) { d.Owner = "foreign-owner" },
			func(d *stmt.Definition) { d.Plan.Hot = false },
			func(d *stmt.Definition) { d.Plan.AllowedScans = []string{"p"} },
			func(d *stmt.Definition) { d.Plan.Seeks = nil },
			func(d *stmt.Definition) { d.Plan.Indexes = nil },
		}
		for n, mutate := range mutations {
			changed := rbPersonalPrepareRecentOctetControl(part)
			mutate(&changed)
			if closedOctetDefinition(changed) || checkSyntax(changed) == nil {
				t.Fatal("octet authority borrowed", part, n, changed)
			}
		}
	}
}
