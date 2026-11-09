package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// The native owner/identity UNIQUE point. A create must resolve a previously
// deleted row with the same (owner,source,series,match_title,match_channel)
// identity instead of colliding on it, and a live row with that identity is a
// duplicate. Updates and deletes use the primary-key point in mg_rule_statements.
const mgRuleIdentity stmt.ID = "writer.mg.rule.identity"

// Legacy DeleteRule(future=keep) detaches the rule's future intents. The MG
// delete performs that detach in the SAME transaction through these two closed
// statements: one bounded count (never an unbounded scan), and one bounded
// UPDATE over exactly the counted rows. Both are scoped to the deleted rule and
// its owner; the state set and manual-keep columns are the legacy ones.
const (
	mgRuleDetachCount stmt.ID = "writer.mg.rule.detach-count"
	mgRuleDetach      stmt.ID = "writer.mg.rule.detach"
)

// The rule's future intents, bounded by the shared detach ceiling. The rule
// index (rule_id,start_ms,id) supplies the access path; start_ms,id makes the
// cut deterministic when the caller is at the bound.
const mgRuleDetachStates = `state IN('scheduled','conflicted','waiting-source','waiting-guide')`

func mgRuleCommandDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
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
	count := stmt.Definition{
		ID: mgRuleDetachCount, Kind: stmt.Read, Owner: "writer.mg.rule",
		SQL: `SELECT count(*) FROM (SELECT id FROM dvr_recordings INDEXED BY dvr_recordings_rule WHERE rule_id=? AND owner_key=? AND ` +
			mgRuleDetachStates + ` ORDER BY start_ms,id LIMIT ?)`,
		Params:    []stmt.Param{s("rule"), s("owner"), i("bound")},
		CallSites: []string{"writer/mg_rule_commands.go"},
		Cohorts:   []stmt.Cohort{{Name: "missing-rule", Bindings: []any{"missing", "missing", int64(mgRuleDetachMaxRecordings + 1)}}},
		Plan: stmt.Plan{
			Hot:     true,
			Indexes: []string{"dvr_recordings_rule"},
			Seeks:   []string{"rule_id=? AND owner_key=? AND state=?"},
		},
	}
	detach := stmt.Definition{
		ID: mgRuleDetach, Kind: stmt.Write, Owner: "writer.mg.rule",
		SQL: `UPDATE dvr_recordings SET manual=1,rule_id='',rule_revision=0,revision=revision+1,updated_ms=? WHERE id IN (SELECT id FROM dvr_recordings INDEXED BY dvr_recordings_rule WHERE rule_id=? AND owner_key=? AND ` +
			mgRuleDetachStates + ` ORDER BY start_ms,id LIMIT ?)`,
		Params:    []stmt.Param{i("updated"), s("rule"), s("owner"), i("bound")},
		CallSites: []string{"writer/mg_rule_commands.go"},
		Cohorts:   []stmt.Cohort{{Name: "missing-rule", Bindings: []any{int64(1), "missing", "missing", int64(mgRuleDetachMaxRecordings)}}},
		Plan: stmt.Plan{
			Hot:     true,
			Indexes: []string{"sqlite_autoindex_dvr_recordings_1", "dvr_recordings_rule"},
			Seeks:   []string{"id=?", "rule_id=? AND owner_key=? AND state=?"},
			AutoIndexes: []stmt.AutoIndexBinding{{
				Table: "dvr_recordings", Name: "sqlite_autoindex_dvr_recordings_1", Origin: "pk",
				Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}},
			}},
		},
	}
	return []stmt.Definition{d, count, detach}
}
