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

type registeredStep struct {
	statementStep
	kind stmt.Kind
}

// commandTurn shares the existing private transaction's custody and quantum.
// It cannot open, admit, commit, ACK or retire a command. The sole dispatcher
// composes its finite statement definitions/steps; there is no public builder.
type commandTurn struct {
	transaction *transaction
	registry    *stmt.Registry
	steps       map[stmt.ID]registeredStep
	rows        []*commandRows
}

// checkLifetimes closes the gap after the last native read/EOF/close or digest
// work. It proves no principal authority: the dispatcher must independently
// check current disclosure even when this reports cancellation.
func (t *commandTurn) checkLifetimes(ctx context.Context) error {
	if t == nil {
		return ErrInvalid
	}
	return checkCommandLifetimes(ctx, t.transaction)
}

func checkCommandLifetimes(ctx context.Context, tx *transaction) error {
	if ctx == nil || tx == nil || tx.ctx == nil {
		return ErrInvalid
	}
	return errors.Join(ctx.Err(), tx.ctx.Err())
}

func newCommandTurn(tx *transaction, registry *stmt.Registry, steps []statementStep) (*commandTurn, error) {
	if tx == nil || tx.done || !tx.begun || registry == nil || len(steps) == 0 {
		return nil, ErrInvalid
	}
	t := &commandTurn{transaction: tx, registry: registry, steps: make(map[stmt.ID]registeredStep, len(steps))}
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
		t.steps[step.id] = registeredStep{step, definition.Kind}
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
	allowed, ok := t.steps[step.id]
	if !ok || allowed.statementStep != step {
		return stmt.Bound{}, invalid("unregistered_command_step")
	}
	if exec && allowed.kind == stmt.Read {
		return stmt.Bound{}, invalid("read_step_exec")
	}
	statement, err := t.registry.Lookup(step.id)
	if err != nil {
		return stmt.Bound{}, err
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
	result, err := bound.ExecContext(t.transaction.ctx, t.transaction.conn)
	t.panicAfter(err)
	if result == nil {
		return nil, err
	}
	return commandResult{result, t}, err
}

func (t *commandTurn) query(ctx context.Context, step statementStep, args ...any) (*commandRows, error) {
	bound, err := t.bind(ctx, step, false, args)
	if err != nil {
		return nil, err
	}
	defer t.transaction.guardPanic()
	rows, err := bound.QueryContext(t.transaction.ctx, t.transaction.conn)
	t.panicAfter(err)
	if err != nil {
		return nil, err
	}
	// Each query costs at least one raw row, bounding this bookkeeping by
	// the already admitted recipe. Rows still close in the handler; the
	// dispatcher also drains these references before terminal execution.
	owned := &commandRows{rows: rows, turn: t}
	t.rows = append(t.rows, owned)
	return owned, nil
}

func (t *commandTurn) queryRow(ctx context.Context, step statementStep, args ...any) (commandRow, error) {
	rows, err := t.query(ctx, step, args...)
	return commandRow{rows}, err
}

func (t *commandTurn) closeRows() error {
	defer t.transaction.guardPanic()
	err := t.closeRowsRaw()
	t.panicAfter(err)
	if err != nil {
		return fmt.Errorf("close command rows: %w", err)
	}
	return nil
}
