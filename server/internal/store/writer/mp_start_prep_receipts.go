package writer

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// MP-only proposed typed history. The parent's exact per-actual-owner mapping
// owns birth roots/floors and lifetime admission. These fields are DATA from
// that mapping, never a newly minted polymorphic owner or personal/security row.
type mpPrepReceiptHeader struct {
	identity     CommandIdentity
	request      RequestDigest
	kind         CommandID
	appliedMS    int64
	resultDigest ResultDigest
}
type mpStartPrepReceipt struct {
	header mpPrepReceiptHeader
	result mpStartPrepEffect
}
type mpTranscodeReceipt struct {
	header mpPrepReceiptHeader
	result mpTranscodeEffect
}

type mpPrepOwnerBefore struct {
	incarnation                    [16]byte
	last, ack, rows, variableBytes int64
}

func (o mpPrepOwnerBefore) valid(h mpPrepReceiptHeader) bool {
	return o.incarnation == h.identity.Incarnation && o.last >= 0 && o.last < math.MaxInt64 && o.last == o.ack && h.identity.Sequence == o.last+1 && o.rows >= 0 && o.rows < math.MaxInt64 && o.variableBytes >= 0 && (o.rows == 0) == (o.variableBytes == 0)
}
func mpPrepHeaderValid(h mpPrepReceiptHeader) bool {
	return h.identity.Sequence > 0 && h.identity.Incarnation != ([16]byte{}) && h.identity.OperationID != ([16]byte{}) && h.request != (RequestDigest{}) && h.appliedMS > 0
}

// This is a DRAFT closed codec for the allocated MP schemas, reusing W's
// bounded LP/integer encoder. Adoption into W's closed result switch is still
// required. No Describe bypass is used to execute an unregistered command.
func mpPrepReceiptEncoder(h mpPrepReceiptHeader, family string) (*requestEncoder, error) {
	if !mpPrepHeaderValid(h) {
		return nil, ErrInvalid
	}
	e := &requestEncoder{h: sha256.New()}
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(family)
	e.number(uint64(h.kind))
	e.text("effect")
	e.bytes(h.identity.Incarnation[:])
	e.number(uint64(h.identity.Sequence))
	e.bytes(h.identity.OperationID[:])
	e.bytes(h.request[:])
	e.number(uint64(h.appliedMS))
	return e, nil
}
func mpStartPrepResultDigest(v mpStartPrepReceipt) (ResultDigest, error) {
	h, r := v.header, v.result
	if h.kind < MPStartPrepBegin || h.kind > MPStartPrepTimeout || !mpPrepTexts(r.preparation) || !mpPrepText(r.execution, true) || !mpPrepText(r.stopExecution, true) || !mpPrepRevision(r.revision) || r.deadlineMS <= 0 || r.cleanupCursor < 0 || !r.ids.valid() {
		return ResultDigest{}, ErrInvalid
	}
	if r.rootIncarnation == ([16]byte{}) {
		return ResultDigest{}, ErrInvalid
	}
	switch h.kind {
	case MPStartPrepBegin:
		if r.state != "pending" || r.revision != 1 || r.execution != "" || r.cleanupPending || r.cleanupCursor != 0 {
			return ResultDigest{}, ErrInvalid
		}
	case MPStartPrepClaim:
		if r.state != "preparing" || r.execution == "" || !r.cleanupPending {
			return ResultDigest{}, ErrInvalid
		}
	case MPStartPrepSeal:
		if r.state != "prepared" || r.execution == "" || !r.cleanupPending {
			return ResultDigest{}, ErrInvalid
		}
	case MPStartPrepAccept:
		if r.state != "accepted" || r.execution == "" {
			return ResultDigest{}, ErrInvalid
		}
	case MPStartPrepRetire, MPStartPrepTimeout:
		if r.state != "retired" {
			return ResultDigest{}, ErrInvalid
		}
	}
	switch r.state {
	case "pending", "preparing", "prepared", "accepted", "retired":
	default:
		return ResultDigest{}, ErrInvalid
	}
	e, err := mpPrepReceiptEncoder(h, "mp_start_preparation")
	if err != nil {
		return ResultDigest{}, err
	}
	e.bytes(r.rootIncarnation[:])
	for _, s := range []string{r.preparation, r.state, r.execution, r.ids.session, r.ids.legacySession, r.ids.snapshot, r.stopExecution} {
		e.text(s)
	}
	for _, n := range []int64{r.revision, r.deadlineMS, r.ids.legacyGeneration, r.cleanupCursor} {
		e.number(uint64(n))
	}
	if r.cleanupPending {
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
func mpTranscodeResultDigest(v mpTranscodeReceipt) (ResultDigest, error) {
	h, r := v.header, v.result
	if h.kind < MPTranscodeCreate || h.kind > MPTranscodeRetry || !mpPrepTexts(r.job) || !mpPrepText(r.execution, true) || !mpPrepText(r.version, true) || !mpPrepText(r.stopExecution, true) || (r.digest != "" && !lowerHex(r.digest, 64)) || !mpPrepRevision(r.revision) || r.generation < 0 || r.cleanedGeneration < 0 || r.cleanedGeneration > r.generation || r.leaseOwner < 0 || r.leaseGeneration < 0 || r.leaseExpiresMS < 0 || r.attempts < 0 || r.attempts > math.MaxUint32 || r.size < 0 || r.nextAttemptMS < -1 || r.cleanupCursor < 0 || r.versionRevision < 0 || !mpPrepText(r.versionState, true) {
		return ResultDigest{}, ErrInvalid
	}
	if r.rootIncarnation == ([16]byte{}) {
		return ResultDigest{}, ErrInvalid
	}
	if r.version == "" {
		if r.versionIncarnation != ([16]byte{}) || r.versionState != "" || r.versionRevision != 0 || r.digest != "" {
			return ResultDigest{}, ErrInvalid
		}
	} else {
		if r.versionIncarnation == ([16]byte{}) || !lowerHex(r.digest, 64) || r.size <= 0 || !mpPrepRevision(r.versionRevision) {
			return ResultDigest{}, ErrInvalid
		}
		switch r.versionState {
		case "published", "deleting", "deleted":
		default:
			return ResultDigest{}, ErrInvalid
		}
	}
	switch h.kind {
	case MPTranscodeCreate:
		if r.state != "queued" || r.revision != 1 || r.generation != 0 || r.execution != "" || r.cleanupPending {
			return ResultDigest{}, ErrInvalid
		}
	case MPTranscodeClaim, MPTranscodeRenew:
		if r.state != "running" || r.execution == "" || r.leaseOwner <= 0 || !r.cleanupPending {
			return ResultDigest{}, ErrInvalid
		}
	case MPTranscodeDeadline:
		if r.state != "cancelling" || r.execution == "" || !r.cleanupPending || r.stopExecution != r.execution {
			return ResultDigest{}, ErrInvalid
		}
	case MPTranscodeRetry:
		if r.state != "queued" || r.cleanupPending || r.leaseOwner != 0 || r.leaseExpiresMS != 0 || r.nextAttemptMS < 0 {
			return ResultDigest{}, ErrInvalid
		}
	case MPTranscodeCancel:
		if r.state != "cancelling" && r.state != "cancelled" {
			return ResultDigest{}, ErrInvalid
		}
	case MPTranscodePublish:
		if r.state != "succeeded" || r.versionState != "published" || !r.cleanupPending {
			return ResultDigest{}, ErrInvalid
		}
	case MPTranscodeRetire:
		if r.state != "succeeded" || r.versionState != "deleting" || !r.cleanupPending {
			return ResultDigest{}, ErrInvalid
		}
	}
	switch r.state {
	case "queued", "running", "cancelling", "cancelled", "failed", "succeeded":
	default:
		return ResultDigest{}, ErrInvalid
	}
	e, err := mpPrepReceiptEncoder(h, "mp_transcode")
	if err != nil {
		return ResultDigest{}, err
	}
	e.bytes(r.rootIncarnation[:])
	e.bytes(r.versionIncarnation[:])
	for _, s := range []string{r.job, r.state, r.execution, r.version, r.digest, r.stopExecution, r.versionState} {
		e.text(s)
	}
	for _, n := range []int64{r.revision, r.generation, r.cleanedGeneration, r.leaseOwner, r.leaseGeneration, r.leaseExpiresMS, r.attempts, r.size, r.cleanupCursor, r.versionRevision} {
		e.number(uint64(n))
	}
	if r.nextAttemptMS < 0 {
		e.bytes([]byte{0})
	} else {
		e.bytes([]byte{1})
		e.number(uint64(r.nextAttemptMS))
	}
	if r.cleanupPending {
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

// Closed request inputs commit their complete structs, including actor, source,
// queue/replacement/lease, recipe and external-work observations. This draft uses
// the SAME request framing as canonicalRequestDigest. RD2 registration will let
// W's existing constructor encode them without this temporary preparation seam.
type mpStartPrepClaimInput struct {
	fence     mpStartPrepFence
	execution string
}
type mpStartPrepSealInput struct {
	fence      mpStartPrepFence
	completion mpStartPrepCompletion
}
type mpStartPrepCleanupInput struct {
	fence   mpStartPrepFence
	cleanup mpStartPrepCleanup
}
type mpTranscodeClaimInput struct {
	fence          mpTranscodeFence
	owner, untilMS int64
	execution      string
}
type mpTranscodeRenewInput struct {
	fence   mpTranscodeFence
	untilMS int64
}
type mpTranscodePublishInput struct {
	fence    mpTranscodeFence
	artifact mpTranscodeVerifiedArtifact
}
type mpTranscodeRetireInput struct {
	fence   mpTranscodeFence
	version mpTranscodeVersionFence
}
type mpTranscodeCleanupInput struct {
	fence   mpTranscodeFence
	cleanup mpTranscodeCleanup
}

func mpPrepDraftRequestDigest(kind CommandID, identity CommandIdentity, input any) (RequestDigest, error) {
	match := false
	switch input.(type) {
	case mpStartPrepBegin:
		match = kind == MPStartPrepBegin
	case mpStartPrepClaimInput:
		match = kind == MPStartPrepClaim
	case mpStartPrepSealInput:
		match = kind == MPStartPrepSeal
	case mpStartPrepFence:
		match = kind == MPStartPrepAccept || kind == MPStartPrepRetire || kind == MPStartPrepTimeout
	case mpStartPrepCleanupInput:
		match = kind == MPStartPrepRetire
	case mpTranscodeCreate:
		match = kind == MPTranscodeCreate
	case mpVersionFinalizeInput:
		match = kind == MPVersionFinalize
	case mpTranscodeRetryInput:
		match = kind == MPTranscodeRetry
	case mpTranscodeClaimInput:
		match = kind == MPTranscodeClaim
	case mpTranscodeRenewInput:
		match = kind == MPTranscodeRenew
	case mpTranscodeFence:
		match = kind == MPTranscodeCancel || kind == MPTranscodeDeadline
	case mpTranscodePublishInput:
		match = kind == MPTranscodePublish
	case mpTranscodeRetireInput:
		match = kind == MPTranscodeRetire
	case mpTranscodeCleanupInput:
		match = kind == MPTranscodeReconcile
	}
	if !match || identity.Sequence <= 0 {
		return RequestDigest{}, ErrInvalid
	}
	e := requestEncoder{h: sha256.New(), path: make(map[uintptr]bool)}
	e.bytes([]byte(canonicalRequestCodec))
	var b [4]byte
	binary.BigEndian.PutUint16(b[:2], uint16(kind))
	binary.BigEndian.PutUint16(b[2:], 1)
	e.bytes(b[:])
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.value(reflect.ValueOf(input), 0)
	if e.err != nil {
		return RequestDigest{}, e.err
	}
	var d RequestDigest
	copy(d[:], e.h.Sum(nil))
	return d, nil
}

func storeMPStartPrepReceipt(ctx context.Context, t *commandTurn, o mpPrepOwnerBefore, v mpStartPrepReceipt) (bytes int64, err error) {
	defer func() { bytes, err = finishCommand(ctx, commandLifetime(t), bytes, err) }()
	h, r := v.header, v.result
	if err = mpPrepTurn(t, h.kind); err != nil {
		return
	}
	if h.kind == MPStartPrepBegin || h.kind == MPTranscodeCreate || r.rootIncarnation != h.identity.Incarnation || !o.valid(h) {
		return 0, ErrResolveRequired
	}
	d, err := mpStartPrepResultDigest(v)
	if err != nil {
		return 0, err
	}
	if d != h.resultDigest {
		return 0, ErrResolveRequired
	}
	pending := int64(0)
	if r.cleanupPending {
		pending = 1
	}
	row, err := mpPrepRow(ctx, t, "mp.start.receipt_insert", r.preparation, h.identity.Incarnation[:], h.identity.Sequence, h.identity.OperationID[:], h.request[:], int64(h.kind), h.appliedMS, d[:], r.state, r.revision, r.execution, r.deadlineMS, r.ids.session, r.ids.legacySession, r.ids.legacyGeneration, r.ids.snapshot, r.stopExecution, pending, r.rootIncarnation[:], r.cleanupCursor)
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&bytes); err != nil {
		return 0, err
	}
	if bytes <= 0 || bytes > math.MaxInt64-o.variableBytes {
		return 0, ErrResolveRequired
	}
	err = mpPrepOne(ctx, t, "mp.start.owner_advance", h.identity.Sequence, o.rows+1, o.variableBytes+bytes, r.preparation, o.incarnation[:], o.last, o.ack, o.rows, o.variableBytes)
	return
}
func storeMPTranscodeReceipt(ctx context.Context, t *commandTurn, o mpPrepOwnerBefore, v mpTranscodeReceipt) (bytes int64, err error) {
	defer func() { bytes, err = finishCommand(ctx, commandLifetime(t), bytes, err) }()
	h, r := v.header, v.result
	if err = mpPrepTurn(t, h.kind); err != nil {
		return
	}
	if h.kind == MPStartPrepBegin || h.kind == MPTranscodeCreate || h.kind == MPTranscodeRetire || r.rootIncarnation != h.identity.Incarnation || !o.valid(h) {
		return 0, ErrResolveRequired
	}
	d, err := mpTranscodeResultDigest(v)
	if err != nil {
		return 0, err
	}
	if d != h.resultDigest {
		return 0, ErrResolveRequired
	}
	pending := int64(0)
	if r.cleanupPending {
		pending = 1
	}
	var next any
	if r.nextAttemptMS >= 0 {
		next = r.nextAttemptMS
	}
	row, err := mpPrepRow(ctx, t, "mp.transcode.receipt_insert", r.job, h.identity.Incarnation[:], h.identity.Sequence, h.identity.OperationID[:], h.request[:], int64(h.kind), h.appliedMS, d[:], r.state, r.revision, r.execution, r.version, r.digest, r.generation, r.cleanedGeneration, r.leaseOwner, r.leaseGeneration, r.leaseExpiresMS, r.attempts, r.size, next, r.stopExecution, pending, r.cleanupCursor, r.rootIncarnation[:], r.versionState, r.versionRevision, r.versionIncarnation[:])
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&bytes); err != nil {
		return 0, err
	}
	if bytes <= 0 || bytes > math.MaxInt64-o.variableBytes {
		return 0, ErrResolveRequired
	}
	err = mpPrepOne(ctx, t, "mp.transcode.owner_advance", h.identity.Sequence, o.rows+1, o.variableBytes+bytes, r.job, o.incarnation[:], o.last, o.ack, o.rows, o.variableBytes)
	return
}

// Read ONLY the immutable typed result cohort, never today's mutable domain
// state. W calls this only AFTER fresh disclosure and receiptReplay classification.
func loadMPStartPrepReceipt(ctx context.Context, t *commandTurn, id string, h mpPrepReceiptHeader) (out mpStartPrepReceipt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, h.kind); err != nil {
		return
	}
	statement := stmt.ID("mp.start.receipt_load")
	if h.kind == MPStartPrepBegin {
		statement = "mp.start.birth_receipt_load"
	}
	r, err := mpPrepRow(ctx, t, statement, id, h.identity.Incarnation[:], h.identity.Sequence)
	if err != nil {
		return
	}
	var op, request, digest, root []byte
	var kind, version, pending int64
	out.header = h
	out.result.preparation = id
	v := &out.result
	err = r.Scan(&op, &request, &kind, &version, &out.header.appliedMS, &digest, &v.state, &v.revision, &v.execution, &v.deadlineMS, &v.ids.session, &v.ids.legacySession, &v.ids.legacyGeneration, &v.ids.snapshot, &v.stopExecution, &pending, &root, &v.cleanupCursor)
	if err != nil {
		return
	}
	if len(root) != 16 {
		return out, ErrResolveRequired
	}
	copy(v.rootIncarnation[:], root)
	v.cleanupPending = pending == 1
	d, e := mpStartPrepResultDigest(out)
	if e != nil || string(op) != string(h.identity.OperationID[:]) || string(request) != string(h.request[:]) || kind != int64(h.kind) || version != 1 || len(digest) != 32 || string(digest) != string(d[:]) || pending < 0 || pending > 1 {
		return out, errors.Join(ErrResolveRequired, e)
	}
	// The original effect is returned without any mutable-head lookup.
	out.header.resultDigest = d
	return
}
func loadMPTranscodeReceipt(ctx context.Context, t *commandTurn, id string, h mpPrepReceiptHeader) (out mpTranscodeReceipt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, h.kind); err != nil {
		return
	}
	if h.kind == MPTranscodeRetire {
		return out, ErrInvalid
	}
	statement := stmt.ID("mp.transcode.receipt_load")
	if h.kind == MPTranscodeCreate {
		statement = "mp.transcode.birth_receipt_load"
	}
	r, err := mpPrepRow(ctx, t, statement, id, h.identity.Incarnation[:], h.identity.Sequence)
	if err != nil {
		return
	}
	var op, request, digest, root, versionRoot []byte
	var kind, version, pending int64
	out.header = h
	out.result.job = id
	v := &out.result
	err = r.Scan(&op, &request, &kind, &version, &out.header.appliedMS, &digest, &v.state, &v.revision, &v.execution, &v.version, &v.digest, &v.generation, &v.cleanedGeneration, &v.leaseOwner, &v.leaseGeneration, &v.leaseExpiresMS, &v.attempts, &v.size, &v.nextAttemptMS, &v.stopExecution, &pending, &v.cleanupCursor, &root, &v.versionState, &v.versionRevision, &versionRoot)
	if err != nil {
		return
	}
	if len(root) != 16 {
		return out, ErrResolveRequired
	}
	copy(v.rootIncarnation[:], root)
	if len(versionRoot) != 16 {
		return out, ErrResolveRequired
	}
	copy(v.versionIncarnation[:], versionRoot)
	v.cleanupPending = pending == 1
	d, e := mpTranscodeResultDigest(out)
	if e != nil || string(op) != string(h.identity.OperationID[:]) || string(request) != string(h.request[:]) || kind != int64(h.kind) || version != 1 || len(digest) != 32 || string(digest) != string(d[:]) || pending < 0 || pending > 1 {
		return out, errors.Join(ErrResolveRequired, e)
	}
	out.header.resultDigest = d
	return
}

func mpPrepReceiptDefinitions() []stmt.Definition {
	specs := []struct {
		id, sql, types string
		kind           stmt.Kind
	}{
		{"mp.start.receipt_insert", `INSERT INTO playback_start_preparation_command_history(operation_id,command_incarnation,command_sequence,command_operation_id,request_digest,command_kind,result_version,applied_ms,result_digest,result_state,result_revision,result_execution_id,result_deadline_ms,result_session_id,result_legacy_session_id,result_legacy_generation,result_snapshot_id,result_stop_execution_id,result_cleanup_pending,result_root_incarnation,result_cleanup_cursor) VALUES(?,?,?,?,?,?,1,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING octet_length(operation_id)+octet_length(command_incarnation)+octet_length(command_operation_id)+octet_length(request_digest)+octet_length(result_digest)+octet_length(result_state)+octet_length(result_execution_id)+octet_length(result_session_id)+octet_length(result_legacy_session_id)+octet_length(result_snapshot_id)+octet_length(result_stop_execution_id)+octet_length(result_root_incarnation)`, "sbibbiibsisississibi", stmt.Write},
		{"mp.transcode.receipt_insert", `INSERT INTO prepared_media_job_command_history(job_id,command_incarnation,command_sequence,command_operation_id,request_digest,command_kind,result_version,applied_ms,result_digest,result_state,result_revision,result_execution_id,result_version_id,result_artifact_digest,result_generation,result_cleaned_generation,result_lease_owner,result_lease_generation,result_lease_expires_ms,result_attempts,result_size,result_next_attempt_ms,result_stop_execution_id,result_cleanup_pending,result_cleanup_cursor,result_root_incarnation,result_version_state,result_version_revision,result_version_incarnation) VALUES(?,?,?,?,?,?,1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING octet_length(job_id)+octet_length(command_incarnation)+octet_length(command_operation_id)+octet_length(request_digest)+octet_length(result_digest)+octet_length(result_state)+octet_length(result_execution_id)+octet_length(result_version_id)+octet_length(result_artifact_digest)+octet_length(result_stop_execution_id)+octet_length(result_root_incarnation)+octet_length(result_version_state)+octet_length(result_version_incarnation)`, "sbibbiibsisssiiiiiiiisiibsib", stmt.Write},
		{"mp.start.owner_advance", `UPDATE playback_start_preparations SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=? WHERE id=? AND mp_command_incarnation=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? RETURNING 1`, "iiisbiiii", stmt.Write},
		{"mp.transcode.owner_advance", `UPDATE prepared_media_jobs SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=? WHERE id=? AND mp_command_incarnation=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? RETURNING 1`, "iiisbiiii", stmt.Write},
		{"mp.start.receipt_load", `SELECT command_operation_id,request_digest,command_kind,result_version,applied_ms,result_digest,result_state,result_revision,result_execution_id,result_deadline_ms,result_session_id,result_legacy_session_id,result_legacy_generation,result_snapshot_id,result_stop_execution_id,result_cleanup_pending,result_root_incarnation,result_cleanup_cursor FROM playback_start_preparation_command_history WHERE operation_id=? AND command_incarnation=? AND command_sequence=?`, "sbi", stmt.Read},
		{"mp.transcode.receipt_load", `SELECT command_operation_id,request_digest,command_kind,result_version,applied_ms,result_digest,result_state,result_revision,result_execution_id,result_version_id,result_artifact_digest,result_generation,result_cleaned_generation,result_lease_owner,result_lease_generation,result_lease_expires_ms,result_attempts,result_size,coalesce(result_next_attempt_ms,-1),result_stop_execution_id,result_cleanup_pending,result_cleanup_cursor,result_root_incarnation,result_version_state,result_version_revision,result_version_incarnation FROM prepared_media_job_command_history WHERE job_id=? AND command_incarnation=? AND command_sequence=?`, "sbi", stmt.Read},
	}
	specs = append(specs, []struct {
		id, sql, types string
		kind           stmt.Kind
	}{
		{"mp.start.birth_receipt_insert", `INSERT INTO playback_start_preparation_birth_results(birth_owner_id,operation_id,command_incarnation,command_sequence,command_operation_id,request_digest,command_kind,result_version,applied_ms,result_digest,result_state,result_revision,result_execution_id,result_deadline_ms,result_session_id,result_legacy_session_id,result_legacy_generation,result_snapshot_id,result_stop_execution_id,result_cleanup_pending,result_root_incarnation,result_cleanup_cursor) VALUES(?,?,?,?,?,?,?,1,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING octet_length(birth_owner_id)+octet_length(operation_id)+octet_length(command_incarnation)+octet_length(command_operation_id)+octet_length(request_digest)+octet_length(result_digest)+octet_length(result_state)+octet_length(result_execution_id)+octet_length(result_session_id)+octet_length(result_legacy_session_id)+octet_length(result_snapshot_id)+octet_length(result_stop_execution_id)+octet_length(result_root_incarnation)`, "ssbibbiibsisississibi", stmt.Write},
		{"mp.transcode.birth_receipt_insert", `INSERT INTO prepared_media_job_birth_results(birth_owner_id,job_id,command_incarnation,command_sequence,command_operation_id,request_digest,command_kind,result_version,applied_ms,result_digest,result_state,result_revision,result_execution_id,result_version_id,result_artifact_digest,result_generation,result_cleaned_generation,result_lease_owner,result_lease_generation,result_lease_expires_ms,result_attempts,result_size,result_next_attempt_ms,result_stop_execution_id,result_cleanup_pending,result_cleanup_cursor,result_root_incarnation,result_version_state,result_version_revision,result_version_incarnation) VALUES(?,?,?,?,?,?,?,1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING octet_length(birth_owner_id)+octet_length(job_id)+octet_length(command_incarnation)+octet_length(command_operation_id)+octet_length(request_digest)+octet_length(result_digest)+octet_length(result_state)+octet_length(result_execution_id)+octet_length(result_version_id)+octet_length(result_artifact_digest)+octet_length(result_stop_execution_id)+octet_length(result_root_incarnation)+octet_length(result_version_state)+octet_length(result_version_incarnation)`, "ssbibbiibsisssiiiiiiiisiibsib", stmt.Write},
		{"mp.start.birth_owner_advance", `UPDATE playback_start_preparation_birth_owners SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=? WHERE id=? AND mp_command_incarnation=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? RETURNING 1`, "iiisbiiii", stmt.Write},
		{"mp.transcode.birth_owner_advance", `UPDATE prepared_media_job_birth_owners SET mp_command_last_sequence=?,mp_receipt_rows=?,mp_receipt_variable_bytes=? WHERE id=? AND mp_command_incarnation=? AND mp_command_last_sequence=? AND mp_command_acknowledged_sequence=? AND mp_receipt_rows=? AND mp_receipt_variable_bytes=? RETURNING 1`, "iiisbiiii", stmt.Write},
		{"mp.start.birth_receipt_load", `SELECT command_operation_id,request_digest,command_kind,result_version,applied_ms,result_digest,result_state,result_revision,result_execution_id,result_deadline_ms,result_session_id,result_legacy_session_id,result_legacy_generation,result_snapshot_id,result_stop_execution_id,result_cleanup_pending,result_root_incarnation,result_cleanup_cursor FROM playback_start_preparation_birth_results WHERE operation_id=? AND command_incarnation=? AND command_sequence=?`, "sbi", stmt.Read},
		{"mp.transcode.birth_receipt_load", `SELECT command_operation_id,request_digest,command_kind,result_version,applied_ms,result_digest,result_state,result_revision,result_execution_id,result_version_id,result_artifact_digest,result_generation,result_cleaned_generation,result_lease_owner,result_lease_generation,result_lease_expires_ms,result_attempts,result_size,coalesce(result_next_attempt_ms,-1),result_stop_execution_id,result_cleanup_pending,result_cleanup_cursor,result_root_incarnation,result_version_state,result_version_revision,result_version_incarnation FROM prepared_media_job_birth_results WHERE job_id=? AND command_incarnation=? AND command_sequence=?`, "sbi", stmt.Read},
	}...)
	var out []stmt.Definition
	for _, s := range specs {
		v := mpPrepDefinition(s.id, s.sql, s.types, s.kind)
		if s.id == "mp.transcode.birth_receipt_insert" {
			v.Params[21].Nullable = true
		}
		if s.id == "mp.transcode.receipt_insert" {
			v.Params[20].Nullable = true
		}
		out = append(out, v)
	}
	return out
}

// Separate retained public-birth owners, supplied by parent lifetime mapping.
type mpStartPrepBirthOwner struct {
	id     string
	before mpPrepOwnerBefore
}
type mpTranscodeBirthOwner struct {
	id     string
	before mpPrepOwnerBefore
}

func storeMPStartPrepBirthReceipt(ctx context.Context, t *commandTurn, birth mpStartPrepBirthOwner, v mpStartPrepReceipt) (bytes int64, err error) {
	defer func() { bytes, err = finishCommand(ctx, commandLifetime(t), bytes, err) }()
	o := birth.before
	h, r := v.header, v.result
	if err = mpPrepTurn(t, h.kind); err != nil {
		return
	}
	if !mpPrepTexts(birth.id) || !o.valid(h) {
		return 0, ErrResolveRequired
	}
	if h.kind != MPStartPrepBegin || r.rootIncarnation == ([16]byte{}) {
		return 0, ErrInvalid
	}
	d, err := mpStartPrepResultDigest(v)
	if err != nil {
		return 0, err
	}
	if d != h.resultDigest {
		return 0, ErrResolveRequired
	}
	pending := int64(0)
	if r.cleanupPending {
		pending = 1
	}
	row, err := mpPrepRow(ctx, t, "mp.start.birth_receipt_insert", birth.id, r.preparation, h.identity.Incarnation[:], h.identity.Sequence, h.identity.OperationID[:], h.request[:], int64(h.kind), h.appliedMS, d[:], r.state, r.revision, r.execution, r.deadlineMS, r.ids.session, r.ids.legacySession, r.ids.legacyGeneration, r.ids.snapshot, r.stopExecution, pending, r.rootIncarnation[:], r.cleanupCursor)
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&bytes); err != nil {
		return 0, err
	}
	if bytes <= 0 || bytes > math.MaxInt64-o.variableBytes {
		return 0, ErrResolveRequired
	}
	err = mpPrepOne(ctx, t, "mp.start.birth_owner_advance", h.identity.Sequence, o.rows+1, o.variableBytes+bytes, birth.id, o.incarnation[:], o.last, o.ack, o.rows, o.variableBytes)
	return
}
func storeMPTranscodeBirthReceipt(ctx context.Context, t *commandTurn, birth mpTranscodeBirthOwner, v mpTranscodeReceipt) (bytes int64, err error) {
	defer func() { bytes, err = finishCommand(ctx, commandLifetime(t), bytes, err) }()
	o := birth.before
	h, r := v.header, v.result
	if err = mpPrepTurn(t, h.kind); err != nil {
		return
	}
	if !mpPrepTexts(birth.id) || !o.valid(h) {
		return 0, ErrResolveRequired
	}
	if h.kind != MPTranscodeCreate || r.rootIncarnation == ([16]byte{}) {
		return 0, ErrInvalid
	}
	d, err := mpTranscodeResultDigest(v)
	if err != nil {
		return 0, err
	}
	if d != h.resultDigest {
		return 0, ErrResolveRequired
	}
	pending := int64(0)
	if r.cleanupPending {
		pending = 1
	}
	var next any
	if r.nextAttemptMS >= 0 {
		next = r.nextAttemptMS
	}
	row, err := mpPrepRow(ctx, t, "mp.transcode.birth_receipt_insert", birth.id, r.job, h.identity.Incarnation[:], h.identity.Sequence, h.identity.OperationID[:], h.request[:], int64(h.kind), h.appliedMS, d[:], r.state, r.revision, r.execution, r.version, r.digest, r.generation, r.cleanedGeneration, r.leaseOwner, r.leaseGeneration, r.leaseExpiresMS, r.attempts, r.size, next, r.stopExecution, pending, r.cleanupCursor, r.rootIncarnation[:], r.versionState, r.versionRevision, r.versionIncarnation[:])
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&bytes); err != nil {
		return 0, err
	}
	if bytes <= 0 || bytes > math.MaxInt64-o.variableBytes {
		return 0, ErrResolveRequired
	}
	err = mpPrepOne(ctx, t, "mp.transcode.birth_owner_advance", h.identity.Sequence, o.rows+1, o.variableBytes+bytes, birth.id, o.incarnation[:], o.last, o.ack, o.rows, o.variableBytes)
	return
}
