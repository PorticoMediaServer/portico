package writer

import (
	"bytes"
	"context"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// W supplies the immutable, purpose-bound original payload and digest after its
// sole codec preflight. This adapter never encodes or hashes a result, declares
// Applied, commits, advances ACK, or creates a public birth/lifetime owner.
type mpWMediaPayload struct {
	header         mpPrepReceiptHeader
	version, count int64
	payload        []byte
}
type mpMediaWorkerBefore struct {
	incarnation                              [16]byte
	revision, last, ack, rows, variableBytes int64
}
type mpMediaWorkerAdmission struct{ maxRows, maxBytes, reclaimDueMS int64 }

func (o mpMediaWorkerBefore) admits(owner string, v mpWMediaPayload, a mpMediaWorkerAdmission) (int64, error) {
	h := v.header
	if !mpPrepTexts(owner) || !mpPrepHeaderValid(h) || h.resultDigest == (ResultDigest{}) || v.version != 1 || v.count < 0 || v.count > 10000 || len(v.payload) < 1 || len(v.payload) > 65536 {
		return 0, ErrInvalid
	}
	n := int64(len(owner)) + 96 + int64(len(v.payload))
	if o.incarnation != h.identity.Incarnation || o.revision <= 0 || o.revision == math.MaxInt64 || o.last < 0 || o.last == math.MaxInt64 || o.last != o.ack || h.identity.Sequence != o.last+1 || o.rows < 0 || o.rows == math.MaxInt64 || o.variableBytes < 0 || (o.rows == 0) != (o.variableBytes == 0) || a.maxRows <= o.rows || a.maxBytes < o.variableBytes || n > a.maxBytes-o.variableBytes || a.reclaimDueMS < h.appliedMS {
		return 0, ErrResolveRequired
	}
	return n, nil
}

// Standalone actual-root worker stream only. Nested703/760/762 installation
// results belong to the actual parent operation;710 is a UO user birth.
func storeMPSessionWorkerPayload(ctx context.Context, t *commandTurn, owner string, o mpMediaWorkerBefore, a mpMediaWorkerAdmission, v mpWMediaPayload) (debt int64, err error) {
	defer func() { debt, err = finishCommand(ctx, commandLifetime(t), debt, err) }()
	h := v.header
	if h.kind < 711 || h.kind > 719 {
		return 0, ErrInvalid
	}
	if err = mpPrepTurn(t, h.kind); err != nil {
		return
	}
	expected, e := o.admits(owner, v, a)
	if e != nil {
		return 0, e
	}
	row, e := mpPrepRow(ctx, t, "mp.session.worker.insert", owner, h.identity.Incarnation[:], h.identity.Sequence, h.identity.OperationID[:], int64(h.kind), h.request[:], v.version, v.count, h.resultDigest[:], v.payload, h.appliedMS)
	if e != nil {
		return 0, e
	}
	if err = row.Scan(&debt); err != nil {
		return
	}
	if debt != expected {
		return 0, ErrResolveRequired
	}
	err = mpPrepOne(ctx, t, "mp.session.worker.advance", h.identity.Sequence, o.rows+1, o.variableBytes+debt, a.reclaimDueMS, owner, o.incarnation[:], o.revision, o.last, o.ack, o.rows, o.variableBytes)
	return
}

// Returns saved ORIGINAL W bytes after a point byte preflight. Caller must
// supply actual admitted native bytes before copying and use W's verification/decode path;
// no mutable source/window/session row is used to reconstruct a replay.
func loadMPSessionWorkerPayload(ctx context.Context, t *commandTurn, owner string, want mpPrepReceiptHeader, admittedVariableBytes int64) (out mpWMediaPayload, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if want.kind < 711 || want.kind > 719 || !mpPrepTexts(owner) || want.identity.Incarnation == ([16]byte{}) || want.identity.OperationID == ([16]byte{}) || want.identity.Sequence <= 0 || want.request == (RequestDigest{}) {
		return out, ErrInvalid
	}
	if err = mpPrepTurn(t, want.kind); err != nil {
		return
	}
	row, e := mpPrepRow(ctx, t, "mp.session.worker.octets", owner, want.identity.Incarnation[:], want.identity.Sequence)
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
	row, e = mpPrepRow(ctx, t, "mp.session.worker.original", owner, want.identity.Incarnation[:], want.identity.Sequence)
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
func mpSessionWorkerPayloadDefinitions() []stmt.Definition {
	specs := []struct {
		id, sql, types string
		kind           stmt.Kind
	}{
		{"mp.session.worker.insert", `INSERT INTO mp_session_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,command_kind,request_digest,result_version,result_count,result_digest,result_payload,applied_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, "sbibibiibbi", stmt.Write},
		{"mp.session.worker.advance", `UPDATE playback_v1_sessions SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=?,mp_receipt_reclaim_due_ms=CASE WHEN mp_receipt_reclaim_due_ms IS NULL THEN ? ELSE mp_receipt_reclaim_due_ms END,mp_owner_revision=mp_owner_revision+1 WHERE id=? AND mp_command_incarnation=? AND mp_owner_revision=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? AND mp_retirement_state IN('live','draining') RETURNING 1`, "iiiisbiiiii", stmt.Write},
		{"mp.session.worker.octets", `SELECT variable_bytes,octet_length(result_payload) FROM mp_session_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, "sbi", stmt.Read},
		{"mp.session.worker.original", `SELECT operation_id,command_kind,request_digest,result_version,result_count,result_digest,result_payload,applied_ms FROM mp_session_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, "sbi", stmt.Read},
	}
	var defs []stmt.Definition
	for _, s := range specs {
		d := mpPrepDefinition(s.id, s.sql, s.types, s.kind)
		d.CallSites = []string{"store/writer/mp_session_receipts.go"}
		defs = append(defs, d)
	}
	return defs
}

// Point-only native preflight: no payload is copied. Outer W admission consumes
// this exact value before load; this function grants no memory credit.
func mpSessionWorkerPayloadBytes(ctx context.Context, t *commandTurn, owner string, h mpPrepReceiptHeader) (n int64, err error) {
	defer func() { n, err = finishCommand(ctx, commandLifetime(t), n, err) }()
	if h.kind < 711 || h.kind > 719 || !mpPrepTexts(owner) || h.identity.Incarnation == ([16]byte{}) || h.identity.Sequence <= 0 {
		return 0, ErrInvalid
	}
	if err = mpPrepTurn(t, h.kind); err != nil {
		return
	}
	row, e := mpPrepRow(ctx, t, "mp.session.worker.octets", owner, h.identity.Incarnation[:], h.identity.Sequence)
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
