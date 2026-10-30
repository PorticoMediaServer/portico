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

const SetupStartKind = "portico.setup-start.v1"
const SetupStartLifetime = 5 * time.Minute

var ErrSetupStart = errors.New("invalid setup start")

// SetupStart is the distinct initial-setup wire. These seventeen fields are
// signed in declaration order. It is not an OwnerStart or an issued session.
type SetupStart struct {
	Kind            string    `json:"kind"`
	Audience        string    `json:"audience"`
	ClaimHash       string    `json:"claimHash"`
	OperationID     string    `json:"operationId"`
	ServerID        string    `json:"serverId"`
	PublicKey       string    `json:"publicKey"`
	LocalGeneration int64     `json:"localGeneration,string"`
	Address         string    `json:"address"`
	Purpose         string    `json:"purpose"`
	SetupRequestID  string    `json:"setupRequestId"`
	SetupRevision   int64     `json:"setupRevision,string"`
	State           string    `json:"state"`
	CodeChallenge   string    `json:"codeChallenge"`
	InstallationID  string    `json:"installationId"`
	ServerChallenge string    `json:"serverChallenge"`
	IssuedAt        time.Time `json:"issuedAt"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

func (SetupStart) String() string   { return "[setup start redacted]" }
func (SetupStart) GoString() string { return "[setup start redacted]" }

// VerifySetupStart verifies the two complete signed envelopes. Runtime must
// separately enforce the captured setup-token/request/revision, live one-use
// challenge, installed owner and atomic setup completion. No token is here.
func VerifySetupStart(original, signed Signed, audience string, now time.Time) (Request, SetupStart, error) {
	invalid := func() (Request, SetupStart, error) { return Request{}, SetupStart{}, ErrSetupStart }
	var claim Request
	claimRaw, e := ownerStartBase64(original.Payload, -1)
	if e != nil || ownerStartJSON(claimRaw, &claim) != nil {
		return invalid()
	}
	key, e := ownerStartBase64(claim.PublicKey, ed25519.PublicKeySize)
	if e != nil {
		return invalid()
	}
	if _, e = ownerStartBase64(original.Signature, ed25519.SignatureSize); e != nil {
		return invalid()
	}
	if _, e = Verify(original, audience, now); e != nil || claim.Purpose != "setup" || claim.ReturnRegistrationID != "" {
		return invalid()
	}
	raw, e := ownerStartBase64(signed.Payload, -1)
	var start SetupStart
	if e != nil || ownerStartJSON(raw, &start) != nil {
		return invalid()
	}
	start.IssuedAt, start.ExpiresAt = start.IssuedAt.UTC(), start.ExpiresAt.UTC()
	canonical, e := json.Marshal(start)
	if e != nil || !bytes.Equal(canonical, raw) {
		return invalid()
	}
	signature, e := ownerStartBase64(signed.Signature, ed25519.SignatureSize)
	if e != nil || !ed25519.Verify(key, raw, signature) {
		return invalid()
	}
	hash, e := ownerStartBase64(start.ClaimHash, sha256.Size)
	digest := sha256.Sum256(claimRaw)
	if e != nil || subtle.ConstantTimeCompare(hash, digest[:]) != 1 {
		return invalid()
	}
	if _, e = ownerStartBase64(start.CodeChallenge, sha256.Size); e != nil {
		return invalid()
	}
	if start.Kind != SetupStartKind || start.Purpose != "setup" || start.Audience != audience || start.Audience != claim.Audience ||
		start.OperationID != claim.OperationID || start.ServerID != claim.ServerID || start.PublicKey != claim.PublicKey ||
		start.LocalGeneration != claim.LocalGeneration || start.Address != claim.Address || !ownerStartAddress(start.Address) ||
		!id.MatchString(start.SetupRequestID) || start.SetupRevision <= 0 || !ownerStartSecret.MatchString(start.State) ||
		!ownerStartSecret.MatchString(start.InstallationID) || !ownerStartSecret.MatchString(start.ServerChallenge) ||
		start.IssuedAt.IsZero() || start.IssuedAt.Before(claim.IssuedAt) || start.IssuedAt.After(now.Add(time.Minute)) ||
		!start.ExpiresAt.After(now) || !start.ExpiresAt.After(start.IssuedAt) ||
		start.ExpiresAt.Sub(start.IssuedAt) > SetupStartLifetime || start.ExpiresAt.After(claim.ExpiresAt) {
		return invalid()
	}
	return claim, start, nil
}
