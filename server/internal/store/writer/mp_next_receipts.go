package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type mpNextBirthEffect struct {
	result   mpQueueEffect
	prepared mpNextPrepared
}

// Exact original session/presentation scalars supplied by the parent's core
// recipe. No current session or physical exit lookup may fill missing fields.
type mpNextReceiptHeader struct {
	state, media, presentationURL                                string
	sessionGeneration, sessionRevision, mediaGeneration, expires int64
	physicalPending                                              bool
}
type mpNextSavedResult struct {
	token         string
	identity      CommandIdentity
	requestDigest RequestDigest
	kind          CommandID
	appliedMS     int64
	digest        [32]byte
	effect        mpQueueEffect
	header        mpNextReceiptHeader
	retireMedia   []mpReceiptRetireMedia
}

func mpNextResultDefinitions() []stmt.Definition {
	return []stmt.Definition{
		mpDefinition("mp.next.result_insert", `INSERT INTO mp_next_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,applied_ms,result_digest,result_count,result_shuffle_lap,result_changed,result_token,result_state,result_cancel_reason,result_queue_id,result_queue_revision,result_custody_generation,result_current_position,result_entry_id,result_session_id,result_session_generation,result_session_revision,result_media_session_id,result_media_generation,result_expires_ms,result_presentation_url,result_physical_pending,retire_media_count) VALUES(?,?,?,?,?,?,1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "sbibbiibiiissssiiissiisiisii", stmt.Write),
		mpDefinition("mp.next.result_read", `SELECT operation_id,request_digest,command_kind,result_version,applied_ms,result_digest,result_count,result_shuffle_lap,result_changed,result_token,result_state,result_cancel_reason,result_queue_id,result_queue_revision,result_custody_generation,result_current_position,result_entry_id,result_session_id,result_session_generation,result_session_revision,result_media_session_id,result_media_generation,result_expires_ms,result_presentation_url,result_physical_pending,retire_media_count FROM mp_next_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, "sbi", stmt.Read),
		mpDefinition("mp.next.result_retire_insert", `INSERT INTO mp_next_receipt_retire_media(owner_id,command_incarnation,command_sequence,ordinal,media_session_id,generation,request_id,physical_pending) VALUES(?,?,?,?,?,?,?,?)`, "sbiisisi", stmt.Write),
		mpDefinition("mp.next.result_retire", `SELECT ordinal,media_session_id,generation,request_id,physical_pending FROM mp_next_receipt_retire_media WHERE owner_id=? AND command_incarnation=? AND command_sequence=? ORDER BY ordinal LIMIT 3`, "sbi", stmt.Read),
	}
}
func mpNextRootPurpose(kind CommandID) bool {
	return kind == mpCommitNext || kind == mpCancelNext || kind == mpNextPhysicalExit
}
func mpNextSavedCommitment(v mpNextSavedResult) ([32]byte, error) {
	var zero [32]byte
	e, h := v.effect, v.header
	if !mpNextRootPurpose(v.kind) || !mpReceiptIdentityValid(v.identity, v.requestDigest) || v.appliedMS < 0 || v.token != e.token || !mpID(v.token) || !e.done || e.cursor != 0 || e.count != 0 || !mpRetireResultsValid(v.retireMedia) || h.state != "prepared" && h.state != "committed" && h.state != "canceled" || h.sessionGeneration < 1 || h.sessionRevision < 1 || h.mediaGeneration < 1 || h.expires < 1 || !mpID(h.media) || !mpID(e.session) {
		return zero, ErrInvalid
	}
	effect, err := mpResultBytes(v.kind, e)
	if err != nil {
		return zero, err
	}
	out := mpReceiptEncoder{bytes: []byte("MP-F-next-receipt-v1\x00")}
	mpEncodeReceiptIdentity(&out, v.token, v.identity, v.requestDigest, v.kind, v.appliedMS)
	out.bytes = append(out.bytes, effect...)
	out.integer(1 + int64(len(v.retireMedia)))
	out.text(h.state, 9)
	out.text(h.media, 128)
	out.text(h.presentationURL, 16384)
	out.integer(h.sessionGeneration)
	out.integer(h.sessionRevision)
	out.integer(h.mediaGeneration)
	out.integer(h.expires)
	out.boolean(h.physicalPending)
	mpEncodeRetireResults(&out, v.retireMedia)
	if out.err != nil {
		return zero, out.err
	}
	return sha256.Sum256(out.bytes), nil
}
func mpNextScalarResultArgs(v mpNextSavedResult) []any {
	e, h := v.effect, v.header
	return []any{v.token, v.identity.Incarnation[:], v.identity.Sequence, v.identity.OperationID[:], v.requestDigest[:], int64(v.kind), v.appliedMS, v.digest[:], int64(1 + len(v.retireMedia)), e.lap, mpBool(e.changed), v.token, h.state, e.reason, e.queue, e.revision, e.custody, e.position, e.entry, e.session, h.sessionGeneration, h.sessionRevision, h.media, h.mediaGeneration, h.expires, h.presentationURL, mpBool(h.physicalPending), int64(len(v.retireMedia))}
}
func mpPersistNextResult(ctx context.Context, t *commandTurn, v mpNextSavedResult) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if err = mpPurpose(t, v.kind); err != nil {
		return err
	}
	d, err := mpNextSavedCommitment(v)
	if err != nil {
		return err
	}
	if d != v.digest {
		return ErrConflict
	}
	if err = mpExec(ctx, t, "mp.next.result_insert", mpNextScalarResultArgs(v)...); err != nil {
		return err
	}
	return mpPersistRetireResults(ctx, t, "mp.next.result_retire_insert", v.token, v.identity, v.retireMedia)
}
func mpLoadNextOriginalResult(ctx context.Context, t *commandTurn, token string, identity CommandIdentity, kind CommandID, expected [32]byte) (out mpNextSavedResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, kind); err != nil {
		return out, err
	}
	if !mpNextRootPurpose(kind) || identity.Sequence < 1 || identity.Incarnation == [16]byte{} {
		return out, ErrInvalid
	}
	row, err := mpQuery(ctx, t, "mp.next.result_read", token, identity.Incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	out.token = token
	out.identity = identity
	out.kind = kind
	out.effect.done = true
	e, h := &out.effect, &out.header
	var operation, request, digest []byte
	var savedKind, version, count, changed, pending, retire int64
	err = row.Scan(&operation, &request, &savedKind, &version, &out.appliedMS, &digest, &count, &e.lap, &changed, &e.token, &h.state, &e.reason, &e.queue, &e.revision, &e.custody, &e.position, &e.entry, &e.session, &h.sessionGeneration, &h.sessionRevision, &h.media, &h.mediaGeneration, &h.expires, &h.presentationURL, &pending, &retire)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrResolveRequired
	}
	if err != nil {
		return out, err
	}
	if string(operation) != string(identity.OperationID[:]) || len(request) != 32 || len(digest) != 32 || version != 1 || savedKind != int64(kind) || e.token != token || retire < 0 || retire > 2 || count != 1+retire || changed < 0 || changed > 1 || pending < 0 || pending > 1 {
		return out, ErrResolveRequired
	}
	copy(out.requestDigest[:], request)
	copy(out.digest[:], digest)
	e.changed = changed == 1
	h.physicalPending = pending == 1
	out.retireMedia, err = mpReadRetireResults(ctx, t, "mp.next.result_retire", token, identity, retire)
	if err != nil {
		return out, err
	}
	computed, err := mpNextSavedCommitment(out)
	if err != nil || computed != expected || computed != out.digest {
		return out, errors.Join(ErrResolveRequired, err)
	}
	return out, nil
}
