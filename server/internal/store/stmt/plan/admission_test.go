package plan_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

type admissionExecutor struct {
	conn                 *sql.Conn
	queries, execs, rows int
}

func (e *admissionExecutor) QueryContext(ctx context.Context, sql string, args ...any) (*sql.Rows, error) {
	e.queries++
	return e.conn.QueryContext(ctx, sql, args...)
}
func (e *admissionExecutor) ExecContext(ctx context.Context, sql string, args ...any) (sql.Result, error) {
	e.execs++
	return e.conn.ExecContext(ctx, sql, args...)
}
func (e *admissionExecutor) QueryRowContext(ctx context.Context, sql string, args ...any) *sql.Row {
	e.rows++
	return e.conn.QueryRowContext(ctx, sql, args...)
}

// Every execute method is attempted unconditionally. There is no Check/error
// branch which can skip the reported New→Lookup→Bind→execute vulnerability.
func refuseAdmissionExecution(t *testing.T, r *stmt.Registry, id stmt.ID, e *admissionExecutor) {
	t.Helper()
	s, err := r.Lookup(id)
	if err == nil {
		t.Fatal("unqualified Lookup succeeded")
	}
	b, err := s.Bind(int64(1))
	if err == nil {
		t.Fatal("unqualified Bind succeeded")
	}
	if rows, err := b.QueryContext(t.Context(), e); err == nil || rows != nil {
		t.Fatal("unqualified QueryContext succeeded", err)
	}
	if result, err := b.ExecContext(t.Context(), e); err == nil || result != nil {
		t.Fatal("unqualified ExecContext succeeded", err)
	}
	var idValue int64
	if err := b.QueryRowContext(t.Context(), e).Scan(&idValue); err == nil {
		t.Fatal("unqualified QueryRowContext succeeded")
	}
	if e.queries != 0 || e.execs != 0 || e.rows != 0 {
		t.Fatalf("unqualified registry reached executor: %+v", e)
	}
}

func TestPlanCaptureReceiptRequiredForRegistrationAndExecution(t *testing.T) {
	conn, _, f, _ := fixture(t)
	for _, sql := range []string{
		"CREATE TABLE rp(id INTEGER PRIMARY KEY)", "INSERT INTO rp VALUES(1)",
		"CREATE TABLE rc(id INTEGER PRIMARY KEY,parent_id INTEGER NOT NULL,flag INTEGER NOT NULL)",
		"CREATE INDEX rc_parent ON rc(parent_id)", "INSERT INTO rc VALUES(1,1,0)",
		"CREATE VIEW rv AS SELECT id,parent_id,flag FROM rc WHERE (flag+0)=0",
		"CREATE TEMP TABLE rt(id INTEGER PRIMARY KEY)", "INSERT INTO rt VALUES(1)",
	} {
		if _, err := conn.ExecContext(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []struct{ table, column string }{{"rv", "parent_id"}, {"rt", "id"}, {"rc", "parent_id"}} {
		for _, hot := range []bool{false, true} {
			d := stmt.Definition{ID: "admission.read", SQL: "SELECT p.id FROM rp AS p INNER JOIN " + source.table + " AS b ON b." + source.column + "=p.id WHERE p.id=?", Kind: stmt.Read, Owner: "plan-test", CallSites: []string{"admission_test.go"}, Params: []stmt.Param{{Name: "id", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "one", Bindings: []any{int64(1)}}}, Plan: stmt.Plan{Hot: hot, PrimaryKeys: []string{"p"}}}
			definitions := []stmt.Definition{d}
			catalogue, err := stmt.Describe(definitions)
			if err != nil {
				t.Fatal(err)
			}
			if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
				t.Fatal("counterexample no longer exercises lexical admission", issues)
			}
			if err := plan.CheckEQP(d, reviewRawEQP(t, conn, d.SQL, int64(1))); err != nil {
				t.Fatal("counterexample no longer exercises all-SEARCH admission", err)
			}
			// The refused native SQL really could read a row without the registry gate.
			var native int64
			if err := conn.QueryRowContext(t.Context(), d.SQL, int64(1)).Scan(&native); err != nil || native != 1 {
				t.Fatal("native counterexample is not executable", native, err)
			}
			if r, err := stmt.New(definitions); !errors.Is(err, stmt.ErrUnqualified) || r != nil {
				t.Fatal("caller Hot authorized New", hot, err)
			}
			refuseAdmissionExecution(t, catalogue, d.ID, &admissionExecutor{conn: conn})
			golden, captureErr := plan.Capture(t.Context(), conn, catalogue, f, indexPolicy(t))
			r, registerErr := stmt.New(definitions, golden.Receipt())
			if source.table != "rc" {
				if captureErr == nil || golden.Receipt() != nil {
					t.Fatal("Capture minted VIEW/TEMP receipt", source.table, captureErr)
				}
				if !errors.Is(registerErr, stmt.ErrUnqualified) || r != nil {
					t.Fatal("failed Capture authorized New", registerErr)
				}
				refuseAdmissionExecution(t, r, d.ID, &admissionExecutor{conn: conn})
				continue
			}
			if captureErr != nil || registerErr != nil {
				t.Fatal("ordinary MAIN capture/registration refused", captureErr, registerErr)
			}
			s, err := r.Lookup(d.ID)
			if err != nil {
				t.Fatal(err)
			}
			b, err := s.Bind(int64(1))
			if err != nil {
				t.Fatal(err)
			}
			executor := &admissionExecutor{conn: conn}
			rows, err := b.QueryContext(t.Context(), executor)
			if err != nil {
				t.Fatal(err)
			}
			if !rows.Next() {
				t.Fatal("qualified query did not execute")
			}
			var got int64
			if err := rows.Scan(&got); err != nil || got != 1 {
				t.Fatal(got, err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if err := b.QueryRowContext(t.Context(), executor).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if executor.queries != 1 || executor.rows != 1 {
				t.Fatal("positive execution branch was not reached", executor)
			}
		}
	}
}

type forgedAdmissionReceipt struct{ stmt.Receipt }

func TestPlanCaptureReceiptsBindExactDefinitionAndCannotBeDecoded(t *testing.T) {
	conn, r, f, _ := fixture(t)
	definitions := []stmt.Definition{}
	for _, e := range r.Manifest() {
		definitions = append(definitions, e.Definition)
	}
	g, err := plan.Capture(t.Context(), conn, r, f, indexPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*stmt.Definition){
		func(d *stmt.Definition) { d.SQL += " " },
		func(d *stmt.Definition) {
			d.SQL = "SELECT id,key FROM items WHERE id=?"
			d.Params = []stmt.Param{{Name: "id", Type: stmt.Integer}}
			d.Cohorts = []stmt.Cohort{{Name: "one", Bindings: []any{int64(1)}}}
		},
		func(d *stmt.Definition) { d.ID = "another.id" },
		func(d *stmt.Definition) { d.Plan.Hot = !d.Plan.Hot },
		func(d *stmt.Definition) { d.Plan.Seeks = append(d.Plan.Seeks, "id=?") },
		func(d *stmt.Definition) { d.Owner = "another owner" },
		func(d *stmt.Definition) { d.CallSites = []string{"another.go"} },
		func(d *stmt.Definition) { d.Params[0].Name = "another_parameter" },
		func(d *stmt.Definition) { d.Cohorts[0].Name = "another_population" },
	} {
		// Manifest is a deep copy; a mutation cannot alter either registry or receipt.
		d := r.Manifest()[0].Definition
		mutate(&d)
		if candidate, err := stmt.New([]stmt.Definition{d}, g.Receipt()); !errors.Is(err, stmt.ErrUnqualified) || candidate != nil {
			t.Fatal("receipt reused for another definition", d.ID, err)
		}
	}
	var decoded plan.Golden
	data, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	zeroConcrete := reflect.New(reflect.TypeOf(g.Receipt()).Elem()).Interface().(stmt.Receipt)
	for _, proof := range []stmt.Receipt{zeroConcrete, nil, decoded.Receipt(), (plan.Golden{}).Receipt(), forgedAdmissionReceipt{}, forgedAdmissionReceipt{g.Receipt()}} {
		if candidate, err := stmt.New(definitions, proof); !errors.Is(err, stmt.ErrUnqualified) || candidate != nil {
			t.Fatal("zero/decoded/embedded receipt authorized registration", err)
		}
	}
	if candidate, err := stmt.New(definitions, g.Receipt()); err != nil || candidate == nil {
		t.Fatal("unaltered receipt rejected", err)
	}
	// A later invalid entry must not leak an earlier qualified entry.
	other := r.Manifest()[0].Definition
	other.ID = "uncaptured"
	if candidate, err := stmt.New(append(definitions, other), g.Receipt()); !errors.Is(err, stmt.ErrUnqualified) || candidate != nil {
		t.Fatal("partial registry escaped", err)
	}
}

func TestPlanConnectionFreeCheckNeverCertifiesAnySQL(t *testing.T) {
	for _, sql := range []string{"SELECT 1", "SELECT id FROM items WHERE id=?", "UPDATE items SET key=? WHERE id=?", "DELETE FROM items WHERE id=?", "INSERT INTO items(id) VALUES(?)"} {
		for _, hot := range []bool{false, true} {
			if err := plan.Check(stmt.Definition{ID: "admission.any", SQL: sql, Plan: stmt.Plan{Hot: hot}}, plan.Rows{}); !errors.Is(err, plan.ErrCaptureRequired) {
				t.Fatal(sql, hot, err)
			}
		}
	}
}

func TestPlanCaptureReceiptExecAndReturning(t *testing.T) {
	conn, _, f, _ := fixture(t)
	definitions := []stmt.Definition{
		{ID: "admission.write", SQL: "UPDATE items SET key=? WHERE id=?", Kind: stmt.Write, Owner: "plan-test", CallSites: []string{"admission_test.go"}, Params: []stmt.Param{{Name: "key", Type: stmt.Integer}, {Name: "id", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "one", Bindings: []any{int64(77), int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"items"}, Seeks: []string{"rowid=?"}}},
		{ID: "admission.returning", SQL: "UPDATE items SET key=? WHERE id=? RETURNING key", Kind: stmt.Write, Owner: "plan-test", CallSites: []string{"admission_test.go"}, Params: []stmt.Param{{Name: "key", Type: stmt.Integer}, {Name: "id", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "one", Bindings: []any{int64(88), int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"items"}, Seeks: []string{"rowid=?"}}},
	}
	catalogue, err := stmt.Describe(definitions)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := plan.Capture(t.Context(), conn, catalogue, f, indexPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	r, err := stmt.New(definitions, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	executor := &admissionExecutor{conn: conn}
	s, err := r.Lookup(definitions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Bind(int64(77), int64(1))
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.ExecContext(t.Context(), executor)
	if err != nil {
		t.Fatal(err)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	s, err = r.Lookup(definitions[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.Bind(int64(88), int64(1))
	if err != nil {
		t.Fatal(err)
	}
	var key int64
	if err := b.QueryRowContext(t.Context(), executor).Scan(&key); err != nil || key != 88 {
		t.Fatal(key, err)
	}
	if executor.execs != 1 || executor.rows != 1 {
		t.Fatal("write execution branches did not run", executor)
	}
}

func TestPlanCaptureReceiptCannotAuthorizeRefusedViewOrTemp(t *testing.T) {
	conn, _, f, _ := fixture(t)
	for _, sql := range []string{"CREATE VIEW receipt_view AS SELECT id FROM items WHERE (kind+0)=1", "CREATE TEMP TABLE receipt_temp(id INTEGER PRIMARY KEY)", "INSERT INTO receipt_temp VALUES(1)"} {
		if _, err := conn.ExecContext(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	d := stmt.Definition{ID: "admission.stolen", SQL: "SELECT id FROM items WHERE id=?", Owner: "plan-test", CallSites: []string{"admission_test.go"}, Kind: stmt.Read, Params: []stmt.Param{{Name: "id", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "one", Bindings: []any{int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"items"}}}
	catalogue, err := stmt.Describe([]stmt.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	good, err := plan.Capture(t.Context(), conn, catalogue, f, indexPolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"receipt_view", "receipt_temp"} {
		bad := d
		bad.SQL = "SELECT id FROM " + source + " WHERE id=?"
		catalogue, err := stmt.Describe([]stmt.Definition{bad})
		if err != nil {
			t.Fatal(err)
		}
		failed, err := plan.Capture(t.Context(), conn, catalogue, f, indexPolicy(t))
		if err == nil || failed.Receipt() != nil {
			t.Fatal("refused source minted a receipt", source, err)
		}
		for _, proof := range []stmt.Receipt{good.Receipt(), failed.Receipt()} {
			r, err := stmt.New([]stmt.Definition{bad}, proof)
			if !errors.Is(err, stmt.ErrUnqualified) || r != nil {
				t.Fatal("borrowed receipt authorized refused source", source, err)
			}
			refuseAdmissionExecution(t, r, bad.ID, &admissionExecutor{conn: conn})
		}
	}
}
