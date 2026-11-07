package plan

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func savedViewOctetControl(owner bool) stmt.Definition {
	p := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	key := func(table, name string, cols ...string) stmt.AutoIndexBinding {
		b := stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk"}
		for _, c := range cols {
			b.Columns = append(b.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		return b
	}
	r := key("saved_resources", "sqlite_autoindex_saved_resources_1", "id")
	s := key("saved_resource_shares", "sqlite_autoindex_saved_resource_shares_1", "resource_id", "authority", "account_id", "profile_id")
	d := stmt.Definition{ID: "read.browse.rb.saved.view.resource.shared.bytes", SQL: rbSavedViewSharedOctetsSQL, Kind: stmt.Read, Owner: "store/read", Params: []stmt.Param{p("authority"), p("share_account"), p("share_profile"), p("id")}, Plan: stmt.Plan{Hot: true, Indexes: []string{r.Name, s.Name}, AutoIndexes: []stmt.AutoIndexBinding{r, s}, Seeks: []string{"id=?", "resource_id=? AND authority=? AND account_id=? AND profile_id=?"}}}
	if owner {
		d.ID = "read.browse.rb.saved.view.resource.owner.bytes"
		d.SQL = rbSavedViewOwnerOctetsSQL
		d.Params = []stmt.Param{p("id"), p("owner_authority"), p("owner_account"), p("owner_profile")}
		d.Plan = stmt.Plan{Hot: true, Indexes: []string{r.Name}, AutoIndexes: []stmt.AutoIndexBinding{r}, Seeks: []string{"id=?"}}
	}
	return d
}
func TestRBSavedViewOctetPurpose(t *testing.T) {
	for _, owner := range []bool{false, true} {
		d := savedViewOctetControl(owner)
		if !closedOctetDefinition(d) || checkSyntax(d) != nil || len(LintSQL(d.SQL)) == 0 {
			t.Fatal("closed point widened generic grammar", d.ID)
		}
		mutations := []func(*stmt.Definition){
			func(d *stmt.Definition) { d.ID += ".foreign-purpose" },
			func(d *stmt.Definition) { d.SQL += " " },
			func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "r.id=?", "r.id>?", 1) },
			func(d *stmt.Definition) {
				d.SQL = strings.Replace(d.SQL, "octet_length(r.definition)", "length(r.definition)", 1)
			},
			func(d *stmt.Definition) { d.Owner = "foreign-owner" },
			func(d *stmt.Definition) { d.Params[0].Type = stmt.Integer },
			func(d *stmt.Definition) { d.Params[0].Nullable = true },
			func(d *stmt.Definition) { d.Plan.Hot = false },
			func(d *stmt.Definition) { d.Plan.AllowedScans = []string{"r"} },
			func(d *stmt.Definition) { d.Plan.Seeks = nil },
			func(d *stmt.Definition) { d.Plan.AutoIndexes[0].Columns[0].Collation = "NOCASE" },
		}
		for n, mutate := range mutations {
			changed := savedViewOctetControl(owner)
			mutate(&changed)
			if closedOctetDefinition(changed) || checkSyntax(changed) == nil {
				t.Fatal("octet authority borrowed", owner, n, changed)
			}
		}
	}
}
