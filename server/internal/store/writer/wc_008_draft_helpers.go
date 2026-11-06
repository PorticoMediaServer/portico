package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Private effect glue over W's existing commandTurn. These helpers
// cannot open, register, admit, classify, acknowledge or finish a transaction.
func wc008DraftQuery(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	if turn == nil {
		return commandRow{}, ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return commandRow{}, invalid("wc_008_step")
	}
	return turn.queryRow(ctx, step.statementStep, args...)
}

func wc008DraftInteger(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (int64, error) {
	row, err := wc008DraftQuery(ctx, turn, id, args...)
	if err != nil {
		return 0, err
	}
	var n int64
	if err = row.Scan(&n); errors.Is(err, sql.ErrNoRows) {
		return 0, ErrConflict
	}
	if err != nil {
		return 0, err
	}
	return n, turn.checkLifetimes(ctx)
}

func wc008DraftChanged(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) error {
	if turn == nil {
		return ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return invalid("wc_008_step")
	}
	result, err := turn.exec(ctx, step.statementStep, args...)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrConflict
	}
	return turn.checkLifetimes(ctx)
}
