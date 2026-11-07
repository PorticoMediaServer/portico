package writer

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

const (
	mgRuleWorkerRead    stmt.ID = "writer.mg.rule-worker.read"
	mgRuleWorkerCAS     stmt.ID = "writer.mg.rule-worker.cas"
	mgRuleWorkerColumns         = `rule_id,rule_revision,generation,phase,cursor,updated_ms,diagnostic,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms,job_revision`
)

func mgRuleWorkRow(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	if t == nil || t.transaction == nil {
		return commandRow{}, ErrInvalid
	}
	switch id {
	case mgRuleWorkerRead, mgRuleRead, mgRuleSource:
	default:
		return commandRow{}, ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return commandRow{}, ErrInvalid
	}
	return t.queryRow(ctx, step.statementStep, args...)
}
func mgRuleWorkDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	columns := strings.Split(mgRuleWorkerColumns, ",")
	params := func(prefix string) []stmt.Param {
		p := make([]stmt.Param, len(columns))
		for j, c := range columns {
			p[j] = stmt.Param{Name: prefix + c, Type: stmt.Integer, Nullable: j == 11}
			if j == 0 || j == 2 || j == 3 || j == 4 || j == 6 {
				p[j].Type = stmt.Text
			}
		}
		return p
	}
	sets := make([]string, len(columns))
	where := make([]string, len(columns))
	for j, c := range columns {
		sets[j] = c + "=?"
		where[j] = c + "=?"
	}
	defs := []stmt.Definition{
		{ID: mgRuleWorkerRead, Kind: stmt.Read, SQL: `SELECT ` + mgRuleWorkerColumns + ` FROM dvr_rule_work INDEXED BY sqlite_autoindex_dvr_rule_work_1 WHERE rule_id=?`, Params: []stmt.Param{text("id")}},
		{ID: mgRuleWorkerCAS, Kind: stmt.Write, SQL: `UPDATE dvr_rule_work INDEXED BY sqlite_autoindex_dvr_rule_work_1 SET ` + strings.Join(sets, ",") + ` WHERE ` + strings.Join(where, " AND "), Params: append(params("new_"), params("old_")...)},
	}
	for j := range defs {
		d := &defs[j]
		d.Owner = "writer.mg.rule-worker"
		d.CallSites = []string{"writer/mg_rule_work_effects.go"}
		d.Plan.Indexes = []string{"sqlite_autoindex_dvr_rule_work_1"}
		d.Plan.AutoIndexes = []stmt.AutoIndexBinding{{Table: "dvr_rule_work", Name: "sqlite_autoindex_dvr_rule_work_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "rule_id", Collation: "BINARY"}}}}
		args := make([]any, len(d.Params))
		for k, p := range d.Params {
			if p.Type == stmt.Text {
				args[k] = "missing"
			} else {
				args[k] = int64(1)
			}
		}

		d.Cohorts = []stmt.Cohort{{Name: "missing-point", Bindings: args}}
	}
	for _, d := range mgRuleDefinitions() {
		if d.ID == mgRuleRead || d.ID == mgRuleSource {
			defs = append(defs, d)
		}
	}
	return defs
}
