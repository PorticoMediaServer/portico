package networking

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Purpose string

const (
	FinalizePurpose Purpose = "claim-finalize"
	ResultPurpose   Purpose = "claim-result"
)
const nonceLifetime = 30 * time.Second

// Timing tolerance is explicit review input, not a way to extend expiry.
const maxClockSkew = 5 * time.Second

type SignedProof struct {
	Payload   []byte
	Signature []byte
}

func (p SignedProof) String() string   { return "[redacted claim proof]" }
func (p SignedProof) GoString() string { return p.String() }

type Challenge struct {
	RequestDigest string
	NonceID       string
	Nonce         []byte
	IssuedAt      time.Time
	ExpiresAt     time.Time
}

// Fixed order mirrors the checked Hosted claim Wire. Only the locally reserved
// approved identity is included in finalization; result retrieval grants none.
type claimWire struct {
	Kind                   string  `json:"kind"`
	Version                string  `json:"version"`
	Audience               string  `json:"audience"`
	Purpose                Purpose `json:"purpose"`
	OperationID            string  `json:"operationId"`
	ServerID               string  `json:"serverId"`
	AccountID              string  `json:"accountId"`
	PublicKey              string  `json:"publicKey"`
	LocalGeneration        int64   `json:"localGeneration,string"`
	ApprovalRevision       int64   `json:"approvalRevision,string"`
	RequestID              string  `json:"requestId"`
	NonceID                string  `json:"nonceId,omitempty"`
	Nonce                  string  `json:"nonce,omitempty"`
	IssuedAt               string  `json:"issuedAt"`
	ExpiresAt              string  `json:"expiresAt"`
	ReservationAccountID   string  `json:"reservationAccountId,omitempty"`
	ReservationUsername    string  `json:"reservationUsername,omitempty"`
	ReservationGeneration  string  `json:"reservationGeneration,omitempty"`
	ReservationOperationID string  `json:"reservationOperationId,omitempty"`
}
type cancelWire struct {
	Kind            string `json:"kind"`
	Version         string `json:"version"`
	Audience        string `json:"audience"`
	OperationID     string `json:"operationId"`
	ServerID        string `json:"serverId"`
	AccountID       string `json:"accountId"`
	PublicKey       string `json:"publicKey"`
	LocalGeneration int64  `json:"localGeneration,string"`
	RequestID       string `json:"requestId"`
	Mode            string `json:"mode"`
	ClaimGeneration string `json:"claimGeneration,omitempty"`
}

func newRequestID() (string, error) {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
func (c *Coordinator) proof(ctx context.Context, v Intent, purpose Purpose) (SignedProof, error) {
	if purpose != FinalizePurpose && purpose != ResultPurpose {
		return SignedProof{}, ErrInvalid
	}
	if e := c.approved(v); e != nil {
		return SignedProof{}, e
	}
	if e := c.store.Current(ctx, v); e != nil {
		return SignedProof{}, e
	}
	requestID, e := newRequestID()
	if e != nil {
		return SignedProof{}, e
	}
	now := c.now().UTC()
	expiry := now.Add(nonceLifetime)
	if v.ApprovalExpiresAt.Before(expiry) {
		expiry = v.ApprovalExpiresAt
	}
	w := claimWire{Kind: "portico.claim.nonce-request", Version: "1", Audience: c.audience, Purpose: purpose, OperationID: v.OperationID, ServerID: v.ServerID, AccountID: v.AccountID, PublicKey: base64.RawURLEncoding.EncodeToString(v.PublicKey), LocalGeneration: v.LocalGeneration, ApprovalRevision: v.ApprovalRevision, RequestID: requestID, IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: expiry.UTC().Format(time.RFC3339Nano)}
	if purpose == FinalizePurpose && (v.Purpose == "switch" || v.Purpose == "change-owner") {
		if !claimUsername.MatchString(v.ReservationUsername) {
			return SignedProof{}, ErrStale
		}
		w.ReservationAccountID = v.AccountID
		w.ReservationUsername = v.ReservationUsername
		w.ReservationGeneration = strconv.FormatInt(v.LocalGeneration, 10)
		w.ReservationOperationID = v.OperationID
	}
	raw, e := json.Marshal(w)
	if e != nil {
		return SignedProof{}, e
	}
	request, e := c.sign(ctx, v.Binding, raw)
	if e != nil {
		return SignedProof{}, e
	}
	challenge, e := c.transport.Challenge(ctx, v, purpose, request)
	if e != nil {
		return SignedProof{}, safeRemote(e)
	}
	if e = ctx.Err(); e != nil {
		return SignedProof{}, e
	}
	digest := sha256.Sum256(request.Payload)
	now = c.now().UTC()
	if challenge.RequestDigest != base64.RawURLEncoding.EncodeToString(digest[:]) || !validID(challenge.NonceID) || len(challenge.Nonce) != 32 || !now.Before(challenge.ExpiresAt) || !challenge.IssuedAt.Before(challenge.ExpiresAt) || challenge.ExpiresAt.Sub(challenge.IssuedAt) > nonceLifetime || challenge.IssuedAt.After(now.Add(maxClockSkew)) || challenge.ExpiresAt.After(expiry) {
		return SignedProof{}, ErrInvalid
	}
	if e = c.approved(v); e != nil {
		return SignedProof{}, e
	}
	if e = c.store.Current(ctx, v); e != nil {
		return SignedProof{}, e
	}
	w.Kind = "portico.claim.proof"
	w.NonceID = challenge.NonceID
	w.Nonce = base64.RawURLEncoding.EncodeToString(challenge.Nonce)
	w.IssuedAt = challenge.IssuedAt.UTC().Format(time.RFC3339Nano)
	w.ExpiresAt = challenge.ExpiresAt.UTC().Format(time.RFC3339Nano)
	raw, e = json.Marshal(w)
	if e != nil {
		return SignedProof{}, e
	}
	proof, e := c.sign(ctx, v.Binding, raw)
	if e != nil {
		return SignedProof{}, e
	}
	if e = c.store.Current(ctx, v); e != nil {
		return SignedProof{}, e
	}
	if !c.now().Before(challenge.ExpiresAt) {
		return SignedProof{}, ErrInvalid
	}
	return proof, nil
}
func (c *Coordinator) sign(ctx context.Context, b Binding, raw []byte) (SignedProof, error) {
	if e := ctx.Err(); e != nil {
		return SignedProof{}, e
	}
	if !validBinding(b) || len(raw) > 16384 {
		return SignedProof{}, ErrInvalid
	}
	signature, e := c.signer.Sign(ctx, b, append([]byte(nil), raw...))
	if e != nil {
		return SignedProof{}, e
	}
	if e = ctx.Err(); e != nil {
		return SignedProof{}, e
	}
	if !ed25519.Verify(ed25519.PublicKey(b.PublicKey), raw, signature) {
		return SignedProof{}, ErrInvalid
	}
	return SignedProof{Payload: raw, Signature: append([]byte(nil), signature...)}, nil
}
func (c *Coordinator) cancellation(ctx context.Context, v Intent) (Cancellation, error) {
	id, e := newRequestID()
	if e != nil {
		return Cancellation{}, e
	}
	mode := "intent"
	if v.ClaimGeneration != "" {
		mode = "committed"
	}
	w := cancelWire{Kind: "portico.claim.cancel", Version: "1", Audience: c.audience, OperationID: v.OperationID, ServerID: v.ServerID, AccountID: v.AccountID, PublicKey: base64.RawURLEncoding.EncodeToString(v.PublicKey), LocalGeneration: v.LocalGeneration, RequestID: id, Mode: mode, ClaimGeneration: v.ClaimGeneration}
	raw, e := json.Marshal(w)
	if e != nil {
		return Cancellation{}, e
	}
	proof, e := c.sign(ctx, v.Binding, raw)
	if e != nil {
		return Cancellation{}, e
	}
	return Cancellation{Binding: v.Binding, RequestID: id, ClaimGeneration: v.ClaimGeneration, Proof: proof}, nil
}
func validateCancellation(audience string, item Cancellation) error {
	if !validBinding(item.Binding) || !validID(item.RequestID) || len(item.Proof.Payload) > 16384 {
		return ErrInvalid
	}
	var w cancelWire
	if json.Unmarshal(item.Proof.Payload, &w) != nil {
		return ErrInvalid
	}
	raw, e := json.Marshal(w)
	if e != nil || !bytes.Equal(raw, item.Proof.Payload) {
		return ErrInvalid
	}
	mode := "intent"
	if item.ClaimGeneration != "" {
		if !validGeneration(item.ClaimGeneration) {
			return ErrInvalid
		}
		mode = "committed"
	}
	if w.Kind != "portico.claim.cancel" || w.Version != "1" || w.Audience != audience || w.OperationID != item.OperationID || w.ServerID != item.ServerID || w.AccountID != item.AccountID || w.PublicKey != base64.RawURLEncoding.EncodeToString(item.PublicKey) || w.LocalGeneration != item.LocalGeneration || w.RequestID != item.RequestID || w.Mode != mode || w.ClaimGeneration != item.ClaimGeneration || !ed25519.Verify(ed25519.PublicKey(item.PublicKey), raw, item.Proof.Signature) {
		return ErrInvalid
	}
	return nil
}
func validID(v string) bool {
	if len(v) < 1 || len(v) > 128 {
		return false
	}
	for _, b := range []byte(v) {
		if !((b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-') {
			return false
		}
	}
	return true
}
func validGeneration(v string) bool {
	n, e := strconv.ParseInt(v, 10, 64)
	return e == nil && n > 0 && strconv.FormatInt(n, 10) == v
}
func validAudience(v string) bool {
	if len(v) > 2048 || strings.ContainsAny(v, "\r\n\t ") {
		return false
	}
	u, e := url.Parse(v)
	return e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawFragment == "" && u.Opaque == "" && strings.ToLower(u.Host) == u.Host && u.String() == v
}
func validBinding(b Binding) bool {
	return validID(b.OperationID) && validID(b.ServerID) && validID(b.AccountID) && len(b.PublicKey) == ed25519.PublicKeySize && b.LocalGeneration >= 0
}
func sameBinding(a, b Binding) bool {
	return a.OperationID == b.OperationID && a.ServerID == b.ServerID && a.AccountID == b.AccountID && a.LocalGeneration == b.LocalGeneration && bytes.Equal(a.PublicKey, b.PublicKey)
}
func validateIntent(v Intent, operation string) error {
	if !validBinding(v.Binding) || v.OperationID != operation || v.Revision < 1 || v.ApprovalRevision < 0 {
		return ErrInvalid
	}
	switch v.Stage {
	case Prepared, Approved, Finalizing, Retrieving, Installed, CancelPending, Cancelled:
	default:
		return ErrInvalid
	}
	if (v.ClaimGeneration == "") != (v.CredentialGeneration == "") {
		return ErrInvalid
	}
	if v.ClaimGeneration != "" && (!validGeneration(v.ClaimGeneration) || !validGeneration(v.CredentialGeneration)) {
		return ErrInvalid
	}
	if (v.Stage == Retrieving || v.Stage == Installed) && v.ClaimGeneration == "" {
		return ErrInvalid
	}
	if v.InstallationAcknowledged && v.Stage != Installed && v.Stage != CancelPending && v.Stage != Cancelled {
		return ErrInvalid
	}
	return nil
}
func validateCommit(v Intent, r Commit) error {
	if r.OperationID != v.OperationID || r.ServerID != v.ServerID || !validGeneration(r.ClaimGeneration) || !validGeneration(r.CredentialGeneration) {
		return ErrInvalid
	}
	if v.ClaimGeneration != "" && (v.ClaimGeneration != r.ClaimGeneration || v.CredentialGeneration != r.CredentialGeneration) {
		return ErrStale
	}
	return nil
}
