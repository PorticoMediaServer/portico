package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// The native owner/identity UNIQUE point. A create must resolve a previously
// deleted row with the same (owner,source,series,match_title,match_channel)
// identity instead of colliding on it, and a live row with that identity is a
// duplicate. Updates and deletes use the primary-key point in mg_rule_statements.
const mgRuleIdentity stmt.ID = "writer.mg.rule.identity"

func mgRuleCommandDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	sql := `SELECT ` + mgRuleColumns + ` FROM dvr_rules INDEXED BY sqlite_autoindex_dvr_rules_2 WHERE owner_key=? AND source_id=? AND series_id=? AND match_title=? AND match_channel=? AND authority='local'`
	d := stmt.Definition{
		ID: mgRuleIdentity, Kind: stmt.Read, Owner: "writer.mg.rule", SQL: sql,
		Params:    []stmt.Param{s("owner"), s("source"), s("series"), s("title"), s("channel")},
		CallSites: []string{"writer/mg_rule_commands.go"},
		Cohorts:   []stmt.Cohort{{Name: "missing-identity", Bindings: []any{"missing", "missing", "missing", "missing", "missing"}}},
		Plan: stmt.Plan{
			Hot:     true,
			Indexes: []string{"sqlite_autoindex_dvr_rules_2"},
			Seeks:   []string{"owner_key=? AND source_id=? AND series_id=? AND match_title=? AND match_channel=?"},
			AutoIndexes: []stmt.AutoIndexBinding{{
				Table: "dvr_rules", Name: "sqlite_autoindex_dvr_rules_2", Origin: "u",
				Columns: []stmt.IndexColumn{
					{Name: "owner_key", Collation: "BINARY"},
					{Name: "source_id", Collation: "BINARY"},
					{Name: "series_id", Collation: "BINARY"},
					{Name: "match_title", Collation: "BINARY"},
					{Name: "match_channel", Collation: "BINARY"},
				},
			}},
		},
	}
	return []stmt.Definition{d}
}
