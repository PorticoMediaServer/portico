package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type usInvitationShortReceiptLookup struct {
	owner    usPolicyOwnerFacts
	decision receiptDecision
	result   usInvitationShortSavedResult
	digest   ResultDigest
}

func (usInvitationShortReceiptLookup) String() string {
	return "usInvitationShortReceiptLookup{redacted}"
}
func (usInvitationShortReceiptLookup) GoString() string {
	return "usInvitationShortReceiptLookup{redacted}"
}
func (usInvitationShortReceiptLookup) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationShortReceiptLookup")
}

// The original fixed binding is classified under current authority before any
// saved payload is read. Neither invitation nor staged key source is consulted
// during replay. The protected display is private issuance evidence; public
// retries still omit ShortCode.
func lookupUSInvitationShort(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, q usPolicyReceiptRequest) (out usInvitationShortReceiptLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, errors.Join(err, a.check(turn))) }()
	if err = a.check(turn); err != nil {
		return out, err
	}
	if q.kind != usInvitationShortIssue {
		return out, ErrInvalid
	}
	custody, err := lookupUSSecurityBinding(ctx, turn, a, q)
	if err != nil {
		return out, err
	}
	out.owner, out.decision = custody.owner, custody.decision
	if out.decision != receiptReplay {
		return out, nil
	}
	if custody.binding.resultCount != 0 {
		return out, ErrResolveRequired
	}
	key := []any{q.ownerID, q.reference.Identity.Incarnation[:], q.reference.Identity.Sequence}
	row, err := usPolicyRow(ctx, turn, usInvitationShortReceiptOctets, key...)
	if err != nil {
		return out, err
	}
	var header, child int64
	if err = row.Scan(&header); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	row, err = usPolicyRow(ctx, turn, usInvitationShortReceiptResultOctets, key...)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&child); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if header <= 0 || child <= 0 || header > maxCanonicalResultBytes || child > maxCanonicalResultBytes-header {
		return out, ErrResolveRequired
	}
	if err = a.requireMemory(ctx, turn, header+child); err != nil {
		return out, err
	}
	v := usInvitationShortSavedResult{kind: q.kind, version: 1, ownerID: q.ownerID, issuerServer: a.issuer.server}
	row, err = usPolicyRow(ctx, turn, usInvitationShortReceiptHeader, key...)
	if err != nil {
		return out, err
	}
	var kind, version, count, actualBytes int64
	var codec string
	var digest []byte
	var keyID sql.NullString
	var nonce, body sql.Null[[]byte]
	if err = row.Scan(&kind, &version, &codec, &v.appliedMS, &digest, &count, &keyID, &nonce, &body, &actualBytes); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if kind != int64(q.kind) || version != 1 || codec != "invitation_short_issue_v1" || count != 0 || actualBytes != header || keyID.Valid || nonce.Valid || body.Valid || len(digest) != 32 || ResultDigest(digest) != ResultDigest(custody.binding.resultDigest) {
		return out, ErrResolveRequired
	}
	row, err = usPolicyRow(ctx, turn, usInvitationShortReceiptResult, key...)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&v.invitationID, &v.invitationRevision, &v.codeHash, &v.role, &v.invitationExpiryMS, &v.serverID, &v.publicKey, &v.keyIncarnation, &v.resetGeneration, &v.signInKind, &v.kindRevision, &v.issuedMS, &v.expiresMS, &v.lookupHash, &v.bindingHash, &v.wire.keyID, &v.wire.nonce, &v.wire.body, &v.wire.aad, &actualBytes); err != nil {
		return out, errors.Join(ErrResolveRequired, err)
	}
	if actualBytes != child {
		return out, ErrResolveRequired
	}
	out.digest, err = usInvitationShortResultDigest(q.reference.Identity, q.reference.RequestDigest, v)
	if err != nil || out.digest != ResultDigest(custody.binding.resultDigest) {
		return out, errors.Join(ErrResolveRequired, err)
	}
	actual, err := usInvitationShortResultVariableBytes(v)
	if err != nil || actual != header+child {
		return out, errors.Join(ErrResolveRequired, err)
	}
	out.result = v
	return out, turn.checkLifetimes(ctx)
}
