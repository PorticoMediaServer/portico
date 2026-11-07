package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type mpNextBirthEffect struct {
	result   mpQueueEffect
	prepared mpNextPrepared // exact immutable original birth projection
}
type mpNextSavedResult struct {
	token     string // actual next owner, never a caller-selected owner kind
	identity  CommandIdentity
	kind      CommandID
	appliedMS int64
	digest    [32]byte
	effect    mpQueueEffect
}

func mpNextResultDefinitions() []stmt.Definition {
	return []stmt.Definition{
		mpDefinition("mp.next.result_insert", `INSERT INTO playback_v1_next_command_results(token,command_incarnation,command_sequence,result_version,command_kind,applied_ms,result_count,result_digest,result_queue_id,result_entry_id,result_session_id,result_reason,result_revision,result_custody_generation,result_position,result_shuffle_lap,result_cursor,result_count_value,result_changed,result_done) VALUES(?,?,?,1,?,?,1,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "sbiiibssssiiiiiiii", stmt.Write),
		mpDefinition("mp.next.result_read", `SELECT result_version,command_kind,applied_ms,result_count,result_digest,result_queue_id,result_entry_id,result_session_id,result_reason,result_revision,result_custody_generation,result_position,result_shuffle_lap,result_cursor,result_count_value,result_changed,result_done FROM playback_v1_next_command_results WHERE token=? AND command_incarnation=? AND command_sequence=?`, "sbi", stmt.Read),
	}
}
func mpNextRootPurpose(kind CommandID) bool {
	return kind == mpCommitNext || kind == mpCancelNext || kind == mpNextPhysicalExit
}
func mpPersistNextResult(ctx context.Context, t *commandTurn, v mpNextSavedResult) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if err = mpPurpose(t, v.kind); err != nil {
		return err
	}
	if !mpNextRootPurpose(v.kind) || v.identity.Sequence < 1 || v.identity.Incarnation == [16]byte{} || v.identity.OperationID == [16]byte{} || v.token != v.effect.token {
		return ErrInvalid
	}
	d, err := mpQueueResultCommitment(v.kind, v.effect, v.appliedMS)
	if err != nil {
		return err
	}
	if d != v.digest {
		return ErrConflict
	}
	e := v.effect
	return mpExec(ctx, t, "mp.next.result_insert", v.token, v.identity.Incarnation[:], v.identity.Sequence, int64(v.kind), v.appliedMS, d[:], e.queue, e.entry, e.session, e.reason, e.revision, e.custody, e.position, e.lap, e.cursor, e.count, mpBool(e.changed), mpBool(e.done))
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
	var version, savedKind, count, changed, done int64
	var digest []byte
	e := mpQueueEffect{token: token}
	err = row.Scan(&version, &savedKind, &out.appliedMS, &count, &digest, &e.queue, &e.entry, &e.session, &e.reason, &e.revision, &e.custody, &e.position, &e.lap, &e.cursor, &e.count, &changed, &done)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrResolveRequired
	}
	if err != nil {
		return out, err
	}
	if version != 1 || savedKind != int64(kind) || count != 1 || len(digest) != 32 || changed < 0 || changed > 1 || done < 0 || done > 1 {
		return out, ErrResolveRequired
	}
	e.changed = changed == 1
	e.done = done == 1
	d, err := mpQueueResultCommitment(kind, e, out.appliedMS)
	if err != nil || d != expected || string(d[:]) != string(digest) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.token = token
	out.identity = identity
	out.kind = kind
	out.digest = d
	out.effect = e
	return out, nil
}
