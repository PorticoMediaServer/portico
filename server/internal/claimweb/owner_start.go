package claimweb

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
)

const OwnerStartKind = "portico.owner-start.v1"

// OwnerStartKindV2 adds the signed closed transition mode. ClaimHash binds the
// original claim; the mode is covered by the start signature. v1 is ordinary switch.
const OwnerStartKindV2 = "portico.owner-start.v2"
const OwnerStartLifetime = 5 * time.Minute

var ErrOwnerStart = errors.New("invalid owner start")
var ownerStartSecret = regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`)

// OwnerStart binds a browser's private pending state before an owner switch.
// Its declaration order is the signed wire order. S256 is fixed by this kind.
// It grants no account, claim, installation, registration or session authority.
type OwnerStart struct {
	Kind            string    `json:"kind"`
	Audience        string    `json:"audience"`
	ClaimHash       string    `json:"claimHash"`
	OperationID     string    `json:"operationId"`
	ServerID        string    `json:"serverId"`
	PublicKey       string    `json:"publicKey"`
	LocalGeneration int64     `json:"localGeneration,string"`
	Address         string    `json:"address"`
	Purpose         string    `json:"purpose"`
	State           string    `json:"state"`
	CodeChallenge   string    `json:"codeChallenge"`
	InstallationID  string    `json:"installationId"`
	ServerChallenge string    `json:"serverChallenge"`
	IssuedAt        time.Time `json:"issuedAt"`
	ExpiresAt       time.Time `json:"expiresAt"`
	// TransitionMode is the signed closed mode on a v2 start, empty on v1.
	TransitionMode string `json:"transitionMode,omitempty"`
}

func (OwnerStart) String() string   { return "[owner start redacted]" }
func (OwnerStart) GoString() string { return "[owner start redacted]" }

// VerifyOwnerStart verifies both complete original envelopes without rewriting
// the claim or accepting a digest as its replacement. This pure protocol check
// cannot establish the live owner/family/reservation, the stored challenge's
// expiry, a verified route, or a coherent cross-database installation. Runtime
// consumers must enforce those authorities in their owning transactions.
func VerifyOwnerStart(original, signed Signed, audience string, now time.Time) (Request, OwnerStart, error) {
	var claim Request
	var start OwnerStart
	fail := func() (Request, OwnerStart, error) { return Request{}, OwnerStart{}, ErrOwnerStart }
	claimRaw, e := ownerStartBase64(original.Payload, -1)
	if e != nil || ownerStartJSON(claimRaw, &claim) != nil {
		return fail()
	}
	key, e := ownerStartBase64(claim.PublicKey, ed25519.PublicKeySize)
	if e != nil {
		return fail()
	}
	if _, e = ownerStartBase64(original.Signature, ed25519.SignatureSize); e != nil {
		return fail()
	}
	// Keep the existing claim's signature, derived server identity, audience,
	// purpose and time checks. The stricter canonical wrapper is owner-only.
	if _, e = Verify(original, audience, now); e != nil || claim.Purpose != "switch" {
		return fail()
	}
	startRaw, e := ownerStartBase64(signed.Payload, -1)
	if e != nil || ownerStartJSON(startRaw, &start) != nil {
		return fail()
	}
	signature, e := ownerStartBase64(signed.Signature, ed25519.SignatureSize)
	if e != nil || !ed25519.Verify(key, startRaw, signature) {
		return fail()
	}
	hash, e := ownerStartBase64(start.ClaimHash, sha256.Size)
	digest := sha256.Sum256(claimRaw)
	if e != nil || subtle.ConstantTimeCompare(hash, digest[:]) != 1 {
		return fail()
	}
	if _, e = ownerStartBase64(start.PublicKey, ed25519.PublicKeySize); e != nil {
		return fail()
	}
	if _, e = ownerStartBase64(start.CodeChallenge, sha256.Size); e != nil {
		return fail()
	}
	if start.Kind != OwnerStartKind && start.Kind != OwnerStartKindV2 {
		return fail()
	}
	if start.Kind == OwnerStartKind && start.TransitionMode != "" || start.Kind == OwnerStartKindV2 && start.TransitionMode != "switch" && start.TransitionMode != "start-over" {
		return fail()
	}
	if start.Audience != audience || start.Audience != claim.Audience ||
		start.OperationID != claim.OperationID || start.ServerID != claim.ServerID || start.PublicKey != claim.PublicKey ||
		start.LocalGeneration != claim.LocalGeneration || start.Address != claim.Address || !ownerStartAddress(start.Address) ||
		start.Purpose != claim.Purpose || !ownerStartSecret.MatchString(start.State) ||
		!ownerStartSecret.MatchString(start.InstallationID) || !ownerStartSecret.MatchString(start.ServerChallenge) ||
		start.IssuedAt.IsZero() || start.IssuedAt.Before(claim.IssuedAt) || start.IssuedAt.After(now.Add(time.Minute)) ||
		!start.ExpiresAt.After(now) || !start.ExpiresAt.After(start.IssuedAt) ||
		start.ExpiresAt.Sub(start.IssuedAt) > OwnerStartLifetime || start.ExpiresAt.After(claim.ExpiresAt) {
		return fail()
	}
	return claim, start, nil
}

// The owner browser may start on the server's secure address or on literal
// localhost (ruling17.5). This is syntax only: it cannot attest a secure browser,
// protected storage or a trusted native channel. Registered returns keep their
// existing HTTPS-only Origin predicate.
func ownerStartAddress(value string) bool {
	u, e := url.Parse(value)
	if e != nil {
		return false
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return false
		}
	}
	if serverreturn.Origin(value) {
		return true
	}
	return strings.HasPrefix(value, "http://") && u.Hostname() == "localhost" &&
		serverreturn.Origin("https://"+strings.TrimPrefix(value, "http://"))
}

func ownerStartBase64(value string, size int) ([]byte, error) {
	// A payload is bounded before allocation; fixed fields are smaller still.
	if len(value) == 0 || len(value) > base64.RawURLEncoding.EncodedLen(8192) {
		return nil, ErrOwnerStart
	}
	raw, e := base64.RawURLEncoding.Strict().DecodeString(value)
	// Strict checks padding bits but deliberately ignores CR/LF. Exact encoding
	// equality also excludes those and any alternate representation.
	if e != nil || len(raw) > 8192 || (size >= 0 && len(raw) != size) || base64.RawURLEncoding.EncodeToString(raw) != value {
		return nil, ErrOwnerStart
	}
	return raw, nil
}

func ownerStartJSON(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrOwnerStart
	}
	// Normalize times to UTC in the typed value before comparing exact bytes.
	// Re-encoding also rejects duplicates, missing fields, alternate field order,
	// whitespace, escapes and noncanonical decimal-string generations.
	switch value := target.(type) {
	case *Request:
		value.IssuedAt, value.ExpiresAt = value.IssuedAt.UTC(), value.ExpiresAt.UTC()
	case *OwnerStart:
		value.IssuedAt, value.ExpiresAt = value.IssuedAt.UTC(), value.ExpiresAt.UTC()
	}
	canonical, e := json.Marshal(target)
	if e != nil || !bytes.Equal(canonical, raw) {
		return ErrOwnerStart
	}
	return nil
}
