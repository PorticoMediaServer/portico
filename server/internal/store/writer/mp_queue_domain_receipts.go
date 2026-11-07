package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Proposed typed queue command RESULT child, owned by the actual queue root.
// The parent owns its header/floor/history mapping and the five retained birth
// owners. This child is NOT a generic media receipt and cannot serve reading,
// WC, security or personal roots. Birth 750 is decoded through the parent's
// separate retained queue-birth result, not this root's subsequent sequence 1.
func mpQueueResultDefinitions() []stmt.Definition {
	return []stmt.Definition{
		mpDefinition("mp.queue.result_insert", `INSERT INTO queue_v1_command_results(queue_id,command_incarnation,command_sequence,result_version,command_kind,applied_ms,result_count,result_digest,result_entry_id,result_session_id,result_next_token,result_reason,result_revision,result_custody_generation,result_position,result_shuffle_lap,result_cursor,result_count_value,result_changed,result_done) VALUES(?,?,?,1,?,?,1,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "sbiii bssssiiiiiiii", stmt.Write),
		mpDefinition("mp.queue.result_read", `SELECT result_version,command_kind,applied_ms,result_count,result_digest,result_entry_id,result_session_id,result_next_token,result_reason,result_revision,result_custody_generation,result_position,result_shuffle_lap,result_cursor,result_count_value,result_changed,result_done FROM queue_v1_command_results WHERE queue_id=? AND command_incarnation=? AND command_sequence=?`, "sbi", stmt.Read),
	}
}

type mpQueueSavedResult struct {
	kind      CommandID
	identity  CommandIdentity
	queue     string
	appliedMS int64
	effect    mpQueueEffect
	digest    [32]byte
}

func mpQueueResultCommitment(kind CommandID, e mpQueueEffect, appliedMS int64) ([32]byte, error) {
	var zero [32]byte
	b, err := mpResultBytes(kind, e)
	if err != nil || appliedMS < 0 {
		if err == nil {
			err = ErrInvalid
		}
		return zero, err
	}
	b = append(b, byte(1)) // result version, independent of request encoding
	for shift := 56; shift >= 0; shift -= 8 {
		b = append(b, byte(uint64(appliedMS)>>shift))
	}
	return sha256.Sum256(b), nil
}
func mpPersistQueueResult(ctx context.Context, t *commandTurn, v mpQueueSavedResult) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if err = mpPurpose(t, v.kind); err != nil {
		return err
	}
	if !mpQueueRootPurpose(v.kind) || v.identity.Sequence < 1 || v.identity.Incarnation == [16]byte{} || v.identity.OperationID == [16]byte{} || v.queue != v.effect.queue {
		return ErrInvalid
	}
	d, err := mpQueueResultCommitment(v.kind, v.effect, v.appliedMS)
	if err != nil {
		return err
	}
	if v.digest != d {
		return ErrConflict
	}
	e := v.effect
	return mpExec(ctx, t, "mp.queue.result_insert", v.queue, v.identity.Incarnation[:], v.identity.Sequence, int64(v.kind), v.appliedMS, d[:], e.entry, e.session, e.token, e.reason, e.revision, e.custody, e.position, e.lap, e.cursor, e.count, mpBool(e.changed), mpBool(e.done))
}

// The dispatcher MUST have performed fresh disclosure and canonical
// classifyReceipt==receiptReplay for the exact original identity/digest/kind,
// above the ACK floor, before calling this decoder. It does not consult current
// source/lease/queue CAS or manufacture a success from current domain effects.
func mpLoadQueueOriginalResult(ctx context.Context, t *commandTurn, queue string, identity CommandIdentity, kind CommandID, expectedDigest [32]byte) (out mpQueueSavedResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if err = mpPurpose(t, kind); err != nil {
		return out, err
	}
	if !mpQueueRootPurpose(kind) || identity.Sequence < 1 || identity.Incarnation == [16]byte{} {
		return out, ErrInvalid
	}
	row, err := mpQuery(ctx, t, "mp.queue.result_read", queue, identity.Incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	var version, savedKind, count, changed, done int64
	var digest []byte
	e := mpQueueEffect{queue: queue}
	err = row.Scan(&version, &savedKind, &out.appliedMS, &count, &digest, &e.entry, &e.session, &e.token, &e.reason, &e.revision, &e.custody, &e.position, &e.lap, &e.cursor, &e.count, &changed, &done)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrResolveRequired
	}
	if err != nil {
		return out, err
	}
	if version != 1 || savedKind != int64(kind) || count != 1 || changed < 0 || changed > 1 || done < 0 || done > 1 || len(digest) != 32 {
		return out, ErrResolveRequired
	}
	e.changed = changed == 1
	e.done = done == 1
	d, err := mpQueueResultCommitment(kind, e, out.appliedMS)
	if err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if string(d[:]) != string(digest) || d != expectedDigest {
		return out, ErrResolveRequired
	}
	out.kind = kind
	out.identity = identity
	out.queue = queue
	out.effect = e
	out.digest = d
	return out, nil
}

func mpQueueRootPurpose(kind CommandID) bool {
	switch kind {
	case mpQueueStage, mpQueueSeal, mpQueueEdit, mpQueueSettings, mpQueueRetire, mpQueueSave, mpQueueCleanup, mpQueueSnapshotBegin, mpQueueAdvance:
		return true
	}
	return false
}
