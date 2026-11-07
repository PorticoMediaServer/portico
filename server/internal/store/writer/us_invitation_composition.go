package writer

import (
	"context"
	"errors"
	"math"
	"slices"
	"time"
)

// Inert native prefix. W still owns the one scoped event, shared admission,
// final physical FULL terminal and publication. A prefix is never Applied.
type usInvitationComposition struct {
	decision          receiptDecision
	result            usInvitationSavedResult
	digest            ResultDigest
	chargedBytes      int64
	changedInvitation string
}

// Exactly one frozen input is used on a fresh request: issuance for creation,
// mutation for edit/revoke. Resolve and immutable replay require neither.
func composeUSInvitationAfterAuthority(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, q usPolicyReceiptRequest, mutation *usInvitationCommand, issuance *usInvitationIssuanceCommand) (out usInvitationComposition, err error) {
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
	lookup, err := lookupUSInvitation(ctx, turn, a, q)
	if err != nil {
		return out, err
	}
	out.decision = lookup.decision
	if lookup.decision == receiptReplay {
		out.result, out.digest = lookup.result, lookup.digest
		return out, nil
	}
	if lookup.decision == receiptRetired || mutation == nil && issuance == nil {
		return out, nil
	}
	if lookup.decision != receiptFresh {
		return out, ErrConflict
	}
	var p *usInvitationPrepared
	var request any
	var frozen RequestDigest
	if q.kind == usInvitationCreate {
		if issuance == nil || mutation != nil {
			return out, ErrConflict
		}
		p, request, frozen = &issuance.request.invitation, issuance.request, issuance.digest
	} else {
		if mutation == nil || issuance != nil {
			return out, ErrConflict
		}
		p, request, frozen = &mutation.request, mutation.request, mutation.digest
	}
	if p.kind != q.kind || p.identity != q.reference.Identity || p.scope.ownerID != q.ownerID || p.scope.issuer != a.issuer || frozen != q.reference.RequestDigest {
		return out, ErrConflict
	}
	digest, err := canonicalRequestDigest(q.kind, 1, p.identity, request)
	if err != nil || digest != frozen {
		return out, errors.Join(ErrConflict, err)
	}
	now, err := usPolicyTimeMS(ctx, turn, a)
	if err != nil {
		return out, err
	}
	if now <= 0 || now > usMaxRevision-usInvitationMaxSpanMS {
		return out, ErrInvalid
	}
	if err = validateUSInvitationProjection(p, now); err != nil {
		return out, err
	}
	var v usInvitationSavedResult
	if q.kind == usInvitationCreate {
		if err = checkUSInvitationFreshLink(ctx, turn, issuance.request.link); err != nil {
			return out, err
		}
		if err = checkUSInvitationFreshCapacity(ctx, turn, a, now); err != nil {
			return out, err
		}
		v = usInvitationSavedResult{id: p.id, serverID: issuance.request.link.serverID, notes: p.notes, role: p.role, state: "pending", createdMS: now, expiresMS: now + int64(p.hours)*usInvitationHourMS, revision: 1, boundUsername: p.boundUsername, access: p.access, codeHash: p.codeHash, libraries: p.libraries, code: issuance.request.code}
	} else {
		v, err = readUSInvitationSource(ctx, turn, a, p.id, p.expectedRevision)
		if err != nil {
			return out, err
		}
		if v.state == "accepted" || v.acceptedMS.Valid || v.accountID.Valid {
			return out, ErrConflict
		}
		if q.kind == usInvitationEdit {
			if v.state != "pending" || v.expiresMS <= now {
				return out, ErrConflict
			}
			v.notes, v.role, v.libraries, v.expiresMS = p.notes, p.role, p.libraries, p.expiresMS
			if p.replaceAccess {
				v.access = p.access
			}
		} else if q.kind == usInvitationRevoke {
			if v.state != "pending" && v.state != "revoked" {
				return out, ErrResolveRequired
			}
			v.state = "revoked"
		} else {
			return out, ErrInvalid
		}
		v.revision++
	}
	if q.kind != usInvitationRevoke {
		if err = checkUSInvitationFreshLibraries(ctx, turn, a, p.libraries); err != nil {
			return out, err
		}
	}
	v.kind, v.version, v.count = q.kind, 1, len(v.libraries)
	v.ownerID, v.issuerServer, v.appliedMS = q.ownerID, a.issuer.server, now
	charge, err := usInvitationResultVariableBytes(v)
	if err != nil {
		return out, err
	}
	if a.receiptBytes <= 0 || charge > a.receiptBytes || lookup.owner.count == math.MaxInt64 || charge > math.MaxInt64-lookup.owner.bytes {
		return out, &Refusal{Code: RefusalCapacity}
	}
	if err = a.requireMemory(ctx, turn, charge); err != nil {
		return out, err
	}
	// Owned output is allocated only after reserving its complete native image.
	v.libraries = slices.Clone(v.libraries)
	v.code.nonce, v.code.body = slices.Clone(v.code.nonce), slices.Clone(v.code.body)
	out.digest, err = usInvitationResultDigest(p.identity, frozen, v)
	if err != nil {
		return out, err
	}
	id, args, err := p.effectBindings(now)
	if err != nil {
		return out, err
	}
	row, err := usPolicyRow(ctx, turn, id, args...)
	if err != nil {
		return out, err
	}
	var actualID, actualState string
	var revision, expires int64
	if err = row.Scan(&actualID, &revision, &actualState, &expires); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	if actualID != v.id || revision != v.revision || actualState != v.state || expires != v.expiresMS {
		return out, ErrResolveRequired
	}
	post, err := readUSInvitationSource(ctx, turn, a, v.id, v.revision)
	if err != nil {
		return out, err
	}
	post.kind, post.version, post.count = v.kind, v.version, len(post.libraries)
	post.ownerID, post.issuerServer, post.appliedMS = v.ownerID, v.issuerServer, v.appliedMS
	post.serverID, post.code = v.serverID, v.code
	actualDigest, err := usInvitationResultDigest(p.identity, frozen, post)
	if err != nil || actualDigest != out.digest {
		return out, errors.Join(ErrResolveRequired, err)
	}
	actualCharge, err := storeUSInvitationResult(ctx, turn, q, lookup.owner, v, out.digest)
	if err != nil || actualCharge != charge {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.result, out.chargedBytes, out.changedInvitation = v, actualCharge, v.id
	return out, turn.checkLifetimes(ctx)
}

// The cached SQL/omission projection is rebuilt from its closed purpose. This
// catches changes in unused fields and independently mutated cached JSON.
func validateUSInvitationProjection(p *usInvitationPrepared, now int64) error {
	var notes *string
	if p.notes.Valid {
		notes = &p.notes.String
	}
	var rebuilt *usInvitationPrepared
	var err error
	switch p.kind {
	case usInvitationCreate:
		rebuilt, err = prepareUSInvitationCreate(p.identity, p.scope, p.id, p.codeHash, usInvitationCreateInput{notes: notes, role: p.role, libraries: p.libraries, expiresInHours: p.hours, boundUsername: p.boundUsername, access: &p.access})
	case usInvitationEdit:
		var access *usInvitationAccess
		if p.replaceAccess {
			access = &p.access
		}
		rebuilt, err = prepareUSInvitationEdit(p.identity, p.scope, p.id, usInvitationEditInput{expectedRevision: p.expectedRevision, notes: notes, role: p.role, libraries: p.libraries, expiresAt: time.UnixMilli(p.expiresMS).UTC().Format(time.RFC3339Nano), access: access}, now)
	case usInvitationRevoke:
		rebuilt, err = prepareUSInvitationRevoke(p.identity, p.scope, p.id, p.expectedRevision)
	default:
		return ErrInvalid
	}
	if err != nil {
		return err
	}
	actual, e := canonicalRequestDigest(p.kind, 1, p.identity, *p)
	want, e2 := canonicalRequestDigest(p.kind, 1, p.identity, *rebuilt)
	if e != nil || e2 != nil || actual != want {
		return errors.Join(ErrConflict, e, e2)
	}
	return nil
}

func storeUSInvitationResult(ctx context.Context, turn *commandTurn, q usPolicyReceiptRequest, owner usPolicyOwnerFacts, v usInvitationSavedResult, digest ResultDigest) (charged int64, err error) {
	defer func() { charged, err = finishCommand(ctx, commandLifetime(turn), charged, err) }()
	want, err := usInvitationResultVariableBytes(v)
	if err != nil || v.kind != q.kind || v.ownerID != q.ownerID {
		return 0, errors.Join(ErrResolveRequired, err)
	}
	codec, _ := usInvitationCodec(q.kind)
	key := []any{q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence}
	var keyID, nonce, body any
	if q.kind == usInvitationCreate {
		keyID, nonce, body = v.code.keyID, v.code.nonce, v.code.body
	}
	args := append(append([]any(nil), key...), q.reference.Identity.OperationID[:], q.reference.RequestDigest[:], int64(q.kind), int64(1), codec, v.appliedMS, digest[:], int64(v.count), keyID, nonce, body)
	row, err := usPolicyRow(ctx, turn, usInvitationReceiptInsert, args...)
	if err != nil {
		return 0, err
	}
	var count int64
	if err = row.Scan(&charged, &count); err != nil {
		return 0, err
	}
	if charged <= 0 || count != int64(v.count) {
		return 0, ErrResolveRequired
	}
	var notes, accepted, account any
	if v.notes.Valid {
		notes = v.notes.String
	}
	if v.acceptedMS.Valid {
		accepted = v.acceptedMS.Int64
	}
	if v.accountID.Valid {
		account = v.accountID.String
	}
	args = append(append([]any(nil), key...), v.id, v.serverID, notes, v.role, v.state, v.createdMS, v.expiresMS, accepted, account, v.revision, v.boundUsername, v.access.mode, int64(v.access.streams), usPolicyBool(v.access.allowRemote), usPolicyBool(v.access.accessTokens), v.access.rating, v.codeHash)
	row, err = usPolicyRow(ctx, turn, usInvitationReceiptResultInsert, args...)
	if err != nil {
		return 0, err
	}
	var n int64
	if err = row.Scan(&n); err != nil {
		return 0, err
	}
	if n <= 0 || n > math.MaxInt64-charged {
		return 0, ErrResolveRequired
	}
	charged += n
	for ordinal, id := range v.libraries {
		args = append(append([]any(nil), key...), "invitation", int64(ordinal), id)
		row, err = usPolicyRow(ctx, turn, usInvitationReceiptLibraryInsert, args...)
		if err != nil {
			return 0, err
		}
		if err = row.Scan(&n); err != nil {
			return 0, err
		}
		if n != int64(len(v.ownerID)+16+len("invitation")+len(id)) || n > math.MaxInt64-charged {
			return 0, ErrResolveRequired
		}
		charged += n
	}
	if charged != want {
		return 0, ErrResolveRequired
	}
	row, err = usPolicyRow(ctx, turn, usPolicyOwnerAdvance, q.reference.Identity.Sequence, charged, q.ownerID, q.reference.Identity.Incarnation[:], owner.lastSequence, owner.acknowledgedSequence, owner.count, owner.bytes)
	if err != nil {
		return 0, err
	}
	var last, retained, bytes int64
	if err = row.Scan(&last, &retained, &bytes); err != nil {
		return 0, errors.Join(ErrConflict, err)
	}
	if last != q.reference.Identity.Sequence || retained != owner.count+1 || bytes != owner.bytes+charged {
		return 0, ErrResolveRequired
	}
	return charged, turn.checkLifetimes(ctx)
}
