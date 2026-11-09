package writer

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// MG-12 rule-work custody. One full native row point and one exact 13-column
// CAS on the embedded G+013 dvr_rule_work table. The row is addressed by its
// primary key only; the due index is the governor's scheduler input, never a
// caller-supplied ordering.
const (
	mgReconcileWorkRead stmt.ID = "writer.mg.reconcile.work-read"
	mgReconcileWorkCAS  stmt.ID = "writer.mg.reconcile.work-cas"
)

const mgReconcileWorkColumns = `rule_id,rule_revision,generation,phase,cursor,updated_ms,diagnostic,job_lease_owner,job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms,job_revision`

func mgReconcileWorkDefinitions() []stmt.Definition {
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	columns := []struct {
		name     string
		text     bool
		nullable bool
	}{
		{"rule_id", true, false},
		{"rule_revision", false, false},
		{"generation", true, false},
		{"phase", true, false},
		{"cursor", true, false},
		{"updated_ms", false, false},
		{"diagnostic", true, false},
		{"job_lease_owner", false, false},
		{"job_lease_generation", false, false},
		{"job_lease_expires_ms", false, false},
		{"job_attempts", false, false},
		{"job_next_attempt_ms", false, true},
		{"job_revision", false, false},
	}
	params := func(prefix string) []stmt.Param {
		out := make([]stmt.Param, len(columns))
		for n, c := range columns {
			out[n] = stmt.Param{Name: prefix + c.name, Type: stmt.Integer, Nullable: c.nullable}
			if c.text {
				out[n].Type = stmt.Text
			}
		}
		return out
	}
	sets, where := make([]string, len(columns)), make([]string, len(columns))
	for n, c := range columns {
		sets[n] = c.name + "=?"
		where[n] = c.name + "=?"
	}
	defs := []stmt.Definition{
		{ID: mgReconcileWorkRead, Kind: stmt.Read, SQL: `SELECT ` + mgReconcileWorkColumns + ` FROM dvr_rule_work INDEXED BY sqlite_autoindex_dvr_rule_work_1 WHERE rule_id=?`, Params: []stmt.Param{s("rule")}},
		{ID: mgReconcileWorkCAS, Kind: stmt.Write, SQL: `UPDATE dvr_rule_work INDEXED BY sqlite_autoindex_dvr_rule_work_1 SET ` + strings.Join(sets, ",") + ` WHERE ` + strings.Join(where, " AND "), Params: append(params("next_"), params("old_")...)},
	}
	pin := stmt.AutoIndexBinding{Table: "dvr_rule_work", Name: "sqlite_autoindex_dvr_rule_work_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "rule_id", Collation: "BINARY"}}}
	for n := range defs {
		defs[n].Owner = "writer.mg.reconcile"
		defs[n].CallSites = []string{"writer/mg_rule_work_effects.go"}
		defs[n].Plan.Hot = true
		defs[n].Plan.Indexes = []string{"sqlite_autoindex_dvr_rule_work_1"}
		defs[n].Plan.AutoIndexes = []stmt.AutoIndexBinding{pin}
		defs[n].Plan.Seeks = []string{"rule_id=?"}
	}
	read := []any{"missing"}
	write := make([]any, 0, 26)
	for _, p := range defs[1].Params {
		if p.Nullable {
			write = append(write, nil)
		} else if p.Type == stmt.Text {
			write = append(write, "missing")
		} else {
			write = append(write, int64(1))
		}
	}
	defs[0].Cohorts = []stmt.Cohort{{Name: "missing-rule-work", Bindings: read}}
	defs[1].Cohorts = []stmt.Cohort{{Name: "missing-rule-work", Bindings: write}}
	return defs
}

func mgReconcileWorkStep(t *commandTurn, id stmt.ID) (statementStep, error) {
	if t == nil || t.transaction == nil {
		return statementStep{}, ErrInvalid
	}
	switch id {
	case mgReconcileWorkRead, mgReconcileWorkCAS:
	default:
		return statementStep{}, ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return statementStep{}, ErrInvalid
	}
	return step.statementStep, nil
}

func (f mgReconcileWorkFacts) values() []any {
	var due any
	if f.hasDue {
		due = f.dueMS
	}
	return []any{f.rule, f.ruleRevision, f.generation, f.phase, f.cursor, f.updated, f.diagnostic,
		f.jobOwner, f.leaseGeneration, f.expires, f.attempts, due, f.jobRevision}
}

func mgReconcileWorkScan(row commandRow) (out mgReconcileWorkFacts, err error) {
	var due sql.NullInt64
	if err = row.Scan(&out.rule, &out.ruleRevision, &out.generation, &out.phase, &out.cursor, &out.updated,
		&out.diagnostic, &out.jobOwner, &out.leaseGeneration, &out.expires, &out.attempts, &due, &out.jobRevision); err != nil {
		return mgReconcileWorkFacts{}, err
	}
	if due.Valid {
		out.dueMS, out.hasDue = due.Int64, true
	}
	if !out.valid() {
		return mgReconcileWorkFacts{}, ErrInvalid
	}
	return out, nil
}

func mgReconcileReadWork(ctx context.Context, t *commandTurn, rule string) (out mgReconcileWorkFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !mgRecordingHex(rule, 64) {
		return out, ErrInvalid
	}
	step, err := mgReconcileWorkStep(t, mgReconcileWorkRead)
	if err != nil {
		return out, err
	}
	row, err := t.queryRow(ctx, step, rule)
	if err != nil {
		return out, err
	}
	out, err = mgReconcileWorkScan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return mgReconcileWorkFacts{}, ErrConflict
	}
	if err != nil {
		return mgReconcileWorkFacts{}, err
	}
	return out, nil
}
