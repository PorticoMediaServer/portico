package stmt

import (
	"context"
	"database/sql"
	"testing"
)

type deniedExecutor struct{ calls int }

func (e *deniedExecutor) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	e.calls++
	return nil, nil
}
func (e *deniedExecutor) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	e.calls++
	return nil, nil
}
func (e *deniedExecutor) QueryRowContext(context.Context, string, ...any) *sql.Row {
	e.calls++
	return nil
}

// Exercise non-nil unqualified entries at every layer, independently of New's
// fail-closed result and the zero-value checks in the external native tests.
func TestUnqualifiedEntryCannotLookupBindOrExecute(t *testing.T) {
	for _, hot := range []bool{false, true} {
		e := &Entry{Definition: Definition{ID: "unqualified", SQL: "SELECT 1", Kind: Write, Plan: Plan{Hot: hot}}}
		r := &Registry{entries: map[ID]*Entry{e.ID: e}}
		if _, err := r.Lookup(e.ID); err == nil {
			t.Fatal("unqualified entry looked up")
		}
		s := Statement{entry: e}
		if _, err := s.Definition(); err == nil {
			t.Fatal("unqualified entry exposed as executable statement")
		}
		if _, err := s.Bind(); err == nil {
			t.Fatal("unqualified entry bound")
		}
		b := Bound{entry: e}
		executor := &deniedExecutor{}
		if _, err := b.ExecContext(t.Context(), executor); err == nil {
			t.Fatal("unqualified ExecContext entered executor")
		}
		if _, err := b.QueryContext(t.Context(), executor); err == nil {
			t.Fatal("unqualified QueryContext entered executor")
		}
		var value int64
		if err := b.QueryRowContext(t.Context(), executor).Scan(&value); err == nil {
			t.Fatal("unqualified QueryRowContext entered executor")
		}
		if executor.calls != 0 {
			t.Fatal("unqualified entry reached native custody", executor.calls)
		}
	}
}
