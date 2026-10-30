package networking

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"regexp"
	"time"
)

type approvalWire struct {
	TargetUsername        string `json:"targetUsername,omitempty"`
	Kind                  string `json:"kind"`
	Version               string `json:"version"`
	Audience              string `json:"audience"`
	OperationID           string `json:"operationId"`
	ServerID              string `json:"serverId"`
	AccountID             string `json:"accountId"`
	PublicKey             string `json:"publicKey"`
	LocalGeneration       int64  `json:"localGeneration,string"`
	ApprovalRevision      int64  `json:"approvalRevision,string"`
	Purpose               string `json:"purpose"`
	CurrentOwnerAccountID string `json:"currentOwnerAccountId"`
	PreviousOwnerRole     string `json:"previousOwnerRole"`
	ReturnRegistrationID  string `json:"returnRegistrationId"`
	TransitionMode        string `json:"transitionMode,omitempty"`
	IssuedAt              string `json:"issuedAt"`
	ExpiresAt             string `json:"expiresAt"`
}

var claimUsername = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,30}[a-z0-9]$`)

type approvalEnvelope = trust.Envelope

// NewApprovalVerifier pins one Hosted signing identity. It grants no current
// family or member authority; Hosted must recheck those facts on every consume.
func NewApprovalVerifier(audience, keyID string, key ed25519.PublicKey, accept ...func(context.Context, trust.Envelope) error) (ApprovalVerifier, error) {
	// The root is pinned by key and by ID: an ID that does not name this key
	// is a misconfiguration, not a second trust anchor (A15).
	if !validAudience(audience) || len(key) != ed25519.PublicKeySize || keyID != trust.KeyID(key) {
		return nil, ErrInvalid
	}
	pin := append(ed25519.PublicKey(nil), key...)
	return func(ctx context.Context, proof []byte) (ApprovalFacts, error) {
		if len(accept) > 0 {
			var envelope trust.Envelope
			if json.Unmarshal(proof, &envelope) != nil {
				return ApprovalFacts{}, ErrInvalid
			}
			if e := accept[0](ctx, envelope); e != nil {
				return ApprovalFacts{}, e
			}
		}
		return verifyApprovalAt(ctx, audience, keyID, pin, proof, time.Now())
	}, nil
}
func canonicalApprovalJSON(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrInvalid
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return ErrInvalid
	}
	canonical, e := json.Marshal(out)
	if e != nil || !bytes.Equal(raw, canonical) {
		return ErrInvalid
	}
	return nil
}
func approvalBase64(raw string, size int) ([]byte, error) {
	decoded, e := base64.RawURLEncoding.Strict().DecodeString(raw)
	if e != nil || (size >= 0 && len(decoded) != size) || base64.RawURLEncoding.EncodeToString(decoded) != raw {
		return nil, ErrInvalid
	}
	return decoded, nil
}
func verifyApprovalAt(ctx context.Context, audience, keyID string, pin ed25519.PublicKey, proof []byte, now time.Time) (ApprovalFacts, error) {
	if e := ctx.Err(); e != nil {
		return ApprovalFacts{}, e
	}
	if len(proof) < 1 || len(proof) > 16<<10 || len(pin) != ed25519.PublicKeySize || keyID != trust.KeyID(pin) {
		return ApprovalFacts{}, ErrInvalid
	}
	var envelope approvalEnvelope
	if canonicalApprovalJSON(proof, &envelope) != nil {
		return ApprovalFacts{}, ErrInvalid
	}
	raw, e := envelope.Verify(pin, "documents", now)
	if e != nil || len(raw) > 16<<10 {
		return ApprovalFacts{}, ErrInvalid
	}

	var wire approvalWire
	if canonicalApprovalJSON(raw, &wire) != nil || wire.Kind != "portico.claim.approval" || (wire.Version != "2" && wire.Version != "3") || wire.Audience != audience || wire.ApprovalRevision < 1 {
		return ApprovalFacts{}, ErrInvalid
	}
	// A v2 approval is ordinary switch only. A v3 approval must state its closed
	// mode, and only a switch claim may carry one.
	if wire.Version == "2" && wire.TransitionMode != "" ||
		wire.Version == "3" && wire.TransitionMode != "switch" && wire.TransitionMode != "start-over" ||
		wire.TransitionMode != "" && wire.Purpose != "switch" {
		return ApprovalFacts{}, ErrInvalid
	}
	public, e := approvalBase64(wire.PublicKey, ed25519.PublicKeySize)
	if e != nil {
		return ApprovalFacts{}, ErrInvalid
	}
	b := Binding{OperationID: wire.OperationID, ServerID: wire.ServerID, AccountID: wire.AccountID, PublicKey: public, LocalGeneration: wire.LocalGeneration}
	purpose := ClaimPurpose{wire.Purpose, wire.CurrentOwnerAccountID, wire.PreviousOwnerRole, wire.ReturnRegistrationID}
	if !purpose.Valid() || !validBinding(b) || ((wire.Purpose == "change-owner" || wire.Purpose == "switch") && !claimUsername.MatchString(wire.TargetUsername)) || (wire.Purpose != "change-owner" && wire.Purpose != "switch" && wire.TargetUsername != "") {
		return ApprovalFacts{}, ErrInvalid
	}
	issued, e := time.Parse(time.RFC3339Nano, wire.IssuedAt)
	if e != nil || issued.UTC().Format(time.RFC3339Nano) != wire.IssuedAt {
		return ApprovalFacts{}, ErrInvalid
	}
	expires, e := time.Parse(time.RFC3339Nano, wire.ExpiresAt)
	if e != nil || expires.UTC().Format(time.RFC3339Nano) != wire.ExpiresAt || !issued.Before(expires) || expires.Sub(issued) > 5*time.Minute || issued.After(now.Add(maxClockSkew)) || !now.Before(expires) {
		return ApprovalFacts{}, ErrApprovalRequired
	}
	if e = ctx.Err(); e != nil {
		return ApprovalFacts{}, e
	}
	return ApprovalFacts{TargetUsername: wire.TargetUsername, ClaimPurpose: purpose, Binding: b, TransitionMode: wire.TransitionMode, Revision: wire.ApprovalRevision, IssuedAt: issued, ExpiresAt: expires}, nil
}
