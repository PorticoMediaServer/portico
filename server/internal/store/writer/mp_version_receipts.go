package writer

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"math"
)

// Version745/749 belongs to prepared_media_versions, not its mutable job or
// a generic media receipt. W obtains this original scalar projection in the
// same fresh command turn; replay never rereads today's version/job state.
type mpVersionResult struct {
	cleanupRequest, cleanupExecution                                        string
	cleanupGeneration, cleanupCursor                                        int64
	cleanupFinished, physicalExited, cleaned                                bool
	version, job, sourceRevision, transformation, digest, state, retirement string
	revision, size                                                          int64
	physicalPending                                                         bool
}
type mpVersionReceipt struct {
	header mpPrepReceiptHeader
	result mpVersionResult
}

func mpVersionResultDigest(v mpVersionReceipt) (ResultDigest, error) {
	h, r := v.header, v.result
	if !mpPrepTexts(r.cleanupRequest) || !mpPrepText(r.cleanupExecution, true) || r.cleanupGeneration < 0 || r.cleanupCursor < 0 || (r.cleanupGeneration == 0) != (r.cleanupExecution == "") || r.cleaned && !r.physicalExited || r.cleanupFinished && (!r.cleaned || r.cleanupGeneration == 0) || r.physicalPending == r.cleanupFinished {
		return ResultDigest{}, ErrResolveRequired
	}
	if h.kind != 745 && h.kind != 749 || !mpPrepTexts(r.version, r.job) || !lowerHex(r.sourceRevision, 64) || !lowerHex(r.transformation, 64) || !lowerHex(r.digest, 64) || !mpPrepRevision(r.revision) || r.size <= 0 || r.retirement != "live" && r.retirement != "draining" && r.retirement != "retired" {
		return ResultDigest{}, ErrResolveRequired
	}
	if h.kind == 745 && (r.state != "deleting" || !r.physicalPending || r.cleanupGeneration != 0 || r.cleanupCursor != 0 || !r.physicalExited || !r.cleaned) || h.kind == 749 && (r.state != "deleting" && r.state != "deleted" || r.cleanupFinished != (r.state == "deleted")) {
		return ResultDigest{}, ErrResolveRequired
	}
	e, err := mpPrepReceiptEncoder(h, "mp_prepared_version")
	if err != nil {
		return ResultDigest{}, err
	}
	for _, s := range []string{r.version, r.job, r.sourceRevision, r.transformation, r.digest, r.state, r.retirement, r.cleanupRequest, r.cleanupExecution} {
		e.text(s)
	}
	e.number(uint64(r.revision))
	e.number(uint64(r.size))
	e.number(uint64(r.cleanupGeneration))
	e.number(uint64(r.cleanupCursor))
	for _, b := range []bool{r.cleanupFinished, r.physicalExited, r.cleaned} {
		if b {
			e.number(1)
		} else {
			e.number(0)
		}
	}
	if r.physicalPending {
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
func storeMPVersionReceipt(ctx context.Context, t *commandTurn, o mpPrepOwnerBefore, v mpVersionReceipt) (bytes int64, err error) {
	defer func() { bytes, err = finishCommand(ctx, commandLifetime(t), bytes, err) }()
	h, r := v.header, v.result
	if err = mpPrepTurn(t, h.kind); err != nil {
		return 0, err
	}
	if !o.valid(h) {
		return 0, ErrResolveRequired
	}
	d, e := mpVersionResultDigest(v)
	if e != nil || d != h.resultDigest {
		return 0, errors.Join(ErrResolveRequired, e)
	}
	pending := int64(0)
	if r.physicalPending {
		pending = 1
	}
	row, e := mpPrepRow(ctx, t, "mp.version.receipt_insert", r.version, h.identity.Incarnation[:], h.identity.Sequence, h.identity.OperationID[:], h.request[:], int64(h.kind), h.appliedMS, d[:], r.version, r.revision, r.state, r.digest, r.size, r.sourceRevision, r.transformation, r.job, pending, r.retirement, r.cleanupRequest, r.cleanupExecution, r.cleanupGeneration, r.cleanupCursor, mpBool(r.cleanupFinished), mpBool(r.physicalExited), mpBool(r.cleaned))
	if e != nil {
		return 0, e
	}
	if e = row.Scan(&bytes); e != nil {
		return 0, e
	}
	if bytes <= 0 || bytes > math.MaxInt64-o.variableBytes {
		return 0, ErrResolveRequired
	}
	if e = mpPrepOne(ctx, t, "mp.version.receipt_advance", h.identity.Sequence, o.rows+1, o.variableBytes+bytes, r.version, o.incarnation[:], o.last, o.ack, o.rows, o.variableBytes); e != nil {
		return 0, e
	}
	return bytes, t.checkLifetimes(ctx)
}
func loadMPVersionReceipt(ctx context.Context, t *commandTurn, id string, h mpPrepReceiptHeader) (out mpVersionReceipt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, h.kind); err != nil {
		return out, err
	}
	row, e := mpPrepRow(ctx, t, "mp.version.receipt_load", id, h.identity.Incarnation[:], h.identity.Sequence)
	if e != nil {
		return out, e
	}
	out.header = h
	var op, request, digest []byte
	var kind, version, count, pending, finished, exited, cleaned int64
	r := &out.result
	if e = row.Scan(&op, &request, &kind, &version, &out.header.appliedMS, &digest, &count, &r.version, &r.revision, &r.state, &r.digest, &r.size, &r.sourceRevision, &r.transformation, &r.job, &pending, &r.retirement, &r.cleanupRequest, &r.cleanupExecution, &r.cleanupGeneration, &r.cleanupCursor, &finished, &exited, &cleaned); e != nil {
		return out, e
	}
	r.physicalPending = pending == 1
	r.cleanupFinished = finished == 1
	r.physicalExited = exited == 1
	r.cleaned = cleaned == 1
	d, e := mpVersionResultDigest(out)
	if e != nil || r.version != id || string(op) != string(h.identity.OperationID[:]) || string(request) != string(h.request[:]) || kind != int64(h.kind) || version != 1 || count != 1 || pending < 0 || pending > 1 || finished < 0 || finished > 1 || exited < 0 || exited > 1 || cleaned < 0 || cleaned > 1 || string(digest) != string(d[:]) {
		return out, errors.Join(ErrResolveRequired, e)
	}
	out.header.resultDigest = d
	return out, t.checkLifetimes(ctx)
}
func mpVersionReceiptDefinitions() []stmt.Definition {
	return []stmt.Definition{
		mpVersionReceiptDefinition("mp.version.receipt_insert", `INSERT INTO mp_version_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,applied_ms,result_digest,result_count,result_version_id,result_revision,result_state,result_object_digest,result_object_size,result_source_revision,result_transformation_digest,result_job_id,result_physical_pending,result_retirement_state,result_cleanup_request_id,result_cleanup_execution_id,result_cleanup_generation,result_cleanup_cursor,result_cleanup_finished,result_physical_exited,result_cleaned) VALUES(?,?,?,?,?,?,1,?,?,1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, "sbibbiibsississsisssiiiii", stmt.Write),
		mpVersionReceiptDefinition("mp.version.receipt_advance", `UPDATE prepared_media_versions SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=? WHERE id=? AND mp_command_incarnation=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? RETURNING 1`, "iiisbiiii", stmt.Write),
		mpVersionReceiptDefinition("mp.version.receipt_load", `SELECT operation_id,request_digest,command_kind,result_version,applied_ms,result_digest,result_count,result_version_id,result_revision,result_state,result_object_digest,result_object_size,result_source_revision,result_transformation_digest,result_job_id,result_physical_pending,result_retirement_state,result_cleanup_request_id,result_cleanup_execution_id,result_cleanup_generation,result_cleanup_cursor,result_cleanup_finished,result_physical_exited,result_cleaned FROM mp_version_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, "sbi", stmt.Read),
	}
}

func mpVersionReceiptDefinition(id, sql, types string, kind stmt.Kind) stmt.Definition {
	d := mpPrepDefinition(id, sql, types, kind)
	d.CallSites = []string{"store/writer/mp_version_receipts.go"}
	return d
}
