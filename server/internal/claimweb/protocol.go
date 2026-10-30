// Package claimweb is the signed, short-lived handoff from a locally authorized
// server setup to the Hosted web account. It carries no account credentials.
package claimweb

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
	"regexp"
	"time"
)

type Request struct {
	Kind        string `json:"kind"`
	Audience    string `json:"audience"`
	OperationID string `json:"operationId"`
	ServerID    string `json:"serverId"`
	// AccountID is empty for a web claim: the approver's account is bound at
	// approval. A non-empty value restricts approval to that account.
	AccountID             string    `json:"accountId"`
	PublicKey             string    `json:"publicKey"`
	LocalGeneration       int64     `json:"localGeneration,string"`
	Name                  string    `json:"name"`
	Address               string    `json:"address"`
	Purpose               string    `json:"purpose"`
	CurrentOwnerAccountID string    `json:"currentOwnerAccountId"`
	PreviousOwnerRole     string    `json:"previousOwnerRole"`
	ReturnRegistrationID  string    `json:"returnRegistrationId"`
	IssuedAt              time.Time `json:"issuedAt"`
	ExpiresAt             time.Time `json:"expiresAt"`
}
type Signed struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}
type Pending struct {
	Purpose              string                     `json:"purpose"`
	CurrentOwnerUsername string                     `json:"currentOwnerUsername,omitempty"`
	PreviousOwnerRole    string                     `json:"previousOwnerRole,omitempty"`
	ReturnRegistration   *serverreturn.Registration `json:"returnRegistration,omitempty"`
	Code                 string                     `json:"code"`
	ServerID             string                     `json:"serverId"`
	Name                 string                     `json:"name"`
	Address              string                     `json:"address"`
	RequestedAt          time.Time                  `json:"requestedAt"`
	ExpiresAt            time.Time                  `json:"expiresAt"`
	Status               string                     `json:"status"`
	SetupTransactionID   string                     `json:"setupTransactionId,omitempty"`
	OwnerTransactionID   string                     `json:"ownerTransactionId,omitempty"`
}

var ErrInvalid = errors.New("invalid pending claim")
var id = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func Verify(s Signed, audience string, now time.Time) (Request, error) {
	var q Request
	raw, e := base64.RawURLEncoding.Strict().DecodeString(s.Payload)
	if e != nil || len(raw) > 8192 || json.Unmarshal(raw, &q) != nil {
		return q, ErrInvalid
	}
	key, e := base64.RawURLEncoding.Strict().DecodeString(q.PublicKey)
	if e != nil || len(key) != 32 {
		return q, ErrInvalid
	}
	// The server id is the domain-separated digest of the key (networking /
	// claims ServerIdentity): SHA-256("portico.server.identity.v1\x00" || key).
	digest := sha256.New()
	_, _ = digest.Write([]byte("portico.server.identity.v1\x00"))
	_, _ = digest.Write(key)
	sum := digest.Sum(nil)
	sig, e := base64.RawURLEncoding.Strict().DecodeString(s.Signature)
	u, ue := url.Parse(q.Address)
	if e != nil || !ed25519.Verify(key, raw, sig) || q.Kind != "portico.claim.web.v2" || q.Audience != audience || q.ServerID != "srv_"+base64.RawURLEncoding.EncodeToString(sum) || !id.MatchString(q.OperationID) || (q.AccountID != "" && !id.MatchString(q.AccountID)) || !validPurposeBinding(q.Purpose, q.CurrentOwnerAccountID, q.PreviousOwnerRole, q.ReturnRegistrationID) || q.LocalGeneration < 0 || len(q.Name) < 1 || len(q.Name) > 120 || ue != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || q.IssuedAt.After(now.Add(time.Minute)) || !q.ExpiresAt.After(now) || q.ExpiresAt.Sub(q.IssuedAt) > RequestLifetime(q.Purpose) || !q.ExpiresAt.After(q.IssuedAt) {
		return q, ErrInvalid
	}
	return q, nil
}

func validPurposeBinding(p, current, previous, registration string) bool {
	if registration != "" && !id.MatchString(registration) {
		return false
	}
	switch p {
	case "setup":
		return current == "" && previous == ""
	case "switch":
		return (current == "" || id.MatchString(current)) && previous == ""
	case "change-owner":
		return id.MatchString(current) && (previous == "admin" || previous == "member" || previous == "remove")
	}
	return false
}
