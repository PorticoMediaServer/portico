package writer

import (
	"context"
	"database/sql"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// These are separate actual media-owner selectors, never a new polymorphic
// persisted owner or a capability. Only W's same-turn current disclosure and
// validated original receipt classification may supply an ACK observation.
type mpReceiptAckObservation struct {
	ownerID                                  string
	revision                                 int64
	owner                                    receiptOwnerFacts
	reference                                ReceiptReference
	decision                                 receiptDecision
	receiptRows, receiptBytes, afterSequence int64
	due                                      sql.NullInt64
	appliedMS                                int64
}

type mpStartReceiptAck struct{ observation mpReceiptAckObservation }

func acknowledgeMPStartReceipt(ctx context.Context, t *commandTurn, q mpStartReceiptAck) (int64, error) {
	return acknowledgeMPReceipt(ctx, t, q.observation, "mp.receipt.start.ack", "mp.receipt.start.ack_due")
}

type mpPhysicalReceiptAck struct{ observation mpReceiptAckObservation }

func acknowledgeMPPhysicalReceipt(ctx context.Context, t *commandTurn, q mpPhysicalReceiptAck) (int64, error) {
	return acknowledgeMPReceipt(ctx, t, q.observation, "mp.receipt.physical.ack", "mp.receipt.physical.ack_due")
}

type mpSessionReceiptAck struct{ observation mpReceiptAckObservation }

func acknowledgeMPSessionReceipt(ctx context.Context, t *commandTurn, q mpSessionReceiptAck) (int64, error) {
	return acknowledgeMPReceipt(ctx, t, q.observation, "mp.receipt.session.ack", "mp.receipt.session.ack_due")
}

type mpJobReceiptAck struct{ observation mpReceiptAckObservation }

func acknowledgeMPJobReceipt(ctx context.Context, t *commandTurn, q mpJobReceiptAck) (int64, error) {
	return acknowledgeMPReceipt(ctx, t, q.observation, "mp.receipt.job.ack", "mp.receipt.job.ack_due")
}

type mpVersionReceiptAck struct{ observation mpReceiptAckObservation }

func acknowledgeMPVersionReceipt(ctx context.Context, t *commandTurn, q mpVersionReceiptAck) (int64, error) {
	return acknowledgeMPReceipt(ctx, t, q.observation, "mp.receipt.version.ack", "mp.receipt.version.ack_due")
}

type mpQueueReceiptAck struct{ observation mpReceiptAckObservation }

func acknowledgeMPQueueReceipt(ctx context.Context, t *commandTurn, q mpQueueReceiptAck) (int64, error) {
	return acknowledgeMPReceipt(ctx, t, q.observation, "mp.receipt.queue.ack", "mp.receipt.queue.ack_due")
}

type mpNextReceiptAck struct{ observation mpReceiptAckObservation }

func acknowledgeMPNextReceipt(ctx context.Context, t *commandTurn, q mpNextReceiptAck) (int64, error) {
	return acknowledgeMPReceipt(ctx, t, q.observation, "mp.receipt.next.ack", "mp.receipt.next.ack_due")
}

// Floor-only and refund0. Retired history returns the retained floor before any
// source result/debt/due freshness check. An ACK never decodes a partly reclaimed
// cohort, clears a fixed result, removes debt, or releases physical custody.
func acknowledgeMPReceipt(ctx context.Context, t *commandTurn, q mpReceiptAckObservation, empty, due stmt.ID) (floor int64, err error) {
	defer func() { floor, err = finishCommand(ctx, commandLifetime(t), floor, err) }()
	if err = mpPrepTurn(t, 780); err != nil {
		return 0, err
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	o, i := q.owner, q.reference.Identity
	if !mpPrepTexts(q.ownerID) || q.revision <= 0 || q.revision == math.MaxInt64 || !o.exists || o.incarnation == ([16]byte{}) || o.incarnation != i.Incarnation || i.Sequence <= 0 || i.OperationID == ([16]byte{}) || q.reference.RequestDigest == (RequestDigest{}) || o.lastSequence < 0 || o.acknowledgedSequence < 0 || o.acknowledgedSequence > o.lastSequence || o.lastSequence-o.acknowledgedSequence > 1 {
		return 0, ErrResolveRequired
	}
	if q.decision == receiptRetired && i.Sequence <= o.acknowledgedSequence {
		return o.acknowledgedSequence, nil
	}
	if q.decision != receiptReplay || i.Sequence != o.lastSequence || i.Sequence <= o.acknowledgedSequence || q.receiptRows < 0 || q.receiptBytes < 0 || (q.receiptRows == 0) != (q.receiptBytes == 0) || q.afterSequence < 0 || q.afterSequence > o.acknowledgedSequence || q.appliedMS < 0 || q.due.Valid && q.due.Int64 < 0 || !q.due.Valid && q.due.Int64 != 0 {
		return 0, ErrResolveRequired
	}
	// A fixed reclaim-slot ACK may have no source headers. Preserve existing due;
	// only source headers newly eligible under this floor are newly made due.
	var next any
	if q.due.Valid {
		next = q.due.Int64
	}
	if q.receiptRows > 0 {
		n := q.appliedMS
		if q.due.Valid {
			n = min(n, q.due.Int64)
		}
		next = n
	}
	args := []any{i.Sequence, next, q.ownerID, i.Incarnation[:], q.revision, o.lastSequence, o.acknowledgedSequence, q.receiptRows, q.receiptBytes, q.afterSequence}
	id := empty
	if q.due.Valid {
		id = due
		args = append(args, q.due.Int64)
	}
	row, e := mpPrepRow(ctx, t, id, args...)
	if e != nil {
		return 0, e
	}
	if err = row.Scan(&floor); err != nil {
		return 0, err
	}
	if floor != i.Sequence {
		return 0, ErrResolveRequired
	}
	return floor, t.checkLifetimes(ctx)
}

func mpReceiptAckDefinitions() []stmt.Definition {
	return []stmt.Definition{
		mpReceiptAckDefinition("mp.receipt.start.ack", `UPDATE playback_start_preparations SET mp_owner_revision=mp_owner_revision+1,mp_command_acknowledged_sequence=?,mp_receipt_reclaim_due_ms=? WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_due_ms IS NULL RETURNING mp_command_acknowledged_sequence`, false),
		mpReceiptAckDefinition("mp.receipt.start.ack_due", `UPDATE playback_start_preparations SET mp_owner_revision=mp_owner_revision+1,mp_command_acknowledged_sequence=?,mp_receipt_reclaim_due_ms=? WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_due_ms=? RETURNING mp_command_acknowledged_sequence`, true),
		mpReceiptAckDefinition("mp.receipt.physical.ack", `UPDATE playback_sessions SET mp_owner_revision=mp_owner_revision+1,mp_command_acknowledged_sequence=?,mp_receipt_reclaim_due_ms=? WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_due_ms IS NULL RETURNING mp_command_acknowledged_sequence`, false),
		mpReceiptAckDefinition("mp.receipt.physical.ack_due", `UPDATE playback_sessions SET mp_owner_revision=mp_owner_revision+1,mp_command_acknowledged_sequence=?,mp_receipt_reclaim_due_ms=? WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_due_ms=? RETURNING mp_command_acknowledged_sequence`, true),
		mpReceiptAckDefinition("mp.receipt.session.ack", `UPDATE playback_v1_sessions SET mp_owner_revision=mp_owner_revision+1,mp_command_acknowledged_sequence=?,mp_receipt_reclaim_due_ms=? WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_due_ms IS NULL RETURNING mp_command_acknowledged_sequence`, false),
		mpReceiptAckDefinition("mp.receipt.session.ack_due", `UPDATE playback_v1_sessions SET mp_owner_revision=mp_owner_revision+1,mp_command_acknowledged_sequence=?,mp_receipt_reclaim_due_ms=? WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_due_ms=? RETURNING mp_command_acknowledged_sequence`, true),
		mpReceiptAckDefinition("mp.receipt.job.ack", `UPDATE prepared_media_jobs SET mp_owner_revision=mp_owner_revision+1,mp_command_acknowledged_sequence=?,mp_receipt_reclaim_due_ms=? WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_due_ms IS NULL RETURNING mp_command_acknowledged_sequence`, false),
		mpReceiptAckDefinition("mp.receipt.job.ack_due", `UPDATE prepared_media_jobs SET mp_owner_revision=mp_owner_revision+1,mp_command_acknowledged_sequence=?,mp_receipt_reclaim_due_ms=? WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_due_ms=? RETURNING mp_command_acknowledged_sequence`, true),
		mpReceiptAckDefinition("mp.receipt.version.ack", `UPDATE prepared_media_versions SET mp_owner_revision=mp_owner_revision+1,mp_command_acknowledged_sequence=?,mp_receipt_reclaim_due_ms=? WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_due_ms IS NULL RETURNING mp_command_acknowledged_sequence`, false),
		mpReceiptAckDefinition("mp.receipt.version.ack_due", `UPDATE prepared_media_versions SET mp_owner_revision=mp_owner_revision+1,mp_command_acknowledged_sequence=?,mp_receipt_reclaim_due_ms=? WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_due_ms=? RETURNING mp_command_acknowledged_sequence`, true),
		mpReceiptAckDefinition("mp.receipt.queue.ack", `UPDATE queues_v1 SET mp_owner_revision=mp_owner_revision+1,mp_command_acknowledged_sequence=?,mp_receipt_reclaim_due_ms=? WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_due_ms IS NULL RETURNING mp_command_acknowledged_sequence`, false),
		mpReceiptAckDefinition("mp.receipt.queue.ack_due", `UPDATE queues_v1 SET mp_owner_revision=mp_owner_revision+1,mp_command_acknowledged_sequence=?,mp_receipt_reclaim_due_ms=? WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_due_ms=? RETURNING mp_command_acknowledged_sequence`, true),
		mpReceiptAckDefinition("mp.receipt.next.ack", `UPDATE playback_v1_prepared SET mp_owner_revision=mp_owner_revision+1,mp_command_acknowledged_sequence=?,mp_receipt_reclaim_due_ms=? WHERE token=? AND mp_command_incarnation=? AND mp_owner_revision=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_due_ms IS NULL RETURNING mp_command_acknowledged_sequence`, false),
		mpReceiptAckDefinition("mp.receipt.next.ack_due", `UPDATE playback_v1_prepared SET mp_owner_revision=mp_owner_revision+1,mp_command_acknowledged_sequence=?,mp_receipt_reclaim_due_ms=? WHERE token=? AND mp_command_incarnation=? AND mp_owner_revision=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_due_ms=? RETURNING mp_command_acknowledged_sequence`, true),
	}
}
func mpReceiptAckDefinition(id, sql string, oldDue bool) stmt.Definition {
	types := "iisbiiiiii"
	if oldDue {
		types += "i"
	}
	d := mpPrepDefinition(id, sql, types, stmt.Write)
	d.Owner = "store/writer MP receipt floor"
	d.CallSites = []string{"store/writer/mp_receipt_ack.go"}
	d.Params[1].Nullable = true
	return d
}
