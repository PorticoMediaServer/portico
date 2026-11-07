package writer

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// Inert native prefix. W owns its one scoped event, shared resource admission,
// physical FULL terminal and delivery. Protected issuance evidence is never a
// public retry display or a grant of current authority.
type usInvitationShortComposition struct {
	decision          receiptDecision
	result            usInvitationShortSavedResult
	digest            ResultDigest
	chargedBytes      int64
	changedInvitation string
}

func (usInvitationShortComposition) String() string { return "usInvitationShortComposition{redacted}" }
func (usInvitationShortComposition) GoString() string {
	return "usInvitationShortComposition{redacted}"
}
func (usInvitationShortComposition) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationShortComposition")
}

func usInvitationShortPreparedResult(c *usInvitationShortCommand, appliedMS int64) (usInvitationShortSavedResult, error) {
	if c == nil {
		return usInvitationShortSavedResult{}, ErrInvalid
	}
	p, r := c.request, c.request.original
	if r.state != "pending" || r.expectedRevision != r.invitationRevision {
		return usInvitationShortSavedResult{}, ErrConflict
	}
	v := usInvitationShortSavedResult{kind: usInvitationShortIssue, version: 1, ownerID: r.scope.ownerID, issuerServer: r.scope.issuer.server, appliedMS: appliedMS, invitationID: r.invitationID, invitationRevision: r.invitationRevision, codeHash: r.codeHash, role: r.role, invitationExpiryMS: r.invitationExpiryMS, serverID: r.serverID, publicKey: r.publicKey, keyIncarnation: r.keyIncarnation, resetGeneration: r.resetGeneration, signInKind: r.signInKind, kindRevision: r.kindRevision, issuedMS: r.issuedMS, expiresMS: p.expiresMS, lookupHash: p.lookupHash, bindingHash: p.bindingHash, wire: p.wire}
	if err := validateUSInvitationShortResult(v); err != nil {
		return usInvitationShortSavedResult{}, err
	}
	return v, nil
}

func composeUSInvitationShortAfterAuthority(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, q usPolicyReceiptRequest, input *usInvitationShortCommand) (out usInvitationShortComposition, err error) {
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
	lookup, err := lookupUSInvitationShort(ctx, turn, a, q)
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
	if lookup.decision != receiptFresh {
		return out, ErrConflict
	}
	r := input.request.original
	if r.identity != q.reference.Identity || r.scope.ownerID != q.ownerID || r.scope.issuer != a.issuer || input.digest != q.reference.RequestDigest {
		return out, ErrConflict
	}
	if _, err = input.checkedDigest(); err != nil {
		return out, errors.Join(ErrConflict, err)
	}
	now, err := usPolicyTimeMS(ctx, turn, a)
	if err != nil {
		return out, err
	}
	v, err := usInvitationShortPreparedResult(input, now)
	if err != nil {
		return out, err
	}
	// Fresh source/key proof and exact old replacement bytes are supplied by
	// the domain helper before effect; replay above never enters this path.
	oldBytes, err := checkUSInvitationShortFresh(ctx, turn, a, input, now)
	if err != nil {
		return out, err
	}
	charge, err := usInvitationShortResultVariableBytes(v)
	if err != nil {
		return out, err
	}
	if a.receiptBytes <= 0 || charge > a.receiptBytes || lookup.owner.count == math.MaxInt64 || charge > math.MaxInt64-lookup.owner.bytes || oldBytes > maxCanonicalResultBytes-charge {
		return out, &Refusal{Code: RefusalCapacity}
	}
	if err = a.requireMemory(ctx, turn, oldBytes+charge); err != nil {
		return out, err
	}
	v = cloneUSInvitationShortResult(v)
	out.digest, err = usInvitationShortResultDigest(q.reference.Identity, q.reference.RequestDigest, v)
	if err != nil {
		return out, err
	}
	if err = applyUSInvitationShort(ctx, turn, a, input, oldBytes); err != nil {
		return out, err
	}
	out.chargedBytes, err = storeUSInvitationShortResult(ctx, turn, q, lookup.owner, v, out.digest)
	if err != nil || out.chargedBytes != charge {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.result, out.changedInvitation = v, v.invitationID
	return out, turn.checkLifetimes(ctx)
}
