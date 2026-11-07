package plan

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func homeManifestOctetControl(list bool) stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	d := stmt.Definition{ID: "read.browse.rb.home.manifest.library.bytes", SQL: rbHomeManifestLibrarySQL, Owner: "store/read", Kind: stmt.Read, Params: []stmt.Param{text("library")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_libraries_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "libraries", Name: "sqlite_autoindex_libraries_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}, Seeks: []string{"id=?"}}}
	if list {
		d.ID = "read.browse.rb.home.manifest.list.bytes"
		d.SQL = rbHomeManifestListSQL
		d.Params = []stmt.Param{text("authority"), text("account"), text("profile"), text("scope"), text("device"), text("key"), {Name: "position", Type: stmt.Integer}}
		d.Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"preference_list_items"}, Seeks: []string{"authority=? AND account_id=? AND profile_id=? AND scope=? AND device_key=? AND key=? AND position=?"}}
	}
	return d
}
func TestRBHomeManifestOctetPurpose(t *testing.T) {
	for _, list := range []bool{false, true} {
		original := homeManifestOctetControl(list)
		if !closedOctetDefinition(original) || checkSyntax(original) != nil || len(LintSQL(original.SQL)) == 0 {
			t.Fatal("closed manifest point widened generic grammar", list)
		}
		for n, mutate := range []func(*stmt.Definition){
			func(d *stmt.Definition) { d.ID += ".borrowed" },
			func(d *stmt.Definition) { d.SQL += " " },
			func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "=?", ">?", 1) },
			func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "octet_length", "length", 1) },
			func(d *stmt.Definition) { d.Owner = "other" },
			func(d *stmt.Definition) { d.Params[0].Nullable = true },
			func(d *stmt.Definition) { d.Params[0].Type = stmt.Integer },
			func(d *stmt.Definition) { d.Plan.Hot = false },
			func(d *stmt.Definition) { d.Plan.AllowedScans = []string{"libraries"} },
			func(d *stmt.Definition) { d.Plan.Seeks = nil },
		} {
			changed := homeManifestOctetControl(list)
			mutate(&changed)
			if closedOctetDefinition(changed) || checkSyntax(changed) == nil {
				t.Fatal("manifest byte authority borrowed", list, n)
			}
		}
	}
}
