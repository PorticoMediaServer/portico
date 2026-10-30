package serverreturn

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"time"
)

// Security is deliberately not a ReturnPurpose. These signed contexts and
// one-use references can never enter the session/claim exchange.
const SecurityContextKind = "portico.security-return-prepare.v1"
const SecurityConfirmationKind = "portico.security-return-confirm.v1"

type SecuritySigned struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

// ActorBinding is an opaque server-authenticated digest of the original local
// account/profile/family/epoch and revision. Hosted stores no server profiles.
type SecurityContext struct {
	SettingsScope      string    `json:"settingsScope"`
	SettingsSection    string    `json:"settingsSection"`
	Purpose            string    `json:"purpose"`
	Target             string    `json:"target"`
	Kind               string    `json:"kind"`
	ServerID           string    `json:"serverId"`
	PublicKey          string    `json:"publicKey"`
	RegistrationID     string    `json:"registrationId"`
	Origin             string    `json:"origin"`
	HostedOrigin       string    `json:"hostedOrigin"`
	ClaimOperationID   string    `json:"claimOperationId"`
	AccountID          string    `json:"accountId"`
	ActorBinding       string    `json:"actorBinding"`
	OpeningID          string    `json:"openingId"`
	TaskID             string    `json:"taskId"`
	OperationID        string    `json:"operationId"`
	ProviderIdentityID string    `json:"providerIdentityId"`
	ProviderRevision   int64     `json:"providerRevision,string"`
	Nonce              string    `json:"nonce"`
	IssuedAt           time.Time `json:"issuedAt"`
	ExpiresAt          time.Time `json:"expiresAt"`
	AuthorityExpiresAt time.Time `json:"authorityExpiresAt"`
}

type SecurityReturnStart struct {
	Context       SecuritySigned `json:"context"`
	State         string         `json:"state"`
	CodeChallenge string         `json:"codeChallenge"`
}

type SecurityConfirmation struct {
	Kind          string    `json:"kind"`
	ContextDigest string    `json:"contextDigest"`
	TransactionID string    `json:"transactionId"`
	CodeDigest    string    `json:"codeDigest"`
	IssuedAt      time.Time `json:"issuedAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
}

type SecurityReturnConsume struct {
	Code         string         `json:"code"`
	Verifier     string         `json:"verifier"`
	Confirmation SecuritySigned `json:"confirmation"`
}

type SecurityReturnReceipt struct {
	TransactionID string `json:"transactionId"`
	OperationID   string `json:"operationId"`
	AccountID     string `json:"accountId"`
	FamilyID      string `json:"familyId"`
}

func SecurityDigest(raw []byte) string {
	h := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func securityDecode(q SecuritySigned, public []byte, out any) ([]byte, error) {
	if len(q.Payload) > 8192 || len(public) != ed25519.PublicKeySize {
		return nil, ErrInvalid
	}
	raw, e := base64.RawURLEncoding.Strict().DecodeString(q.Payload)
	sig, se := base64.RawURLEncoding.Strict().DecodeString(q.Signature)
	if e != nil || se != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(public, raw, sig) || json.Unmarshal(raw, out) != nil {
		return nil, ErrInvalid
	}
	canonical, e := json.Marshal(out)
	if e != nil || !bytes.Equal(canonical, raw) {
		return nil, ErrInvalid
	}
	return raw, nil
}

func securityTime(issued, expires, now time.Time, maximum time.Duration) bool {
	return !issued.IsZero() && !issued.After(now.Add(time.Minute)) && expires.After(now) && expires.After(issued) && expires.Sub(issued) <= maximum
}

func VerifySecurityContext(q SecuritySigned, public []byte, now time.Time) (SecurityContext, []byte, error) {
	var out SecurityContext
	raw, e := securityDecode(q, public, &out)
	key, ke := base64.RawURLEncoding.Strict().DecodeString(out.PublicKey)
	if e != nil || ke != nil || !bytes.Equal(key, public) || out.Kind != SecurityContextKind || out.SettingsScope != "account" || out.SettingsSection != "profile" || !CredentialPurpose(out.Purpose, out.Target) || !identifier.MatchString(out.ServerID) ||
		!identifier.MatchString(out.RegistrationID) || !identifier.MatchString(out.ClaimOperationID) || !Origin(out.Origin) || !Origin(out.HostedOrigin) ||
		!identifier.MatchString(out.AccountID) || !identifier.MatchString(out.ProviderIdentityID) || out.ProviderRevision < 1 ||
		!secret.MatchString(out.ActorBinding) || !identifier.MatchString(out.OpeningID) || len(out.OpeningID) < 16 ||
		!identifier.MatchString(out.TaskID) || len(out.TaskID) < 16 || !identifier.MatchString(out.OperationID) || len(out.OperationID) < 16 ||
		!secret.MatchString(out.Nonce) || out.AuthorityExpiresAt.IsZero() || out.ExpiresAt.After(out.AuthorityExpiresAt) || !securityTime(out.IssuedAt, out.ExpiresAt, now, 5*time.Minute) {
		return SecurityContext{}, nil, ErrInvalid
	}
	return out, raw, nil
}

func VerifySecurityConfirmation(q SecuritySigned, public []byte, context, transaction, code string, now time.Time) (SecurityConfirmation, error) {
	var out SecurityConfirmation
	_, e := securityDecode(q, public, &out)
	if e != nil || out.Kind != SecurityConfirmationKind || out.ContextDigest != context || out.TransactionID != transaction ||
		out.CodeDigest != SecurityDigest([]byte(code)) || !securityTime(out.IssuedAt, out.ExpiresAt, now, time.Minute) {
		return SecurityConfirmation{}, ErrInvalid
	}
	return out, nil
}

func (q SecurityReturnStart) Valid() bool {
	challenge, e := base64.RawURLEncoding.Strict().DecodeString(q.CodeChallenge)
	return secret.MatchString(q.State) && e == nil && len(challenge) == 32 && base64.RawURLEncoding.EncodeToString(challenge) == q.CodeChallenge
}

// A80 has three credential-change families. Recovery-code operations are part
// of the existing second-factor flow, not another reauthentication category.
func CredentialPurpose(purpose, target string) bool {
	switch purpose {
	case "password_set_or_change", "mfa_enroll", "mfa_disable", "recovery_codes":
		return target == ""
	case "email_change":
		return len(target) >= 3 && len(target) <= 320
	}
	return false
}
