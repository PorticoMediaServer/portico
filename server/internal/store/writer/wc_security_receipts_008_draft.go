package writer

import "context"

const wc008SecurityReceipt = "wc.security.receipt"

type wc008IssuerScope struct {
	account, profile, ingressFamily    string
	memberIncarnation, profileLifetime string
}

// The permanent FIRST scalar and command last advance together, preserving
// the exact native reason/time previously returned by the deny CAS. W still
// owns current issuer authorization, closed digest validation and FULL finish.
// Actual control-byte delta must also be charged by W before commit; this
// helper returns native new control bytes, not a guessed old charge or refund.
func storeWC008SecurityResult(ctx context.Context, turn *commandTurn, q wc008SecurityCommand, issuer wc008IssuerScope,
	effect wc008SecurityEffect, request RequestDigest, result ResultDigest) (controlBytes int64, err error) {
	if turn == nil {
		return 0, ErrInvalid
	}
	defer func() { controlBytes, err = finishCommand(ctx, commandLifetime(turn), controlBytes, err) }()
	if err = q.validate(); err != nil {
		return 0, err
	}
	if effect.family != q.family || effect.reason != "admin_revoke" || effect.revokedAt != q.revokedAt || effect.appliedMS != q.appliedMS || effect.controlVariableBytes <= 0 ||
		issuer.ingressFamily != q.issuerFamily || issuer.account == "" || issuer.profile == "" ||
		issuer.memberIncarnation == "" || issuer.profileLifetime == "" || request == (RequestDigest{}) || result == (ResultDigest{}) {
		return 0, ErrResolveRequired
	}
	controlBytes, err = wc008DraftInteger(ctx, turn, wc008SecurityReceipt, q.identity.Sequence, q.identity.Sequence,
		q.identity.OperationID[:], request[:], issuer.account, issuer.profile, issuer.ingressFamily,
		issuer.memberIncarnation, issuer.profileLifetime, effect.reason, effect.revokedAt, effect.appliedMS, q.resultVersion, result[:],
		q.family, q.identity.Incarnation[:], q.last, q.acknowledged, effect.revokedAt, effect.controlVariableBytes)
	if err != nil {
		return 0, err
	}
	if controlBytes <= 0 {
		return 0, ErrResolveRequired
	}
	return controlBytes, turn.checkLifetimes(ctx)
}
