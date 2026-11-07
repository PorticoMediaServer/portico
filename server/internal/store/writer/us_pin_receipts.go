package writer

import (
	"context"
	"errors"
	"math"
)

type usPINReceiptLookup struct {
	custody usSecurityBindingLookup
	result  usPINSavedResult
	digest  ResultDigest
}
type usPINComposition struct {
	decision       receiptDecision
	result         usPINSavedResult
	digest         ResultDigest
	chargedBytes   int64
	changedAccount string
}

func lookupUSPIN(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, q usPolicyReceiptRequest) (out usPINReceiptLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, errors.Join(err, a.check(turn))) }()
	codec, err := usPINCodec(q.kind)
	if err != nil {
		return out, err
	}
	out.custody, err = lookupUSSecurityBinding(ctx, turn, a, q)
	if err != nil || out.custody.decision != receiptReplay {
		return out, err
	}
	if out.custody.binding.resultCount != 0 {
		return out, ErrResolveRequired
	}
	row, err := usPolicyRow(ctx, turn, usPolicyReceiptOctets, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var actualBytes int64
	if err = row.Scan(&actualBytes); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if err = a.requireMemory(ctx, turn, actualBytes); err != nil {
		return out, err
	}
	row, err = usPolicyRow(ctx, turn, usPINReceiptHeader, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence)
	if err != nil {
		return out, err
	}
	var kind, version, pin, changed, count, headerBytes int64
	var actualCodec string
	v := usPINSavedResult{owner: q.ownerID, kind: q.kind}
	if err = row.Scan(&kind, &version, &actualCodec, &v.appliedMS, &v.account, &v.member, &v.profile, &v.lifetime, &v.revision, &v.pinRevision, &pin, &changed, &count, &headerBytes); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if kind != int64(q.kind) || version != 1 || actualCodec != codec || count != 0 || changed != 1 || (pin != 0 && pin != 1) || headerBytes != actualBytes {
		return out, ErrResolveRequired
	}
	v.pinRequired = pin == 1
	expectedBytes, err := usPINResultVariableBytes(v)
	if err != nil || expectedBytes != actualBytes {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.digest, err = usPINResultDigest(q.reference.Identity, q.reference.RequestDigest, v)
	if err != nil || out.digest != ResultDigest(out.custody.binding.resultDigest) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.result = v
	return out, turn.checkLifetimes(ctx)
}

// A native prefix only, never Applied: the parent still owes its one scoped
// event, global resource accounting, final disclosure and FULL physical terminal.
func composeUSPINAfterAuthority(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, q usPolicyReceiptRequest, input *usPINCommand) (out usPINComposition, err error) {
	defer func() {
		if err == nil {
			err = checkUSSecurityDisclosure(ctx, turn, a)
		} else {
			err = errors.Join(err, a.check(turn))
		}
		lifetime := commandLifetime(turn)
		if a != nil {
			lifetime = a.lifetime
		}
		out, err = finishCommand(ctx, lifetime, out, err)
	}()
	lookup, err := lookupUSPIN(ctx, turn, a, q)
	if err != nil {
		return out, err
	}
	out.decision = lookup.custody.decision
	if out.decision == receiptReplay {
		out.result, out.digest = lookup.result, lookup.digest
		return out, nil
	}
	if out.decision == receiptRetired || input == nil {
		return out, nil
	}
	request := input.request
	prepared := request.prepared
	target := prepared.request
	if out.decision != receiptFresh || input.identity != q.reference.Identity || input.digest != q.reference.RequestDigest || request.ownerID != q.ownerID || request.issuer != a.issuer || prepared.kind != q.kind {
		return out, ErrConflict
	}
	expected, err := canonicalRequestDigest(q.kind, 1, input.identity, request)
	if err != nil || expected != input.digest {
		return out, errors.Join(ErrConflict, err)
	}
	// Only this independently checked OWNER recipe is supported. An admin or
	// credential recipe cannot acquire these target powers by sharing a stamp.
	if a.stamp.recipe != usDirectOwnerRecipe {
		return out, ErrDisclosureDenied
	}
	id := usPINTargetSelf
	if target.account != a.issuer.account {
		if q.kind != usPINClear {
			return out, ErrDisclosureDenied
		}
		id = usPINTargetManaged
	}
	row, err := usPolicyRow(ctx, turn, id, target.account, request.memberIncarnation, target.profile, target.profileLifetime)
	if err != nil {
		return out, err
	}
	var revision, pinRevision int64
	if err = row.Scan(&revision, &pinRevision); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if revision != target.expectedRevision || pinRevision != target.expectedPINRevision {
		return out, ErrConflict
	}
	now, err := usPolicyTimeMS(ctx, turn, a)
	if err != nil {
		return out, err
	}
	v := usPINSavedResult{kind: q.kind, owner: q.ownerID, account: target.account, member: request.memberIncarnation, profile: target.profile, lifetime: target.profileLifetime, revision: revision + 1, pinRevision: pinRevision + 1, pinRequired: q.kind == usPINSet, appliedMS: now}
	charge, err := usPINResultVariableBytes(v)
	if err != nil {
		return out, err
	}
	owner := lookup.custody.owner
	if a.receiptBytes <= 0 || charge > a.receiptBytes || owner.count == math.MaxInt64 || charge > math.MaxInt64-owner.bytes {
		return out, &Refusal{Code: RefusalCapacity}
	}
	if err = a.requireMemory(ctx, turn, charge+int64(len(target.hash))); err != nil {
		return out, err
	}
	out.digest, err = usPINResultDigest(q.reference.Identity, q.reference.RequestDigest, v)
	if err != nil {
		return out, err
	}
	effect, err := applyUSPIN(ctx, turn, &prepared)
	if err != nil {
		return out, err
	}
	if effect.account != v.account || effect.profile != v.profile || effect.lifetime != v.lifetime || effect.revision != v.revision || effect.pinRevision != v.pinRevision || effect.pinRequired != v.pinRequired {
		return out, ErrResolveRequired
	}
	var keep *usPINKeepProof
	if target.account == a.issuer.account && target.profile == a.issuer.profile && target.profileLifetime == a.issuer.profileLifetime && request.memberIncarnation == a.issuer.memberIncarnation {
		keep = &usPINKeepProof{frame: turn.transaction, issuer: a.issuer}
	}
	if _, err = advanceUSProfilePINFence(ctx, turn, usPINFenceScope{v.account, v.member, v.profile, v.lifetime}, now, keep); err != nil {
		return out, err
	}
	codec, _ := usPINCodec(q.kind)
	row, err = usPolicyRow(ctx, turn, usPINReceiptInsert, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, q.reference.Identity.OperationID[:], q.reference.RequestDigest[:], int64(q.kind), int64(1), codec, now, out.digest[:], v.account, v.member, v.profile, v.lifetime, v.revision, int64(1), v.pinRevision, usPolicyBool(v.pinRequired), int64(0))
	if err != nil {
		return out, err
	}
	var actualCharge int64
	if err = row.Scan(&actualCharge); err != nil {
		return out, err
	}
	if actualCharge != charge {
		return out, ErrResolveRequired
	}
	row, err = usPolicyRow(ctx, turn, usPolicyOwnerAdvance, q.reference.Identity.Sequence, charge, q.ownerID, q.reference.Identity.Incarnation[:], owner.lastSequence, owner.acknowledgedSequence, owner.count, owner.bytes)
	if err != nil {
		return out, err
	}
	var last, count, bytes int64
	if err = row.Scan(&last, &count, &bytes); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if last != q.reference.Identity.Sequence || count != owner.count+1 || bytes != owner.bytes+charge {
		return out, ErrResolveRequired
	}
	out.result, out.chargedBytes, out.changedAccount = v, charge, v.account
	return out, turn.checkLifetimes(ctx)
}
