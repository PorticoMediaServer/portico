package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
)

// Captured raw probe values only; this tuple conveys no publication authority.
type basicFirstOldScalar struct {
	field, kind int64
	integer     sql.NullInt64
	real        sql.NullFloat64
	text        sql.NullString
}

// Validate the entire capture before building SQL arguments or cloning text.
func validateBasicFirstScalarOld(before []basicFirstOldScalar) (charge int64, err error) {
	if len(before) > 6 {
		return 0, ErrInvalid
	}
	var previous int64
	for _, v := range before {
		if v.field < 4 || v.field > 9 || v.field <= previous ||
			(!v.integer.Valid && v.integer.Int64 != 0) ||
			(!v.real.Valid && v.real.Float64 != 0) ||
			math.IsNaN(v.real.Float64) || math.IsInf(v.real.Float64, 0) ||
			(!v.text.Valid && v.text.String != "") {
			return 0, ErrInvalid
		}
		previous = v.field
		switch {
		case v.field <= 6:
			if v.kind != 3 || !v.text.Valid || v.integer.Valid || v.real.Valid {
				return 0, ErrInvalid
			}
		case v.field <= 8:
			if v.kind != 1 || !v.integer.Valid || v.real.Valid || v.text.Valid {
				return 0, ErrInvalid
			}
		default:
			if v.kind != 2 || !v.real.Valid || v.integer.Valid || v.text.Valid {
				return 0, ErrInvalid
			}
		}
		if int64(len(v.text.String)) > 33554432-charge-176 {
			return 0, ErrCap
		}
		charge += 176 + int64(len(v.text.String))
	}
	return charge, nil
}

func cloneBasicFirstScalarOld(before []basicFirstOldScalar) ([]basicFirstOldScalar, error) {
	if _, err := validateBasicFirstScalarOld(before); err != nil {
		return nil, err
	}
	out := append([]basicFirstOldScalar(nil), before...)
	for n := range out {
		out[n].text.String = strings.Clone(out[n].text.String)
	}
	return out, nil
}

// The parent owns rollback on every error, and final run/receipt publication in
// this same transaction. Refund only rows physically deleted, never ACK debt.
func dischargeBasicFirstScalarOld(ctx context.Context, turn *commandTurn, in basicLeafPublication, before []basicFirstOldScalar) (removed int64, err error) {
	defer func() { removed, err = finishCommand(ctx, commandLifetime(turn), removed, err) }()
	if _, err = readBasicLeafPublicationProof(ctx, turn, in); err != nil {
		return 0, err
	}
	charge, err := validateBasicFirstScalarOld(before)
	if err != nil {
		return 0, err
	}
	s := in.activation.source
	rows, err := basicRows(ctx, turn, basicAcceptOwners, s.assetID, int64(2))
	if err != nil {
		return 0, err
	}
	if err = basicMediaExactRow(rows, []any{in.entityID, s.libraryID, nil}); err != nil {
		return 0, err
	}
	rows, err = basicRows(ctx, turn, basicSettleForward, in.entityID)
	if err != nil {
		return 0, err
	}
	if err = basicMediaExactRow(rows, []any{in.entityID, int64(0)}); err != nil {
		return 0, err
	}
	rows, err = basicRows(ctx, turn, basicSettleReverse, in.entityID)
	if err != nil {
		return 0, err
	}
	if err = basicMediaExactRow(rows, []any{s.libraryID, in.entityID, in.entityID, int64(0)}); err != nil {
		return 0, err
	}
	row, err := basicStageRead(ctx, turn, basicSettleCollection, in.entityID)
	if err != nil {
		return 0, err
	}
	var unexpected int64
	err = row.Scan(&unexpected)
	if err == nil {
		return 0, ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}

	// Keys only: never read unknown OLD TEXT, including unowned fields 1..3.
	rows, err = basicRows(ctx, turn, basicFirstScalarOldKeys, s.changeID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var field, kind int64
		if err = rows.Scan(&field, &kind); err != nil {
			return 0, err
		}
		if n >= len(before) || before[n].field != field || before[n].kind != kind {
			return 0, ErrConflict
		}
		n++
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	if n != len(before) {
		return 0, ErrConflict
	}
	for _, v := range before {
		id := basicFirstScalarOldText
		var value any = v.text.String
		var integer, real, text any
		switch v.kind {
		case 1:
			id = basicFirstScalarOldInteger
			value = v.integer.Int64
			integer = value
		case 2:
			id = basicFirstScalarOldReal
			value = v.real.Float64
			real = value
		case 3:
			text = value
		}
		rows, err = basicRows(ctx, turn, id, s.changeID, v.field, v.kind, value)
		if err != nil {
			return 0, err
		}
		if err = basicMediaExactRow(rows, []any{s.changeID, v.field, v.kind, integer, real, text}); err != nil {
			return 0, err
		}
	}
	row, err = basicStageRead(ctx, turn, basicStageBudget, int64(1))
	if err != nil {
		return 0, err
	}
	var used, cap int64
	if err = row.Scan(&used, &cap); err != nil {
		return 0, basicStageError(err)
	}
	if cap != 33554432 || used < charge || used > cap {
		return 0, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicStageBudgetCAS, used-charge, int64(1), used, cap)
	if err != nil {
		return 0, err
	}
	var actual int64
	if err = row.Scan(&actual); err != nil {
		return 0, basicStageError(err)
	}
	if actual != used-charge {
		return 0, ErrConflict
	}
	return int64(len(before)), nil
}
