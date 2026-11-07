package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Exact F scalar projection. The caller supplies the ORIGINAL full header in
// the same fresh turn. No default, current-row lookup, or scalar effect alone
// can reconstruct a public DTO or mint receipt authority.
type mpQueueReceiptHeader struct {
	repeat, retirement                                        string
	shuffle, durationKnown                                    bool
	seed, domain, first, total, remaining, automatic, updated int64
	duration                                                  sql.NullInt64
}
type mpQueueReceiptSegment struct {
	id, snapshot, kind, label, sourceRevision, state string
	count                                            int64
}

// This is an IMMUTABLE original result child, not mutable physical custody.
// ACK/reclaim of this copy must not release the physical root/stop obligation.
type mpReceiptRetireMedia struct {
	media, requestID string
	generation       int64
	pending          bool
}
type mpQueueSavedResult struct {
	kind          CommandID
	identity      CommandIdentity
	requestDigest RequestDigest
	queue         string
	appliedMS     int64
	effect        mpQueueEffect
	header        mpQueueReceiptHeader
	segments      []mpQueueReceiptSegment
	retireMedia   []mpReceiptRetireMedia
	digest        [32]byte
}

func mpQueueResultDefinitions() []stmt.Definition {
	return []stmt.Definition{
		mpDefinition("mp.queue.result_insert", `INSERT INTO mp_queue_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,applied_ms,result_digest,result_count,result_step_count,result_done,result_reason,result_automatic_advances,result_revision,result_custody_generation,result_current_entry_id,result_current_position,result_repeat,result_shuffle_on,result_shuffle_seed,result_shuffle_lap,result_shuffle_domain,result_shuffle_first,result_total,result_remaining_count,result_duration_ms,result_duration_known,result_session_id,result_updated_ms,result_changed,result_after_chunk,result_retirement_state,segments_count,retire_media_count) VALUES(?,?,?,?,?,?,1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "sbibbiibiiisiiisisiiiiiiinisiiisii", stmt.Write),
		mpDefinition("mp.queue.result_read", `SELECT operation_id,request_digest,command_kind,result_version,applied_ms,result_digest,result_count,result_step_count,result_done,result_reason,result_automatic_advances,result_revision,result_custody_generation,result_current_entry_id,result_current_position,result_repeat,result_shuffle_on,result_shuffle_seed,result_shuffle_lap,result_shuffle_domain,result_shuffle_first,result_total,result_remaining_count,result_duration_ms,result_duration_known,result_session_id,result_updated_ms,result_changed,result_after_chunk,result_retirement_state,segments_count,retire_media_count FROM mp_queue_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, "sbi", stmt.Read),
		mpDefinition("mp.queue.result_segment_insert", `INSERT INTO mp_queue_receipt_segments(owner_id,command_incarnation,command_sequence,ordinal,segment_id,snapshot_id,kind,label,source_revision,count,state) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, "sbiisssssis", stmt.Write),
		mpDefinition("mp.queue.result_segments", `SELECT ordinal,segment_id,snapshot_id,kind,label,source_revision,count,state FROM mp_queue_receipt_segments WHERE owner_id=? AND command_incarnation=? AND command_sequence=? ORDER BY ordinal LIMIT 41`, "sbi", stmt.Read),
		mpDefinition("mp.queue.result_retire_insert", `INSERT INTO mp_queue_receipt_retire_media(owner_id,command_incarnation,command_sequence,ordinal,media_session_id,generation,request_id,physical_pending) VALUES(?,?,?,?,?,?,?,?)`, "sbiisisi", stmt.Write),
		mpDefinition("mp.queue.result_retire", `SELECT ordinal,media_session_id,generation,request_id,physical_pending FROM mp_queue_receipt_retire_media WHERE owner_id=? AND command_incarnation=? AND command_sequence=? ORDER BY ordinal LIMIT 3`, "sbi", stmt.Read),
	}
}

// Stable primitive encoding shared by two separately typed root codecs. This
// is neither a generic result graph nor a serialized native receipt column.
type mpReceiptEncoder struct {
	bytes []byte
	err   error
}

func (e *mpReceiptEncoder) text(s string, max int) {
	if len(s) > max || !utf8.ValidString(s) {
		e.err = ErrInvalid
		return
	}
	e.integer(int64(len(s)))
	e.bytes = append(e.bytes, s...)
}
func (e *mpReceiptEncoder) integer(n int64) {
	e.bytes = binary.BigEndian.AppendUint64(e.bytes, uint64(n))
}
func (e *mpReceiptEncoder) boolean(b bool) { e.integer(mpBool(b)) }
func (e *mpReceiptEncoder) optional(n sql.NullInt64) {
	e.boolean(n.Valid)
	if n.Valid {
		e.integer(n.Int64)
	}
}
func mpReceiptIdentityValid(identity CommandIdentity, request RequestDigest) bool {
	return identity.Sequence > 0 && identity.Incarnation != [16]byte{} && identity.OperationID != [16]byte{} && request != RequestDigest{}
}
func mpEncodeReceiptIdentity(e *mpReceiptEncoder, owner string, identity CommandIdentity, request RequestDigest, kind CommandID, at int64) {
	e.text(owner, 128)
	e.bytes = append(e.bytes, identity.Incarnation[:]...)
	e.integer(identity.Sequence)
	e.bytes = append(e.bytes, identity.OperationID[:]...)
	e.bytes = append(e.bytes, request[:]...)
	e.integer(int64(kind))
	e.integer(1)
	e.integer(at)
}
func mpRetireResultsValid(values []mpReceiptRetireMedia) bool {
	if len(values) > 2 {
		return false
	}
	for i, v := range values {
		if !mpID(v.media) || v.generation < 1 || v.requestID == "" || len(v.requestID) > 512 || !utf8.ValidString(v.requestID) {
			return false
		}
		for _, p := range values[:i] {
			if p.media == v.media && p.generation == v.generation {
				return false
			}
		}
	}
	return true
}
func mpEncodeRetireResults(e *mpReceiptEncoder, values []mpReceiptRetireMedia) {
	e.integer(int64(len(values)))
	for i, v := range values {
		e.integer(int64(i))
		e.text(v.media, 128)
		e.integer(v.generation)
		e.text(v.requestID, 512)
		e.boolean(v.pending)
	}
}

func mpQueueSavedCommitment(v mpQueueSavedResult) ([32]byte, error) {
	var zero [32]byte
	e, h := v.effect, v.header
	if !mpQueueRootPurpose(v.kind) || !mpReceiptIdentityValid(v.identity, v.requestDigest) || v.appliedMS < 0 || v.queue != e.queue || e.token != "" || len(v.segments) > 40 || !mpRetireResultsValid(v.retireMedia) {
		return zero, ErrInvalid
	}
	if h.repeat != "off" && h.repeat != "one" && h.repeat != "all" || h.retirement != "live" && h.retirement != "draining" && h.retirement != "retired" || h.seed < 0 || h.domain < 0 || h.first < 0 || h.total < 0 || h.remaining < 0 || h.remaining > h.total || h.automatic < 0 || h.updated < 0 || h.duration.Valid && h.duration.Int64 < 0 || h.durationKnown && !h.duration.Valid {
		return zero, ErrInvalid
	}
	effect, err := mpResultBytes(v.kind, e)
	if err != nil {
		return zero, err
	}
	out := mpReceiptEncoder{bytes: []byte("MP-F-queue-receipt-v1\x00")}
	mpEncodeReceiptIdentity(&out, v.queue, v.identity, v.requestDigest, v.kind, v.appliedMS)
	out.bytes = append(out.bytes, effect...)
	out.integer(1 + int64(len(v.segments)+len(v.retireMedia)))
	out.text(h.repeat, 3)
	out.text(h.retirement, 8)
	out.boolean(h.shuffle)
	out.integer(h.seed)
	out.integer(h.domain)
	out.integer(h.first)
	out.integer(h.total)
	out.integer(h.remaining)
	out.integer(h.automatic)
	out.integer(h.updated)
	out.optional(h.duration)
	out.boolean(h.durationKnown)
	out.integer(int64(len(v.segments)))
	for i, s := range v.segments {
		if !mpID(s.id) || !mpSnapshotID(s.snapshot) || s.count < 0 {
			return zero, ErrInvalid
		}
		out.integer(int64(i))
		out.text(s.id, 128)
		out.text(s.snapshot, 128)
		out.text(s.kind, 128)
		out.text(s.label, 1024)
		out.text(s.sourceRevision, 256)
		out.integer(s.count)
		out.text(s.state, 32)
	}
	mpEncodeRetireResults(&out, v.retireMedia)
	if out.err != nil {
		return zero, out.err
	}
	return sha256.Sum256(out.bytes), nil
}
func mpQueueScalarResultArgs(v mpQueueSavedResult) []any {
	e, h := v.effect, v.header
	var duration any
	if h.duration.Valid {
		duration = h.duration.Int64
	}
	return []any{v.queue, v.identity.Incarnation[:], v.identity.Sequence, v.identity.OperationID[:], v.requestDigest[:], int64(v.kind), v.appliedMS, v.digest[:], int64(1 + len(v.segments) + len(v.retireMedia)), e.count, mpBool(e.done), e.reason, h.automatic, e.revision, e.custody, e.entry, e.position, h.repeat, mpBool(h.shuffle), h.seed, e.lap, h.domain, h.first, h.total, h.remaining, duration, mpBool(h.durationKnown), e.session, h.updated, mpBool(e.changed), e.cursor, h.retirement, int64(len(v.segments)), int64(len(v.retireMedia))}
}
func mpPersistQueueResult(ctx context.Context, t *commandTurn, v mpQueueSavedResult) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if err = mpPurpose(t, v.kind); err != nil {
		return err
	}
	d, err := mpQueueSavedCommitment(v)
	if err != nil {
		return err
	}
	if d != v.digest {
		return ErrConflict
	}
	if err = mpExec(ctx, t, "mp.queue.result_insert", mpQueueScalarResultArgs(v)...); err != nil {
		return err
	}
	for i, s := range v.segments {
		if err = mpExec(ctx, t, "mp.queue.result_segment_insert", v.queue, v.identity.Incarnation[:], v.identity.Sequence, int64(i), s.id, s.snapshot, s.kind, s.label, s.sourceRevision, s.count, s.state); err != nil {
			return err
		}
	}
	return mpPersistRetireResults(ctx, t, "mp.queue.result_retire_insert", v.queue, v.identity, v.retireMedia)
}
func mpPersistRetireResults(ctx context.Context, t *commandTurn, id, owner string, identity CommandIdentity, values []mpReceiptRetireMedia) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if id != "mp.queue.result_retire_insert" && id != "mp.next.result_retire_insert" {
		return ErrInvalid
	}
	if !mpRetireResultsValid(values) {
		return ErrInvalid
	}
	for i, v := range values {
		if err = mpExec(ctx, t, id, owner, identity.Incarnation[:], identity.Sequence, int64(i), v.media, v.generation, v.requestID, mpBool(v.pending)); err != nil {
			return err
		}
	}
	return nil
}
func mpReadRetireResults(ctx context.Context, t *commandTurn, id, owner string, identity CommandIdentity, count int64) (out []mpReceiptRetireMedia, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if id != "mp.queue.result_retire" && id != "mp.next.result_retire" {
		return out, ErrInvalid
	}
	if count < 0 || count > 2 {
		return out, ErrResolveRequired
	}
	rows, err := mpRows(ctx, t, id, owner, identity.Incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var ordinal, pending int64
		var v mpReceiptRetireMedia
		if err = rows.Scan(&ordinal, &v.media, &v.generation, &v.requestID, &pending); err != nil {
			break
		}
		if ordinal != int64(len(out)) || ordinal >= count || pending < 0 || pending > 1 {
			err = ErrResolveRequired
			break
		}
		v.pending = pending == 1
		out = append(out, v)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return out, err
	}
	if int64(len(out)) != count || !mpRetireResultsValid(out) {
		return out, ErrResolveRequired
	}
	return out, nil
}

// Fresh disclosure + canonical identity/digest/floor classification precedes
// this complete original decoder. No current source/header/exit lookup occurs.
func mpLoadQueueOriginalResult(ctx context.Context, t *commandTurn, queue string, identity CommandIdentity, kind CommandID, expected [32]byte) (out mpQueueSavedResult, err error) {
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
	var operation, request, digest []byte
	var savedKind, version, count, done, shuffle, known, changed, segments, retire int64
	out.queue = queue
	out.identity = identity
	out.kind = kind
	out.effect.queue = queue
	e, h := &out.effect, &out.header
	err = row.Scan(&operation, &request, &savedKind, &version, &out.appliedMS, &digest, &count, &e.count, &done, &e.reason, &h.automatic, &e.revision, &e.custody, &e.entry, &e.position, &h.repeat, &shuffle, &h.seed, &e.lap, &h.domain, &h.first, &h.total, &h.remaining, &h.duration, &known, &e.session, &h.updated, &changed, &e.cursor, &h.retirement, &segments, &retire)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrResolveRequired
	}
	if err != nil {
		return out, err
	}
	if string(operation) != string(identity.OperationID[:]) || len(request) != 32 || len(digest) != 32 || version != 1 || savedKind != int64(kind) || segments < 0 || segments > 40 || retire < 0 || retire > 2 || count != 1+segments+retire || done < 0 || done > 1 || shuffle < 0 || shuffle > 1 || known < 0 || known > 1 || changed < 0 || changed > 1 {
		return out, ErrResolveRequired
	}
	copy(out.requestDigest[:], request)
	copy(out.digest[:], digest)
	e.done = done == 1
	e.changed = changed == 1
	h.shuffle = shuffle == 1
	h.durationKnown = known == 1
	rows, err := mpRows(ctx, t, "mp.queue.result_segments", queue, identity.Incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var ordinal int64
		var s mpQueueReceiptSegment
		if err = rows.Scan(&ordinal, &s.id, &s.snapshot, &s.kind, &s.label, &s.sourceRevision, &s.count, &s.state); err != nil {
			break
		}
		if ordinal != int64(len(out.segments)) || ordinal >= segments {
			err = ErrResolveRequired
			break
		}
		out.segments = append(out.segments, s)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return out, err
	}
	if int64(len(out.segments)) != segments {
		return out, ErrResolveRequired
	}
	out.retireMedia, err = mpReadRetireResults(ctx, t, "mp.queue.result_retire", queue, identity, retire)
	if err != nil {
		return out, err
	}
	computed, err := mpQueueSavedCommitment(out)
	if err != nil || computed != expected || computed != out.digest {
		return out, errors.Join(ErrResolveRequired, err)
	}
	return out, nil
}

// Pure effect-only commitment remains a component test utility, never the F
// receipt digest. F persistence/load above commits every native scalar/child.
func mpQueueEffectCommitment(kind CommandID, e mpQueueEffect, appliedMS int64) ([32]byte, error) {
	var zero [32]byte
	b, err := mpResultBytes(kind, e)
	if err != nil || appliedMS < 0 {
		if err == nil {
			err = ErrInvalid
		}
		return zero, err
	}
	b = binary.BigEndian.AppendUint64(b, uint64(appliedMS))
	return sha256.Sum256(b), nil
}
func mpQueueRootPurpose(kind CommandID) bool {
	switch kind {
	case mpQueueStage, mpQueueSeal, mpQueueEdit, mpQueueSettings, mpQueueRetire, mpQueueSave, mpQueueCleanup, mpQueueSnapshotBegin, mpQueueAdvance:
		return true
	}
	return false
}
