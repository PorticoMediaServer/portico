package writer

import (
	"context"
	"errors"
)

type usSecurityBindingLookup struct {
	owner    usPolicyOwnerFacts
	decision receiptDecision
	binding  receiptFacts
}

// Shared fixed-width custody only. Every concrete purpose owns its own native
// result codec/decoder. Namespace reservation grants no result-schema authority.
func lookupUSSecurityBinding(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, q usPolicyReceiptRequest) (out usSecurityBindingLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, errors.Join(err, a.check(turn))) }()
	if err = a.check(turn); err != nil {
		return out, err
	}
	if !usIdentifier(q.ownerID) || q.kind != turn.transaction.descriptor.ID || q.reference.Identity.Sequence <= 0 || q.reference.Identity.Incarnation == ([16]byte{}) || q.reference.Identity.OperationID == ([16]byte{}) || q.reference.RequestDigest == (RequestDigest{}) {
		return out, ErrInvalid
	}
	if err = a.requireMemory(ctx, turn, 0); err != nil {
		return out, err
	}
	out.owner, err = readUSPolicyOwner(ctx, turn, a, q.ownerID)
	if err != nil {
		return out, err
	}
	expected := receiptIdentity{q.reference.Identity, q.reference.RequestDigest}
	if !out.owner.exists || out.owner.incarnation != expected.command.Incarnation {
		_, err = classifyReceipt(expected, q.kind, out.owner.receiptOwnerFacts, receiptFacts{})
		return out, err
	}
	binding, err := readUSPolicyBinding(ctx, turn, q)
	if err != nil {
		return out, err
	}
	out.decision, err = classifyReceipt(expected, q.kind, out.owner.receiptOwnerFacts, binding)
	if err == nil && out.decision == receiptFresh && out.owner.retiredMS != 0 {
		return out, errors.Join(ErrResolveRequired, ErrOwnerUnavailable)
	}
	if err != nil {
		return out, err
	}
	out.binding = binding
	return out, nil
}

func isUSSecurityReceiptPurpose(id CommandID) bool {
	switch id {
	case usPINSet, usPINClear, USMemberLibraries, USProfileLibraries, usInvitationCreate, usInvitationEdit, usInvitationRevoke:
		return true
	default:
		return false
	}
}
