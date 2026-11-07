package writer

import (
	"bytes"
	"context"
	"crypto/rand"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Concrete F worker stream. User births belong to UO; W alone encodes results.
func storeMPQueueWorkerPayload(ctx context.Context, t *commandTurn, owner string, o mpMediaWorkerBefore, a mpMediaWorkerAdmission, v mpWMediaPayload) (debt int64, err error) {
	defer func() { debt, err = finishCommand(ctx, commandLifetime(t), debt, err) }()
	h := v.header
	if !mpQueueWorkerPurpose(h.kind) {
		return 0, ErrInvalid
	}
	if err = mpPrepTurn(t, h.kind); err != nil {
		return
	}
	expected, e := o.admits(owner, v, a)
	if e != nil {
		return 0, e
	}
	row, e := mpPrepRow(ctx, t, "mp.queue.worker.insert", owner, h.identity.Incarnation[:], h.identity.Sequence, h.identity.OperationID[:], int64(h.kind), h.request[:], v.version, v.count, h.resultDigest[:], v.payload, h.appliedMS)
	if e != nil {
		return 0, e
	}
	if err = row.Scan(&debt); err != nil {
		return
	}
	if debt != expected {
		return 0, ErrResolveRequired
	}
	err = mpPrepOne(ctx, t, "mp.queue.worker.advance", h.identity.Sequence, o.rows+1, o.variableBytes+debt, a.reclaimDueMS, owner, o.incarnation[:], o.revision, o.last, o.ack, o.rows, o.variableBytes)
	return
}

// Returns saved ORIGINAL W bytes after a point byte preflight. Caller must
// supply actual admitted native bytes before copying and use W's verification/decode path;
// no mutable source/window/session row is used to reconstruct a replay.
func loadMPQueueWorkerPayload(ctx context.Context, t *commandTurn, owner string, want mpPrepReceiptHeader, admittedVariableBytes int64) (out mpWMediaPayload, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if !mpQueueWorkerPurpose(want.kind) || !mpPrepTexts(owner) || want.identity.Incarnation == ([16]byte{}) || want.identity.OperationID == ([16]byte{}) || want.identity.Sequence <= 0 || want.request == (RequestDigest{}) {
		return out, ErrInvalid
	}
	if err = mpPrepTurn(t, want.kind); err != nil {
		return
	}
	row, e := mpPrepRow(ctx, t, "mp.queue.worker.octets", owner, want.identity.Incarnation[:], want.identity.Sequence)
	if e != nil {
		return out, e
	}
	var debt, n int64
	if err = row.Scan(&debt, &n); err != nil {
		return
	}
	if n < 1 || n > 65536 || debt != int64(len(owner))+96+n || debt > admittedVariableBytes {
		return out, ErrResolveRequired
	}
	row, e = mpPrepRow(ctx, t, "mp.queue.worker.original", owner, want.identity.Incarnation[:], want.identity.Sequence)
	if e != nil {
		return out, e
	}
	var op, request, digest []byte
	var kind int64
	out.header = want
	if err = row.Scan(&op, &kind, &request, &out.version, &out.count, &digest, &out.payload, &out.header.appliedMS); err != nil {
		return
	}
	if !bytes.Equal(op, want.identity.OperationID[:]) || !bytes.Equal(request, want.request[:]) || kind != int64(want.kind) || len(digest) != 32 || out.version != 1 || out.count < 0 || out.count > 10000 || int64(len(out.payload)) != n || out.header.appliedMS < 0 {
		return out, ErrResolveRequired
	}
	copy(out.header.resultDigest[:], digest)
	if out.header.resultDigest == (ResultDigest{}) {
		return out, ErrResolveRequired
	}
	return
}
func mpQueueWorkerPayloadDefinitions() []stmt.Definition {
	specs := []struct {
		id, sql, types string
		kind           stmt.Kind
	}{
		{"mp.queue.worker.birth", `UPDATE queues_v1 SET mp_command_incarnation=?,mp_user_root_id=?,mp_owner_member_incarnation=?,mp_owner_profile_lifetime=? WHERE id=? AND mp_command_incarnation IS NULL AND mp_command_last_sequence=0 AND mp_command_acknowledged_sequence=0 AND mp_owner_revision=1 AND mp_receipt_rows=0 AND mp_receipt_variable_bytes=0 AND mp_user_root_id IS NULL AND mp_owner_member_incarnation IS NULL AND mp_owner_profile_lifetime IS NULL RETURNING 1`, "bssss", stmt.Write},
		{"mp.queue.worker.insert", `INSERT INTO mp_queue_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,command_kind,request_digest,result_version,result_count,result_digest,result_payload,applied_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, "sbibibiibbi", stmt.Write},
		{"mp.queue.worker.advance", `UPDATE queues_v1 SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=?,mp_receipt_reclaim_due_ms=CASE WHEN mp_receipt_reclaim_due_ms IS NULL THEN ? ELSE mp_receipt_reclaim_due_ms END,mp_owner_revision=mp_owner_revision+1 WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_retirement_state IN('live','draining') RETURNING 1`, "iiiisbiiiii", stmt.Write},
		{"mp.queue.worker.octets", `SELECT variable_bytes,octet_length(result_payload) FROM mp_queue_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, "sbi", stmt.Read},
		{"mp.queue.worker.original", `SELECT operation_id,command_kind,request_digest,result_version,result_count,result_digest,result_payload,applied_ms FROM mp_queue_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, "sbi", stmt.Read},
	}
	var defs []stmt.Definition
	for _, s := range specs {
		d := mpPrepDefinition(s.id, s.sql, s.types, s.kind)
		d.CallSites = []string{"store/writer/mp_queue_worker_payload.go"}
		defs = append(defs, d)
	}
	return defs
}

// Point-only native preflight: no payload is copied. Outer W admission consumes
// this exact value before load; this function grants no memory credit.
func mpQueueWorkerPayloadBytes(ctx context.Context, t *commandTurn, owner string, h mpPrepReceiptHeader) (n int64, err error) {
	defer func() { n, err = finishCommand(ctx, commandLifetime(t), n, err) }()
	if !mpQueueWorkerPurpose(h.kind) || !mpPrepTexts(owner) || h.identity.Incarnation == ([16]byte{}) || h.identity.Sequence <= 0 {
		return 0, ErrInvalid
	}
	if err = mpPrepTurn(t, h.kind); err != nil {
		return
	}
	row, e := mpPrepRow(ctx, t, "mp.queue.worker.octets", owner, h.identity.Incarnation[:], h.identity.Sequence)
	if e != nil {
		return 0, e
	}
	var payload int64
	if err = row.Scan(&n, &payload); err != nil {
		return
	}
	if payload < 1 || payload > 65536 || n != int64(len(owner))+96+payload {
		return 0, ErrResolveRequired
	}
	return
}

// Historical UO tuple is data only; worker sequence starts independently at zero.
type mpQueueWorkerBirth struct {
	userIncarnation               [16]byte
	root, member, profileLifetime string
}

func (b mpQueueWorkerBirth) valid() bool {
	return b.userIncarnation != ([16]byte{}) && len(b.root) == 22 && mpID(b.root) && len(b.member) == 32 && len(b.profileLifetime) == 32 && utf8.ValidString(b.member) && utf8.ValidString(b.profileLifetime)
}
func mpFreshQueueWorkerIncarnation(b mpQueueWorkerBirth) ([16]byte, error) {
	var id [16]byte
	if !b.valid() {
		return id, ErrInvalid
	}
	if _, err := rand.Read(id[:]); err != nil {
		return [16]byte{}, err
	}
	if id == ([16]byte{}) || id == b.userIncarnation {
		return [16]byte{}, ErrResolveRequired
	}
	return id, nil
}

func mpQueueWorkerPurpose(kind CommandID) bool { return kind >= 751 && kind <= 760 }
