package writer

import (
	"fmt"
	"math"
	"time"
)

// Compiled metadata lives in commands_us.go; no schema acceptance or issuer.

// The root independently verifies exact signed bytes, pinned key/root, kind,
// server audience, account, installation and current source revision BEFORE
// freezing this comparison. No role/boolean is authority. No raw challenge,
// entropy, signed evidence or protected response belongs in this carrier.
type usHostedPreparation struct {
	kind                                                         CommandID
	identity                                                     CommandIdentity
	server, account, profile, installation, challengeHash, nonce string
	issuedMS, expiresMS, challengeExpiresMS, nowMS               int64
	source, next                                                 usHostedRevisions
}
type usHostedRevisions struct{ pin, profile, trust, epoch int64 }

// Native facts only, never a final sign-in result or Applied. Parent's packet
// must retain kind/identity/digest, exact scope, source+post revisions, assertion
// kind/key/source-version and verified evidence digest, clock/expiry/skew facts,
// and complete private response bytes with key/format/nonce/AAD/digest binding.
// 570 additionally needs the exact protected challenge response; 571 needs the
// exact verified identity DTO+consent retained privately and the admission/session
// result in its OWN typed command. 574 is cached revision acceptance only.
type usHostedNativeResult struct {
	kind                                                                                 CommandID
	identity                                                                             CommandIdentity
	server, account, profile, installation, challengeHash, nonce                         string
	challengeExpiresMS, assertionIssuedMS, assertionExpiresMS, nonceExpiresMS, appliedMS int64
	source, post                                                                         usHostedRevisions
}

func (p *usHostedPreparation) nativeResult(now int64) usHostedNativeResult {
	r := usHostedNativeResult{kind: p.kind, identity: p.identity, server: p.server, account: p.account, profile: p.profile, installation: p.installation, challengeHash: p.challengeHash, nonce: p.nonce, challengeExpiresMS: p.challengeExpiresMS, assertionIssuedMS: p.issuedMS, assertionExpiresMS: p.expiresMS, appliedMS: now, source: p.source, post: p.next}
	if p.kind == usHostedChallengeSpend {
		r.nonceExpiresMS = p.expiresMS + 300000
	}
	return r
}

func (usHostedPreparation) String() string             { return "usHostedPreparation{redacted}" }
func (usHostedPreparation) GoString() string           { return "usHostedPreparation{redacted}" }
func (usHostedPreparation) Format(s fmt.State, _ rune) { formatUSPreparation(s, "usHostedPreparation") }
func (usHostedRevisions) String() string               { return "usHostedRevisions{redacted}" }
func (usHostedRevisions) GoString() string             { return "usHostedRevisions{redacted}" }
func (usHostedRevisions) Format(s fmt.State, _ rune)   { formatUSPreparation(s, "usHostedRevisions") }
func (usHostedNativeResult) String() string            { return "usHostedNativeResult{redacted}" }
func (usHostedNativeResult) GoString() string          { return "usHostedNativeResult{redacted}" }
func (usHostedNativeResult) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usHostedNativeResult")
}

func prepareUSHosted(in usHostedPreparation) (*usHostedPreparation, error) {
	if in.identity.Incarnation == [16]byte{} || in.identity.OperationID == [16]byte{} || in.identity.Sequence <= 0 || !usIdentifier(in.server) || in.nowMS <= 0 {
		return nil, invalid("us_hosted_scope")
	}
	switch in.kind {
	case usHostedChallengeIssue, usHostedChallengeSpend:
		if len(in.installation) < 32 || !usIdentifier(in.installation) || !usHex(in.challengeHash, 64) || in.challengeExpiresMS <= in.nowMS || in.challengeExpiresMS-in.nowMS > int64(30*time.Minute/time.Millisecond) {
			return nil, invalid("us_hosted_challenge")
		}
		if in.kind == usHostedChallengeIssue {
			if in.challengeExpiresMS-in.nowMS != int64(30*time.Minute/time.Millisecond) || in.nonce != "" || in.issuedMS != 0 || in.expiresMS != 0 {
				return nil, invalid("us_hosted_issue")
			}
		} else if !usIdentifier(in.account) || len(in.nonce) < 22 || !usIdentifier(in.nonce) || !in.fresh(in.nowMS) {
			return nil, invalid("us_hosted_assertion")
		}
	case usHostedProfileAccept:
		if !usIdentifier(in.account) || !usIdentifier(in.profile) || !in.source.valid() || !in.next.valid() || in.next.pin < in.source.pin || in.next.profile < in.source.profile || in.next.trust < in.source.trust || in.next.epoch < in.source.epoch {
			return nil, invalid("us_hosted_revision")
		}
	default:
		return nil, ErrInvalid
	}
	return &in, nil
}
func (r usHostedRevisions) valid() bool {
	return r.pin >= 0 && r.profile >= 0 && r.trust >= 0 && r.epoch >= 0
}
func (p *usHostedPreparation) fresh(now int64) bool {
	// Subtraction only after positive ordered operands: no overflowing time addition.
	const skew = int64(5 * time.Minute / time.Millisecond)
	return p.expiresMS <= math.MaxInt64-300000 && p.issuedMS > 0 && p.expiresMS > p.issuedMS && p.expiresMS-p.issuedMS <= int64(10*time.Minute/time.Millisecond) && (p.issuedMS <= now || p.issuedMS-now <= skew) && (p.expiresMS > now || now-p.expiresMS < skew)
}
