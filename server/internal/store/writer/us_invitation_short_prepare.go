package writer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
)

// Comparison witnesses only. Parent owes current native issuer/tier admission,
// the exact joined staged server/key/incarnation/reset-generation and kind/revision
// comparisons, and trusted transaction-clock pending/expiry checks. Neither this
// preparation nor possession of its protected evidence grants delivery permission.
// Public retries MUST omit ShortCode, even when private evidence is available.
// Root owns the concrete 553 result schema and adoption/composition packet.
type usInvitationShortRequest struct {
	identity                                                 CommandIdentity
	scope                                                    usInvitationScope
	invitationID, state, role, codeHash                      string
	expectedRevision, invitationRevision, invitationExpiryMS int64
	serverID                                                 string
	publicKey                                                []byte
	keyIncarnation                                           string
	resetGeneration                                          int64
	signInKind                                               string
	kindRevision                                             int64
	issuedMS                                                 int64
}

// Purpose-specific private evidence: AES-256-GCM of ONLY the exact public
// four-group 19-byte display code. Key ID is immutable; AAD is the complete
// reconstructible native result manifest including source hashes and frozen clocks.
type usInvitationShortWire struct {
	keyID            string
	nonce, body, aad []byte
}
type usInvitationShortPayload struct {
	original                usInvitationShortRequest
	expiresMS               int64
	lookupHash, bindingHash string // native lowercase HEX64, not identityDigest43
	wire                    usInvitationShortWire
}
type usInvitationShortCommand struct {
	request usInvitationShortPayload
	digest  RequestDigest
}

// Call strictly outside writer with fresh independent 80-bit entropy and a
// fresh nonce under the protection key. Retry retains this command unchanged;
// it must never regenerate entropy, nonce, issuedMS or expiry on uncertain COMMIT.
// Source durable invitation keys are exactly 32 bytes; no invented upper cap.
// Missing identity_state is Portico with COALESCE revision zero in source;
// that comparison grants no authority. A Local row requires positive revision.
func prepareUSInvitationShort(in usInvitationShortRequest, entropy, invitationKey, protectionKey, nonce []byte, keyID string) (*usInvitationShortCommand, error) {
	if !validUSInvitationScope(in.identity, in.scope) || !usIdentifier(in.invitationID) || !usHex(in.codeHash, 64) || in.state != "pending" || (in.role != "member" && in.role != "admin") || in.expectedRevision < 1 || in.expectedRevision >= usMaxRevision || in.invitationRevision != in.expectedRevision || in.issuedMS <= 0 || in.issuedMS > math.MaxInt64-600000 || in.invitationExpiryMS <= in.issuedMS || len(in.publicKey) != 32 || !usIdentifier(in.keyIncarnation) || in.resetGeneration < 0 || (in.signInKind != "local" && in.signInKind != "portico") || in.kindRevision < 0 || (in.signInKind == "local" && in.kindRevision == 0) || len(entropy) != 10 || len(invitationKey) != 32 || len(protectionKey) != 32 || len(nonce) != 12 || !usIdentifier(keyID) {
		return nil, invalid("us_invitation_short")
	}
	h := sha256.New()
	h.Write([]byte("portico.server.identity.v1\x00"))
	h.Write(in.publicKey)
	if in.serverID != "srv_"+base64.RawURLEncoding.EncodeToString(h.Sum(nil)) {
		return nil, invalid("us_invitation_short_server")
	}
	in.publicKey = slices.Clone(in.publicKey)
	code := base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding).EncodeToString(entropy)
	expires := min(in.issuedMS+600000, in.invitationExpiryMS)
	mac := func(domain string, tuple ...any) string {
		raw, _ := json.Marshal(tuple)
		h := hmac.New(sha256.New, invitationKey)
		h.Write([]byte("portico-invitation-short-v1\x00" + domain + "\x00"))
		h.Write(raw)
		return hex.EncodeToString(h.Sum(nil))
	}
	p := usInvitationShortPayload{original: in, expiresMS: expires,
		lookupHash:  mac("lookup", in.serverID, in.signInKind, "accept", code),
		bindingHash: mac("binding", in.serverID, in.invitationID, in.invitationRevision, in.signInKind, in.kindRevision, "accept", expires, code)}
	// Reconstructible exclusively from the exact 553 native result, header
	// identity/owner and separately retained original issuerServer. No transient
	// issuer comparisons, request digest or protected body enter this manifest.
	manifest := usInvitationShortAAD{
		purpose: "portico-us-invitation-short-display-aad-v1", ownerID: in.scope.ownerID,
		issuerServer: in.scope.issuer.server, invitationID: in.invitationID,
		revision: in.invitationRevision, codeHash: in.codeHash, role: in.role,
		invitationExpiryMS: in.invitationExpiryMS, serverID: in.serverID,
		publicKey: in.publicKey, keyIncarnation: in.keyIncarnation,
		resetGeneration: in.resetGeneration, signInKind: in.signInKind,
		kindRevision: in.kindRevision, issuedMS: in.issuedMS, expiresMS: expires,
		lookupHash: p.lookupHash, bindingHash: p.bindingHash, keyID: keyID,
	}
	aad, err := canonicalRequestDigest(usInvitationShortIssue, 1, in.identity, manifest)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(protectionKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	display := code[:4] + "-" + code[4:8] + "-" + code[8:12] + "-" + code[12:]
	p.wire = usInvitationShortWire{keyID: keyID, nonce: slices.Clone(nonce), aad: slices.Clone(aad[:])}
	p.wire.body = gcm.Seal(nil, nonce, []byte(display), p.wire.aad)
	digest, err := canonicalRequestDigest(usInvitationShortIssue, 1, in.identity, p)
	if err != nil {
		return nil, err
	}
	return &usInvitationShortCommand{request: p, digest: digest}, nil
}
func (c *usInvitationShortCommand) checkedDigest() (RequestDigest, error) {
	if c == nil {
		return RequestDigest{}, invalid("us_invitation_short_nil")
	}
	d, e := canonicalRequestDigest(usInvitationShortIssue, 1, c.request.original.identity, c.request)
	if e != nil {
		return RequestDigest{}, e
	}
	if d != c.digest {
		return RequestDigest{}, invalid("us_invitation_short_changed")
	}
	return d, nil
}
func (usInvitationShortRequest) String() string   { return "usInvitationShortRequest{redacted}" }
func (usInvitationShortRequest) GoString() string { return "usInvitationShortRequest{redacted}" }
func (usInvitationShortRequest) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationShortRequest")
}
func (usInvitationShortWire) String() string   { return "usInvitationShortWire{redacted}" }
func (usInvitationShortWire) GoString() string { return "usInvitationShortWire{redacted}" }
func (usInvitationShortWire) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationShortWire")
}
func (usInvitationShortPayload) String() string   { return "usInvitationShortPayload{redacted}" }
func (usInvitationShortPayload) GoString() string { return "usInvitationShortPayload{redacted}" }
func (usInvitationShortPayload) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationShortPayload")
}
func (usInvitationShortCommand) String() string   { return "usInvitationShortCommand{redacted}" }
func (usInvitationShortCommand) GoString() string { return "usInvitationShortCommand{redacted}" }
func (usInvitationShortCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationShortCommand")
}

// Field order and types are the v1 AAD codec contract; keep reconstruction exact.
type usInvitationShortAAD struct {
	purpose, ownerID, issuerServer, invitationID string
	revision                                     int64
	codeHash, role                               string
	invitationExpiryMS                           int64
	serverID                                     string
	publicKey                                    []byte
	keyIncarnation                               string
	resetGeneration                              int64
	signInKind                                   string
	kindRevision, issuedMS, expiresMS            int64
	lookupHash, bindingHash, keyID               string
}

func (usInvitationShortAAD) String() string   { return "usInvitationShortAAD{redacted}" }
func (usInvitationShortAAD) GoString() string { return "usInvitationShortAAD{redacted}" }
func (usInvitationShortAAD) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationShortAAD")
}
