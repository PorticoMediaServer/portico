package writer

import (
	"encoding/base64"
	"fmt"
)

// Reserved metadata only. Root owns registration, canonical request/result codecs,
// custody, current authority and admission; these preparations execute nothing.
const (
	usSetupCode             CommandID = 561
	usSetupOutsideAdmission CommandID = 562
	usSetupAdmissionSpanMS  int64     = 1800000
)

// Private scalar manifest: identity; old/new keyed code digests; token/peer/origin
// keyed digests; expected source expiry. No plaintext code, token or setup result.
// Empty old code means insert-only; otherwise rotation compares the current hash.
// Root distinguishes boot rotation from proof-authorized rotation; a supplied
// hash is a comparison witness, never evidence that a code was verified.
type usSetupPrepared struct {
	kind                               CommandID
	identity                           CommandIdentity
	oldCode, code, token, peer, origin string
	expectedExpiry                     int64
	consume                            bool
}

func prepareUSSetupCode(i CommandIdentity, old, next string) (*usSetupPrepared, error) {
	if !validUSSetupIdentity(i) || (old != "" && !usSetupDigest(old)) || !usSetupDigest(next) || old == next {
		return nil, invalid("setup_code")
	}
	return &usSetupPrepared{kind: usSetupCode, identity: i, oldCode: old, code: next}, nil
}
func prepareUSSetupAdmission(i CommandIdentity, token, peer, origin string, consume bool, expectedExpiry int64) (*usSetupPrepared, error) {
	if !validUSSetupIdentity(i) || !usSetupDigest(token) || !usSetupDigest(peer) || !usSetupDigest(origin) || (consume && expectedExpiry <= 0) || (!consume && expectedExpiry != 0) {
		return nil, invalid("setup_admission")
	}
	return &usSetupPrepared{kind: usSetupOutsideAdmission, identity: i, token: token, peer: peer, origin: origin, consume: consume, expectedExpiry: expectedExpiry}, nil
}
func validUSSetupIdentity(i CommandIdentity) bool {
	return i.Incarnation != [16]byte{} && i.OperationID != [16]byte{} && i.Sequence > 0
}
func usSetupDigest(s string) bool {
	if len(s) != 43 {
		return false
	}
	b, e := base64.RawURLEncoding.Strict().DecodeString(s)
	return e == nil && len(b) == 32
}
func (usSetupPrepared) String() string             { return "usSetupPrepared{redacted}" }
func (usSetupPrepared) GoString() string           { return "usSetupPrepared{redacted}" }
func (usSetupPrepared) Format(s fmt.State, _ rune) { formatUSPreparation(s, "usSetupPrepared") }

// Native post manifest for US-001: code singleton, code_hash, created_ms;
// admission token_hash, peer_hash, origin_hash, expires_ms (consume returns the
// deleted preimage, consumed outcome derives only from exactly one returned row).
// These are private scalars, not a complete public setup result. Legacy sealSetup
// is JSON, not AEAD. Root must separately protect any exact issued secret envelope.
