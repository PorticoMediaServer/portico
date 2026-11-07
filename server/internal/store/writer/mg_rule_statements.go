package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	mgRuleRead     stmt.ID = "writer.mg.rule.read"
	mgRuleInsert   stmt.ID = "writer.mg.rule.insert"
	mgRuleUpdate   stmt.ID = "writer.mg.rule.update"
	mgRuleSource   stmt.ID = "writer.mg.rule.source"
	mgRuleWork     stmt.ID = "writer.mg.rule.work"
	mgRuleWorkRead stmt.ID = "writer.mg.rule.work-read"
)
const mgRuleWorkUpdate stmt.ID = "writer.mg.rule.work-update"

const mgRuleColumns = `id,owner_key,account_id,profile_id,source_id,series_id,match_title,match_channel,config_json,revision,enabled,deleted,updated_ms`

func mgRuleDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	values := []stmt.Param{s("id"), s("owner"), s("account"), s("profile"), s("source"), s("series"), s("title"), s("channel"), s("config"), i("revision"), i("enabled"), i("deleted"), i("updated")}
	defs := []stmt.Definition{
		{ID: mgRuleRead, Kind: stmt.Read, SQL: `SELECT ` + mgRuleColumns + ` FROM dvr_rules INDEXED BY sqlite_autoindex_dvr_rules_1 WHERE id=? AND authority='local'`, Params: []stmt.Param{s("id")}},
		{ID: mgRuleInsert, Kind: stmt.Write, SQL: `INSERT INTO dvr_rules(` + mgRuleColumns + `,authority) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, Params: append(append([]stmt.Param{}, values...), s("authority"))},
		{ID: mgRuleUpdate, Kind: stmt.Write, SQL: `UPDATE dvr_rules INDEXED BY sqlite_autoindex_dvr_rules_1 SET config_json=?,revision=?,enabled=?,deleted=?,updated_ms=? WHERE id=? AND owner_key=? AND authority='local' AND account_id=? AND profile_id=? AND source_id=? AND series_id=? AND match_title=? AND match_channel=? AND config_json=? AND revision=? AND enabled=? AND deleted=? AND updated_ms=?`, Params: append([]stmt.Param{s("new_config"), i("new_revision"), i("new_enabled"), i("new_deleted"), i("new_updated")}, values...)},
		{ID: mgRuleSource, Kind: stmt.Read, SQL: `SELECT s.revision,s.active_generation,s.state,i.has_live_source_removals FROM live_sources AS s INDEXED BY sqlite_autoindex_live_sources_1 CROSS JOIN live_source_identities AS i INDEXED BY sqlite_autoindex_live_source_identities_1 ON i.id=s.id CROSS JOIN live_generations AS g INDEXED BY sqlite_autoindex_live_generations_1 ON g.id=s.active_generation AND g.source_id=s.id WHERE s.id=?`, Params: []stmt.Param{s("source")}},
		// Revoke stale reconciliation by exact rule revision and job revision, while
		// preserving active lease owner/generation/expiry/attempts. Actual workers MUST
		// fence both revisions and native rule enabled/deleted/source head on commit.
		// Completion/reclaim/lease ownership remain the parent seam, never reset here.
		{ID: mgRuleWork, Kind: stmt.Write, SQL: `INSERT INTO dvr_rule_work(rule_id,rule_revision,generation,phase,cursor,updated_ms,diagnostic,job_next_attempt_ms) VALUES(?,?,?,?,?,?,?,?)`, Params: []stmt.Param{s("id"), i("revision"), s("generation"), s("phase"), s("cursor"), i("updated"), s("diagnostic"), i("due")}},
		{ID: mgRuleWorkUpdate, Kind: stmt.Write, SQL: `UPDATE dvr_rule_work INDEXED BY sqlite_autoindex_dvr_rule_work_1 SET rule_revision=?,generation=?,phase='existing',cursor='',updated_ms=?,diagnostic='',job_next_attempt_ms=?,job_revision=job_revision+1 WHERE rule_id=? AND rule_revision=? AND job_revision=? AND job_revision<9223372036854775807`, Params: []stmt.Param{i("revision"), s("generation"), i("updated"), i("due"), s("id"), i("expected_revision"), i("expected_job_revision")}},
		{ID: mgRuleWorkRead, Kind: stmt.Read, SQL: `SELECT rule_revision,generation,phase,cursor,updated_ms,diagnostic,job_revision FROM dvr_rule_work INDEXED BY sqlite_autoindex_dvr_rule_work_1 WHERE rule_id=?`, Params: []stmt.Param{s("id")}},
	}
	// Native current-lifetime observation is not an authorization grant.
	defs = append(defs, mgOwnerDefinition())
	for n := range defs {
		d := &defs[n]
		if d.ID == mgRecordingIntentOwner {
			continue
		}
		tables := []string{}
		switch d.ID {
		case mgRuleRead, mgRuleUpdate:
			tables = []string{"dvr_rules"}
		case mgRuleWorkRead, mgRuleWorkUpdate:
			tables = []string{"dvr_rule_work"}
		case mgRuleSource:
			tables = []string{"live_sources", "live_source_identities", "live_generations"}
		}
		for _, table := range tables {
			column := "id"
			if table == "dvr_rule_work" {
				column = "rule_id"
			}
			name := "sqlite_autoindex_" + table + "_1"
			d.Plan.Indexes = append(d.Plan.Indexes, name)
			d.Plan.AutoIndexes = append(d.Plan.AutoIndexes, stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk", Columns: []stmt.IndexColumn{{Name: column, Collation: "BINARY"}}})
		}
		d.Owner = "writer.mg.rule"
		d.CallSites = []string{"writer/mg_rule_effects.go"}
		args := make([]any, len(d.Params))
		for j, p := range d.Params {
			if p.Type == stmt.Text {
				args[j] = "missing"
			} else {
				args[j] = int64(1)
			}
		}
		d.Cohorts = []stmt.Cohort{{Name: "missing-point", Bindings: args}}
	}
	return defs
}
