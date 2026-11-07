package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"slices"
)

// Native prefix only, NOT Applied. W still owns its shared audience/event/quota
// effects, final current disclosure, FULL COMMIT/physical return and publication.
// No command constructor here can create current issuer or resource authority.
type usPolicyComposition struct {
	decision       receiptDecision
	result         usPolicySavedResult
	digest         ResultDigest
	chargedBytes   int64
	changedAccount string // one scoped change, never an enumerated user audience
}

func composeUSPolicyAfterAuthority(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, q usPolicyReceiptRequest, input *usLibraryPolicyCommand) (out usPolicyComposition, err error) {
	defer func() { out, err = finishUSPolicy(ctx, turn, a, out, err) }()
	if err = a.check(turn); err != nil {
		return out, err
	}
	lookup, err := lookupUSPolicy(ctx, turn, a, q)
	if err != nil {
		return out, err
	}
	out.decision = lookup.decision
	if lookup.decision == receiptReplay {
		out.result, out.digest = lookup.result, lookup.digest
		return out, nil // ZERO current target CAS, source/event/owner writes
	}
	if lookup.decision == receiptRetired || input == nil {
		return out, nil
	}
	if lookup.decision != receiptFresh || input.kind != q.kind || input.identity != q.reference.Identity || input.digest != q.reference.RequestDigest || input.request.ownerID != q.ownerID || input.request.issuer != a.issuer {
		return out, ErrConflict
	}
	// A fresh submission must be the exact frozen preparation. Historical
	// lookup above is deliberately independent of a now-stale preparation.
	digest, err := canonicalRequestDigest(input.kind, 1, input.identity, input.request)
	if err != nil || digest != input.digest {
		return out, errors.Join(ErrConflict, err)
	}
	if err = a.requireMemory(ctx, turn, int64(len(input.policy.String))); err != nil {
		return out, err
	}
	if err = validateUSPolicyProjection(input); err != nil {
		return out, err
	}
	before, revision, lifetime, err := readUSPolicyFreshTarget(ctx, turn, a, input)
	if err != nil {
		return out, err
	}
	nowMS, err := usPolicyTimeMS(ctx, turn, a)
	if err != nil {
		return out, err
	}
	v := usPolicySavedResult{kind: q.kind, ownerID: q.ownerID, account: input.request.account, memberIncarnation: input.request.memberIncarnation,
		profile: input.request.profile, profileLifetime: input.request.profileLifetime, revision: revision, appliedMS: nowMS,
		inherits: !input.policy.Valid, libraries: slices.Clone(input.request.libraries)}
	unchanged, err := usPolicyUnchanged(before, input)
	if err != nil {
		return out, err
	}
	v.changed = !unchanged
	if !unchanged {
		v.revision++
	}
	// Reserve the complete prospective immutable result BEFORE any source effect.
	charge, err := usPolicyResultVariableBytes(v)
	if err != nil {
		return out, err
	}
	if charge > a.receiptBytes || a.receiptBytes <= 0 {
		return out, &Refusal{Code: RefusalCapacity}
	}
	if lookup.owner.count == math.MaxInt64 || charge > math.MaxInt64-lookup.owner.bytes {
		return out, &Refusal{Code: RefusalCapacity}
	}
	if err = a.requireMemory(ctx, turn, charge); err != nil {
		return out, err
	}
	if !unchanged {
		effect, err := applyUSLibraryPolicy(ctx, turn, input, before, revision)
		if err != nil {
			return out, err
		}
		if !effect.changed || effect.revision != v.revision || effect.lifetime != lifetime {
			return out, ErrResolveRequired
		}
	}
	out.digest, err = usPolicyResultDigest(q.reference.Identity, q.reference.RequestDigest, v)
	if err != nil {
		return out, err
	}
	actualCharge, err := storeUSPolicyResult(ctx, turn, q, lookup.owner, v, out.digest)
	if err != nil || actualCharge != charge {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.result, out.chargedBytes = v, actualCharge
	if v.changed {
		out.changedAccount = v.account
	}
	return out, turn.checkLifetimes(ctx)
}

func readUSPolicyFreshTarget(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, input *usLibraryPolicyCommand) (policy sql.NullString, revision int64, lifetime string, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), struct {
			Policy   sql.NullString
			Revision int64
			Lifetime string
		}{policy, revision, lifetime}, err)
		policy, revision, lifetime, err = v.Policy, v.Revision, v.Lifetime, e
	}()
	q := input.request
	id, args := usMemberPolicyOctets, []any{q.account, q.memberIncarnation}
	if input.kind == USProfileLibraries {
		id = usProfilePolicyOctets
		args = append(args, q.profile, q.profileLifetime)
	}
	row, err := usPolicyRow(ctx, turn, id, args...)
	if err != nil {
		return policy, 0, "", err
	}
	var sizes [3]int64
	if input.kind == USMemberLibraries {
		err = row.Scan(&sizes[0], &sizes[1])
	} else {
		err = row.Scan(&sizes[0], &sizes[1], &sizes[2])
	}
	if err != nil {
		return policy, 0, "", errors.Join(ErrConflict, err)
	}
	var total int64
	for _, n := range sizes {
		if n < 0 || n > maxCanonicalRequestBytes-total {
			return policy, 0, "", &Refusal{Code: RefusalCapacity}
		}
		total += n
	}
	if err = a.requireMemory(ctx, turn, total); err != nil {
		return policy, 0, "", err
	}
	id = usMemberPolicyFacts
	if input.kind == USProfileLibraries {
		id = usProfilePolicyFacts
	}
	row, err = usPolicyRow(ctx, turn, id, args...)
	if err != nil {
		return policy, 0, "", err
	}
	var role, memberJSON string
	var memberRevision, disabled, deleted, primary int64
	if input.kind == USMemberLibraries {
		err = row.Scan(&role, &revision, &disabled, &memberJSON)
		policy = sql.NullString{String: memberJSON, Valid: true}
		lifetime = q.memberIncarnation
	} else {
		err = row.Scan(&role, &memberRevision, &disabled, &memberJSON, &revision, &deleted, &primary, &policy)
		lifetime = q.profileLifetime
	}
	if err != nil {
		return policy, 0, "", errors.Join(ErrConflict, err)
	}
	if disabled != 0 || deleted != 0 || revision != q.expectedRevision || int64(len(role)) != sizes[0] || int64(len(memberJSON)) != sizes[1] || input.kind == USProfileLibraries && int64(len(policy.String)) != sizes[2] {
		return policy, 0, "", ErrConflict
	}
	if role != "owner" && role != "admin" && role != "member" {
		return policy, 0, "", ErrDisclosureDenied
	}
	// This first recipe is independently issued owner/main-profile authority.
	// A member-level command never edits the owner. Profile policy is the own-
	// account profile-manager primitive, not a new cross-account policy power.
	if input.kind == USMemberLibraries && role == "owner" || input.kind == USProfileLibraries && q.account != a.issuer.account {
		return policy, 0, "", ErrDisclosureDenied
	}
	if _, err = decodeUSLibraryIDs(memberJSON); err != nil {
		return policy, 0, "", err
	}
	if policy.Valid {
		if _, err = decodeUSLibraryIDs(policy.String); err != nil {
			return policy, 0, "", err
		}
	}
	return policy, revision, lifetime, nil
}

func usPolicyUnchanged(before sql.NullString, input *usLibraryPolicyCommand) (bool, error) {
	if before.Valid != input.policy.Valid {
		return false, nil
	}
	if !before.Valid {
		return true, nil
	}
	ids, err := decodeUSLibraryIDs(before.String)
	return slices.Equal(ids, input.request.libraries), err
}

func storeUSPolicyResult(ctx context.Context, turn *commandTurn, q usPolicyReceiptRequest, owner usPolicyOwnerFacts, v usPolicySavedResult, digest ResultDigest) (charged int64, err error) {
	defer func() { charged, err = finishCommand(ctx, commandLifetime(turn), charged, err) }()
	codec, err := usPolicyCodec(q.kind)
	if err != nil {
		return 0, err
	}
	var profile, lifetime any
	if q.kind == USProfileLibraries {
		profile, lifetime = v.profile, v.profileLifetime
	}
	row, err := usPolicyRow(ctx, turn, usPolicyReceiptInsert, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence,
		q.reference.Identity.OperationID[:], q.reference.RequestDigest[:], int64(q.kind), int64(1), codec, v.appliedMS, digest[:], v.account, v.memberIncarnation, profile, lifetime, v.revision, usPolicyBool(v.inherits), usPolicyBool(v.changed), int64(len(v.libraries)))
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&charged); err != nil {
		return 0, err
	}
	if charged <= 0 {
		return 0, ErrResolveRequired
	}
	for ordinal, id := range v.libraries {
		row, err = usPolicyRow(ctx, turn, usPolicyReceiptLibraryInsert, q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence, "policy", int64(ordinal), id)
		if err != nil {
			return 0, err
		}
		var n int64
		if err = row.Scan(&n); err != nil {
			return 0, err
		}
		if n <= 0 || n != int64(len(q.ownerID)+16+len("policy")+len(id)) || n > math.MaxInt64-charged {
			return 0, ErrResolveRequired
		}
		charged += n
	}
	row, err = usPolicyRow(ctx, turn, usPolicyOwnerAdvance, q.reference.Identity.Sequence, charged, q.ownerID, q.reference.Identity.Incarnation[:], owner.lastSequence, owner.acknowledgedSequence, owner.count, owner.bytes)
	if err != nil {
		return 0, err
	}
	var last, count, bytes int64
	if err = row.Scan(&last, &count, &bytes); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if last != q.reference.Identity.Sequence || count != owner.count+1 || bytes != owner.bytes+charged {
		return 0, ErrResolveRequired
	}
	return charged, turn.checkLifetimes(ctx)
}

func acknowledgeUSPolicyAfterAuthority(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, q usPolicyReceiptRequest) (ack int64, err error) {
	defer func() { ack, err = finishCommand(ctx, commandLifetime(turn), ack, errors.Join(err, a.check(turn))) }()
	lookup, err := lookupUSPolicy(ctx, turn, a, q)
	if err != nil {
		return 0, err
	}
	if lookup.decision == receiptRetired {
		return lookup.owner.acknowledgedSequence, nil
	}
	if lookup.decision != receiptReplay || q.reference.Identity.Sequence != lookup.owner.lastSequence {
		return 0, ErrConflict
	}
	row, err := usPolicyRow(ctx, turn, usPolicyOwnerAck, q.reference.Identity.Sequence, q.ownerID, q.reference.Identity.Incarnation[:], lookup.owner.lastSequence, lookup.owner.acknowledgedSequence)
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&ack); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if ack != q.reference.Identity.Sequence {
		return 0, ErrResolveRequired
	}
	return ack, turn.checkLifetimes(ctx)
}

func usPolicyBool(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

// The cached SQL projection is not an independent request. A future internal
// composition bug cannot mutate policy JSON while retaining the old digest.
func validateUSPolicyProjection(input *usLibraryPolicyCommand) error {
	wantValid := input.kind == USMemberLibraries || input.request.libraries != nil
	if input.policy.Valid != wantValid {
		return ErrConflict
	}
	if !wantValid {
		if input.policy.String != "" {
			return ErrConflict
		}
		return nil
	}
	ids, err := decodeUSLibraryIDs(input.policy.String)
	if err != nil || !slices.Equal(ids, input.request.libraries) {
		return errors.Join(ErrConflict, err)
	}
	return nil
}
