package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"math"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Exact F scalar receipt values. W owns current disclosure/classification and
// terminal release; these private values mint no authority or second owner.
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
	if r.rootIncarnation == ([16]byte{}) || r.state == "pending" && r.sealed || (r.state == "preparing" || r.state == "prepared" || r.state == "accepted") && !r.sealed {
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
	if r.sealed {
		e.number(1)
	} else {
		e.number(0)
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
	if !mpJobResultPurpose(h.kind) || !mpPrepTexts(r.job) || !mpPrepText(r.execution, true) || !mpPrepText(r.version, true) || !mpPrepText(r.stopExecution, true) || (r.digest != "" && !lowerHex(r.digest, 64)) || !mpPrepRevision(r.revision) || r.generation < 0 || r.cleanedGeneration < 0 || r.cleanedGeneration > r.generation || r.leaseOwner < 0 || r.leaseGeneration < 0 || r.leaseExpiresMS < 0 || r.attempts < 0 || r.attempts > math.MaxUint32 || r.size < 0 || r.nextAttemptMS < -1 || r.cleanupCursor < 0 || r.versionRevision < 0 || !mpPrepText(r.versionState, true) || !mpPrepTexts(r.phase) || !mpPrepText(r.errorCode, true) || !lowerHex(r.sourceRevision, 64) {
		return ResultDigest{}, ErrInvalid
	}
	if r.rootIncarnation == ([16]byte{}) {
		return ResultDigest{}, ErrInvalid
	}
	if r.version == "" {
		if r.objectSize != 0 || r.versionIncarnation != ([16]byte{}) || r.versionState != "" || r.versionRevision != 0 || r.digest != "" {
			return ResultDigest{}, ErrInvalid
		}
	} else {
		if r.versionIncarnation == ([16]byte{}) || r.versionIncarnation == r.rootIncarnation || !lowerHex(r.digest, 64) || r.objectSize <= 0 || r.size <= 0 || !mpPrepRevision(r.versionRevision) {
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
	for _, s := range []string{r.job, r.state, r.execution, r.version, r.digest, r.stopExecution, r.versionState, r.phase, r.errorCode, r.sourceRevision} {
		e.text(s)
	}
	for _, n := range []int64{r.revision, r.generation, r.cleanedGeneration, r.leaseOwner, r.leaseGeneration, r.leaseExpiresMS, r.attempts, r.size, r.objectSize, r.cleanupCursor, r.versionRevision} {
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
	case mpVersionCleanupClaim:
		match = kind == MPVersionFinalize
	case mpVersionCleanupConfirmation:
		match = kind == MPVersionFinalize
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

func mpJobResultPurpose(kind CommandID) bool {
	switch kind {
	case MPTranscodeCreate, MPTranscodeClaim, MPTranscodeRenew, MPTranscodeCancel, MPTranscodePublish, MPTranscodeReconcile, MPTranscodeDeadline, MPTranscodeRetry:
		return true
	}
	return false
}

// These are fresh-turn completion reads, never replay decoders. The expected
// effect revision/root must match the native post-effect row. Extra scalar
// values come from that same row, including phase/error/source/object size.
func completeMPStartPrepResult(ctx context.Context, t *commandTurn, in mpStartPrepEffect) (out mpStartPrepEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, err := mpPrepRow(ctx, t, "mp.start.result_native", in.preparation)
	if err != nil {
		return out, err
	}
	var root []byte
	var sealed int64
	var execution sql.NullString
	var cleaned, cursor sql.NullInt64
	out.preparation = in.preparation
	err = row.Scan(&root, &out.state, &out.revision, &out.execution, &sealed, &out.deadlineMS, &out.ids.session, &out.ids.legacySession, &out.ids.legacyGeneration, &out.ids.snapshot, &execution, &cleaned, &cursor)
	if err != nil {
		return out, err
	}
	if len(root) != 16 || sealed < 0 || sealed > 1 {
		return out, ErrResolveRequired
	}
	copy(out.rootIncarnation[:], root)
	out.sealed = sealed == 1
	if out.execution != "" {
		if !execution.Valid || execution.String != out.execution || !cleaned.Valid || cleaned.Int64 < 0 || cleaned.Int64 > 1 || !cursor.Valid || cursor.Int64 < 0 {
			return out, ErrResolveRequired
		}
		out.cleanupPending = cleaned.Int64 == 0
		out.cleanupCursor = cursor.Int64
	} else if execution.Valid {
		return out, ErrResolveRequired
	}
	out.stopExecution = in.stopExecution
	in.sealed = out.sealed
	if out != in || out.stopExecution != "" && out.stopExecution != out.execution {
		return out, ErrConflict
	}
	return out, nil
}
func completeMPTranscodeResult(ctx context.Context, t *commandTurn, in mpTranscodeEffect) (out mpTranscodeEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, err := mpPrepRow(ctx, t, "mp.transcode.result_native", in.job)
	if err != nil {
		return out, err
	}
	var root, version []byte
	var cleaned int64
	out.job = in.job
	err = row.Scan(&root, &out.state, &out.revision, &out.phase, &out.errorCode, &out.sourceRevision, &out.generation, &out.cleanedGeneration, &out.leaseOwner, &out.leaseGeneration, &out.leaseExpiresMS, &out.attempts, &out.nextAttemptMS, &out.size, &out.execution, &cleaned, &out.cleanupCursor, &out.version, &version, &out.digest, &out.objectSize, &out.versionState, &out.versionRevision)
	if err != nil {
		return out, err
	}
	if len(root) != 16 || cleaned < 0 || cleaned > 1 || len(version) != 0 && len(version) != 16 {
		return out, ErrResolveRequired
	}
	copy(out.rootIncarnation[:], root)
	copy(out.versionIncarnation[:], version)
	out.cleanupPending = cleaned == 0
	out.stopExecution = in.stopExecution
	in.phase = out.phase
	in.errorCode = out.errorCode
	in.sourceRevision = out.sourceRevision
	in.objectSize = out.objectSize
	if out != in || out.stopExecution != "" && out.stopExecution != out.execution {
		return out, ErrConflict
	}
	return out, nil
}
func prepareMPStartPrepReceipt(ctx context.Context, t *commandTurn, h mpPrepReceiptHeader, r mpStartPrepEffect) (out mpStartPrepReceipt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, h.kind); err != nil {
		return out, err
	}
	out.header = h
	out.result, err = completeMPStartPrepResult(ctx, t, r)
	if err != nil {
		return out, err
	}
	out.header.resultDigest, err = mpStartPrepResultDigest(out)
	return out, err
}
func prepareMPTranscodeReceipt(ctx context.Context, t *commandTurn, h mpPrepReceiptHeader, r mpTranscodeEffect) (out mpTranscodeReceipt, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPrepTurn(t, h.kind); err != nil {
		return out, err
	}
	if h.kind == MPTranscodeRetire {
		return out, ErrInvalid
	}
	out.header = h
	out.result, err = completeMPTranscodeResult(ctx, t, r)
	if err != nil {
		return out, err
	}
	out.header.resultDigest, err = mpTranscodeResultDigest(out)
	return out, err
}
func mpPrepReceiptDefinitions() []stmt.Definition {
	out := []stmt.Definition{
		mpPrepDefinition("mp.start.result_native", `SELECT p.mp_command_incarnation,l.state,l.revision,l.execution_id,l.sealed,p.deadline_ms,r.session_id,r.legacy_session_id,r.legacy_generation,r.snapshot_id,x.execution_id,x.cleaned,x.cleanup_cursor FROM playback_start_preparations p JOIN playback_start_preparation_lifecycle l ON l.operation_id=p.id JOIN playback_start_preparation_results r ON r.operation_id=p.id LEFT JOIN mp_start_execution x ON x.operation_id=p.id WHERE p.id=?`, "s", stmt.Read),
		mpPrepDefinition("mp.transcode.result_native", `SELECT j.mp_command_incarnation,j.state,j.revision,j.phase,j.error_code,j.source_revision,j.generation,j.cleaned_generation,j.job_lease_owner,j.job_lease_generation,j.job_lease_expires_ms,j.job_attempts,coalesce(j.job_next_attempt_ms,-1),j.bytes,c.execution_id,c.cleaned,c.cleanup_cursor,j.version_id,v.mp_command_incarnation,coalesce(v.digest,''),coalesce(v.size,0),coalesce(v.state,''),coalesce(v.revision,0) FROM prepared_media_jobs j JOIN mp_job_custody c ON c.job_id=j.id AND c.generation=j.generation LEFT JOIN prepared_media_versions v ON v.id=j.version_id AND v.job_id=j.id WHERE j.id=?`, "s", stmt.Read),
	}
	out = append(out, mpStartWorkerPayloadDefinitions()...)
	return append(out, mpJobWorkerPayloadDefinitions()...)
}
