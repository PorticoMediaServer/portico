package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Native producer continuation facts. These are additive MG-LC statements in
// the dispatch file; the merged effects/plan inventory stays untouched. A
// recorded EXPLAIN golden for this family belongs to the train-level aggregate
// Capture, not a hand-written fixture here.
const (
	mgLCRuleStateRead  stmt.ID = "writer.mg.lc.rule-state-read"
	mgLCRuleStateWrite stmt.ID = "writer.mg.lc.rule-state-write"
	mgLCUsedRead       stmt.ID = "writer.mg.lc.used-read"
	mgLCUsedWrite      stmt.ID = "writer.mg.lc.used-write"
)

const (
	mgLCRuleStateColumns = `draw,cycle,last_sort,last_show,last_block,done`
	mgLCUsedColumns      = `item_id,cycle`
)

type mgLCRuleState struct {
	rule                          string
	draw, cycle                   int64
	lastSort, lastShow, lastBlock string
	done                          bool
}

type mgLCUsed struct {
	rule        string
	item, cycle int64
}

func mgLCDispatchStateDefinitions() []stmt.Definition {
	p := func(n string, typ stmt.Type) stmt.Param { return stmt.Param{Name: n, Type: typ} }
	t, i := stmt.Text, stmt.Integer
	defs := []stmt.Definition{
		{ID: mgLCRuleStateRead, Kind: stmt.Read, SQL: `SELECT ` + mgLCRuleStateColumns + ` FROM lc_rule_state INDEXED BY sqlite_autoindex_lc_rule_state_1 WHERE generation_id=? AND rule_id=?`, Params: []stmt.Param{p("generation", t), p("rule", t)},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_lc_rule_state_1"}, Seeks: []string{"generation_id=?", "rule_id=?"},
				AutoIndexes: []stmt.AutoIndexBinding{{Table: "lc_rule_state", Name: "sqlite_autoindex_lc_rule_state_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "generation_id", Collation: "BINARY"}, {Name: "rule_id", Collation: "BINARY"}}}}}},
		{ID: mgLCRuleStateWrite, Kind: stmt.Write, SQL: `INSERT INTO lc_rule_state(generation_id,rule_id,` + mgLCRuleStateColumns + `) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(generation_id,rule_id) DO UPDATE SET draw=excluded.draw,cycle=excluded.cycle,last_sort=excluded.last_sort,last_show=excluded.last_show,last_block=excluded.last_block,done=excluded.done RETURNING ` + mgLCRuleStateColumns,
			Params: []stmt.Param{p("generation", t), p("rule", t), p("draw", i), p("cycle", i), p("last_sort", t), p("last_show", t), p("last_block", t), p("done", i)},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"lc_rule_state"}, Seeks: []string{"generation_id=?", "rule_id=?"}}},
		{ID: mgLCUsedRead, Kind: stmt.Read, SQL: `SELECT ` + mgLCUsedColumns + ` FROM lc_used INDEXED BY sqlite_autoindex_lc_used_1 WHERE generation_id=? AND rule_id=? AND item_id>? ORDER BY item_id LIMIT ?`, Params: []stmt.Param{p("generation", t), p("rule", t), p("after", i), p("limit", i)},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_lc_used_1"}, Seeks: []string{"generation_id=?", "rule_id=?", "item_id>?"},
				AutoIndexes: []stmt.AutoIndexBinding{{Table: "lc_used", Name: "sqlite_autoindex_lc_used_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "generation_id", Collation: "BINARY"}, {Name: "rule_id", Collation: "BINARY"}, {Name: "item_id", Collation: "BINARY"}}}}}},
		{ID: mgLCUsedWrite, Kind: stmt.Write, SQL: `INSERT INTO lc_used(generation_id,rule_id,item_id,cycle) VALUES(?,?,?,?) ON CONFLICT(generation_id,rule_id,item_id) DO UPDATE SET cycle=excluded.cycle RETURNING rule_id,` + mgLCUsedColumns,
			Params: []stmt.Param{p("generation", t), p("rule", t), p("item", i), p("cycle", i)},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"lc_used"}, Seeks: []string{"generation_id=?", "rule_id=?", "item_id=?"}}},
	}
	for k := range defs {
		d := &defs[k]
		d.Owner = "writer.mg.lc.dispatch"
		d.CallSites = []string{"writer/mg_lc_dispatch.go"}
		args := make([]any, len(d.Params))
		for j, param := range d.Params {
			switch param.Type {
			case stmt.Text:
				args[j] = "missing"
			case stmt.Integer:
				args[j] = int64(1)
			}
		}
		d.Cohorts = []stmt.Cohort{{Name: "missing-native-keys", Bindings: args}}
	}
	return defs
}

// The dispatch turn needs both the merged LC native effects and its own
// continuation facts in one registry. No production runtime composes this yet.
func mgLCDispatchDefinitions() []stmt.Definition {
	return append(mgLCDefinitions(), mgLCDispatchStateDefinitions()...)
}
