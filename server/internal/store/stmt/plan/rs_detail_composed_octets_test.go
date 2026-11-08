package plan

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func composedItemOctetControl() stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	return stmt.Definition{ID: rsDetailComposedItemOctetsID, SQL: rsDetailComposedItemOctetsSQL, Kind: stmt.Read, Owner: "store/read", Params: []stmt.Param{{Name: "profile", Type: stmt.Text}, i("item"), i("library"), i("kind")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}}
}

func TestRSDetailComposedOctetPurpose(t *testing.T) {
	d := composedItemOctetControl()
	if !closedOctetDefinition(d) || checkSyntax(d) != nil || len(LintSQL(d.SQL)) == 0 {
		t.Fatal("composed item point widened generic grammar")
	}
	for n, mutate := range []func(*stmt.Definition){
		func(d *stmt.Definition) { d.ID += ".foreign-purpose" },
		func(d *stmt.Definition) { d.SQL += " " },
		func(d *stmt.Definition) { d.SQL = strings.Replace(d.SQL, "e.id=?", "e.id>?", 1) },
		func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "octet_length(e.title)", "length(e.title)", 1)
		},
		func(d *stmt.Definition) { d.Params[0].Type = stmt.Integer },
		func(d *stmt.Definition) { d.Params[0].Nullable = true },
		func(d *stmt.Definition) { d.Plan.Hot = false },
		func(d *stmt.Definition) { d.Plan.AllowedScans = []string{"e"} },
		func(d *stmt.Definition) { d.Plan.PrimaryKeys = nil },
		func(d *stmt.Definition) { d.Plan.Indexes = []string{"personal_current_live"} },
	} {
		changed := composedItemOctetControl()
		mutate(&changed)
		if closedOctetDefinition(changed) || checkSyntax(changed) == nil {
			t.Fatal("composed octet authority borrowed", n, changed)
		}
	}
}

func TestRSDetailComposedItemPointShape(t *testing.T) {
	pk := []any{int64(2), int64(0), int64(45), "SEARCH e USING INTEGER PRIMARY KEY (rowid=?)"}
	personal := []any{int64(7), int64(0), int64(0), "SEARCH p USING INDEX personal_current_live (profile_id=? AND item_id=?)"}
	for _, rows := range [][][]any{
		{pk},
		{pk, personal},
	} {
		if err := checkRSDetailComposedItemPoint(Rows{Values: rows}); err != nil {
			t.Fatal("exact composed item point refused", rows, err)
		}
	}
	for _, rows := range [][][]any{
		nil,
		{[]any{int64(2), int64(0), int64(45), "SCAN e"}},
		{personal, pk},
		{pk, []any{int64(7), int64(0), int64(0), "SCAN p"}},
	} {
		if err := checkRSDetailComposedItemPoint(Rows{Values: rows}); err == nil {
			t.Fatal("range/foreign item point admitted", rows)
		}
	}
}
