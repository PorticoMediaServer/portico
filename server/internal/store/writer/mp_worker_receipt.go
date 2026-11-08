package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"
)

type mpWorkerReceiptLimits struct{ rows, bytes int64 }

func (q mpWorkerReceiptLimits) valid() bool { return q.rows > 0 && q.bytes > 0 }

type mpWorkerOwnerFacts struct {
	receiptOwnerFacts
	revision, rows, bytes, control int64
	state                          string
}

func checkMPWorkerNativeDisclosure(ctx context.Context, t *commandTurn, v *mpRepeatInvocation) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if err = checkMPWorkerDisclosure(v.disclosure.writer, v); err != nil {
		return err
	}
	s := v.disclosure.scope
	n := mpWorkerWorkingBytes(v, 0)
	if err = t.requireCommandMemory(ctx, MPQueueRepeat, n, "mp_worker_memory"); err != nil {
		return err
	}
	id := wcProgressNativeScope
	args := []any{s.accountID, s.profileID, s.memberBirth, s.profileBirth, s.memberLibraries, s.memberRevision, s.profileRevision, s.membershipRole, s.primary, s.primaryProfile}
	if s.profileLibraries.Valid {
		id = wcProgressNativeProfileScope
		args = append(args, s.profileLibraries.String)
	}
	row, e := wc008DraftQuery(ctx, t, id, args...)
	if e != nil {
		return e
	}
	var epoch, primary int64
	if e = row.Scan(&epoch, &primary); e != nil {
		return errors.Join(ErrDisclosureDenied, e)
	}
	if epoch != s.epoch || s.role != "member" && (primary == 0 || s.membershipRole != s.role) {
		return ErrDisclosureDenied
	}
	row, e = wc008DraftQuery(ctx, t, wcProgressNativeToken, s.tokenHash, s.familyID, s.serverID, s.accountID, s.profileID, s.role, s.epoch, s.tokenGeneration, s.memberBirth, s.profileBirth, s.horizon, s.expires)
	if e != nil {
		return e
	}
	var generation int64
	if e = row.Scan(&generation); e != nil {
		return errors.Join(ErrDisclosureDenied, e)
	}
	horizon, e := time.Parse(time.RFC3339Nano, s.horizon)
	if e != nil {
		return ErrDisclosureDenied
	}
	expires, e := time.Parse(time.RFC3339Nano, s.expires)
	if e != nil {
		return ErrDisclosureDenied
	}
	deadline, ok := t.transaction.ctx.Deadline()
	if !ok || generation != s.tokenGeneration || deadline.After(horizon) || deadline.After(expires) {
		return ErrDisclosureDenied
	}
	return nil
}

func mpWorkerReadOwner(ctx context.Context, t *commandTurn, v *mpRepeatInvocation) (out mpWorkerOwnerFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	s := v.disclosure.scope
	row, err := wc008DraftQuery(ctx, t, mpWorkerOwner, s.familyID, v.queue, s.accountID, s.profileID, v.disclosure.device, s.memberBirth, s.profileBirth)
	if err != nil {
		return out, err
	}
	var inc []byte
	if err = row.Scan(&inc, &out.lastSequence, &out.acknowledgedSequence, &out.revision, &out.rows, &out.bytes, &out.control, &out.state); err != nil {
		return out, errors.Join(ErrDisclosureDenied, err)
	}
	if len(inc) != 16 || out.revision <= 0 || out.rows < 0 || out.bytes < 0 || out.control <= 0 || (out.rows == 0) != (out.bytes == 0) {
		return out, ErrResolveRequired
	}
	copy(out.incarnation[:], inc)
	out.exists = true
	return out, nil
}

func executeMPRepeat(ctx context.Context, t *commandTurn, v *mpRepeatInvocation, limits mpWorkerReceiptLimits) (out mpWorkerResult, historical bool, err error) {
	defer func() {
		x, e := finishCommand(ctx, commandLifetime(t), struct {
			O mpWorkerResult
			H bool
		}{out, historical}, err)
		out, historical, err = x.O, x.H, e
	}()
	owner, err := mpWorkerReadOwner(ctx, t, v)
	if err != nil {
		return out, false, err
	}
	id, request := v.reference.Identity, v.reference.RequestDigest
	row, err := wc008DraftQuery(ctx, t, mpWorkerHeader, v.queue, id.Incarnation[:], id.Sequence)
	if err != nil {
		return out, false, err
	}
	var op, req, digest []byte
	var kind CommandID
	var version, count, applied, size, storedBytes int64
	found := receiptFacts{}
	e := row.Scan(&op, &kind, &req, &version, &count, &digest, &applied, &size, &storedBytes)
	if e == nil {
		if len(op) != 16 || len(req) != 32 || len(digest) != 32 || version != 1 || count != 1 || applied <= 0 || size <= 0 || size > mpRepeatPayloadCap || storedBytes != int64(len(v.queue)+16+16+32+32)+size {
			return out, false, ErrResolveRequired
		}
		found.exists = true
		found.identity.command = id
		copy(found.identity.command.OperationID[:], op)
		copy(found.identity.digest[:], req)
		found.kind = kind
		found.resultCount = count
		copy(found.resultDigest[:], digest)
	} else if !errors.Is(e, sql.ErrNoRows) {
		return out, false, e
	}
	decision, err := classifyReceipt(receiptIdentity{id, request}, MPQueueRepeat, owner.receiptOwnerFacts, found)
	if err != nil {
		return out, false, err
	}
	out.Outcome.Reference = v.reference
	switch decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, true, nil
	case receiptReplay:
		if owner.rows < 1 || owner.bytes < storedBytes {
			return out, true, ErrResolveRequired
		}
		if err = t.requireCommandMemory(ctx, MPQueueRepeat, mpWorkerWorkingBytes(v, uint64(size)), "mp_worker_memory"); err != nil {
			return out, true, err
		}
		row, err = wc008DraftQuery(ctx, t, mpWorkerPayload, v.queue, id.Incarnation[:], id.Sequence)
		if err != nil {
			return out, true, err
		}
		var payload []byte
		if err = row.Scan(&payload); err != nil {
			return out, true, err
		}
		if int64(len(payload)) != size {
			return out, true, ErrResolveRequired
		}
		out.Value, err = decodeMPRepeatPayload(id, request, payload, ResultDigest(found.resultDigest))
		if err != nil || out.Value.Queue != v.queue || out.Value.AppliedMS != applied {
			return out, true, errors.Join(ErrResolveRequired, err)
		}
		out.Digest = ResultDigest(found.resultDigest)
		out.Outcome.State, out.Outcome.Replayed = Applied, true
		return out, true, nil
	case receiptFresh:
		out.Outcome.State = Pending
	default:
		return out, false, ErrResolveRequired
	}
	if v.input == nil {
		return out, false, nil
	}
	if owner.state != "live" || !limits.valid() {
		return out, false, ErrOwnerUnavailable
	}
	if owner.revision == math.MaxInt64 || owner.rows == math.MaxInt64 {
		return out, false, ErrCap
	}
	if owner.rows >= limits.rows {
		return out, false, &Refusal{Code: RefusalCapacity}
	}
	now, err := t.commandTimeMS(ctx, MPQueueRepeat, 0)
	if err != nil {
		return out, false, err
	}
	out.Value, err = applyMPRepeat(ctx, t, v.input.input, now)
	if err != nil {
		return out, false, err
	}
	payload, d, err := mpRepeatPayload(id, request, out.Value)
	if err != nil {
		return out, false, err
	}
	expected := int64(len(v.queue) + 16 + 16 + 32 + 32 + len(payload))
	if expected > limits.bytes || owner.bytes > limits.bytes-expected {
		return out, false, &Refusal{Code: RefusalCapacity}
	}
	row, err = wc008DraftQuery(ctx, t, mpWorkerInsert, v.queue, id.Incarnation[:], id.Sequence, id.OperationID[:], int64(MPQueueRepeat), request[:], int64(1), int64(1), d[:], payload, now)
	if err != nil {
		return out, false, err
	}
	var actual int64
	if err = row.Scan(&actual); err != nil {
		return out, false, err
	}
	if actual != expected || owner.bytes > math.MaxInt64-actual {
		return out, false, ErrResolveRequired
	}
	row, err = wc008DraftQuery(ctx, t, mpWorkerDebt, id.Sequence, actual, v.queue, id.Incarnation[:], owner.lastSequence, owner.acknowledgedSequence, owner.revision, owner.rows, owner.bytes, owner.control)
	if err != nil {
		return out, false, err
	}
	var last, revision, rows, bytes, control int64
	if err = row.Scan(&last, &revision, &rows, &bytes, &control); err != nil {
		return out, false, err
	}
	if last != id.Sequence || revision != owner.revision+1 || rows != owner.rows+1 || bytes != owner.bytes+actual || control != owner.control {
		return out, false, ErrResolveRequired
	}
	out.Digest = d
	return out, false, nil
}
