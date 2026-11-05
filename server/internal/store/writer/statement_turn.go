package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// statementStep belongs to a compiled domain recipe, never a caller registry.
// Work includes examined/rejected rows and all native/index/FK/receipt effects.
type statementStep struct {
	id   stmt.ID
	work Work
}

// commandTurn shares the existing private transaction's custody and quantum.
// It cannot open, admit, commit, ACK or retire a command. The sole dispatcher
// composes its finite statement definitions/steps; there is no public builder.
type commandTurn struct {
	transaction *transaction
	registry    *stmt.Registry
	steps       map[stmt.ID]statementStep
	rows        []*sql.Rows
}

func newCommandTurn(tx *transaction, registry *stmt.Registry, steps []statementStep) (*commandTurn, error) {
	if tx == nil || tx.done || !tx.begun || registry == nil || len(steps) == 0 {
		return nil, ErrInvalid
	}
	t := &commandTurn{transaction: tx, registry: registry, steps: make(map[stmt.ID]statementStep, len(steps))}
	for _, step := range steps {
		if _, duplicate := t.steps[step.id]; duplicate || step.work.Rows == 0 || step.work.VMOps == 0 {
			return nil, invalid("statement_step")
		}
		statement, err := registry.Lookup(step.id)
		if err != nil {
			return nil, err
		}
		definition, err := statement.Definition()
		if err != nil {
			return nil, err
		}
		if definition.Kind == stmt.DDL {
			return nil, invalid("command_ddl")
		}
		t.steps[step.id] = step
	}
	return t, nil
}

func (t *commandTurn) bind(ctx context.Context, step statementStep, exec bool, args []any) (stmt.Bound, error) {
	if t == nil || t.transaction == nil || ctx == nil {
		return stmt.Bound{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return stmt.Bound{}, err
	}
	if allowed, ok := t.steps[step.id]; !ok || allowed != step {
		return stmt.Bound{}, invalid("unregistered_command_step")
	}
	statement, err := t.registry.Lookup(step.id)
	if err != nil {
		return stmt.Bound{}, err
	}
	definition, err := statement.Definition()
	if err != nil {
		return stmt.Bound{}, err
	}
	if exec && definition.Kind == stmt.Read {
		return stmt.Bound{}, invalid("read_step_exec")
	}
	// Reserve/check before binding copies or entering SQLite. The forecast
	// includes binding/result bookkeeping, not only the changed row count.
	if err := t.transaction.before(step.work); err != nil {
		return stmt.Bound{}, err
	}
	return statement.Bind(args...)
}

func (t *commandTurn) exec(ctx context.Context, step statementStep, args ...any) (sql.Result, error) {
	bound, err := t.bind(ctx, step, true, args)
	if err != nil {
		return nil, err
	}
	defer t.transaction.guardPanic()
	// Always use the frame-owned deadline/cancellation. A handler cannot
	// extend the admitted hold by passing a longer-lived statement context.
	return bound.ExecContext(t.transaction.ctx, t.transaction.conn)
}

func (t *commandTurn) query(ctx context.Context, step statementStep, args ...any) (*sql.Rows, error) {
	bound, err := t.bind(ctx, step, false, args)
	if err != nil {
		return nil, err
	}
	defer t.transaction.guardPanic()
	rows, err := bound.QueryContext(t.transaction.ctx, t.transaction.conn)
	if err != nil {
		return nil, err
	}
	// Each query costs at least one raw row, bounding this bookkeeping by
	// the already admitted recipe. Rows still close in the handler; the
	// dispatcher also drains these references before terminal execution.
	t.rows = append(t.rows, rows)
	return rows, nil
}

func (t *commandTurn) queryRow(ctx context.Context, step statementStep, args ...any) (stmt.Row, error) {
	bound, err := t.bind(ctx, step, false, args)
	if err != nil {
		return stmt.Row{}, err
	}
	defer t.transaction.guardPanic()
	return bound.QueryRowContext(t.transaction.ctx, t.transaction.conn), nil
}

func (t *commandTurn) closeRows() error {
	var err error
	for _, rows := range t.rows {
		err = errors.Join(err, rows.Close())
	}
	t.rows = nil
	if err != nil {
		return fmt.Errorf("close command rows: %w", err)
	}
	return nil
}
