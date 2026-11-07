package writer

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/apievents"
)

// The parent captures this COMPLETE immutable source once and hashes it with
// canonicalRequestDigest(SecurityAccept, 1, identity, source). Applied time and
// CURRENT actor tier are execution facts, never cached request authorization.
type wcSecuritySource struct {
	target wcSecuritySnapshot
	issuer wc008IssuerScope
}

type wcSecurityPrepared struct {
	request SecurityReceiptRequest
	source  wcSecuritySource
}

// Supplied afresh by the parent's private CURRENT authority check. This is
// private protocol data, not an issuer, capability or permission validator.
type wcSecurityCurrentActor struct {
	issuer wc008IssuerScope
	tier   string
}

// Private accounting for the parent's same-turn charge/forecast/physical
// terminal. A successful adapter return alone is never Applied/publication.
type wcSecurityApplied struct {
	value                         SecurityValue
	digest                        ResultDigest
	oldControlBytes, controlBytes int64
	event                         wcAPIEventEffect
}

// Caller supplies a trusted real command clock and already-validated CURRENT
// actor. The parent owns admission, complete resource reservations, FULL finish
// and revocation checks before release. Any error rolls back the whole prefix.
func applyWCSecurity(ctx context.Context, turn *commandTurn, p wcSecurityPrepared, current wcSecurityCurrentActor, appliedMS int64) (out wcSecurityApplied, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SecurityAccept || !validWCSecurityRequest(p.request) || appliedMS < 0 {
		return out, ErrInvalid
	}
	s, issuer := p.source.target, p.source.issuer
	if current.issuer != issuer || (current.tier != "owner" && current.tier != "admin") || issuer.ingressFamily == "" || issuer.ingressFamily == s.family || issuer.account == "" || issuer.profile == "" || len(issuer.memberIncarnation) != 32 || len(issuer.profileLifetime) != 32 {
		return out, ErrDisclosureDenied
	}
	if p.request.FamilyID != s.family || p.request.Receipt.Identity.Incarnation != s.incarnation {
		return out, ErrConflict
	}
	requestDigest, err := canonicalRequestDigest(SecurityAccept, 1, p.request.Receipt.Identity, p.source)
	if err != nil || requestDigest != p.request.Receipt.RequestDigest {
		return out, errors.Join(ErrConflict, err)
	}
	lookup, err := lookupWCSecurity(ctx, turn, p.request, s.server)
	if err != nil {
		return out, err
	}
	// Resolve/retired/replay are handled by the parent without entering this
	// fresh-only adapter. Never use new mutable facts to replay an old effect.
	if lookup.decision != receiptFresh || lookup.snapshot != s || s.revoked != 0 {
		return out, ErrConflict
	}
	targetTier, err := readWCSecurityTargetTier(ctx, turn, s)
	if err != nil {
		return out, err
	}
	if !wcSecurityTargetTierAllowed(current.tier, targetTier) {
		return out, ErrDisclosureDenied
	}
	q := wc008SecurityCommand{family: s.family, issuerFamily: issuer.ingressFamily, server: s.server, account: s.account, profile: s.profile,
		memberIncarnation: s.memberIncarnation, profileLifetime: s.profileLifetime, identity: p.request.Receipt.Identity,
		last: s.last, acknowledged: s.acknowledged, epoch: s.epoch, tokenGeneration: s.tokenGeneration,
		appliedMS: appliedMS, revokedAt: time.UnixMilli(appliedMS).UTC().Format(time.RFC3339), resultVersion: 1, oldControlBytes: s.controlBytes}
	effect, err := applyWC008SecurityDeny(ctx, turn, q)
	if err != nil {
		return out, err
	}
	out.value = SecurityValue{FamilyID: effect.family, CommandKind: "admin_revoke", ResultVersion: 1, AppliedMS: effect.appliedMS,
		IssuerAccountID: issuer.account, IssuerProfileID: issuer.profile, IssuerFamilyID: issuer.ingressFamily,
		IssuerMemberIncarnation: issuer.memberIncarnation, IssuerProfileLifetime: issuer.profileLifetime,
		Reason: effect.reason, RevokedAt: effect.revokedAt, ResultCount: 1}
	out.digest, err = canonicalResultDigest(q.identity, requestDigest, out.value)
	if err != nil {
		return out, err
	}
	out.controlBytes, err = storeWC008SecurityResult(ctx, turn, q, issuer, effect, requestDigest, out.digest)
	if err != nil {
		return out, err
	}
	out.oldControlBytes = s.controlBytes
	// Exact legacy devices-list vocabulary/audience from identity_devices
	// list triggers in persistence/migrations/0001_baseline.sql. ONE hint,
	// timestamp revision, empty payload; no target IDs, tokens or fanout.
	out.event, err = appendWCAPIEvent(ctx, turn, wcSecurityDevicesEvent(appliedMS))
	return out, err
}

func wcSecurityDevicesEvent(appliedMS int64) wcAPIEvent {
	return wcAPIEvent{audience: apievents.AdminAudience, eventType: "list.changed", resourceKind: "list", resourceID: "devices", revision: strconv.FormatInt(appliedMS, 10), atMS: appliedMS}
}
