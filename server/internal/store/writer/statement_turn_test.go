package writer

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func turnStatements(t *testing.T) (*stmt.Registry, []statementStep) {
	t.Helper()
	// Synthetic frame controls on the adopted table, not accepting commands
	// or Applied receipts. Production definitions belong to closed domains.
	definitions := []stmt.Definition{
		{ID: "writer.test.read_revision", SQL: "SELECT revision FROM search_revision WHERE singleton=?", Kind: stmt.Read, Params: []stmt.Param{{Name: "singleton", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "one", Bindings: []any{int64(1)}}}},
		{ID: "writer.test.update_revision", SQL: "UPDATE search_revision SET revision=? WHERE singleton=?", Kind: stmt.Write, Params: []stmt.Param{{Name: "revision", Type: stmt.Integer}, {Name: "singleton", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "one", Bindings: []any{int64(2), int64(1)}}}},
		{ID: "writer.test.return_revision", SQL: "UPDATE search_revision SET revision=revision+1 WHERE singleton=? RETURNING revision", Kind: stmt.Write, Params: []stmt.Param{{Name: "singleton", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "one", Bindings: []any{int64(1)}}}},
	}
	var steps []statementStep
	for i := range definitions {
		definitions[i].Owner, definitions[i].CallSites = "writer-test", []string{"statement_turn_test.go"}
		steps = append(steps, statementStep{definitions[i].ID, Work{1, 100, 1, 4096, time.Millisecond}})
	}
	registry, err := stmt.New(definitions)
	if err != nil {
		t.Fatal(err)
	}
	return registry, steps
}

func TestStatementTurnNativeBindingsAndReturning(t *testing.T) {
	w, path, _ := testExecutor(t)
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	registry, steps := turnStatements(t)
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	result, err := turn.exec(t.Context(), steps[1], int64(2), int64(1))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("effect witness: %d %v", n, err)
	}
	row, err := turn.queryRow(t.Context(), steps[2], int64(1))
	if err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err := row.Scan(&revision); err != nil || revision != 3 {
		t.Fatalf("RETURNING: %d %v", revision, err)
	}
	rows, err := turn.query(t.Context(), steps[0], int64(1))
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() || rows.Scan(&revision) != nil || revision != 3 {
		t.Fatalf("bound read: %d %v", revision, rows.Err())
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("tracked Rows survived command drain")
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.commit(); err != nil {
		t.Fatal(err)
	}
	if got := readRevision(t, path); got != 3 {
		t.Fatalf("did not commit synthetic effects: %d", got)
	}
	if _, err := turn.queryRow(t.Context(), steps[0], int64(1)); !errors.Is(err, ErrClosed) {
		t.Fatalf("step reused retired frame: %v", err)
	}
}

func TestStatementTurnRefusalsLeaveEffectsRolledBack(t *testing.T) {
	for _, failure := range []string{"unknown step", "changed budget", "binding", "read exec", "cancel", "grant", "quantum"} {
		t.Run(failure, func(t *testing.T) {
			w, path, _ := testExecutor(t)
			custody := &testCustody{}
			tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), custody)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.rollback(nil)
			registry, steps := turnStatements(t)
			turn, err := newCommandTurn(tx, registry, steps)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := turn.exec(t.Context(), steps[1], int64(2), int64(1)); err != nil {
				t.Fatal(err)
			}
			step, ctx, arg := steps[2], t.Context(), any(int64(1))
			want := ErrInvalid
			switch failure {
			case "unknown step":
				step.id = "writer.test.unregistered"
			case "changed budget":
				step.work.WALBytes--
			case "binding":
				arg, want = "1", nil // native INTEGER accepts only int64
			case "read exec":
				step = steps[0]
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "grant":
				custody.checkErr = errors.New("grant revoked")
				want = custody.checkErr
			case "quantum":
				tx.quantum.remaining.WALBytes = tx.quantum.terminal.WALBytes
				want = ErrQuantum
			}
			_, err = turn.exec(ctx, step, arg)
			if err == nil || want != nil && !errors.Is(err, want) {
				t.Fatalf("refusal %s: %v", failure, err)
			}
			custody.checkErr = nil
			if _, err := tx.rollback(err); err == nil {
				t.Fatal("lost rollback cause")
			}
			if got := readRevision(t, path); got != 1 {
				t.Fatalf("committed a prefix on %s: %d", failure, got)
			}
		})
	}
}

func TestStatementTurnQueryRowNoRowsAndBindingErrors(t *testing.T) {
	w, _, _ := testExecutor(t)
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	registry, steps := turnStatements(t)
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.queryRow(t.Context(), steps[0], "1"); err == nil {
		t.Fatal("queryRow did not return pre-SQL binding error")
	}
	row, err := turn.queryRow(t.Context(), steps[0], int64(2))
	if err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err := row.Scan(&revision); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
}

func TestStatementTurnRejectsMissingDuplicateAndDDLSteps(t *testing.T) {
	w, _, _ := testExecutor(t)
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	registry, steps := turnStatements(t)
	for _, invalid := range [][]statementStep{nil, {steps[0], steps[0]}, {{id: "missing", work: steps[0].work}}, {{id: steps[0].id}}} {
		if _, err := newCommandTurn(tx, registry, invalid); err == nil {
			t.Fatal("accepted missing/duplicate/unqualified step", invalid)
		}
	}
	ddl, err := stmt.New([]stmt.Definition{{ID: "writer.test.ddl", SQL: "CREATE TABLE forbidden(id INTEGER)", Kind: stmt.DDL, Owner: "writer-test", CallSites: []string{"statement_turn_test.go"}, Cohorts: []stmt.Cohort{{Name: "empty"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newCommandTurn(tx, ddl, []statementStep{{id: "writer.test.ddl", work: steps[0].work}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("DDL registered in runtime command turn: %v", err)
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	// Mutating the source recipe slice must not reduce its fixed charge.
	steps[0].work.Rows = 0
	if _, err := turn.query(t.Context(), steps[0], int64(1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("caller changed captured recipe charge: %v", err)
	}
}

func TestStatementTurnCannotExtendFrameCancellation(t *testing.T) {
	w, _, _ := testExecutor(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	custody := &testCustody{}
	tx, err := w.begin(ctx, InventoryAccept, testRecipe(), custody)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	definition := stmt.Definition{ID: "writer.test.cancel", SQL: `WITH RECURSIVE seq(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM seq) SELECT sum(x) FROM seq`, Kind: stmt.Read, Owner: "writer-test", CallSites: []string{"statement_turn_test.go"}, Cohorts: []stmt.Cohort{{Name: "cancel"}}}
	registry, err := stmt.New([]stmt.Definition{definition})
	if err != nil {
		t.Fatal(err)
	}
	step := statementStep{definition.ID, Work{1, 100, 1, 0, time.Millisecond}}
	turn, err := newCommandTurn(tx, registry, []statementStep{step})
	if err != nil {
		t.Fatal(err)
	}
	// A longer-lived handler argument cannot replace the command context.
	row, err := turn.queryRow(context.Background(), step)
	if err == nil {
		var sum int64
		err = row.Scan(&sum)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("native statement escaped frame cancellation: %v", err)
	}
	if _, err := tx.rollback(err); !errors.Is(err, context.DeadlineExceeded) || custody.returns != 1 {
		t.Fatalf("cancelled turn did not retire once: %v %+v", err, custody)
	}
}
