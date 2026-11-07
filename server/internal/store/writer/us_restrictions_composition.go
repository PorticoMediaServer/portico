package writer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// Inert prefix only. W owns the one scoped event, shared governor admission,
// physical FULL terminal, final publication and uncertain-COMMIT resolution.
type usRestrictionsComposition struct {
	decision       receiptDecision
	result         usRestrictionsSavedResult
	digest         ResultDigest
	chargedBytes   int64
	changedProfile string
}

func (usRestrictionsComposition) String() string   { return "usRestrictionsComposition{redacted}" }
func (usRestrictionsComposition) GoString() string { return "usRestrictionsComposition{redacted}" }
func (usRestrictionsComposition) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usRestrictionsComposition")
}

func composeUSRestrictionsAfterAuthority(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, q usPolicyReceiptRequest, input *usRestrictionsCommand) (out usRestrictionsComposition, err error) {
	defer func() {
		if a == nil {
			err = errors.Join(err, ErrDisclosureDenied)
		} else if err == nil {
			err = checkUSSecurityDisclosure(ctx, turn, a)
		} else {
			err = errors.Join(err, a.check(turn))
		}
		out, err = finishCommand(ctx, commandLifetime(turn), out, err)
	}()
	if err = a.check(turn); err != nil {
		return out, err
	}
	lookup, err := lookupUSRestrictions(ctx, turn, a, q)
	if err != nil {
		return out, err
	}
	out.decision = lookup.decision
	if lookup.decision == receiptReplay {
		out.result, out.digest = lookup.result, lookup.digest
		return out, nil
	}
	if lookup.decision == receiptRetired || input == nil {
		return out, nil
	}
	if lookup.decision != receiptFresh || q.kind != usProfileRestrictionsEdit || input.identity != q.reference.Identity || input.digest != q.reference.RequestDigest || input.request.ownerID != q.ownerID || input.request.issuer != a.issuer {
		return out, ErrConflict
	}
	r := input.request
	digest, err := canonicalRequestDigest(q.kind, 1, input.identity, r)
	if err != nil || digest != input.digest {
		return out, errors.Join(ErrConflict, err)
	}
	if r.expectedRevision < 1 || r.expectedRevision == math.MaxInt64 {
		return out, ErrInvalid
	}
	role, primary, err := readUSRestrictionTarget(ctx, turn, a, r.target)
	if err != nil {
		return out, err
	}
	current, present, err := readUSRestrictionSource(ctx, turn, a, r.target.profile.profile)
	if err != nil {
		return out, err
	}
	if present != r.target.present {
		return out, ErrConflict
	}
	if present {
		if current.revision != r.expectedRevision || current.allowDVR != r.target.currentDVR {
			return out, ErrConflict
		}
	} else if r.expectedRevision != 1 || r.target.currentDVR != (primary && role == "owner") {
		return out, ErrConflict
	}
	// Reserve before JSON-decoding the captured SQL projection. This recovers
	// the exact returned spellings after JSON's source-accepted byte repair.
	if err = a.requireMemory(ctx, turn, int64(len(r.labelsJSON))); err != nil {
		return out, err
	}
	var labels []string
	if err = json.Unmarshal([]byte(r.labelsJSON), &labels); err != nil || labels == nil {
		return out, errors.Join(ErrInvalid, err)
	}
	now, err := usPolicyTimeMS(ctx, turn, a)
	if err != nil {
		return out, err
	}
	p := r.target.profile
	v := usRestrictionsSavedResult{kind: q.kind, version: 1, ownerID: q.ownerID, account: p.member.account, memberIncarnation: p.member.incarnation, profile: p.profile, profileLifetime: p.lifetime, appliedMS: now, revision: r.expectedRevision + 1, ratingSystem: r.ratingSystem, maximumAgeRating: r.maximumAgeRating, maximumAge: r.maximumAge, allowUnrated: r.allowUnrated, allowDownloads: r.allowDownloads, allowLiveTV: r.allowLiveTV, allowDVR: r.allowDVR, allowWatchTogether: r.allowWatchTogether, labels: labels}
	charge, err := usRestrictionsResultVariableBytes(v)
	if err != nil {
		return out, err
	}
	if a.receiptBytes <= 0 || charge > a.receiptBytes || lookup.owner.count == math.MaxInt64 || charge > math.MaxInt64-lookup.owner.bytes {
		return out, &Refusal{Code: RefusalCapacity}
	}
	if err = a.requireMemory(ctx, turn, charge); err != nil {
		return out, err
	}
	out.digest, err = usRestrictionsResultDigest(q.reference.Identity, q.reference.RequestDigest, v)
	if err != nil {
		return out, err
	}
	// This narrow composition has CURRENT native owner authority, so its extra
	// DVR-enablement witness is the same frame-bound proof, never a caller bool.
	effect, err := applyUSRestrictions(ctx, turn, input, &usRestrictionsOwnerProof{authority: a})
	if err != nil {
		return out, err
	}
	if effect.profile != v.profile || effect.revision != v.revision || effect.ratingSystem != v.ratingSystem || effect.maximumAgeRating != v.maximumAgeRating || effect.maximumAge != v.maximumAge || effect.allowUnrated != v.allowUnrated || effect.allowDownloads != v.allowDownloads || effect.allowLiveTV != v.allowLiveTV || effect.allowDVR != v.allowDVR || effect.allowWatchTogether != v.allowWatchTogether || effect.labelsJSON != r.labelsJSON {
		return out, ErrResolveRequired
	}
	actual, err := storeUSRestrictionsResult(ctx, turn, q, lookup.owner, v, out.digest)
	if err != nil || actual != charge {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.result, out.chargedBytes, out.changedProfile = v, actual, v.profile
	return out, turn.checkLifetimes(ctx)
}

func storeUSRestrictionsResult(ctx context.Context, turn *commandTurn, q usPolicyReceiptRequest, owner usPolicyOwnerFacts, v usRestrictionsSavedResult, digest ResultDigest) (charged int64, err error) {
	defer func() { charged, err = finishCommand(ctx, commandLifetime(turn), charged, err) }()
	want, err := usRestrictionsResultVariableBytes(v)
	// The concrete mutation normalizes an empty label cohort to []. A nil
	// result is not representable by this receipt card and is never stored.
	if err != nil || q.kind != usProfileRestrictionsEdit || v.kind != q.kind || v.ownerID != q.ownerID || v.labels == nil {
		return 0, errors.Join(ErrResolveRequired, err)
	}
	key := []any{q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence}
	args := append(append([]any(nil), key...), q.reference.Identity.OperationID[:], q.reference.RequestDigest[:], int64(q.kind), int64(1), "profile_restrictions_v1", v.appliedMS, digest[:], int64(len(v.labels)), nil, nil, nil)
	row, err := usPolicyRow(ctx, turn, usRestrictionsReceiptInsert, args...)
	if err != nil {
		return 0, err
	}
	var count int64
	if err = row.Scan(&charged, &count); err != nil {
		return 0, err
	}
	if charged <= 0 || count != int64(len(v.labels)) {
		return 0, ErrResolveRequired
	}
	args = append(append([]any(nil), key...), int64(q.kind), v.account, v.memberIncarnation, v.profile, v.profileLifetime, v.revision, v.ratingSystem, v.maximumAgeRating, int64(v.maximumAge), usPolicyBool(v.allowUnrated), usPolicyBool(v.allowDownloads), usPolicyBool(v.allowLiveTV), usPolicyBool(v.allowDVR), usPolicyBool(v.allowWatchTogether))
	row, err = usPolicyRow(ctx, turn, usRestrictionsReceiptResultInsert, args...)
	if err != nil {
		return 0, err
	}
	var bytes int64
	if err = row.Scan(&bytes); err != nil {
		return 0, err
	}
	if bytes <= 0 || bytes > math.MaxInt64-charged {
		return 0, ErrResolveRequired
	}
	charged += bytes
	for ordinal, label := range v.labels {
		args = append(append([]any(nil), key...), int64(ordinal), label)
		row, err = usPolicyRow(ctx, turn, usRestrictionsReceiptLabelInsert, args...)
		if err != nil {
			return 0, err
		}
		if err = row.Scan(&bytes); err != nil {
			return 0, err
		}
		if bytes != int64(len(v.ownerID)+16+len(label)) || bytes > math.MaxInt64-charged {
			return 0, ErrResolveRequired
		}
		charged += bytes
	}
	if charged != want {
		return 0, ErrResolveRequired
	}
	row, err = usPolicyRow(ctx, turn, usPolicyOwnerAdvance, q.reference.Identity.Sequence, charged, q.ownerID, q.reference.Identity.Incarnation[:], owner.lastSequence, owner.acknowledgedSequence, owner.count, owner.bytes)
	if err != nil {
		return 0, err
	}
	var last, retained, total int64
	if err = row.Scan(&last, &retained, &total); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if last != q.reference.Identity.Sequence || retained != owner.count+1 || total != owner.bytes+charged {
		return 0, ErrResolveRequired
	}
	return charged, turn.checkLifetimes(ctx)
}
