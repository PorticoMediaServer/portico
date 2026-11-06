package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
	"testing"
	"time"
)

// Only a disposable test clone is replaced. The existing reviewed test opener
// supplies the bundled connection; this adds no opener or runtime authority.
func wc008NativeExecutor(t *testing.T) *executor {
	t.Helper()
	w, _, _ := testExecutor(t)
	return w
}

func wc008NativeTurn(t *testing.T, w *executor, id CommandID, defs []stmt.Definition) *commandTurn {
	t.Helper()
	// Synthetic test forecasts and custody, never production admission or a
	// permission/latency receipt. Execute the real domain SQL on one W turn.
	r := turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, time.Millisecond}}
	tx, err := w.begin(t.Context(), id, r, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	for i := range defs {
		if len(defs[i].Cohorts) == 0 {
			var args []any
			for _, p := range defs[i].Params {
				switch p.Type {
				case stmt.Text:
					args = append(args, "unit")
				case stmt.Blob:
					args = append(args, []byte{1})
				default:
					args = append(args, int64(1))
				}
			}
			defs[i].Cohorts = []stmt.Cohort{{Name: "unqualified-native-control", Bindings: args}}
		}
	}
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{16, 2000, 16, 65536, 100 * time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}

func wc008NativeExec(t *testing.T, w *executor, q string, args ...any) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), q, args...); err != nil {
		t.Fatal(q, err)
	}
}

func wc008NativePrepare(t *testing.T, w *executor, defs []stmt.Definition) {
	t.Helper()
	for _, d := range defs {
		t.Run(string(d.ID), func(t *testing.T) {
			args := make([]any, len(d.Params))
			rows, err := w.db.QueryContext(t.Context(), "EXPLAIN "+d.SQL, args...)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
