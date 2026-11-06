package plan_test

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestPlanCheckCannotQualifyRegistryFromLexicalAndEQP(t *testing.T) {
	conn, _, f, _ := fixture(t)
	ctx := context.Background()
	for _, sql := range []string{
		"CREATE TABLE rp(id INTEGER PRIMARY KEY)", "INSERT INTO rp VALUES(1)",
		"CREATE TABLE rc(id INTEGER PRIMARY KEY,parent_id INTEGER NOT NULL,flag INTEGER NOT NULL)",
		"CREATE INDEX rc_parent ON rc(parent_id)", "INSERT INTO rc VALUES(1,1,0)",
		"CREATE VIEW rv AS SELECT id,parent_id,flag FROM rc WHERE (flag+0)=0",
		"CREATE TEMP TABLE rt(id INTEGER PRIMARY KEY)", "INSERT INTO rt VALUES(1)",
	} {
		if _, err := conn.ExecContext(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []struct{ table, column string }{{"rv", "parent_id"}, {"rt", "id"}, {"rc", "parent_id"}} {
		for _, hot := range []bool{false, true} {
			sql := "SELECT p.id FROM rp AS p INNER JOIN " + source.table + " AS b ON b." + source.column + "=p.id WHERE p.id=?"
			d := stmt.Definition{ID: "admission.read", SQL: sql, Kind: stmt.Read, Owner: "plan-test", CallSites: []string{"admission_test.go"},
				Params: []stmt.Param{{Name: "id", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "one", Bindings: []any{int64(1)}}}, Plan: stmt.Plan{Hot: hot, PrimaryKeys: []string{"p"}}}
			// Registration freezes supplied declarations. Neither a Hot bit nor
			// a successful Lookup/Bind is a source/plan certificate.
			r, err := stmt.New([]stmt.Definition{d})
			if err != nil {
				t.Fatal(err)
			}
			s, err := r.Lookup(d.ID)
			if err != nil {
				t.Fatal(err)
			}
			registered, err := s.Definition()
			if err != nil || registered.Plan.Hot != hot {
				t.Fatal("registration changed caller metadata", err)
			}
			bound, err := s.Bind(int64(1))
			if err != nil {
				t.Fatal(err)
			}
			if issues := plan.LintSQL(sql); len(issues) != 0 {
				t.Fatal("counterexample no longer exercises lexical admission", issues)
			}
			eqp := reviewRawEQP(t, conn, sql, int64(1))
			if err := plan.CheckEQP(d, eqp); err != nil {
				t.Fatal("counterexample no longer exercises all-SEARCH admission", err)
			}
			err = plan.Check(registered, eqp)
			if err == nil {
				// Reverting the qualification gate re-opens the exact registered
				// Lookup→Bind→QueryContext path reported in review.
				rows, queryErr := bound.QueryContext(ctx, conn)
				if rows != nil {
					_ = rows.Close()
				}
				t.Fatalf("connection-free qualification reached registered execution: %s: %v", source.table, queryErr)
			}
			if !errors.Is(err, plan.ErrCaptureRequired) {
				t.Fatal("wrong qualification refusal", err)
			}
			_, err = plan.Capture(ctx, conn, r, f, indexPolicy(t))
			if source.table == "rc" && err != nil {
				t.Fatal("ordinary MAIN sources must qualify through Capture", err)
			}
			if source.table != "rc" && err == nil {
				t.Fatal("Capture certified the VIEW/TEMP registry", source.table)
			}
		}
	}
}

func TestPlanConnectionFreeCheckNeverCertifiesAnySQL(t *testing.T) {
	for _, sql := range []string{"SELECT 1", "SELECT id FROM items WHERE id=?", "UPDATE items SET key=? WHERE id=?", "DELETE FROM items WHERE id=?", "INSERT INTO items(id) VALUES(?)"} {
		for _, hot := range []bool{false, true} {
			if err := plan.Check(stmt.Definition{ID: "admission.any", SQL: sql, Plan: stmt.Plan{Hot: hot}}, plan.Rows{}); !errors.Is(err, plan.ErrCaptureRequired) {
				t.Fatalf("connection-free SQL certified: %s hot=%v: %v", sql, hot, err)
			}
		}
	}
}
