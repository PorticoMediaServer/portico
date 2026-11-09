package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
)

// 223/224 bodies. Current owner authority precedes historical receipt
// disclosure; the ORIGINAL saved result is served without any fresh
// source/lease/policy probe. A fresh command then re-proves the exact retained
// subject job, accepted artwork source, resolved language and policy, and the
// native image job whose ID is the intent ID. The installed handoff re-reads its
// complete slot manifest through point octet-preflighted keys before it records
// the same bytes, so a tampered or truncated manifest is refused whole.

func ipArtworkMaterialLookup(ctx context.Context, turn *commandTurn, kind CommandID, q ipArtworkMaterialRequest, identity CommandIdentity, digest RequestDigest) (out ipArtworkMaterialOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != kind {
		return out, ErrInvalid
	}
	rows, err := ipTextRows(ctx, turn, ipAMOwner, q.owner.id)
	if err != nil {
		return out, err
	}
	var owner receiptOwnerFacts
	var incarnation []byte
	if err = ipTextOne(rows, func() error {
		return rows.Scan(&incarnation, &owner.lastSequence, &owner.acknowledgedSequence)
	}); err != nil {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable, err)
	}
	if len(incarnation) != 16 {
		return out, ErrResolveRequired
	}
	owner.exists = true
	copy(owner.incarnation[:], incarnation)

	rows, err = ipTextRows(ctx, turn, ipAMReceiptOctet, q.owner.id, q.owner.incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	var intentBytes, tokenBytes int64
	err = ipTextOne(rows, func() error { return rows.Scan(&intentBytes, &tokenBytes) })
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err == nil && (intentBytes < 1 || intentBytes > ipArtworkMaterialMaxIntent || tokenBytes != 0) {
		return out, ErrResolveRequired
	}
	rows, err = ipTextRows(ctx, turn, ipAMReceipt, q.owner.id, q.owner.incarnation[:], identity.Sequence)
	if err != nil {
		return out, err
	}
	var found receiptFacts
	var operation, request, result, token []byte
	var nativeKind, revision, appliedMS, count, bytesTotal, leaseGeneration, leaseUntil int64
	var intent string
	var saved ipArtworkMaterialResult
	err = ipTextOne(rows, func() error {
		return rows.Scan(&operation, &request, &result, &nativeKind, &revision, &appliedMS,
			&count, &bytesTotal, &leaseGeneration, &leaseUntil, &intent, &token)
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err == nil {
		if len(operation) != 16 || len(request) != 32 || len(result) != 32 ||
			CommandID(nativeKind) != kind || revision != ipArtworkMaterialRevision(kind) ||
			appliedMS < 0 || count != q.spec.expectedSlots || count < 3 || count > 4 ||
			bytesTotal < 0 || bytesTotal > ipArtworkMaterialMaxBytes ||
			leaseGeneration <= 0 || leaseUntil <= 0 || len(token) != 0 ||
			len(intent) != int(intentBytes) || intentBytes < 1 || intentBytes > ipArtworkMaterialMaxIntent {
			return out, ErrResolveRequired
		}
		found.exists = true
		found.kind = kind
		found.resultCount = count
		actual := receiptIdentity{command: identity}
		copy(actual.command.OperationID[:], operation)
		copy(actual.digest[:], request)
		found.identity = actual
		copy(found.resultDigest[:], result)
		saved.IntentID = intent
		saved.Revision, saved.Slots, saved.Bytes, saved.JobGeneration = revision, count, bytesTotal, leaseGeneration
	}
	out.decision, err = classifyReceipt(receiptIdentity{identity, digest}, kind, owner, found)
	if err != nil {
		return out, err
	}
	if out.decision != receiptReplay {
		return out, nil
	}
	if saved.IntentID != q.spec.intentID || saved.Slots != q.spec.expectedSlots ||
		(kind == IPArtworkMaterialInstalled && saved.JobGeneration != q.jobGeneration) ||
		(kind == IPArtworkMaterialInstalled && saved.Bytes != ipArtworkMaterialSlotBytes(q.slots)) ||
		(kind == IPArtworkMaterialIntent && saved.Bytes != 0) {
		return out, ErrResolveRequired
	}
	expected, e := ipArtworkMaterialResultDigest(kind, identity, digest, saved)
	if e != nil || !bytes.Equal(expected[:], result) {
		return out, errors.Join(ErrResolveRequired, e)
	}
	out.result, out.digest = saved, expected
	return out, nil
}

func ipArtworkMaterialRevision(kind CommandID) int64 {
	switch kind {
	case IPArtworkMaterialIntent:
		return 1
	case IPArtworkMaterialInstalled:
		return 2
	}
	return 0
}

// The accepted source and current settings are re-derived, never taken from the
// provider response or from the caller's earlier preparation.
func ipArtworkMaterialFreshSource(ctx context.Context, turn *commandTurn, q ipArtworkMaterialRequest, nowMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	o := q.owner
	rows, err := ipTextRows(ctx, turn, ipAMFreshOwner, o.id, o.incarnation[:], o.entityID, o.personID,
		o.sourceIncarnation[:], o.sourceRevision, o.revision, o.leaseOwner, o.leaseGeneration,
		o.leaseUntilMS, nowMS)
	if err != nil {
		return err
	}
	var one int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&one) }); err != nil {
		return errors.Join(ErrConflict, err)
	}
	var source []byte
	var revision, retired, language int64
	if o.entityID > 0 {
		rows, err = ipTextRows(ctx, turn, ipAMEntity, q.spec.language, o.entityID)
		if err != nil {
			return err
		}
		if err = ipTextOne(rows, func() error {
			return rows.Scan(&source, &revision, &retired, &language)
		}); err != nil {
			return errors.Join(ErrConflict, err)
		}
		if retired != 0 {
			return ErrConflict
		}
	} else {
		rows, err = ipTextRows(ctx, turn, ipAMPerson, q.spec.language, o.personID)
		if err != nil {
			return err
		}
		if err = ipTextOne(rows, func() error {
			return rows.Scan(&source, &revision, &language)
		}); err != nil {
			return errors.Join(ErrConflict, err)
		}
	}
	if !bytes.Equal(source, o.sourceIncarnation[:]) || revision != o.sourceRevision || language != 1 {
		return ErrConflict
	}
	rows, err = ipTextRows(ctx, turn, ipAMPolicy)
	if err != nil {
		return err
	}
	var policy int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&policy) }); err != nil {
		return errors.Join(ErrConflict, err)
	}
	if policy != q.spec.policyRevision {
		return ErrConflict
	}
	return nil
}

// One exact intent row, bound to every declared source fact and to the caller's
// generation/revision/state. The staged count is part of the CAS witness.
func ipArtworkMaterialIntentMatch(ctx context.Context, turn *commandTurn, q ipArtworkMaterialRequest, state string, revision, staged int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	s := q.spec
	rows, err := ipTextRows(ctx, turn, ipAMIntent, s.intentID, q.owner.id, q.owner.incarnation[:],
		q.intentGeneration, revision, s.sourceDigest, s.acceptedMIME, s.acceptedWidth, s.acceptedHeight,
		s.largeEdge, s.encoderRevision, state, s.expectedSlots, staged)
	if err != nil {
		return err
	}
	var one int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&one) }); err != nil {
		return errors.Join(ErrConflict, err)
	}
	return nil
}

// The complete installed manifest is read back exactly: contiguous RAW slot
// keys, point octet preflight, then the scalars and texts. A prefix, gap,
// duplicate or changed byte is a conflict.
func ipArtworkMaterialManifest(ctx context.Context, turn *commandTurn, intentID string, want []ipArtworkMaterialSlot) (total int64, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), total, err)
		total, err = v, e
	}()
	if int64(len(want)) < 3 || int64(len(want)) > 4 {
		return 0, ErrInvalid
	}
	keys, err := ipArtworkMaterialSlotKeys(ctx, turn, intentID, -1, int64(len(want))+1)
	if err != nil {
		return 0, err
	}
	if int64(len(keys)) != int64(len(want)) {
		return 0, ErrConflict
	}
	for n, key := range keys {
		if key != int64(n) {
			return 0, ErrConflict
		}
		slot, e := ipArtworkMaterialSlotRead(ctx, turn, intentID, key)
		if e != nil {
			return 0, e
		}
		if slot != want[n] {
			return 0, ErrConflict
		}
		total += slot.bytes
		if total > ipArtworkMaterialMaxBytes {
			return 0, ErrConflict
		}
	}
	return total, nil
}

func ipArtworkMaterialSlotKeys(ctx context.Context, turn *commandTurn, intentID string, after, limit int64) (out []int64, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	if limit <= 0 || limit > 5 || after < -1 {
		return nil, ErrInvalid
	}
	rows, err := ipTextRows(ctx, turn, ipAMSlotKeys, intentID, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var slot int64
		if err = rows.Scan(&slot); err != nil {
			return nil, err
		}
		if slot <= after || slot > 3 || int64(len(out)) >= limit {
			return nil, ErrConflict
		}
		out = append(out, slot)
		after = slot
	}
	return out, rows.Err()
}

func ipArtworkMaterialSlotRead(ctx context.Context, turn *commandTurn, intentID string, slot int64) (out ipArtworkMaterialSlot, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	rows, err := ipTextRows(ctx, turn, ipAMSlotOctets, intentID, slot)
	if err != nil {
		return out, err
	}
	var digestBytes, mimeBytes int64
	if err = ipTextOne(rows, func() error { return rows.Scan(&digestBytes, &mimeBytes) }); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if digestBytes != 64 || (mimeBytes != 9 && mimeBytes != 10) {
		return out, ErrConflict
	}
	rows, err = ipTextRows(ctx, turn, ipAMSlotExact, intentID, slot)
	if err != nil {
		return out, err
	}
	if err = ipTextOne(rows, func() error {
		return rows.Scan(&out.digest, &out.mime, &out.width, &out.height, &out.bytes, &out.outlier)
	}); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if int64(len(out.digest)) != digestBytes || int64(len(out.mime)) != mimeBytes {
		return out, ErrConflict
	}
	out.slot = slot
	return out, nil
}

func ipArtworkMaterialSlotBytes(slots []ipArtworkMaterialSlot) int64 {
	var total int64
	for _, slot := range slots {
		total += slot.bytes
	}
	return total
}

func ipArtworkMaterialFresh(ctx context.Context, turn *commandTurn, kind CommandID, q ipArtworkMaterialRequest, nowMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if err = ipArtworkMaterialFreshSource(ctx, turn, q, nowMS); err != nil {
		return err
	}
	if kind == IPArtworkMaterialIntent {
		return ipArtworkMaterialJobDue(ctx, turn, q, nowMS)
	}
	if err = ipArtworkMaterialIntentMatch(ctx, turn, q, "prepared", 1, 0); err != nil {
		return err
	}
	return ipArtworkMaterialJobLive(ctx, turn, q, nowMS)
}

func applyIPArtworkMaterial(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b ipPolicyBudget, kind CommandID, c *ipArtworkMaterialCommand) (out ipArtworkMaterialCommandResult, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	var zero ipArtworkMaterialCommandResult
	if c == nil || !c.valid || turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != kind {
		return zero, ErrInvalid
	}
	q := c.request
	if err = ipArtworkMaterialRequestShape(kind, c.identity, q); err != nil {
		return zero, err
	}
	actual, err := canonicalRequestDigest(kind, 1, c.identity, q)
	if err != nil || actual != c.digest {
		return zero, errors.Join(ErrConflict, err)
	}
	auth, err := authorizeIPPolicyOwner(ctx, turn, p, lifetime, b)
	if err != nil {
		return zero, err
	}
	lookup, err := ipArtworkMaterialLookup(ctx, turn, kind, q, c.identity, c.digest)
	if err != nil {
		return zero, err
	}
	out.Outcome.Reference = c.reference()
	switch lookup.decision {
	case receiptRetired:
		out.Outcome.State = ResultRetired
		return out, nil
	case receiptReplay:
		out.Outcome.State = Applied
		out.Outcome.Replayed = true
		out.Value, out.Digest = lookup.result, lookup.digest
		return out, nil
	case receiptFresh:
	default:
		return zero, ErrResolveRequired
	}
	if err = auth.ipPolicyAuthorityRecheck(ctx, turn); err != nil {
		return zero, err
	}
	now, err := turn.transaction.clockTime()
	if err != nil {
		return zero, err
	}
	appliedMS := now.UnixMilli()
	if appliedMS < 0 {
		return zero, ErrInvalid
	}
	if err = ipArtworkMaterialFresh(ctx, turn, kind, q, appliedMS); err != nil {
		return zero, err
	}
	var value ipArtworkMaterialResult
	switch kind {
	case IPArtworkMaterialIntent:
		value, err = ipArtworkMaterialDeclare(ctx, turn, q, appliedMS)
	case IPArtworkMaterialInstalled:
		value, err = ipArtworkMaterialInstall(ctx, turn, q, appliedMS)
	}
	if err != nil {
		return zero, err
	}
	digest, err := ipArtworkMaterialResultDigest(kind, c.identity, c.digest, value)
	if err != nil {
		return zero, err
	}
	if err = ipArtworkMaterialRecord(ctx, turn, kind, q, c.identity, c.digest, digest, value, appliedMS); err != nil {
		return zero, err
	}
	saved, err := ipArtworkMaterialLookup(ctx, turn, kind, q, c.identity, c.digest)
	if err != nil || saved.decision != receiptReplay || saved.digest != digest || saved.result != value {
		return zero, errors.Join(ErrResolveRequired, err)
	}
	out.Value, out.Digest, out.Outcome.State = saved.result, saved.digest, Applied
	return out, nil
}

// The intent is durable BEFORE any file exists: the exact native job is claimed
// and one prepared intent row fixes the original generation, source facts and
// expected slot count. No source/blob/selection/history row is written.
func ipArtworkMaterialDeclare(ctx context.Context, turn *commandTurn, q ipArtworkMaterialRequest, nowMS int64) (out ipArtworkMaterialResult, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	s := q.spec
	generation, err := ipArtworkMaterialClaimJob(ctx, turn, q, nowMS)
	if err != nil {
		return out, err
	}
	if err = ipTextExecOne(ctx, turn, ipAMIntentCreate, s.intentID, q.owner.id, q.owner.incarnation[:],
		generation, s.sourceDigest, s.acceptedMIME, s.acceptedWidth, s.acceptedHeight, s.largeEdge,
		s.encoderRevision, s.expectedSlots); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	q.intentGeneration = generation
	if err = ipArtworkMaterialIntentMatch(ctx, turn, q, "prepared", 1, 0); err != nil {
		return out, err
	}
	keys, e := ipArtworkMaterialSlotKeys(ctx, turn, s.intentID, -1, 1)
	if e != nil || len(keys) != 0 {
		return out, errors.Join(ErrResolveRequired, e)
	}
	return ipArtworkMaterialResult{IntentID: s.intentID, Revision: 1, Slots: s.expectedSlots,
		JobGeneration: generation}, nil
}

// The handoff records the complete installed manifest and advances the intent in
// one CAS. The original slot 3 (when declared) is written like any other slot,
// so its bytes remain charged outside the selection/history closure until IP-20
// publishes or IP-23 retires it.
func ipArtworkMaterialInstall(ctx context.Context, turn *commandTurn, q ipArtworkMaterialRequest, nowMS int64) (out ipArtworkMaterialResult, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	s := q.spec
	for _, slot := range q.slots {
		outlier := int64(0)
		if slot.outlier {
			outlier = 1
		}
		if err = ipTextExecOne(ctx, turn, ipAMSlotCreate, s.intentID, slot.slot, slot.digest, slot.mime,
			slot.width, slot.height, slot.bytes, outlier); err != nil {
			return out, errors.Join(ErrConflict, err)
		}
	}
	if err = ipTextExecOne(ctx, turn, ipAMSlotStage, s.expectedSlots, s.intentID, q.owner.id,
		q.owner.incarnation[:], q.intentGeneration, q.intentRevision, s.expectedSlots,
		s.sourceDigest); err != nil {
		return out, err
	}
	if err = ipArtworkMaterialIntentMatch(ctx, turn, q, "installed", 2, s.expectedSlots); err != nil {
		return out, err
	}
	total, err := ipArtworkMaterialManifest(ctx, turn, s.intentID, q.slots)
	if err != nil {
		return out, err
	}
	if err = ipArtworkMaterialCompleteJob(ctx, turn, q, nowMS); err != nil {
		return out, err
	}
	return ipArtworkMaterialResult{IntentID: s.intentID, Revision: 2, Slots: s.expectedSlots,
		Bytes: total, JobGeneration: q.jobGeneration}, nil
}

// The owner CAS is the command allowance and the receipt is saved in the same
// transaction. ACK alone does not retire the pinned intent or its manifest.
func ipArtworkMaterialRecord(ctx context.Context, turn *commandTurn, kind CommandID, q ipArtworkMaterialRequest, identity CommandIdentity, request RequestDigest, result ResultDigest, value ipArtworkMaterialResult, appliedMS int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	o := q.owner
	if err = ipTextExecOne(ctx, turn, ipAMOwnerCAS, identity.Sequence, o.id, o.incarnation[:], o.revision,
		identity.Sequence-1, identity.Sequence-1, o.entityID, o.personID, o.sourceIncarnation[:],
		o.sourceRevision, o.leaseOwner, o.leaseGeneration, o.leaseUntilMS, appliedMS); err != nil {
		return err
	}
	return ipTextExecOne(ctx, turn, ipAMSave, o.id, o.incarnation[:], identity.Sequence,
		identity.OperationID[:], request[:], appliedMS, result[:], ipArtworkMaterialCommandKind(kind),
		value.Revision, value.Slots, value.Bytes, value.JobGeneration, o.leaseUntilMS, value.IntentID)
}
