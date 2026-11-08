package plan

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
	"testing"
)

// Callback closure controls only; actual composed Capture waits for C embed.
func TestIARefreshOctetPointsStayClosed(t *testing.T) {
	if len(iaRefreshOctetPoints) != 14 {
		t.Fatal("seven current/inverse byte pairs required")
	}
	for _, v := range iaRefreshOctetPoints {
		p := stmt.Plan{Hot: true}
		if v.index == "" {
			p.PrimaryKeys = []string{v.table}
		} else {
			p.Indexes = []string{v.index}
		}
		makeDef := func() stmt.Definition {
			return stmt.Definition{ID: v.id, SQL: v.sql, Kind: stmt.Read, Params: append([]stmt.Param(nil), v.params...), Plan: p}
		}
		d := makeDef()
		if !closedOctetDefinition(d) {
			t.Fatal("missing exact point", v.id)
		}
		kind := "INDEX " + v.index
		if v.index == "" {
			kind = "PRIMARY KEY"
		}
		want := "SEARCH " + v.table + " USING " + kind + " (" + v.seek + ")"
		eqp := func(s string) Rows {
			return Rows{Columns: []string{"id", "parent", "notused", "detail"}, Values: [][]any{{int64(1), int64(0), int64(0), s}}}
		}
		if err := checkOctetPoint(d, eqp(want)); err != nil {
			t.Fatal(v.id, err)
		}
		for _, s := range []string{"SCAN " + v.table, strings.Replace(want, "=?", ">?", 1), want + " extra"} {
			if err := checkOctetPoint(d, eqp(s)); err == nil {
				t.Fatal("bad seek accepted", s)
			}
		}
		for _, mutate := range []func(*stmt.Definition){func(d *stmt.Definition) { d.SQL += " " }, func(d *stmt.Definition) { d.Params[0].Nullable = true }, func(d *stmt.Definition) { d.Plan.Hot = false }, func(d *stmt.Definition) { d.ID += ".borrowed" }} {
			bad := makeDef()
			mutate(&bad)
			if closedOctetDefinition(bad) {
				t.Fatal("borrowed point accepted", v.id)
			}
		}
	}
}
