package writer

import (
	"context"
	"errors"
	"math"
)

// The caller reserves the complete result and source replacement before the
// first effect. A failed child or owner CAS aborts the encompassing private
// prefix; this helper never reports an Applied terminal.
func storeUSInvitationShortResult(ctx context.Context, turn *commandTurn, q usPolicyReceiptRequest, owner usPolicyOwnerFacts, v usInvitationShortSavedResult, digest ResultDigest) (charged int64, err error) {
	defer func() { charged, err = finishCommand(ctx, commandLifetime(turn), charged, err) }()
	want, err := usInvitationShortResultVariableBytes(v)
	if err != nil || q.kind != usInvitationShortIssue || v.kind != q.kind || v.ownerID != q.ownerID || owner.count == math.MaxInt64 || want > math.MaxInt64-owner.bytes {
		return 0, errors.Join(ErrResolveRequired, err)
	}
	actualDigest, err := usInvitationShortResultDigest(q.reference.Identity, q.reference.RequestDigest, v)
	if err != nil || actualDigest != digest {
		return 0, errors.Join(ErrResolveRequired, err)
	}
	key := []any{q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence}
	args := append(append([]any(nil), key...), q.reference.Identity.OperationID[:], q.reference.RequestDigest[:], int64(q.kind), int64(1), "invitation_short_issue_v1", v.appliedMS, digest[:], int64(0), nil, nil, nil)
	row, err := usPolicyRow(ctx, turn, usInvitationShortReceiptInsert, args...)
	if err != nil {
		return 0, err
	}
	var count, child int64
	if err = row.Scan(&charged, &count); err != nil {
		return 0, err
	}
	if charged != int64(len(v.ownerID)+96+len("invitation_short_issue_v1")) || count != 0 {
		return 0, ErrResolveRequired
	}
	args = append(append([]any(nil), key...), int64(q.kind), v.invitationID, v.invitationRevision, v.codeHash, v.role, v.invitationExpiryMS, v.serverID, v.publicKey, v.keyIncarnation, v.resetGeneration, v.signInKind, v.kindRevision, v.issuedMS, v.expiresMS, v.lookupHash, v.bindingHash, v.wire.keyID, v.wire.nonce, v.wire.body, v.wire.aad)
	row, err = usPolicyRow(ctx, turn, usInvitationShortReceiptResultInsert, args...)
	if err != nil {
		return 0, err
	}
	if err = row.Scan(&child); err != nil {
		return 0, err
	}
	if child <= 0 || child > math.MaxInt64-charged || charged+child != want {
		return 0, ErrResolveRequired
	}
	charged += child
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
