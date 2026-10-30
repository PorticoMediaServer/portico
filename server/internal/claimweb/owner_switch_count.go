package claimweb

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"time"
)

const OwnerSwitchCountKind = "portico.owner-switch-count.v1"

var ErrOwnerSwitchCount = errors.New("invalid owner switch count")

// OwnerSwitchCount is an optional captured preview fact, never owner authority.
// These TWELVE fields are the exact signed wire order. Keep OwnerStart unchanged.
type OwnerSwitchCount struct {
	Kind             string    `json:"kind"`
	ServerID         string    `json:"serverId"`
	PublicKey        string    `json:"publicKey"`
	OperationID      string    `json:"operationId"`
	Purpose          string    `json:"purpose"`
	PreviewID        string    `json:"previewId"`
	PreviewRevision  int64     `json:"previewRevision,string"`
	LocalGeneration  int64     `json:"localGeneration,string"`
	ClaimHash        string    `json:"claimHash"`
	OtherPeopleCount int       `json:"otherPeopleCount"`
	IssuedAt         time.Time `json:"issuedAt"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

func (OwnerSwitchCount) String() string   { return "[owner switch count redacted]" }
func (OwnerSwitchCount) GoString() string { return "[owner switch count redacted]" }

// VerifyOwnerSwitchCount checks both original authority envelopes first. A
// caller must omit a missing/invalid count, not fail or change genuine approval.
// Runtime registration freezes the first optional verified proof in the same
// owner-start transaction; this pure leaf cannot establish a live preview/CAS.
func VerifyOwnerSwitchCount(original, ownerStart, signed Signed, audience string, now time.Time) (OwnerSwitchCount, error) {
	invalid := func() (OwnerSwitchCount, error) { return OwnerSwitchCount{}, ErrOwnerSwitchCount }
	claim, start, e := VerifyOwnerStart(original, ownerStart, audience, now)
	if e != nil {
		return invalid()
	}
	raw, e := ownerStartBase64(signed.Payload, -1)
	var count OwnerSwitchCount
	if e != nil || ownerStartJSON(raw, &count) != nil {
		return invalid()
	}
	// The shared canonical decoder leaves this new leaf's times intact. Require
	// the canonical UTC representation as well, without changing old proof bytes.
	count.IssuedAt, count.ExpiresAt = count.IssuedAt.UTC(), count.ExpiresAt.UTC()
	canonical, e := json.Marshal(count)
	if e != nil || !bytes.Equal(canonical, raw) {
		return invalid()
	}
	key, e := ownerStartBase64(claim.PublicKey, ed25519.PublicKeySize)
	if e != nil {
		return invalid()
	}
	signature, e := ownerStartBase64(signed.Signature, ed25519.SignatureSize)
	if e != nil || !ed25519.Verify(key, raw, signature) {
		return invalid()
	}
	claimRaw, e := ownerStartBase64(original.Payload, -1)
	if e != nil {
		return invalid()
	}
	hash, e := ownerStartBase64(count.ClaimHash, sha256.Size)
	digest := sha256.Sum256(claimRaw)
	if e != nil || subtle.ConstantTimeCompare(hash, digest[:]) != 1 {
		return invalid()
	}
	if count.Kind != OwnerSwitchCountKind || count.Purpose != "switch" ||
		count.ServerID != claim.ServerID || count.PublicKey != claim.PublicKey ||
		count.OperationID != claim.OperationID || count.LocalGeneration != claim.LocalGeneration ||
		!id.MatchString(count.PreviewID) || count.PreviewRevision <= 0 ||
		count.OtherPeopleCount < 0 || count.OtherPeopleCount > 10000 ||
		count.IssuedAt.IsZero() || count.IssuedAt.Before(claim.IssuedAt) || count.IssuedAt.After(now.Add(time.Minute)) ||
		!count.ExpiresAt.After(now) || !count.ExpiresAt.After(count.IssuedAt) ||
		count.ExpiresAt.Sub(count.IssuedAt) > OwnerStartLifetime ||
		count.ExpiresAt.After(claim.ExpiresAt) || count.ExpiresAt.After(start.ExpiresAt) {
		return invalid()
	}
	return count, nil
}
