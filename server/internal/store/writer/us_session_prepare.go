package writer

import (
	"math"
	"slices"
	"time"
)

// Compiled metadata lives in commands_us.go. These components remain private;
// the parent owns fresh authority, concrete result schema, receipts and COMMIT.
const (
	usSessionMaxProtectedBytes = 64 << 10
)

func usSessionDescriptors() []Descriptor {
	return []Descriptor{
		{ID: usSessionRenewal, Name: "us_session_renewal", Durability: Full},
		{ID: usSessionRefresh, Name: "us_session_refresh", Durability: Full},
	}
}

// Comparison witnesses from native storage; these are never an authenticated
// principal. In particular no caller role, bearer, refresh secret or controller
// proof enters this structure. Incarnation fences reuse of the public family ID.
type usSessionPins struct {
	server, account, profile, family, device, installation string
	memberIncarnation, profileLifetime                     string
	incarnation                                            [16]byte
	epoch, authorityGeneration, generation                 int64
	accessDigest, accessExpiresAt, horizon                 string
}

// Entropy, hashing and protected response construction belong OUTSIDE writer
// custody. The parent must prove the exact wire decodes to these result facts,
// under the renewal AAD (family/predecessor/controller/epoch/request/generation/
// expiry/horizon) or refresh AAD (family/predecessor/installation/request).
// Structural bounds here are not a cryptographic verification.
type usSessionPreparedOutput struct {
	accessDigest, refreshDigest, accessExpiresAt, horizon string
	nonce, ciphertext                                     []byte
}

type usSessionRotationRequest struct {
	ownerID, requestID              string
	pins                            usSessionPins
	controllerID, controllerEpoch   string // renewal only, verified by the parent
	refreshDigest, refreshExpiresAt string // refresh only
	refreshGeneration               int64  // may lag access generation after controller renewals
	priorReceiptDigest              string // refresh only; exact previous receipt, never family-wide DELETE
	output                          usSessionPreparedOutput
}

// Clocks come only from the trusted root. For refresh, resultHorizon is the
// EXACT policy-derived horizon (including Portico admission cap); the helper
// cannot infer policy authority from a boolean, role or a caller duration.
type usSessionClocks struct{ now, resultHorizon string }

type usSessionRotation struct {
	kind     CommandID
	identity CommandIdentity
	request  usSessionRotationRequest
	clocks   usSessionClocks
}

func prepareUSSessionRenewal(identity CommandIdentity, in usSessionRotationRequest, clocks usSessionClocks) (*usSessionRotation, error) {
	return prepareUSSessionRotation(usSessionRenewal, identity, in, clocks)
}
func prepareUSSessionRefresh(identity CommandIdentity, in usSessionRotationRequest, clocks usSessionClocks) (*usSessionRotation, error) {
	return prepareUSSessionRotation(usSessionRefresh, identity, in, clocks)
}

func prepareUSSessionRotation(kind CommandID, identity CommandIdentity, in usSessionRotationRequest, clocks usSessionClocks) (*usSessionRotation, error) {
	// Raw bounds precede any allocation, parsing or clone.
	if kind != usSessionRenewal && kind != usSessionRefresh ||
		identity.Incarnation == [16]byte{} || identity.OperationID == [16]byte{} || identity.Sequence <= 0 ||
		!usIdentifier(in.ownerID) || !usIdentifier(in.requestID) || len(in.output.ciphertext) == 0 ||
		len(in.output.ciphertext) > usSessionMaxProtectedBytes || len(in.output.nonce) > 64 {
		return nil, invalid("us_session_input")
	}
	p := in.pins
	if !usIdentifier(p.server) || !usIdentifier(p.account) || !usIdentifier(p.profile) ||
		!usIdentifier(p.family) || !usIdentifier(p.device) || !usIdentifier(p.installation) || len(p.installation) < 32 ||
		!usHex(p.memberIncarnation, 32) || !usHex(p.profileLifetime, 32) || p.incarnation == [16]byte{} ||
		p.epoch <= 0 || p.authorityGeneration <= 0 || p.generation <= 0 || p.generation == math.MaxInt64 ||
		!usCredentialDigest(p.accessDigest) || !usCredentialDigest(in.output.accessDigest) || in.output.accessDigest == p.accessDigest {
		return nil, invalid("us_session_pins")
	}
	now, ok := usSessionTime(clocks.now)
	if !ok {
		return nil, invalid("us_session_clock")
	}
	oldHorizon, ok := usSessionTime(p.horizon)
	if !ok || !oldHorizon.After(now) {
		return nil, invalid("us_session_horizon")
	}
	oldExpiry, ok := usSessionTime(p.accessExpiresAt)
	if !ok || oldExpiry.After(oldHorizon) {
		return nil, invalid("us_session_expiry")
	}
	horizon, ok := usSessionTime(clocks.resultHorizon)
	if !ok || !horizon.After(now) || in.output.horizon != clocks.resultHorizon {
		return nil, invalid("us_session_result_horizon")
	}
	expiry := now.Add(15 * time.Minute)
	if horizon.Before(expiry) {
		expiry = horizon
	}
	if in.output.accessExpiresAt != expiry.Format(time.RFC3339) {
		return nil, invalid("us_session_result_expiry")
	}
	switch kind {
	case usSessionRenewal:
		if !usIdentifier(in.controllerID) || !usIdentifier(in.controllerEpoch) || !oldExpiry.After(now) ||
			clocks.resultHorizon != p.horizon || len(in.output.nonce) == 0 ||
			in.refreshDigest != "" || in.refreshExpiresAt != "" || in.refreshGeneration != 0 ||
			in.priorReceiptDigest != "" || in.output.refreshDigest != "" {
			return nil, invalid("us_session_renewal_binding")
		}
	case usSessionRefresh:
		refreshExpiry, valid := usSessionTime(in.refreshExpiresAt)
		if !valid || !refreshExpiry.After(now) || in.refreshGeneration <= 0 || in.refreshGeneration > p.generation ||
			!usCredentialDigest(in.refreshDigest) || !usCredentialDigest(in.output.refreshDigest) || in.refreshDigest == in.output.refreshDigest ||
			in.refreshDigest == p.accessDigest || in.refreshDigest == in.output.accessDigest ||
			in.output.refreshDigest == p.accessDigest || in.output.refreshDigest == in.output.accessDigest ||
			in.priorReceiptDigest != "" && (!usCredentialDigest(in.priorReceiptDigest) || in.priorReceiptDigest == in.refreshDigest) ||
			in.controllerID != "" || in.controllerEpoch != "" || len(in.output.nonce) != 0 {
			return nil, invalid("us_session_refresh_binding")
		}
	}
	in.output.nonce = slices.Clone(in.output.nonce)
	in.output.ciphertext = slices.Clone(in.output.ciphertext)
	// A descriptor is metadata only. The parent must define stable complete
	// request-vs-preparation digest semantics before enabling these purposes.
	return &usSessionRotation{kind: kind, identity: identity, request: in, clocks: clocks}, nil
}

// Canonical UTC whole-second strings permit exact native TEXT comparison. No
// local wall clock, SQLite 'now', truncation, zone coercion or guessed expiry.
func usSessionTime(raw string) (time.Time, bool) {
	if len(raw) != len("2006-01-02T15:04:05Z") {
		return time.Time{}, false
	}
	v, err := time.Parse(time.RFC3339, raw)
	return v, err == nil && v.Year() >= 1 && v.Format(time.RFC3339) == raw
}

// Concrete non-secret result facts for US-001's typed receipt extension.
// Protected response bytes must be retained separately with their exact digest,
// nonce/format/key binding; they must never become generic command-body authority.
// Parent's packet must also pin command identity/digest, policy/authority receipt,
// previous receipt retirement, and typed saved replay/reuse outcomes explicitly.
// US-001 also needs an at-most-one-active-refresh-receipt family invariant. The
// legacy family index is nonunique; empty priorReceiptDigest is safe ONLY after
// the root proves there is no previous active receipt. A supplied digest must
// name that one exact receipt. This helper never discovers/deletes a family set.
// Preserve protocolVersion "2.0" and string tokenGeneration at the API boundary;
// the native INTEGER below is an internal representation only.
type usSessionNativeResult struct {
	kind                                                                           CommandID
	identity                                                                       CommandIdentity
	ownerID, protocolVersion                                                       string
	family, server, account, profile, device, installation                         string
	memberIncarnation, profileLifetime                                             string
	familyIncarnation                                                              [16]byte
	epoch, authorityGeneration, accessPredecessorGeneration, generation            int64
	refreshPredecessorGeneration                                                   int64
	accessPredecessorDigest, refreshPredecessorDigest, accessDigest, refreshDigest string
	requestID, controllerID, controllerEpoch, accessExpiresAt, horizon             string
}

func (c *usSessionRotation) nativeResult() usSessionNativeResult {
	r, p := c.request, c.request.pins
	return usSessionNativeResult{
		kind: c.kind, identity: c.identity, ownerID: r.ownerID, protocolVersion: "2.0", family: p.family, server: p.server, account: p.account, profile: p.profile,
		device: p.device, installation: p.installation, memberIncarnation: p.memberIncarnation, profileLifetime: p.profileLifetime,
		familyIncarnation: p.incarnation, epoch: p.epoch, authorityGeneration: p.authorityGeneration,
		accessPredecessorGeneration: p.generation, generation: p.generation + 1, accessPredecessorDigest: p.accessDigest,
		refreshPredecessorGeneration: r.refreshGeneration, refreshPredecessorDigest: r.refreshDigest,
		accessDigest: r.output.accessDigest, refreshDigest: r.output.refreshDigest,
		requestID: r.requestID, controllerID: r.controllerID, controllerEpoch: r.controllerEpoch,
		accessExpiresAt: r.output.accessExpiresAt, horizon: r.output.horizon,
	}
}
