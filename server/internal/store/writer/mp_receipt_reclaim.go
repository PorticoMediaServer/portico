package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"math"
)

// W must supply current maintenance disclosure and canonical classification in
// this commandTurn. These values grant no authority, admission or commit credit.
// Cleanup can outlive the issuer; physical custody and pins are separate domains.
type mpReclaimInvocation struct {
	ownerID               string
	identity              CommandIdentity
	request               RequestDigest
	appliedMS             int64
	decision              receiptDecision
	original              mpWMediaPayload
	observation           mpReclaimResult
	admittedVariableBytes int64
}
type mpReclaimResult struct {
	ownerID                                                string
	identity                                               CommandIdentity
	kind                                                   CommandID
	request                                                RequestDigest
	appliedMS, capturedACK, beforeCursor, afterCursor      int64
	removedSequence, removedRows, removedBytes             int64
	beforeRows, beforeBytes, remainingRows, remainingBytes int64
	finished                                               bool
	oldControlBytes, newControlBytes                       int64
}
type mpReclaimRoot struct {
	revision, last, ack, rows, bytes, cursor, controlBytes int64
	due, slotSequence                                      sql.NullInt64
	slotOperation, slotRequest, slotDigest                 []byte
	slotPayloadBytes                                       int64
}
type mpReclaimPoints struct {
	kind   CommandID
	family string
}

func (p mpReclaimPoints) id(s string) stmt.ID { return stmt.ID("mp.reclaim." + p.family + "." + s) }

// Observation commitment only; W remains the sole result payload codec.
func mpReclaimDigest(r mpReclaimResult) (ResultDigest, error) {
	if r.ownerID == "" || r.kind < 781 || r.kind > 787 || r.identity.Sequence <= 0 || r.identity.Incarnation == ([16]byte{}) || r.identity.OperationID == ([16]byte{}) || r.request == (RequestDigest{}) || r.appliedMS < 0 || r.capturedACK < 0 || r.capturedACK >= r.identity.Sequence || r.beforeCursor < 0 || r.afterCursor < r.beforeCursor || r.afterCursor > r.capturedACK || r.removedRows < 0 || r.removedRows > 1 || r.removedBytes < 0 || r.beforeRows < r.removedRows || r.beforeBytes < r.removedBytes || r.remainingRows != r.beforeRows-r.removedRows || r.remainingBytes != r.beforeBytes-r.removedBytes || (r.remainingRows == 0) != (r.remainingBytes == 0) || (r.removedRows == 0 && (r.removedSequence != 0 || r.removedBytes != 0 || r.afterCursor != r.beforeCursor || !r.finished)) || (r.removedRows == 1 && (r.removedSequence <= r.beforeCursor || r.removedSequence > r.capturedACK || r.afterCursor != r.removedSequence || r.removedBytes <= 0)) {
		return ResultDigest{}, ErrResolveRequired
	}
	e, err := mpPrepReceiptEncoder(mpPrepReceiptHeader{identity: r.identity, request: r.request, kind: r.kind, appliedMS: r.appliedMS}, "mp_actual_owner_reclaim")
	if err != nil {
		return ResultDigest{}, err
	}
	e.text(r.ownerID)
	for _, n := range []int64{r.appliedMS, r.capturedACK, r.beforeCursor, r.afterCursor, r.removedSequence, r.removedRows, r.removedBytes, r.beforeRows, r.beforeBytes, r.remainingRows, r.remainingBytes} {
		e.number(uint64(n))
	}
	if r.finished {
		e.number(1)
	} else {
		e.number(0)
	}
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	var d ResultDigest
	copy(d[:], e.h.Sum(nil))
	return d, nil
}
func mpReclaimFirst(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (sequence, debt int64, found bool, err error) {
	row, e := mpPrepRow(ctx, t, id, args...)
	if e != nil {
		return 0, 0, false, e
	}
	e = row.Scan(&sequence, &debt)
	if errors.Is(e, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	if e != nil {
		return 0, 0, false, e
	}
	if sequence <= 0 || debt <= 0 {
		return 0, 0, false, ErrResolveRequired
	}
	return sequence, debt, true, nil
}

// Preparation is read-only. W seals this observation with its closed-purpose
// codec, then effect/store repeats preparation in the same transaction.
func prepareMPReclaim(ctx context.Context, t *commandTurn, q mpReclaimInvocation, p mpReclaimPoints) (out mpReclaimResult, b mpReclaimRoot, err error) {
	if err = mpPrepTurn(t, p.kind); err != nil {
		return
	}
	if q.decision != receiptFresh || !mpPrepTexts(q.ownerID) || q.identity.Sequence <= 0 || q.identity.Incarnation == ([16]byte{}) || q.identity.OperationID == ([16]byte{}) || q.request == (RequestDigest{}) || q.appliedMS < 0 {
		err = ErrResolveRequired
		return
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return
	}
	row, e := mpPrepRow(ctx, t, p.id("current"), q.ownerID, q.identity.Incarnation[:])
	if e != nil {
		err = e
		return
	}
	if err = row.Scan(&b.revision, &b.last, &b.ack, &b.rows, &b.bytes, &b.cursor, &b.due, &b.slotSequence, &b.slotOperation, &b.slotRequest, &b.slotDigest, &b.slotPayloadBytes, &b.controlBytes); err != nil {
		return
	}
	if b.revision <= 0 || b.revision == math.MaxInt64 || b.last < 0 || b.last == math.MaxInt64 || b.last != b.ack || q.identity.Sequence != b.last+1 || b.rows < 0 || b.bytes < 0 || (b.rows == 0) != (b.bytes == 0) || b.cursor < 0 || b.cursor > b.ack || !b.due.Valid || b.due.Int64 < 0 || b.due.Int64 > q.appliedMS || b.controlBytes < 0 || (b.slotSequence.Valid && (b.slotSequence.Int64 <= 0 || b.slotSequence.Int64 > b.ack || len(b.slotOperation) != 16 || len(b.slotRequest) != 32 || len(b.slotDigest) != 32 || b.slotPayloadBytes < 1 || b.slotPayloadBytes > 4096)) || (!b.slotSequence.Valid && (len(b.slotOperation) != 0 || len(b.slotRequest) != 0 || len(b.slotDigest) != 0 || b.slotPayloadBytes != 0)) {
		err = ErrResolveRequired
		return
	}
	out = mpReclaimResult{ownerID: q.ownerID, identity: q.identity, kind: p.kind, request: q.request, appliedMS: q.appliedMS, capturedACK: b.ack, beforeCursor: b.cursor, afterCursor: b.cursor, beforeRows: b.rows, beforeBytes: b.bytes, remainingRows: b.rows, remainingBytes: b.bytes, oldControlBytes: b.controlBytes}
	seq, n, found, e := mpReclaimFirst(ctx, t, p.id("first"), q.ownerID, q.identity.Incarnation[:], b.cursor, b.ack)
	if e != nil {
		err = e
		return
	}
	if found {
		out.removedSequence = seq
		out.removedRows = 1
		out.removedBytes = n
		out.afterCursor = seq
		out.remainingRows--
		out.remainingBytes -= n
	}
	_, _, next, e := mpReclaimFirst(ctx, t, p.id("first"), q.ownerID, q.identity.Incarnation[:], out.afterCursor, b.ack)
	if e != nil {
		err = e
		return
	}
	out.finished = !next
	if out.finished != (out.remainingRows == 0) {
		err = ErrResolveRequired
		return
	}
	if !found && b.rows != 0 {
		err = ErrResolveRequired
		return
	}
	_, err = mpReclaimDigest(out)
	return
}
func reclaimMPReceipt(ctx context.Context, t *commandTurn, q mpReclaimInvocation, p mpReclaimPoints) (out mpReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	var b mpReclaimRoot
	out, b, err = prepareMPReclaim(ctx, t, q, p)
	if err != nil {
		return
	}
	v := q.original
	h := v.header
	if q.observation != out || h.identity != q.identity || h.request != q.request || h.kind != p.kind || h.appliedMS != q.appliedMS || h.resultDigest == (ResultDigest{}) || v.version != 1 || v.count != out.removedRows || len(v.payload) < 1 || len(v.payload) > 4096 {
		return out, ErrResolveRequired
	}
	if out.removedRows == 1 {
		row, e := mpPrepRow(ctx, t, p.id("delete"), q.ownerID, q.identity.Incarnation[:], out.removedSequence, out.removedBytes)
		if e != nil {
			return out, e
		}
		var n int64
		if err = row.Scan(&n); err != nil {
			return
		}
		if n != out.removedBytes {
			return out, ErrResolveRequired
		}
	}
	var due, removed any
	if !out.finished {
		due = q.appliedMS
	}
	if out.removedRows == 1 {
		removed = out.removedSequence
	}
	noNext := int64(0)
	if out.finished {
		noNext = 1
	}
	row, e := mpPrepRow(ctx, t, p.id("store"), q.identity.Sequence, out.remainingRows, out.remainingBytes, out.afterCursor, due, q.identity.Sequence, q.identity.OperationID[:], q.request[:], int64(p.kind), v.version, v.count, h.resultDigest[:], v.payload, b.ack, b.cursor, removed, out.removedBytes, out.afterCursor, noNext, q.appliedMS, q.ownerID, q.identity.Incarnation[:], b.revision, b.last, b.ack, b.rows, b.bytes, b.cursor, b.due, b.slotSequence, b.slotOperation, b.slotRequest, b.slotDigest, b.slotPayloadBytes, b.controlBytes)
	if e != nil {
		return out, e
	}
	if err = row.Scan(&out.newControlBytes); err != nil {
		return
	}
	if out.newControlBytes != b.controlBytes-int64(len(b.slotOperation)+len(b.slotRequest)+len(b.slotDigest))-b.slotPayloadBytes+80+int64(len(v.payload)) {
		return out, ErrResolveRequired
	}
	// Source refund and old/new control debt remain separate until W terminal commit.
	return out, t.checkLifetimes(ctx)
}
func loadMPReclaimReceipt(ctx context.Context, t *commandTurn, q mpReclaimInvocation, p mpReclaimPoints) (out mpWMediaPayload, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, p.kind); err != nil {
		return
	}
	if q.decision != receiptReplay || !mpPrepTexts(q.ownerID) || q.identity.Sequence <= 0 || q.identity.Incarnation == ([16]byte{}) || q.identity.OperationID == ([16]byte{}) || q.request == (RequestDigest{}) {
		return out, ErrResolveRequired
	}
	row, e := mpPrepRow(ctx, t, p.id("octets"), q.ownerID, q.identity.Incarnation[:], q.identity.Sequence, q.identity.OperationID[:], q.request[:])
	if e != nil {
		return out, e
	}
	var n int64
	if err = row.Scan(&n); err != nil {
		return
	}
	if n < 1 || n > 4096 || n+80 > q.admittedVariableBytes {
		return out, ErrResolveRequired
	}
	row, e = mpPrepRow(ctx, t, p.id("load"), q.ownerID, q.identity.Incarnation[:], q.identity.Sequence, q.identity.OperationID[:], q.request[:])
	if e != nil {
		return out, e
	}
	var digest, op, request []byte
	var kind int64
	out.header = mpPrepReceiptHeader{identity: q.identity, request: q.request, kind: p.kind}
	if err = row.Scan(&op, &request, &kind, &out.version, &out.count, &digest, &out.payload, &out.header.appliedMS); err != nil {
		return
	}
	if !bytes.Equal(op, q.identity.OperationID[:]) || !bytes.Equal(request, q.request[:]) || kind != int64(p.kind) || out.version != 1 || out.count < 0 || out.count > 1 || len(digest) != 32 || int64(len(out.payload)) != n || out.header.appliedMS < 0 {
		return out, ErrResolveRequired
	}
	copy(out.header.resultDigest[:], digest)
	if out.header.resultDigest == (ResultDigest{}) {
		return out, ErrResolveRequired
	}
	return out, t.checkLifetimes(ctx)
}

func mpStartReclaimPoints() mpReclaimPoints { return mpReclaimPoints{781, "start"} }

type mpStartReclaim struct{ invocation mpReclaimInvocation }

func reclaimMPStartReceipt(ctx context.Context, t *commandTurn, q mpStartReclaim) (out mpReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return reclaimMPReceipt(ctx, t, q.invocation, mpStartReclaimPoints())
}
func prepareMPStartReclaim(ctx context.Context, t *commandTurn, q mpStartReclaim) (out mpReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	out, _, err = prepareMPReclaim(ctx, t, q.invocation, mpStartReclaimPoints())
	return
}
func loadMPStartReclaim(ctx context.Context, t *commandTurn, q mpStartReclaim) (out mpWMediaPayload, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return loadMPReclaimReceipt(ctx, t, q.invocation, mpStartReclaimPoints())
}

func mpPhysicalReclaimPoints() mpReclaimPoints { return mpReclaimPoints{782, "physical"} }

type mpPhysicalReclaim struct{ invocation mpReclaimInvocation }

func reclaimMPPhysicalReceipt(ctx context.Context, t *commandTurn, q mpPhysicalReclaim) (out mpReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return reclaimMPReceipt(ctx, t, q.invocation, mpPhysicalReclaimPoints())
}
func prepareMPPhysicalReclaim(ctx context.Context, t *commandTurn, q mpPhysicalReclaim) (out mpReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	out, _, err = prepareMPReclaim(ctx, t, q.invocation, mpPhysicalReclaimPoints())
	return
}
func loadMPPhysicalReclaim(ctx context.Context, t *commandTurn, q mpPhysicalReclaim) (out mpWMediaPayload, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return loadMPReclaimReceipt(ctx, t, q.invocation, mpPhysicalReclaimPoints())
}

func mpSessionReclaimPoints() mpReclaimPoints { return mpReclaimPoints{783, "session"} }

type mpSessionReclaim struct{ invocation mpReclaimInvocation }

func reclaimMPSessionReceipt(ctx context.Context, t *commandTurn, q mpSessionReclaim) (out mpReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return reclaimMPReceipt(ctx, t, q.invocation, mpSessionReclaimPoints())
}
func prepareMPSessionReclaim(ctx context.Context, t *commandTurn, q mpSessionReclaim) (out mpReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	out, _, err = prepareMPReclaim(ctx, t, q.invocation, mpSessionReclaimPoints())
	return
}
func loadMPSessionReclaim(ctx context.Context, t *commandTurn, q mpSessionReclaim) (out mpWMediaPayload, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return loadMPReclaimReceipt(ctx, t, q.invocation, mpSessionReclaimPoints())
}

func mpJobReclaimPoints() mpReclaimPoints { return mpReclaimPoints{784, "job"} }

type mpJobReclaim struct{ invocation mpReclaimInvocation }

func reclaimMPJobReceipt(ctx context.Context, t *commandTurn, q mpJobReclaim) (out mpReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return reclaimMPReceipt(ctx, t, q.invocation, mpJobReclaimPoints())
}
func prepareMPJobReclaim(ctx context.Context, t *commandTurn, q mpJobReclaim) (out mpReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	out, _, err = prepareMPReclaim(ctx, t, q.invocation, mpJobReclaimPoints())
	return
}
func loadMPJobReclaim(ctx context.Context, t *commandTurn, q mpJobReclaim) (out mpWMediaPayload, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return loadMPReclaimReceipt(ctx, t, q.invocation, mpJobReclaimPoints())
}

func mpVersionReclaimPoints() mpReclaimPoints { return mpReclaimPoints{785, "version"} }

type mpVersionReclaim struct{ invocation mpReclaimInvocation }

func reclaimMPVersionReceipt(ctx context.Context, t *commandTurn, q mpVersionReclaim) (out mpReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return reclaimMPReceipt(ctx, t, q.invocation, mpVersionReclaimPoints())
}
func prepareMPVersionReclaim(ctx context.Context, t *commandTurn, q mpVersionReclaim) (out mpReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	out, _, err = prepareMPReclaim(ctx, t, q.invocation, mpVersionReclaimPoints())
	return
}
func loadMPVersionReclaim(ctx context.Context, t *commandTurn, q mpVersionReclaim) (out mpWMediaPayload, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return loadMPReclaimReceipt(ctx, t, q.invocation, mpVersionReclaimPoints())
}

func mpQueueReclaimPoints() mpReclaimPoints { return mpReclaimPoints{786, "queue"} }

type mpQueueReclaim struct{ invocation mpReclaimInvocation }

func reclaimMPQueueReceipt(ctx context.Context, t *commandTurn, q mpQueueReclaim) (out mpReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return reclaimMPReceipt(ctx, t, q.invocation, mpQueueReclaimPoints())
}
func prepareMPQueueReclaim(ctx context.Context, t *commandTurn, q mpQueueReclaim) (out mpReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	out, _, err = prepareMPReclaim(ctx, t, q.invocation, mpQueueReclaimPoints())
	return
}
func loadMPQueueReclaim(ctx context.Context, t *commandTurn, q mpQueueReclaim) (out mpWMediaPayload, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return loadMPReclaimReceipt(ctx, t, q.invocation, mpQueueReclaimPoints())
}

func mpNextReclaimPoints() mpReclaimPoints { return mpReclaimPoints{787, "next"} }

type mpNextReclaim struct{ invocation mpReclaimInvocation }

func reclaimMPNextReceipt(ctx context.Context, t *commandTurn, q mpNextReclaim) (out mpReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return reclaimMPReceipt(ctx, t, q.invocation, mpNextReclaimPoints())
}
func prepareMPNextReclaim(ctx context.Context, t *commandTurn, q mpNextReclaim) (out mpReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	out, _, err = prepareMPReclaim(ctx, t, q.invocation, mpNextReclaimPoints())
	return
}
func loadMPNextReclaim(ctx context.Context, t *commandTurn, q mpNextReclaim) (out mpWMediaPayload, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return loadMPReclaimReceipt(ctx, t, q.invocation, mpNextReclaimPoints())
}

func mpReceiptReclaimDefinitions() []stmt.Definition {
	var defs []stmt.Definition
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.start.current", `SELECT mp_owner_revision,mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes,mp_receipt_reclaim_after_sequence,mp_receipt_reclaim_due_ms,mp_reclaim_sequence,mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_result_digest,coalesce(octet_length(mp_reclaim_result_payload),0),mp_control_variable_bytes FROM playback_start_preparations WHERE id=? AND mp_command_incarnation=?`, "sb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.start.first", `SELECT command_sequence,variable_bytes FROM mp_start_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=? ORDER BY command_sequence LIMIT 1`, "sbii", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.start.delete", `DELETE FROM mp_start_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND variable_bytes=? RETURNING variable_bytes`, "sbii", stmt.Write, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.start.store", `UPDATE playback_start_preparations SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=?,mp_receipt_reclaim_after_sequence=?,mp_receipt_reclaim_due_ms=?,mp_reclaim_sequence=?,mp_reclaim_operation_id=?,mp_reclaim_request_digest=?,mp_reclaim_command_kind=?,mp_reclaim_result_version=?,mp_reclaim_result_count=?,mp_reclaim_result_digest=?,mp_reclaim_result_payload=?,mp_reclaim_captured_ack=?,mp_reclaim_before_cursor=?,mp_reclaim_removed_sequence=?,mp_reclaim_removed_bytes=?,mp_reclaim_after_cursor=?,mp_reclaim_no_next=?,mp_reclaim_applied_ms=?,mp_owner_revision=mp_owner_revision+1 WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision IS ? AND mp_command_last_sequence IS ? AND mp_command_acknowledged_sequence IS ? AND mp_receipt_rows IS ? AND mp_receipt_variable_bytes IS ? AND mp_receipt_reclaim_after_sequence IS ? AND mp_receipt_reclaim_due_ms IS ? AND mp_reclaim_sequence IS ? AND mp_reclaim_operation_id IS ? AND mp_reclaim_request_digest IS ? AND mp_reclaim_result_digest IS ? AND coalesce(octet_length(mp_reclaim_result_payload),0)=? AND mp_control_variable_bytes=? RETURNING mp_control_variable_bytes`, "iiiiiibbiiibbiiiiiiisbiiiiiiiibbbii", stmt.Write, []int{4, 15, 29, 30, 31, 32}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.start.octets", `SELECT octet_length(mp_reclaim_result_payload) FROM playback_start_preparations WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.start.load", `SELECT mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_command_kind,mp_reclaim_result_version,mp_reclaim_result_count,mp_reclaim_result_digest,mp_reclaim_result_payload,mp_reclaim_applied_ms FROM playback_start_preparations WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.physical.current", `SELECT mp_owner_revision,mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes,mp_receipt_reclaim_after_sequence,mp_receipt_reclaim_due_ms,mp_reclaim_sequence,mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_result_digest,coalesce(octet_length(mp_reclaim_result_payload),0),mp_control_variable_bytes FROM playback_sessions WHERE id=? AND mp_command_incarnation=?`, "sb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.physical.first", `SELECT command_sequence,variable_bytes FROM mp_physical_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=? ORDER BY command_sequence LIMIT 1`, "sbii", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.physical.delete", `DELETE FROM mp_physical_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND variable_bytes=? RETURNING variable_bytes`, "sbii", stmt.Write, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.physical.store", `UPDATE playback_sessions SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=?,mp_receipt_reclaim_after_sequence=?,mp_receipt_reclaim_due_ms=?,mp_reclaim_sequence=?,mp_reclaim_operation_id=?,mp_reclaim_request_digest=?,mp_reclaim_command_kind=?,mp_reclaim_result_version=?,mp_reclaim_result_count=?,mp_reclaim_result_digest=?,mp_reclaim_result_payload=?,mp_reclaim_captured_ack=?,mp_reclaim_before_cursor=?,mp_reclaim_removed_sequence=?,mp_reclaim_removed_bytes=?,mp_reclaim_after_cursor=?,mp_reclaim_no_next=?,mp_reclaim_applied_ms=?,mp_owner_revision=mp_owner_revision+1 WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision IS ? AND mp_command_last_sequence IS ? AND mp_command_acknowledged_sequence IS ? AND mp_receipt_rows IS ? AND mp_receipt_variable_bytes IS ? AND mp_receipt_reclaim_after_sequence IS ? AND mp_receipt_reclaim_due_ms IS ? AND mp_reclaim_sequence IS ? AND mp_reclaim_operation_id IS ? AND mp_reclaim_request_digest IS ? AND mp_reclaim_result_digest IS ? AND coalesce(octet_length(mp_reclaim_result_payload),0)=? AND mp_control_variable_bytes=? RETURNING mp_control_variable_bytes`, "iiiiiibbiiibbiiiiiiisbiiiiiiiibbbii", stmt.Write, []int{4, 15, 29, 30, 31, 32}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.physical.octets", `SELECT octet_length(mp_reclaim_result_payload) FROM playback_sessions WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.physical.load", `SELECT mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_command_kind,mp_reclaim_result_version,mp_reclaim_result_count,mp_reclaim_result_digest,mp_reclaim_result_payload,mp_reclaim_applied_ms FROM playback_sessions WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.session.current", `SELECT mp_owner_revision,mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes,mp_receipt_reclaim_after_sequence,mp_receipt_reclaim_due_ms,mp_reclaim_sequence,mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_result_digest,coalesce(octet_length(mp_reclaim_result_payload),0),mp_control_variable_bytes FROM playback_v1_sessions WHERE id=? AND mp_command_incarnation=?`, "sb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.session.first", `SELECT command_sequence,variable_bytes FROM mp_session_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=? ORDER BY command_sequence LIMIT 1`, "sbii", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.session.delete", `DELETE FROM mp_session_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND variable_bytes=? RETURNING variable_bytes`, "sbii", stmt.Write, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.session.store", `UPDATE playback_v1_sessions SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=?,mp_receipt_reclaim_after_sequence=?,mp_receipt_reclaim_due_ms=?,mp_reclaim_sequence=?,mp_reclaim_operation_id=?,mp_reclaim_request_digest=?,mp_reclaim_command_kind=?,mp_reclaim_result_version=?,mp_reclaim_result_count=?,mp_reclaim_result_digest=?,mp_reclaim_result_payload=?,mp_reclaim_captured_ack=?,mp_reclaim_before_cursor=?,mp_reclaim_removed_sequence=?,mp_reclaim_removed_bytes=?,mp_reclaim_after_cursor=?,mp_reclaim_no_next=?,mp_reclaim_applied_ms=?,mp_owner_revision=mp_owner_revision+1 WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision IS ? AND mp_command_last_sequence IS ? AND mp_command_acknowledged_sequence IS ? AND mp_receipt_rows IS ? AND mp_receipt_variable_bytes IS ? AND mp_receipt_reclaim_after_sequence IS ? AND mp_receipt_reclaim_due_ms IS ? AND mp_reclaim_sequence IS ? AND mp_reclaim_operation_id IS ? AND mp_reclaim_request_digest IS ? AND mp_reclaim_result_digest IS ? AND coalesce(octet_length(mp_reclaim_result_payload),0)=? AND mp_control_variable_bytes=? RETURNING mp_control_variable_bytes`, "iiiiiibbiiibbiiiiiiisbiiiiiiiibbbii", stmt.Write, []int{4, 15, 29, 30, 31, 32}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.session.octets", `SELECT octet_length(mp_reclaim_result_payload) FROM playback_v1_sessions WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.session.load", `SELECT mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_command_kind,mp_reclaim_result_version,mp_reclaim_result_count,mp_reclaim_result_digest,mp_reclaim_result_payload,mp_reclaim_applied_ms FROM playback_v1_sessions WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.job.current", `SELECT mp_owner_revision,mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes,mp_receipt_reclaim_after_sequence,mp_receipt_reclaim_due_ms,mp_reclaim_sequence,mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_result_digest,coalesce(octet_length(mp_reclaim_result_payload),0),mp_control_variable_bytes FROM prepared_media_jobs WHERE id=? AND mp_command_incarnation=?`, "sb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.job.first", `SELECT command_sequence,variable_bytes FROM mp_job_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=? ORDER BY command_sequence LIMIT 1`, "sbii", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.job.delete", `DELETE FROM mp_job_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND variable_bytes=? RETURNING variable_bytes`, "sbii", stmt.Write, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.job.store", `UPDATE prepared_media_jobs SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=?,mp_receipt_reclaim_after_sequence=?,mp_receipt_reclaim_due_ms=?,mp_reclaim_sequence=?,mp_reclaim_operation_id=?,mp_reclaim_request_digest=?,mp_reclaim_command_kind=?,mp_reclaim_result_version=?,mp_reclaim_result_count=?,mp_reclaim_result_digest=?,mp_reclaim_result_payload=?,mp_reclaim_captured_ack=?,mp_reclaim_before_cursor=?,mp_reclaim_removed_sequence=?,mp_reclaim_removed_bytes=?,mp_reclaim_after_cursor=?,mp_reclaim_no_next=?,mp_reclaim_applied_ms=?,mp_owner_revision=mp_owner_revision+1 WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision IS ? AND mp_command_last_sequence IS ? AND mp_command_acknowledged_sequence IS ? AND mp_receipt_rows IS ? AND mp_receipt_variable_bytes IS ? AND mp_receipt_reclaim_after_sequence IS ? AND mp_receipt_reclaim_due_ms IS ? AND mp_reclaim_sequence IS ? AND mp_reclaim_operation_id IS ? AND mp_reclaim_request_digest IS ? AND mp_reclaim_result_digest IS ? AND coalesce(octet_length(mp_reclaim_result_payload),0)=? AND mp_control_variable_bytes=? RETURNING mp_control_variable_bytes`, "iiiiiibbiiibbiiiiiiisbiiiiiiiibbbii", stmt.Write, []int{4, 15, 29, 30, 31, 32}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.job.octets", `SELECT octet_length(mp_reclaim_result_payload) FROM prepared_media_jobs WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.job.load", `SELECT mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_command_kind,mp_reclaim_result_version,mp_reclaim_result_count,mp_reclaim_result_digest,mp_reclaim_result_payload,mp_reclaim_applied_ms FROM prepared_media_jobs WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.version.current", `SELECT mp_owner_revision,mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes,mp_receipt_reclaim_after_sequence,mp_receipt_reclaim_due_ms,mp_reclaim_sequence,mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_result_digest,coalesce(octet_length(mp_reclaim_result_payload),0),mp_control_variable_bytes FROM prepared_media_versions WHERE id=? AND mp_command_incarnation=?`, "sb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.version.first", `SELECT command_sequence,variable_bytes FROM mp_version_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=? ORDER BY command_sequence LIMIT 1`, "sbii", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.version.delete", `DELETE FROM mp_version_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND variable_bytes=? RETURNING variable_bytes`, "sbii", stmt.Write, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.version.store", `UPDATE prepared_media_versions SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=?,mp_receipt_reclaim_after_sequence=?,mp_receipt_reclaim_due_ms=?,mp_reclaim_sequence=?,mp_reclaim_operation_id=?,mp_reclaim_request_digest=?,mp_reclaim_command_kind=?,mp_reclaim_result_version=?,mp_reclaim_result_count=?,mp_reclaim_result_digest=?,mp_reclaim_result_payload=?,mp_reclaim_captured_ack=?,mp_reclaim_before_cursor=?,mp_reclaim_removed_sequence=?,mp_reclaim_removed_bytes=?,mp_reclaim_after_cursor=?,mp_reclaim_no_next=?,mp_reclaim_applied_ms=?,mp_owner_revision=mp_owner_revision+1 WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision IS ? AND mp_command_last_sequence IS ? AND mp_command_acknowledged_sequence IS ? AND mp_receipt_rows IS ? AND mp_receipt_variable_bytes IS ? AND mp_receipt_reclaim_after_sequence IS ? AND mp_receipt_reclaim_due_ms IS ? AND mp_reclaim_sequence IS ? AND mp_reclaim_operation_id IS ? AND mp_reclaim_request_digest IS ? AND mp_reclaim_result_digest IS ? AND coalesce(octet_length(mp_reclaim_result_payload),0)=? AND mp_control_variable_bytes=? RETURNING mp_control_variable_bytes`, "iiiiiibbiiibbiiiiiiisbiiiiiiiibbbii", stmt.Write, []int{4, 15, 29, 30, 31, 32}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.version.octets", `SELECT octet_length(mp_reclaim_result_payload) FROM prepared_media_versions WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.version.load", `SELECT mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_command_kind,mp_reclaim_result_version,mp_reclaim_result_count,mp_reclaim_result_digest,mp_reclaim_result_payload,mp_reclaim_applied_ms FROM prepared_media_versions WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.queue.current", `SELECT mp_owner_revision,mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes,mp_receipt_reclaim_after_sequence,mp_receipt_reclaim_due_ms,mp_reclaim_sequence,mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_result_digest,coalesce(octet_length(mp_reclaim_result_payload),0),mp_control_variable_bytes FROM queues_v1 WHERE id=? AND mp_command_incarnation=?`, "sb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.queue.first", `SELECT command_sequence,variable_bytes FROM mp_queue_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=? ORDER BY command_sequence LIMIT 1`, "sbii", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.queue.delete", `DELETE FROM mp_queue_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND variable_bytes=? RETURNING variable_bytes`, "sbii", stmt.Write, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.queue.store", `UPDATE queues_v1 SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=?,mp_receipt_reclaim_after_sequence=?,mp_receipt_reclaim_due_ms=?,mp_reclaim_sequence=?,mp_reclaim_operation_id=?,mp_reclaim_request_digest=?,mp_reclaim_command_kind=?,mp_reclaim_result_version=?,mp_reclaim_result_count=?,mp_reclaim_result_digest=?,mp_reclaim_result_payload=?,mp_reclaim_captured_ack=?,mp_reclaim_before_cursor=?,mp_reclaim_removed_sequence=?,mp_reclaim_removed_bytes=?,mp_reclaim_after_cursor=?,mp_reclaim_no_next=?,mp_reclaim_applied_ms=?,mp_owner_revision=mp_owner_revision+1 WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision IS ? AND mp_command_last_sequence IS ? AND mp_command_acknowledged_sequence IS ? AND mp_receipt_rows IS ? AND mp_receipt_variable_bytes IS ? AND mp_receipt_reclaim_after_sequence IS ? AND mp_receipt_reclaim_due_ms IS ? AND mp_reclaim_sequence IS ? AND mp_reclaim_operation_id IS ? AND mp_reclaim_request_digest IS ? AND mp_reclaim_result_digest IS ? AND coalesce(octet_length(mp_reclaim_result_payload),0)=? AND mp_control_variable_bytes=? RETURNING mp_control_variable_bytes`, "iiiiiibbiiibbiiiiiiisbiiiiiiiibbbii", stmt.Write, []int{4, 15, 29, 30, 31, 32}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.queue.octets", `SELECT octet_length(mp_reclaim_result_payload) FROM queues_v1 WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.queue.load", `SELECT mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_command_kind,mp_reclaim_result_version,mp_reclaim_result_count,mp_reclaim_result_digest,mp_reclaim_result_payload,mp_reclaim_applied_ms FROM queues_v1 WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.next.current", `SELECT mp_owner_revision,mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes,mp_receipt_reclaim_after_sequence,mp_receipt_reclaim_due_ms,mp_reclaim_sequence,mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_result_digest,coalesce(octet_length(mp_reclaim_result_payload),0),mp_control_variable_bytes FROM playback_v1_prepared WHERE token=? AND mp_command_incarnation=?`, "sb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.next.first", `SELECT command_sequence,variable_bytes FROM mp_next_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=? ORDER BY command_sequence LIMIT 1`, "sbii", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.next.delete", `DELETE FROM mp_next_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND variable_bytes=? RETURNING variable_bytes`, "sbii", stmt.Write, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.next.store", `UPDATE playback_v1_prepared SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=?,mp_receipt_reclaim_after_sequence=?,mp_receipt_reclaim_due_ms=?,mp_reclaim_sequence=?,mp_reclaim_operation_id=?,mp_reclaim_request_digest=?,mp_reclaim_command_kind=?,mp_reclaim_result_version=?,mp_reclaim_result_count=?,mp_reclaim_result_digest=?,mp_reclaim_result_payload=?,mp_reclaim_captured_ack=?,mp_reclaim_before_cursor=?,mp_reclaim_removed_sequence=?,mp_reclaim_removed_bytes=?,mp_reclaim_after_cursor=?,mp_reclaim_no_next=?,mp_reclaim_applied_ms=?,mp_owner_revision=mp_owner_revision+1 WHERE token=? AND mp_command_incarnation=? AND mp_owner_revision IS ? AND mp_command_last_sequence IS ? AND mp_command_acknowledged_sequence IS ? AND mp_receipt_rows IS ? AND mp_receipt_variable_bytes IS ? AND mp_receipt_reclaim_after_sequence IS ? AND mp_receipt_reclaim_due_ms IS ? AND mp_reclaim_sequence IS ? AND mp_reclaim_operation_id IS ? AND mp_reclaim_request_digest IS ? AND mp_reclaim_result_digest IS ? AND coalesce(octet_length(mp_reclaim_result_payload),0)=? AND mp_control_variable_bytes=? RETURNING mp_control_variable_bytes`, "iiiiiibbiiibbiiiiiiisbiiiiiiiibbbii", stmt.Write, []int{4, 15, 29, 30, 31, 32}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.next.octets", `SELECT octet_length(mp_reclaim_result_payload) FROM playback_v1_prepared WHERE token=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}))
	defs = append(defs, mpReceiptReclaimDefinition("mp.reclaim.next.load", `SELECT mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_command_kind,mp_reclaim_result_version,mp_reclaim_result_count,mp_reclaim_result_digest,mp_reclaim_result_payload,mp_reclaim_applied_ms FROM playback_v1_prepared WHERE token=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}))
	return defs
}
func mpReceiptReclaimDefinition(id, sql, types string, kind stmt.Kind, nullable []int) stmt.Definition {
	d := mpPrepDefinition(id, sql, types, kind)
	d.Owner = "store/writer MP concrete receipt reclaim"
	d.CallSites = []string{"store/writer/mp_receipt_reclaim.go"}
	for _, i := range nullable {
		d.Params[i].Nullable = true
	}
	return d
}

// Reports exact native fixed-slot BLOB debt before W reserves memory. No copy,
// codec, admission grant or source/root reconstruction occurs here.
func mpReclaimPayloadBytes(ctx context.Context, t *commandTurn, q mpReclaimInvocation, p mpReclaimPoints) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, p.kind); err != nil {
		return
	}
	if q.decision != receiptReplay || !mpPrepTexts(q.ownerID) || q.identity.Sequence <= 0 || q.identity.Incarnation == ([16]byte{}) || q.identity.OperationID == ([16]byte{}) || q.request == (RequestDigest{}) {
		return 0, ErrResolveRequired
	}
	row, e := mpPrepRow(ctx, t, p.id("octets"), q.ownerID, q.identity.Incarnation[:], q.identity.Sequence, q.identity.OperationID[:], q.request[:])
	if e != nil {
		return 0, e
	}
	if err = row.Scan(&out); err != nil {
		return
	}
	if out < 1 || out > 4096 {
		return 0, ErrResolveRequired
	}
	return out + 80, nil
}
func mpStartReclaimPayloadBytes(ctx context.Context, t *commandTurn, q mpStartReclaim) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return mpReclaimPayloadBytes(ctx, t, q.invocation, mpStartReclaimPoints())
}
func mpPhysicalReclaimPayloadBytes(ctx context.Context, t *commandTurn, q mpPhysicalReclaim) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return mpReclaimPayloadBytes(ctx, t, q.invocation, mpPhysicalReclaimPoints())
}
func mpSessionReclaimPayloadBytes(ctx context.Context, t *commandTurn, q mpSessionReclaim) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return mpReclaimPayloadBytes(ctx, t, q.invocation, mpSessionReclaimPoints())
}
func mpJobReclaimPayloadBytes(ctx context.Context, t *commandTurn, q mpJobReclaim) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return mpReclaimPayloadBytes(ctx, t, q.invocation, mpJobReclaimPoints())
}
func mpVersionReclaimPayloadBytes(ctx context.Context, t *commandTurn, q mpVersionReclaim) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return mpReclaimPayloadBytes(ctx, t, q.invocation, mpVersionReclaimPoints())
}
func mpQueueReclaimPayloadBytes(ctx context.Context, t *commandTurn, q mpQueueReclaim) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return mpReclaimPayloadBytes(ctx, t, q.invocation, mpQueueReclaimPoints())
}
func mpNextReclaimPayloadBytes(ctx context.Context, t *commandTurn, q mpNextReclaim) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	return mpReclaimPayloadBytes(ctx, t, q.invocation, mpNextReclaimPoints())
}
