package writer

import (
	"context"
	"database/sql"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	mgLCDebtRead       stmt.ID = "writer.mg.lc.receipt-debt-read"
	mgLCDebtUpdateNull stmt.ID = "writer.mg.lc.receipt-debt-update-null"
	mgLCDebtUpdateSet  stmt.ID = "writer.mg.lc.receipt-debt-update-set"
	mgLCReceiptBytes   stmt.ID = "writer.mg.lc.receipt-bytes"
)

const mgLCDebtColumns = `receipt_rows,receipt_variable_bytes,receipt_reclaim_after_sequence,receipt_reclaim_due_ms`

type mgLCReceiptDebt struct {
	rows, bytes, after int64
	due                sql.NullInt64
}

func (d mgLCReceiptDebt) valid(ack int64) bool {
	return d.rows >= 0 && d.bytes >= 0 && (d.rows == 0) == (d.bytes == 0) && d.after >= 0 && d.after <= ack &&
		(!d.due.Valid || (d.due.Int64 >= 0 && d.rows > 0 && d.after < ack))
}
func (d mgLCReceiptDebt) dueValue() any {
	if d.due.Valid {
		return d.due.Int64
	}
	return nil
}
func mgLCDebtOwnerArgs(b mgLCBuild, p mgLCProgress) []any {
	return append(b.ownerValues(), p.last, p.ack)
}
func mgLCReceiptDebtDefinitions() []stmt.Definition {
	p := func(n string, t stmt.Type) stmt.Param { return stmt.Param{Name: n, Type: t} }
	t, i, x := stmt.Text, stmt.Integer, stmt.Blob
	owner := []stmt.Param{p("generation", t), p("incarnation", x), p("channel", t), p("config_revision", i), p("catalogue", t), p("history", i), p("create", x), p("birth", x), p("last", i), p("ack", i)}
	predicate := mgLCOwnerPredicate + ` AND last_command_sequence=? AND acknowledged_command_sequence=?`
	nullableDue := stmt.Param{Name: "new_due", Type: i, Nullable: true}
	defs := []stmt.Definition{
		{ID: mgLCDebtRead, Kind: stmt.Read, SQL: `SELECT ` + mgLCDebtColumns + ` FROM lc_generations WHERE ` + predicate, Params: owner},
		{ID: mgLCReceiptBytes, Kind: stmt.Read, SQL: `SELECT variable_bytes FROM lc_generation_command_receipts WHERE generation_id=? AND command_incarnation=? AND command_sequence=?`, Params: []stmt.Param{p("generation", t), p("incarnation", x), p("sequence", i)}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"lc_generation_command_receipts"}, Seeks: []string{"generation_id=?", "command_incarnation=?", "command_sequence=?"}}},
	}
	for _, present := range []bool{false, true} {
		id, condition := mgLCDebtUpdateNull, `receipt_reclaim_due_ms IS NULL`
		params := append([]stmt.Param{p("new_rows", i), p("new_bytes", i), nullableDue}, owner...)
		params = append(params, p("old_rows", i), p("old_bytes", i), p("old_after", i))
		if present {
			id, condition = mgLCDebtUpdateSet, `receipt_reclaim_due_ms=?`
			params = append(params, p("old_due", i))
		}
		defs = append(defs, stmt.Definition{ID: id, Kind: stmt.Write, SQL: `UPDATE lc_generations SET receipt_rows=?,receipt_variable_bytes=?,receipt_reclaim_due_ms=? WHERE ` + predicate + ` AND receipt_rows=? AND receipt_variable_bytes=? AND receipt_reclaim_after_sequence=? AND ` + condition + ` RETURNING ` + mgLCDebtColumns, Params: params})
	}
	return defs
}

func readMGLCReceiptDebt(ctx context.Context, turn *commandTurn, b mgLCBuild, p mgLCProgress) (out mgLCReceiptDebt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := mgLCRow(ctx, turn, mgLCDebtRead, mgLCDebtOwnerArgs(b, p)...)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.rows, &out.bytes, &out.after, &out.due); err != nil {
		return mgLCReceiptDebt{}, mgLCNoRows(err)
	}
	if !out.valid(p.ack) {
		return mgLCReceiptDebt{}, ErrConflict
	}
	return out, nil
}
func replaceMGLCReceiptDebt(ctx context.Context, turn *commandTurn, b mgLCBuild, p mgLCProgress, before, after mgLCReceiptDebt) (out mgLCReceiptDebt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !before.valid(p.ack) || !after.valid(p.ack) || before.after != after.after {
		return out, ErrConflict
	}
	id := mgLCDebtUpdateNull
	args := append([]any{after.rows, after.bytes, after.dueValue()}, mgLCDebtOwnerArgs(b, p)...)
	args = append(args, before.rows, before.bytes, before.after)
	if before.due.Valid {
		id = mgLCDebtUpdateSet
		args = append(args, before.due.Int64)
	}
	row, err := mgLCRow(ctx, turn, id, args...)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.rows, &out.bytes, &out.after, &out.due); err != nil {
		return mgLCReceiptDebt{}, mgLCNoRows(err)
	}
	if out != after {
		return mgLCReceiptDebt{}, ErrConflict
	}
	return out, nil
}

// Exact bytes of the already decoded/validated native receipt. Comparing these
// independent UTF-8/BLOB sizes to the canonical generated column prevents a
// changed native expression or shadowed length function from understating debt.
func mgLCExpectedReceiptBytes(b mgLCBuild, s mgLCSaved) (int64, error) {
	n := int64(len(b.incarnation) + len(s.command.identity.OperationID) + len(s.command.request) + len(s.digest) + len(s.result.entryDigest) + len(s.result.verifyDigest))
	for _, text := range []string{b.generation, string(s.kind), b.channel, b.fence(), s.result.phase, s.result.status, s.result.lease, s.result.reclaimFamily, s.result.reclaimKey} {
		if int64(len(text)) > math.MaxInt64-n {
			return 0, ErrCap
		}
		n += int64(len(text))
	}
	return n, nil
}

// The receipt and its exact owner debt are one accepting frame. No ACK or
// cancellation refunds them; any failure after insert requires whole rollback.
func recordMGLCReceiptDebt(ctx context.Context, turn *commandTurn, b mgLCBuild, p mgLCProgress, s mgLCSaved) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	before, err := readMGLCReceiptDebt(ctx, turn, b, p)
	if err != nil {
		return err
	}
	row, err := mgLCRow(ctx, turn, mgLCReceiptBytes, b.generation, b.incarnation[:], s.command.identity.Sequence)
	if err != nil {
		return err
	}
	var bytes int64
	if err = row.Scan(&bytes); err != nil {
		return mgLCNoRows(err)
	}
	expected, err := mgLCExpectedReceiptBytes(b, s)
	if err != nil {
		return err
	}
	_ = expected
	if bytes <= 0 {
		return ErrConflict
	}
	if before.rows == math.MaxInt64 || bytes > math.MaxInt64-before.bytes {
		return ErrCap
	}
	after := before
	after.rows++
	after.bytes += bytes
	_, err = replaceMGLCReceiptDebt(ctx, turn, b, p, before, after)
	return err
}

// ACK only schedules retained receipt debt. Reclamation, fixed-slot receipts,
// owner-to-floor transfer and physical/control retirement stay separate.
func scheduleMGLCReceiptDebt(ctx context.Context, turn *commandTurn, b mgLCBuild, p mgLCProgress) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	before, err := readMGLCReceiptDebt(ctx, turn, b, p)
	if err != nil {
		return err
	}
	if before.rows == 0 || before.after >= p.ack {
		return ErrConflict
	}
	// ACK has no build lease dependency. The frame/caller deadlines still apply.
	now, err := mgLCTime(ctx, turn, math.MaxInt64)
	if err != nil {
		return err
	}
	after := before
	if !after.due.Valid || now < after.due.Int64 {
		after.due = sql.NullInt64{Int64: now, Valid: true}
	}
	_, err = replaceMGLCReceiptDebt(ctx, turn, b, p, before, after)
	return err
}
