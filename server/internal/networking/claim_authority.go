package networking

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"time"
)

// ClaimAuthority describes only the installed credential. It grants no account
// membership or playback permission.
type ClaimAuthority struct {
	Commit
	AccountID           string
	LocalGeneration     int64
	Revision            int64
	State               string
	IssuedAt, ExpiresAt time.Time
	Digest              [32]byte
}
type ClaimAuthorityVerifier func(context.Context, []byte) (ClaimAuthority, error)
type authorityWire struct {
	Kind                 string `json:"kind"`
	Version              string `json:"version"`
	Audience             string `json:"audience"`
	OperationID          string `json:"operationId"`
	ServerID             string `json:"serverId"`
	AccountID            string `json:"accountId"`
	LocalGeneration      int64  `json:"localGeneration,string"`
	ClaimGeneration      string `json:"claimGeneration"`
	CredentialGeneration string `json:"credentialGeneration"`
	ObservationRevision  int64  `json:"observationRevision,string"`
	State                string `json:"state"`
	IssuedAt             string `json:"issuedAt"`
	ExpiresAt            string `json:"expiresAt"`
}

func terminalClaimState(state string) bool {
	switch state {
	case "revoked", "account_deleted", "unclaimed", "reassigned":
		return true
	}
	return false
}
func NewClaimAuthorityVerifier(audience, keyID string, key ed25519.PublicKey) (ClaimAuthorityVerifier, error) {
	if !validAudience(audience) || len(key) != ed25519.PublicKeySize || keyID != trust.KeyID(key) {
		return nil, ErrInvalid
	}
	pin := append(ed25519.PublicKey(nil), key...)
	return func(ctx context.Context, proof []byte) (ClaimAuthority, error) {
		return verifyClaimAuthorityAt(ctx, audience, keyID, pin, proof, time.Now())
	}, nil
}
func verifyClaimAuthorityAt(ctx context.Context, audience, keyID string, pin ed25519.PublicKey, proof []byte, now time.Time) (ClaimAuthority, error) {
	if e := ctx.Err(); e != nil {
		return ClaimAuthority{}, e
	}
	if len(proof) < 1 || len(proof) > 16<<10 || len(pin) != ed25519.PublicKeySize || keyID != trust.KeyID(pin) {
		return ClaimAuthority{}, ErrInvalid
	}
	var envelope approvalEnvelope
	if canonicalApprovalJSON(proof, &envelope) != nil {
		return ClaimAuthority{}, ErrInvalid
	}
	raw, e := envelope.Verify(pin, "documents", now)
	if e != nil || len(raw) > 16<<10 {
		return ClaimAuthority{}, ErrInvalid
	}

	var w authorityWire
	if canonicalApprovalJSON(raw, &w) != nil || w.Kind != "portico.claim.authority" || w.Version != "1" || w.Audience != audience || !validID(w.OperationID) || !validID(w.ServerID) || !validID(w.AccountID) || w.LocalGeneration < 0 || w.ObservationRevision < 1 || !validGeneration(w.ClaimGeneration) || !validGeneration(w.CredentialGeneration) || (w.State != "active" && !terminalClaimState(w.State)) {
		return ClaimAuthority{}, ErrInvalid
	}
	issued, e := time.Parse(time.RFC3339Nano, w.IssuedAt)
	if e != nil || issued.UTC().Format(time.RFC3339Nano) != w.IssuedAt {
		return ClaimAuthority{}, ErrInvalid
	}
	expires, e := time.Parse(time.RFC3339Nano, w.ExpiresAt)
	if e != nil || expires.UTC().Format(time.RFC3339Nano) != w.ExpiresAt || !issued.Before(expires) || expires.Sub(issued) > 5*time.Minute || issued.After(now.Add(maxClockSkew)) || !now.Before(expires) {
		return ClaimAuthority{}, ErrUnavailable
	}
	if e = ctx.Err(); e != nil {
		return ClaimAuthority{}, e
	}
	return ClaimAuthority{Commit: Commit{w.OperationID, w.ServerID, w.ClaimGeneration, w.CredentialGeneration}, AccountID: w.AccountID, LocalGeneration: w.LocalGeneration, Revision: w.ObservationRevision, State: w.State, IssuedAt: issued, ExpiresAt: expires, Digest: sha256.Sum256(raw)}, nil
}
