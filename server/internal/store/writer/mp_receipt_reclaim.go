package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Per-actual-owner closed reclamation. There is no polymorphic persistent
// receipt store, public owner selector, authority capability, IO or commit here.
// W calls only after current maintenance disclosure and original receiptFresh;
// it charges returned root-control delta and applies refunds only on commit.
type mpReclaimInvocation struct {
	ownerID   string
	identity  CommandIdentity
	request   RequestDigest
	appliedMS int64
	decision  receiptDecision
}
type mpReclaimResult struct {
	ownerID                                                                           string
	identity                                                                          CommandIdentity
	kind                                                                              CommandID
	request                                                                           RequestDigest
	appliedMS, capturedACK, beforeCursor, afterCursor                                 int64
	beforeChild, afterChild                                                           string
	beforeOrdinal, afterOrdinal                                                       int64
	removedRows, removedBytes, beforeRows, beforeBytes, remainingRows, remainingBytes int64
	finished                                                                          bool
	digest                                                                            ResultDigest
	oldControlBytes, newControlBytes                                                  int64 // native observations, not receipt fields
}
type mpReclaimRoot struct {
	last, ack, rows, bytes, cursor, ordinal, slotSequence, controlBytes int64
	child                                                               string
	due                                                                 sql.NullInt64
	slotOperation, slotRequest, slotDigest                              []byte
}
type mpReclaimPoints struct {
	kind                                      CommandID
	family                                    string
	current, first, deleteHeader, store, load stmt.ID
	children                                  []mpReclaimChildPoints
}
type mpReclaimChildPoints struct {
	kind          string
	first, remove stmt.ID
}

func mpReclaimChildForKind(kind CommandID, child string) bool {
	if child == "" {
		return kind >= 781 && kind <= 787
	}
	switch kind {
	case 782:
		return child == "intervals"
	case 783, 787:
		return child == "retire_media"
	case 786:
		return child == "segments" || child == "retire_media"
	}
	return false
}
func mpReclaimDigest(r mpReclaimResult) (ResultDigest, error) {
	if !mpReclaimChildForKind(r.kind, r.beforeChild) || !mpReclaimChildForKind(r.kind, r.afterChild) || r.ownerID == "" || r.kind < 781 || r.kind > 787 || r.identity.Sequence <= 0 || r.identity.Incarnation == ([16]byte{}) || r.identity.OperationID == ([16]byte{}) || r.request == (RequestDigest{}) || r.appliedMS <= 0 || r.capturedACK < 0 || r.beforeCursor < 0 || r.afterCursor < r.beforeCursor || r.afterCursor > r.capturedACK || r.beforeOrdinal < -1 || r.afterOrdinal < -1 || (r.beforeChild == "") != (r.beforeOrdinal == -1) || (r.afterChild == "") != (r.afterOrdinal == -1) || r.removedRows < 0 || r.removedRows > 1 || r.removedBytes < 0 || r.beforeRows < r.removedRows || r.beforeBytes < r.removedBytes || r.remainingRows != r.beforeRows-r.removedRows || r.remainingBytes != r.beforeBytes-r.removedBytes || (r.remainingRows == 0) != (r.remainingBytes == 0) || r.finished && (r.remainingRows != 0 || r.afterChild != "") {
		return ResultDigest{}, ErrResolveRequired
	}
	h := mpPrepReceiptHeader{identity: r.identity, request: r.request, kind: r.kind, appliedMS: r.appliedMS}
	e, err := mpPrepReceiptEncoder(h, "mp_actual_owner_reclaim")
	if err != nil {
		return ResultDigest{}, err
	}
	e.text(r.ownerID)
	e.text(r.beforeChild)
	e.text(r.afterChild)
	for _, n := range []int64{r.appliedMS, r.capturedACK, r.beforeCursor, r.afterCursor, r.beforeOrdinal + 1, r.afterOrdinal + 1, r.removedRows, r.removedBytes, r.beforeRows, r.beforeBytes, r.remainingRows, r.remainingBytes} {
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
func mpReclaimFirst(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (sequence, bytes int64, found bool, err error) {
	row, e := mpPrepRow(ctx, t, id, args...)
	if e != nil {
		return 0, 0, false, e
	}
	e = row.Scan(&sequence, &bytes)
	if errors.Is(e, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	if e != nil {
		return 0, 0, false, e
	}
	if sequence < 0 || bytes <= 0 {
		return 0, 0, false, ErrResolveRequired
	}
	return sequence, bytes, true, nil
}
func reclaimMPReceipt(ctx context.Context, t *commandTurn, q mpReclaimInvocation, p mpReclaimPoints) (out mpReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, p.kind); err != nil {
		return out, err
	}
	if q.decision != receiptFresh || !mpPrepTexts(q.ownerID) || q.identity.Sequence <= 0 || q.identity.Incarnation == ([16]byte{}) || q.identity.OperationID == ([16]byte{}) || q.request == (RequestDigest{}) || q.appliedMS <= 0 {
		return out, ErrResolveRequired
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return out, err
	}
	var b mpReclaimRoot
	row, e := mpPrepRow(ctx, t, p.current, q.ownerID, q.identity.Incarnation[:])
	if e != nil {
		return out, e
	}
	if e = row.Scan(&b.last, &b.ack, &b.rows, &b.bytes, &b.cursor, &b.child, &b.ordinal, &b.due, &b.slotSequence, &b.slotOperation, &b.slotRequest, &b.slotDigest, &b.controlBytes); e != nil {
		return out, e
	}
	if b.last < 0 || b.last == math.MaxInt64 || b.last != b.ack || q.identity.Sequence != b.last+1 || b.rows < 0 || b.bytes < 0 || (b.rows == 0) != (b.bytes == 0) || b.cursor < 0 || b.cursor > b.ack || b.ordinal < -1 || (b.child == "") != (b.ordinal == -1) || !b.due.Valid || b.due.Int64 < 0 || b.due.Int64 > q.appliedMS || b.slotSequence < 0 || b.slotSequence > b.ack || b.controlBytes < 0 {
		return out, ErrResolveRequired
	}
	if b.slotSequence == 0 && (len(b.slotOperation) != 0 || len(b.slotRequest) != 0 || len(b.slotDigest) != 0) || b.slotSequence > 0 && (len(b.slotOperation) != 16 || len(b.slotRequest) != 32 || len(b.slotDigest) != 32) {
		return out, ErrResolveRequired
	}
	out = mpReclaimResult{ownerID: q.ownerID, identity: q.identity, kind: p.kind, request: q.request, appliedMS: q.appliedMS, capturedACK: b.ack, beforeCursor: b.cursor, afterCursor: b.cursor, beforeChild: b.child, afterChild: b.child, beforeOrdinal: b.ordinal, afterOrdinal: b.ordinal, beforeRows: b.rows, beforeBytes: b.bytes, remainingRows: b.rows, remainingBytes: b.bytes, oldControlBytes: b.controlBytes}
	seq, nativeBytes, found, e := mpReclaimFirst(ctx, t, p.first, q.ownerID, q.identity.Incarnation[:], b.cursor, b.ack)
	if e != nil {
		return out, e
	}
	if !found {
		if b.rows != 0 || b.child != "" {
			return out, ErrResolveRequired
		}
		out.afterCursor = b.ack
		out.finished = true
	} else {
		if seq <= b.cursor || seq > b.ack || b.rows == 0 {
			return out, ErrResolveRequired
		}
		start := 0
		if b.child != "" {
			start = -1
			for i, c := range p.children {
				if c.kind == b.child {
					start = i
				}
			}
			if start < 0 {
				return out, ErrResolveRequired
			}
		}
		removedChild := false
		for i := start; i < len(p.children); i++ {
			c := p.children[i]
			after := int64(-1)
			if c.kind == b.child {
				after = b.ordinal
			}
			ordinal, childBytes, exists, e := mpReclaimFirst(ctx, t, c.first, q.ownerID, q.identity.Incarnation[:], seq, after)
			if e != nil {
				return out, e
			}
			if !exists {
				continue
			}
			r, e := mpPrepRow(ctx, t, c.remove, q.ownerID, q.identity.Incarnation[:], seq, ordinal, childBytes)
			if e != nil {
				return out, e
			}
			var removed int64
			if e = r.Scan(&removed); e != nil {
				return out, e
			}
			if removed != childBytes {
				return out, ErrResolveRequired
			}
			out.removedRows, out.removedBytes = 1, removed
			out.afterChild, out.afterOrdinal = c.kind, ordinal
			removedChild = true
			break
		}
		if !removedChild {
			r, e := mpPrepRow(ctx, t, p.deleteHeader, q.ownerID, q.identity.Incarnation[:], seq, nativeBytes)
			if e != nil {
				return out, e
			}
			var removed int64
			if e = r.Scan(&removed); e != nil {
				return out, e
			}
			if removed != nativeBytes {
				return out, ErrResolveRequired
			}
			out.removedRows, out.removedBytes = 1, removed
			out.afterCursor, out.afterChild, out.afterOrdinal = seq, "", -1
		}
		if out.removedRows > b.rows || out.removedBytes > b.bytes {
			return out, ErrResolveRequired
		}
		out.remainingRows, out.remainingBytes = b.rows-out.removedRows, b.bytes-out.removedBytes
		if (out.remainingRows == 0) != (out.remainingBytes == 0) {
			return out, ErrResolveRequired
		}
		if !removedChild {
			_, _, more, e := mpReclaimFirst(ctx, t, p.first, q.ownerID, q.identity.Incarnation[:], out.afterCursor, b.ack)
			if e != nil {
				return out, e
			}
			out.finished = !more
			if out.finished && out.remainingRows != 0 {
				return out, ErrResolveRequired
			}
			if out.finished {
				out.afterCursor = b.ack
			}
		}
	}
	out.digest, err = mpReclaimDigest(out)
	if err != nil {
		return out, err
	}
	var due any
	if !out.finished {
		due = q.appliedMS
	}
	var oldOp, oldReq, oldResult any
	if b.slotSequence > 0 {
		oldOp = b.slotOperation
		oldReq = b.slotRequest
		oldResult = b.slotDigest
	}
	finished := int64(0)
	if out.finished {
		finished = 1
	}
	args := []any{q.identity.Sequence, out.remainingRows, out.remainingBytes, out.afterCursor, out.afterChild, out.afterOrdinal, due, q.identity.Sequence, q.identity.OperationID[:], q.request[:], out.digest[:], q.appliedMS, b.ack, b.cursor, out.afterCursor, out.removedRows, out.removedBytes, b.rows, b.bytes, out.remainingRows, out.remainingBytes, finished, int64(p.kind), b.child, out.afterChild, b.ordinal, out.afterOrdinal, q.ownerID, q.identity.Incarnation[:], b.last, b.ack, b.rows, b.bytes, b.cursor, b.child, b.ordinal, b.due.Int64, b.slotSequence, oldOp, oldReq, oldResult, b.controlBytes}
	row, e = mpPrepRow(ctx, t, p.store, args...)
	if e != nil {
		return out, e
	}
	if e = row.Scan(&out.newControlBytes); e != nil {
		return out, e
	}
	if out.newControlBytes <= 0 {
		return out, ErrResolveRequired
	}
	return out, t.checkLifetimes(ctx)
}

// Replay reads only the requested original fixed slot after W's current
// disclosure/classification; it performs no due/source/debt/generation CAS.
func loadMPReclaimReceipt(ctx context.Context, t *commandTurn, q mpReclaimInvocation, p mpReclaimPoints) (out mpReclaimResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, p.kind); err != nil {
		return out, err
	}
	if q.decision != receiptReplay {
		return out, ErrResolveRequired
	}
	out = mpReclaimResult{ownerID: q.ownerID, identity: q.identity, kind: p.kind, request: q.request}
	row, e := mpPrepRow(ctx, t, p.load, q.ownerID, q.identity.Incarnation[:], q.identity.Sequence, q.identity.OperationID[:], q.request[:])
	if e != nil {
		return out, e
	}
	var digest []byte
	var kind, version, count, finished int64
	if e = row.Scan(&kind, &version, &count, &digest, &out.appliedMS, &out.capturedACK, &out.beforeCursor, &out.afterCursor, &out.removedRows, &out.removedBytes, &out.beforeRows, &out.beforeBytes, &out.remainingRows, &out.remainingBytes, &finished, &out.beforeChild, &out.afterChild, &out.beforeOrdinal, &out.afterOrdinal); e != nil {
		return out, e
	}
	out.finished = finished == 1
	d, e := mpReclaimDigest(out)
	if e != nil || kind != int64(p.kind) || version != 1 || count != 1 || finished < 0 || finished > 1 || len(digest) != 32 || string(digest) != string(d[:]) {
		return out, errors.Join(ErrResolveRequired, e)
	}
	out.digest = d
	return out, t.checkLifetimes(ctx)
}

func mpStartReclaimPoints() mpReclaimPoints {
	return mpReclaimPoints{kind: 781, family: "start", current: "mp.reclaim.start.current", first: "mp.reclaim.start.first", deleteHeader: "mp.reclaim.start.delete", store: "mp.reclaim.start.store", load: "mp.reclaim.start.load", children: []mpReclaimChildPoints{}}
}

type mpStartReclaim struct{ invocation mpReclaimInvocation }

func reclaimMPStartReceipt(ctx context.Context, t *commandTurn, q mpStartReclaim) (mpReclaimResult, error) {
	return reclaimMPReceipt(ctx, t, q.invocation, mpStartReclaimPoints())
}

func mpPhysicalReclaimPoints() mpReclaimPoints {
	return mpReclaimPoints{kind: 782, family: "physical", current: "mp.reclaim.physical.current", first: "mp.reclaim.physical.first", deleteHeader: "mp.reclaim.physical.delete", store: "mp.reclaim.physical.store", load: "mp.reclaim.physical.load", children: []mpReclaimChildPoints{{kind: "intervals", first: "mp.reclaim.physical.intervals.first", remove: "mp.reclaim.physical.intervals.delete"}}}
}

type mpPhysicalReclaim struct{ invocation mpReclaimInvocation }

func reclaimMPPhysicalReceipt(ctx context.Context, t *commandTurn, q mpPhysicalReclaim) (mpReclaimResult, error) {
	return reclaimMPReceipt(ctx, t, q.invocation, mpPhysicalReclaimPoints())
}

func mpSessionReclaimPoints() mpReclaimPoints {
	return mpReclaimPoints{kind: 783, family: "session", current: "mp.reclaim.session.current", first: "mp.reclaim.session.first", deleteHeader: "mp.reclaim.session.delete", store: "mp.reclaim.session.store", load: "mp.reclaim.session.load", children: []mpReclaimChildPoints{{kind: "retire_media", first: "mp.reclaim.session.retire_media.first", remove: "mp.reclaim.session.retire_media.delete"}}}
}

type mpSessionReclaim struct{ invocation mpReclaimInvocation }

func reclaimMPSessionReceipt(ctx context.Context, t *commandTurn, q mpSessionReclaim) (mpReclaimResult, error) {
	return reclaimMPReceipt(ctx, t, q.invocation, mpSessionReclaimPoints())
}

func mpJobReclaimPoints() mpReclaimPoints {
	return mpReclaimPoints{kind: 784, family: "job", current: "mp.reclaim.job.current", first: "mp.reclaim.job.first", deleteHeader: "mp.reclaim.job.delete", store: "mp.reclaim.job.store", load: "mp.reclaim.job.load", children: []mpReclaimChildPoints{}}
}

type mpJobReclaim struct{ invocation mpReclaimInvocation }

func reclaimMPJobReceipt(ctx context.Context, t *commandTurn, q mpJobReclaim) (mpReclaimResult, error) {
	return reclaimMPReceipt(ctx, t, q.invocation, mpJobReclaimPoints())
}

func mpVersionReclaimPoints() mpReclaimPoints {
	return mpReclaimPoints{kind: 785, family: "version", current: "mp.reclaim.version.current", first: "mp.reclaim.version.first", deleteHeader: "mp.reclaim.version.delete", store: "mp.reclaim.version.store", load: "mp.reclaim.version.load", children: []mpReclaimChildPoints{}}
}

type mpVersionReclaim struct{ invocation mpReclaimInvocation }

func reclaimMPVersionReceipt(ctx context.Context, t *commandTurn, q mpVersionReclaim) (mpReclaimResult, error) {
	return reclaimMPReceipt(ctx, t, q.invocation, mpVersionReclaimPoints())
}

func mpQueueReclaimPoints() mpReclaimPoints {
	return mpReclaimPoints{kind: 786, family: "queue", current: "mp.reclaim.queue.current", first: "mp.reclaim.queue.first", deleteHeader: "mp.reclaim.queue.delete", store: "mp.reclaim.queue.store", load: "mp.reclaim.queue.load", children: []mpReclaimChildPoints{{kind: "segments", first: "mp.reclaim.queue.segments.first", remove: "mp.reclaim.queue.segments.delete"}, {kind: "retire_media", first: "mp.reclaim.queue.retire_media.first", remove: "mp.reclaim.queue.retire_media.delete"}}}
}

type mpQueueReclaim struct{ invocation mpReclaimInvocation }

func reclaimMPQueueReceipt(ctx context.Context, t *commandTurn, q mpQueueReclaim) (mpReclaimResult, error) {
	return reclaimMPReceipt(ctx, t, q.invocation, mpQueueReclaimPoints())
}

func mpNextReclaimPoints() mpReclaimPoints {
	return mpReclaimPoints{kind: 787, family: "next", current: "mp.reclaim.next.current", first: "mp.reclaim.next.first", deleteHeader: "mp.reclaim.next.delete", store: "mp.reclaim.next.store", load: "mp.reclaim.next.load", children: []mpReclaimChildPoints{{kind: "retire_media", first: "mp.reclaim.next.retire_media.first", remove: "mp.reclaim.next.retire_media.delete"}}}
}

type mpNextReclaim struct{ invocation mpReclaimInvocation }

func reclaimMPNextReceipt(ctx context.Context, t *commandTurn, q mpNextReclaim) (mpReclaimResult, error) {
	return reclaimMPReceipt(ctx, t, q.invocation, mpNextReclaimPoints())
}

func mpReceiptReclaimDefinitions() []stmt.Definition {
	return []stmt.Definition{
		mpReceiptReclaimDefinition("mp.reclaim.start.current", `SELECT mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes,mp_receipt_reclaim_after_sequence,mp_receipt_reclaim_child_kind,mp_receipt_reclaim_child_ordinal,mp_receipt_reclaim_due_ms,mp_reclaim_sequence,mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_result_digest,mp_control_variable_bytes FROM playback_start_preparations WHERE id=? AND mp_command_incarnation=?`, "sb", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.start.first", `SELECT command_sequence,variable_bytes FROM mp_start_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=? ORDER BY command_sequence LIMIT 1`, "sbii", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.start.delete", `DELETE FROM mp_start_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND variable_bytes=? RETURNING variable_bytes`, "sbii", stmt.Write, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.start.store", `UPDATE playback_start_preparations SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=?,mp_receipt_reclaim_after_sequence=?,mp_receipt_reclaim_child_kind=?,mp_receipt_reclaim_child_ordinal=?,mp_receipt_reclaim_due_ms=?,mp_reclaim_sequence=?,mp_reclaim_operation_id=?,mp_reclaim_request_digest=?,mp_reclaim_result_digest=?,mp_reclaim_applied_ms=?,mp_reclaim_captured_ack=?,mp_reclaim_before_cursor=?,mp_reclaim_after_cursor=?,mp_reclaim_removed_rows=?,mp_reclaim_removed_bytes=?,mp_reclaim_before_rows=?,mp_reclaim_before_bytes=?,mp_reclaim_result_rows=?,mp_reclaim_result_bytes=?,mp_reclaim_finished=?,mp_reclaim_command_kind=?,mp_reclaim_before_child_kind=?,mp_reclaim_after_child_kind=?,mp_reclaim_before_child_ordinal=?,mp_reclaim_after_child_ordinal=?,mp_reclaim_result_version=1,mp_reclaim_result_count=1 WHERE id=? AND mp_command_incarnation=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_child_kind=? AND mp_receipt_reclaim_child_ordinal=? AND mp_receipt_reclaim_due_ms=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id IS ? AND mp_reclaim_request_digest IS ? AND mp_reclaim_result_digest IS ? AND mp_control_variable_bytes=? RETURNING mp_control_variable_bytes`, "iiiisiiibbbiiiiiiiiiiiissiisbiiiiisiiibbbi", stmt.Write, []int{6, 38, 39, 40}),
		mpReceiptReclaimDefinition("mp.reclaim.start.load", `SELECT mp_reclaim_command_kind,mp_reclaim_result_version,mp_reclaim_result_count,mp_reclaim_result_digest,mp_reclaim_applied_ms,mp_reclaim_captured_ack,mp_reclaim_before_cursor,mp_reclaim_after_cursor,mp_reclaim_removed_rows,mp_reclaim_removed_bytes,mp_reclaim_before_rows,mp_reclaim_before_bytes,mp_reclaim_result_rows,mp_reclaim_result_bytes,mp_reclaim_finished,mp_reclaim_before_child_kind,mp_reclaim_after_child_kind,mp_reclaim_before_child_ordinal,mp_reclaim_after_child_ordinal FROM playback_start_preparations WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.physical.current", `SELECT mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes,mp_receipt_reclaim_after_sequence,mp_receipt_reclaim_child_kind,mp_receipt_reclaim_child_ordinal,mp_receipt_reclaim_due_ms,mp_reclaim_sequence,mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_result_digest,mp_control_variable_bytes FROM playback_sessions WHERE id=? AND mp_command_incarnation=?`, "sb", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.physical.first", `SELECT command_sequence,variable_bytes FROM mp_physical_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=? ORDER BY command_sequence LIMIT 1`, "sbii", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.physical.delete", `DELETE FROM mp_physical_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND variable_bytes=? RETURNING variable_bytes`, "sbii", stmt.Write, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.physical.intervals.first", `SELECT ordinal,variable_bytes FROM mp_physical_receipt_intervals WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal>? ORDER BY ordinal LIMIT 1`, "sbii", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.physical.intervals.delete", `DELETE FROM mp_physical_receipt_intervals WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal=? AND variable_bytes=? RETURNING variable_bytes`, "sbiii", stmt.Write, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.physical.store", `UPDATE playback_sessions SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=?,mp_receipt_reclaim_after_sequence=?,mp_receipt_reclaim_child_kind=?,mp_receipt_reclaim_child_ordinal=?,mp_receipt_reclaim_due_ms=?,mp_reclaim_sequence=?,mp_reclaim_operation_id=?,mp_reclaim_request_digest=?,mp_reclaim_result_digest=?,mp_reclaim_applied_ms=?,mp_reclaim_captured_ack=?,mp_reclaim_before_cursor=?,mp_reclaim_after_cursor=?,mp_reclaim_removed_rows=?,mp_reclaim_removed_bytes=?,mp_reclaim_before_rows=?,mp_reclaim_before_bytes=?,mp_reclaim_result_rows=?,mp_reclaim_result_bytes=?,mp_reclaim_finished=?,mp_reclaim_command_kind=?,mp_reclaim_before_child_kind=?,mp_reclaim_after_child_kind=?,mp_reclaim_before_child_ordinal=?,mp_reclaim_after_child_ordinal=?,mp_reclaim_result_version=1,mp_reclaim_result_count=1 WHERE id=? AND mp_command_incarnation=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_child_kind=? AND mp_receipt_reclaim_child_ordinal=? AND mp_receipt_reclaim_due_ms=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id IS ? AND mp_reclaim_request_digest IS ? AND mp_reclaim_result_digest IS ? AND mp_control_variable_bytes=? RETURNING mp_control_variable_bytes`, "iiiisiiibbbiiiiiiiiiiiissiisbiiiiisiiibbbi", stmt.Write, []int{6, 38, 39, 40}),
		mpReceiptReclaimDefinition("mp.reclaim.physical.load", `SELECT mp_reclaim_command_kind,mp_reclaim_result_version,mp_reclaim_result_count,mp_reclaim_result_digest,mp_reclaim_applied_ms,mp_reclaim_captured_ack,mp_reclaim_before_cursor,mp_reclaim_after_cursor,mp_reclaim_removed_rows,mp_reclaim_removed_bytes,mp_reclaim_before_rows,mp_reclaim_before_bytes,mp_reclaim_result_rows,mp_reclaim_result_bytes,mp_reclaim_finished,mp_reclaim_before_child_kind,mp_reclaim_after_child_kind,mp_reclaim_before_child_ordinal,mp_reclaim_after_child_ordinal FROM playback_sessions WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.session.current", `SELECT mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes,mp_receipt_reclaim_after_sequence,mp_receipt_reclaim_child_kind,mp_receipt_reclaim_child_ordinal,mp_receipt_reclaim_due_ms,mp_reclaim_sequence,mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_result_digest,mp_control_variable_bytes FROM playback_v1_sessions WHERE id=? AND mp_command_incarnation=?`, "sb", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.session.first", `SELECT command_sequence,variable_bytes FROM mp_session_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=? ORDER BY command_sequence LIMIT 1`, "sbii", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.session.delete", `DELETE FROM mp_session_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND variable_bytes=? RETURNING variable_bytes`, "sbii", stmt.Write, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.session.retire_media.first", `SELECT ordinal,variable_bytes FROM mp_session_receipt_retire_media WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal>? ORDER BY ordinal LIMIT 1`, "sbii", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.session.retire_media.delete", `DELETE FROM mp_session_receipt_retire_media WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal=? AND variable_bytes=? RETURNING variable_bytes`, "sbiii", stmt.Write, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.session.store", `UPDATE playback_v1_sessions SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=?,mp_receipt_reclaim_after_sequence=?,mp_receipt_reclaim_child_kind=?,mp_receipt_reclaim_child_ordinal=?,mp_receipt_reclaim_due_ms=?,mp_reclaim_sequence=?,mp_reclaim_operation_id=?,mp_reclaim_request_digest=?,mp_reclaim_result_digest=?,mp_reclaim_applied_ms=?,mp_reclaim_captured_ack=?,mp_reclaim_before_cursor=?,mp_reclaim_after_cursor=?,mp_reclaim_removed_rows=?,mp_reclaim_removed_bytes=?,mp_reclaim_before_rows=?,mp_reclaim_before_bytes=?,mp_reclaim_result_rows=?,mp_reclaim_result_bytes=?,mp_reclaim_finished=?,mp_reclaim_command_kind=?,mp_reclaim_before_child_kind=?,mp_reclaim_after_child_kind=?,mp_reclaim_before_child_ordinal=?,mp_reclaim_after_child_ordinal=?,mp_reclaim_result_version=1,mp_reclaim_result_count=1 WHERE id=? AND mp_command_incarnation=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_child_kind=? AND mp_receipt_reclaim_child_ordinal=? AND mp_receipt_reclaim_due_ms=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id IS ? AND mp_reclaim_request_digest IS ? AND mp_reclaim_result_digest IS ? AND mp_control_variable_bytes=? RETURNING mp_control_variable_bytes`, "iiiisiiibbbiiiiiiiiiiiissiisbiiiiisiiibbbi", stmt.Write, []int{6, 38, 39, 40}),
		mpReceiptReclaimDefinition("mp.reclaim.session.load", `SELECT mp_reclaim_command_kind,mp_reclaim_result_version,mp_reclaim_result_count,mp_reclaim_result_digest,mp_reclaim_applied_ms,mp_reclaim_captured_ack,mp_reclaim_before_cursor,mp_reclaim_after_cursor,mp_reclaim_removed_rows,mp_reclaim_removed_bytes,mp_reclaim_before_rows,mp_reclaim_before_bytes,mp_reclaim_result_rows,mp_reclaim_result_bytes,mp_reclaim_finished,mp_reclaim_before_child_kind,mp_reclaim_after_child_kind,mp_reclaim_before_child_ordinal,mp_reclaim_after_child_ordinal FROM playback_v1_sessions WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.job.current", `SELECT mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes,mp_receipt_reclaim_after_sequence,mp_receipt_reclaim_child_kind,mp_receipt_reclaim_child_ordinal,mp_receipt_reclaim_due_ms,mp_reclaim_sequence,mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_result_digest,mp_control_variable_bytes FROM prepared_media_jobs WHERE id=? AND mp_command_incarnation=?`, "sb", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.job.first", `SELECT command_sequence,variable_bytes FROM mp_job_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=? ORDER BY command_sequence LIMIT 1`, "sbii", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.job.delete", `DELETE FROM mp_job_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND variable_bytes=? RETURNING variable_bytes`, "sbii", stmt.Write, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.job.store", `UPDATE prepared_media_jobs SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=?,mp_receipt_reclaim_after_sequence=?,mp_receipt_reclaim_child_kind=?,mp_receipt_reclaim_child_ordinal=?,mp_receipt_reclaim_due_ms=?,mp_reclaim_sequence=?,mp_reclaim_operation_id=?,mp_reclaim_request_digest=?,mp_reclaim_result_digest=?,mp_reclaim_applied_ms=?,mp_reclaim_captured_ack=?,mp_reclaim_before_cursor=?,mp_reclaim_after_cursor=?,mp_reclaim_removed_rows=?,mp_reclaim_removed_bytes=?,mp_reclaim_before_rows=?,mp_reclaim_before_bytes=?,mp_reclaim_result_rows=?,mp_reclaim_result_bytes=?,mp_reclaim_finished=?,mp_reclaim_command_kind=?,mp_reclaim_before_child_kind=?,mp_reclaim_after_child_kind=?,mp_reclaim_before_child_ordinal=?,mp_reclaim_after_child_ordinal=?,mp_reclaim_result_version=1,mp_reclaim_result_count=1 WHERE id=? AND mp_command_incarnation=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_child_kind=? AND mp_receipt_reclaim_child_ordinal=? AND mp_receipt_reclaim_due_ms=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id IS ? AND mp_reclaim_request_digest IS ? AND mp_reclaim_result_digest IS ? AND mp_control_variable_bytes=? RETURNING mp_control_variable_bytes`, "iiiisiiibbbiiiiiiiiiiiissiisbiiiiisiiibbbi", stmt.Write, []int{6, 38, 39, 40}),
		mpReceiptReclaimDefinition("mp.reclaim.job.load", `SELECT mp_reclaim_command_kind,mp_reclaim_result_version,mp_reclaim_result_count,mp_reclaim_result_digest,mp_reclaim_applied_ms,mp_reclaim_captured_ack,mp_reclaim_before_cursor,mp_reclaim_after_cursor,mp_reclaim_removed_rows,mp_reclaim_removed_bytes,mp_reclaim_before_rows,mp_reclaim_before_bytes,mp_reclaim_result_rows,mp_reclaim_result_bytes,mp_reclaim_finished,mp_reclaim_before_child_kind,mp_reclaim_after_child_kind,mp_reclaim_before_child_ordinal,mp_reclaim_after_child_ordinal FROM prepared_media_jobs WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.version.current", `SELECT mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes,mp_receipt_reclaim_after_sequence,mp_receipt_reclaim_child_kind,mp_receipt_reclaim_child_ordinal,mp_receipt_reclaim_due_ms,mp_reclaim_sequence,mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_result_digest,mp_control_variable_bytes FROM prepared_media_versions WHERE id=? AND mp_command_incarnation=?`, "sb", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.version.first", `SELECT command_sequence,variable_bytes FROM mp_version_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=? ORDER BY command_sequence LIMIT 1`, "sbii", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.version.delete", `DELETE FROM mp_version_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND variable_bytes=? RETURNING variable_bytes`, "sbii", stmt.Write, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.version.store", `UPDATE prepared_media_versions SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=?,mp_receipt_reclaim_after_sequence=?,mp_receipt_reclaim_child_kind=?,mp_receipt_reclaim_child_ordinal=?,mp_receipt_reclaim_due_ms=?,mp_reclaim_sequence=?,mp_reclaim_operation_id=?,mp_reclaim_request_digest=?,mp_reclaim_result_digest=?,mp_reclaim_applied_ms=?,mp_reclaim_captured_ack=?,mp_reclaim_before_cursor=?,mp_reclaim_after_cursor=?,mp_reclaim_removed_rows=?,mp_reclaim_removed_bytes=?,mp_reclaim_before_rows=?,mp_reclaim_before_bytes=?,mp_reclaim_result_rows=?,mp_reclaim_result_bytes=?,mp_reclaim_finished=?,mp_reclaim_command_kind=?,mp_reclaim_before_child_kind=?,mp_reclaim_after_child_kind=?,mp_reclaim_before_child_ordinal=?,mp_reclaim_after_child_ordinal=?,mp_reclaim_result_version=1,mp_reclaim_result_count=1 WHERE id=? AND mp_command_incarnation=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_child_kind=? AND mp_receipt_reclaim_child_ordinal=? AND mp_receipt_reclaim_due_ms=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id IS ? AND mp_reclaim_request_digest IS ? AND mp_reclaim_result_digest IS ? AND mp_control_variable_bytes=? RETURNING mp_control_variable_bytes`, "iiiisiiibbbiiiiiiiiiiiissiisbiiiiisiiibbbi", stmt.Write, []int{6, 38, 39, 40}),
		mpReceiptReclaimDefinition("mp.reclaim.version.load", `SELECT mp_reclaim_command_kind,mp_reclaim_result_version,mp_reclaim_result_count,mp_reclaim_result_digest,mp_reclaim_applied_ms,mp_reclaim_captured_ack,mp_reclaim_before_cursor,mp_reclaim_after_cursor,mp_reclaim_removed_rows,mp_reclaim_removed_bytes,mp_reclaim_before_rows,mp_reclaim_before_bytes,mp_reclaim_result_rows,mp_reclaim_result_bytes,mp_reclaim_finished,mp_reclaim_before_child_kind,mp_reclaim_after_child_kind,mp_reclaim_before_child_ordinal,mp_reclaim_after_child_ordinal FROM prepared_media_versions WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.queue.current", `SELECT mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes,mp_receipt_reclaim_after_sequence,mp_receipt_reclaim_child_kind,mp_receipt_reclaim_child_ordinal,mp_receipt_reclaim_due_ms,mp_reclaim_sequence,mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_result_digest,mp_control_variable_bytes FROM queues_v1 WHERE id=? AND mp_command_incarnation=?`, "sb", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.queue.first", `SELECT command_sequence,variable_bytes FROM mp_queue_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=? ORDER BY command_sequence LIMIT 1`, "sbii", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.queue.delete", `DELETE FROM mp_queue_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND variable_bytes=? RETURNING variable_bytes`, "sbii", stmt.Write, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.queue.segments.first", `SELECT ordinal,variable_bytes FROM mp_queue_receipt_segments WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal>? ORDER BY ordinal LIMIT 1`, "sbii", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.queue.segments.delete", `DELETE FROM mp_queue_receipt_segments WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal=? AND variable_bytes=? RETURNING variable_bytes`, "sbiii", stmt.Write, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.queue.retire_media.first", `SELECT ordinal,variable_bytes FROM mp_queue_receipt_retire_media WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal>? ORDER BY ordinal LIMIT 1`, "sbii", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.queue.retire_media.delete", `DELETE FROM mp_queue_receipt_retire_media WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal=? AND variable_bytes=? RETURNING variable_bytes`, "sbiii", stmt.Write, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.queue.store", `UPDATE queues_v1 SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=?,mp_receipt_reclaim_after_sequence=?,mp_receipt_reclaim_child_kind=?,mp_receipt_reclaim_child_ordinal=?,mp_receipt_reclaim_due_ms=?,mp_reclaim_sequence=?,mp_reclaim_operation_id=?,mp_reclaim_request_digest=?,mp_reclaim_result_digest=?,mp_reclaim_applied_ms=?,mp_reclaim_captured_ack=?,mp_reclaim_before_cursor=?,mp_reclaim_after_cursor=?,mp_reclaim_removed_rows=?,mp_reclaim_removed_bytes=?,mp_reclaim_before_rows=?,mp_reclaim_before_bytes=?,mp_reclaim_result_rows=?,mp_reclaim_result_bytes=?,mp_reclaim_finished=?,mp_reclaim_command_kind=?,mp_reclaim_before_child_kind=?,mp_reclaim_after_child_kind=?,mp_reclaim_before_child_ordinal=?,mp_reclaim_after_child_ordinal=?,mp_reclaim_result_version=1,mp_reclaim_result_count=1 WHERE id=? AND mp_command_incarnation=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_child_kind=? AND mp_receipt_reclaim_child_ordinal=? AND mp_receipt_reclaim_due_ms=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id IS ? AND mp_reclaim_request_digest IS ? AND mp_reclaim_result_digest IS ? AND mp_control_variable_bytes=? RETURNING mp_control_variable_bytes`, "iiiisiiibbbiiiiiiiiiiiissiisbiiiiisiiibbbi", stmt.Write, []int{6, 38, 39, 40}),
		mpReceiptReclaimDefinition("mp.reclaim.queue.load", `SELECT mp_reclaim_command_kind,mp_reclaim_result_version,mp_reclaim_result_count,mp_reclaim_result_digest,mp_reclaim_applied_ms,mp_reclaim_captured_ack,mp_reclaim_before_cursor,mp_reclaim_after_cursor,mp_reclaim_removed_rows,mp_reclaim_removed_bytes,mp_reclaim_before_rows,mp_reclaim_before_bytes,mp_reclaim_result_rows,mp_reclaim_result_bytes,mp_reclaim_finished,mp_reclaim_before_child_kind,mp_reclaim_after_child_kind,mp_reclaim_before_child_ordinal,mp_reclaim_after_child_ordinal FROM queues_v1 WHERE id=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.next.current", `SELECT mp_command_last_sequence,mp_command_acknowledged_sequence,mp_receipt_rows,mp_receipt_variable_bytes,mp_receipt_reclaim_after_sequence,mp_receipt_reclaim_child_kind,mp_receipt_reclaim_child_ordinal,mp_receipt_reclaim_due_ms,mp_reclaim_sequence,mp_reclaim_operation_id,mp_reclaim_request_digest,mp_reclaim_result_digest,mp_control_variable_bytes FROM playback_v1_prepared WHERE token=? AND mp_command_incarnation=?`, "sb", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.next.first", `SELECT command_sequence,variable_bytes FROM mp_next_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence>? AND command_sequence<=? ORDER BY command_sequence LIMIT 1`, "sbii", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.next.delete", `DELETE FROM mp_next_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND variable_bytes=? RETURNING variable_bytes`, "sbii", stmt.Write, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.next.retire_media.first", `SELECT ordinal,variable_bytes FROM mp_next_receipt_retire_media WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal>? ORDER BY ordinal LIMIT 1`, "sbii", stmt.Read, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.next.retire_media.delete", `DELETE FROM mp_next_receipt_retire_media WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND ordinal=? AND variable_bytes=? RETURNING variable_bytes`, "sbiii", stmt.Write, []int{}),
		mpReceiptReclaimDefinition("mp.reclaim.next.store", `UPDATE playback_v1_prepared SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=?,mp_receipt_reclaim_after_sequence=?,mp_receipt_reclaim_child_kind=?,mp_receipt_reclaim_child_ordinal=?,mp_receipt_reclaim_due_ms=?,mp_reclaim_sequence=?,mp_reclaim_operation_id=?,mp_reclaim_request_digest=?,mp_reclaim_result_digest=?,mp_reclaim_applied_ms=?,mp_reclaim_captured_ack=?,mp_reclaim_before_cursor=?,mp_reclaim_after_cursor=?,mp_reclaim_removed_rows=?,mp_reclaim_removed_bytes=?,mp_reclaim_before_rows=?,mp_reclaim_before_bytes=?,mp_reclaim_result_rows=?,mp_reclaim_result_bytes=?,mp_reclaim_finished=?,mp_reclaim_command_kind=?,mp_reclaim_before_child_kind=?,mp_reclaim_after_child_kind=?,mp_reclaim_before_child_ordinal=?,mp_reclaim_after_child_ordinal=?,mp_reclaim_result_version=1,mp_reclaim_result_count=1 WHERE token=? AND mp_command_incarnation=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_receipt_reclaim_after_sequence=? AND mp_receipt_reclaim_child_kind=? AND mp_receipt_reclaim_child_ordinal=? AND mp_receipt_reclaim_due_ms=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id IS ? AND mp_reclaim_request_digest IS ? AND mp_reclaim_result_digest IS ? AND mp_control_variable_bytes=? RETURNING mp_control_variable_bytes`, "iiiisiiibbbiiiiiiiiiiiissiisbiiiiisiiibbbi", stmt.Write, []int{6, 38, 39, 40}),
		mpReceiptReclaimDefinition("mp.reclaim.next.load", `SELECT mp_reclaim_command_kind,mp_reclaim_result_version,mp_reclaim_result_count,mp_reclaim_result_digest,mp_reclaim_applied_ms,mp_reclaim_captured_ack,mp_reclaim_before_cursor,mp_reclaim_after_cursor,mp_reclaim_removed_rows,mp_reclaim_removed_bytes,mp_reclaim_before_rows,mp_reclaim_before_bytes,mp_reclaim_result_rows,mp_reclaim_result_bytes,mp_reclaim_finished,mp_reclaim_before_child_kind,mp_reclaim_after_child_kind,mp_reclaim_before_child_ordinal,mp_reclaim_after_child_ordinal FROM playback_v1_prepared WHERE token=? AND mp_command_incarnation=? AND mp_reclaim_sequence=? AND mp_reclaim_operation_id=? AND mp_reclaim_request_digest=?`, "sbibb", stmt.Read, []int{}),
	}
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
