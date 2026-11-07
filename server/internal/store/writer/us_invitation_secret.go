package writer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"slices"
)

// Exact invitation Code only. No permission/source document is encrypted here.
// The trusted parent admits crypto/key custody and verifies server derivation
// BEFORE calling, outside writer custody. This structure grants no disclosure
// authority. Nonce uniqueness and immutable key ID/custody remain parent duties.
type usInvitationProtectedCode struct {
	keyID       string
	nonce, body []byte
	manifest    [32]byte
}

func (usInvitationProtectedCode) String() string   { return "usInvitationProtectedCode{redacted}" }
func (usInvitationProtectedCode) GoString() string { return "usInvitationProtectedCode{redacted}" }
func (usInvitationProtectedCode) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationProtectedCode")
}

// AAD is independent of ciphertext and the final request digest, avoiding a
// circular wire/digest dependency. The final full request and result digest bind
// the complete wire separately. Every field below is retained in the original
// owner/header/typed invitation result, so replay never needs mutable source.
func usInvitationCodeAAD(identity CommandIdentity, owner, server, invitation, codeHash, keyID string) [32]byte {
	e := requestEncoder{h: sha256.New()}
	e.text("portico.users.invitation-code.aes256gcm.v1")
	e.number(uint64(usInvitationCreate))
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.text(owner)
	e.text(server)
	e.text(invitation)
	e.text(codeHash)
	e.text(keyID)
	var out [32]byte
	copy(out[:], e.h.Sum(nil))
	return out
}

func prepareUSInvitationCode(p *usInvitationPrepared, keyID string, key, nonce []byte, exactCode string) (usInvitationProtectedCode, error) {
	var out usInvitationProtectedCode
	if p == nil || p.kind != usInvitationCreate || !validUSInvitationScope(p.identity, p.scope) || !usIdentifier(p.id) || !usHex(p.codeHash, 64) || !usIdentifier(keyID) || len(key) != 32 || len(nonce) != 12 || len(exactCode) != 43 {
		return out, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(exactCode)
	if err != nil || len(raw) != 32 {
		return out, ErrInvalid
	}
	sum := sha256.Sum256([]byte(exactCode))
	if hex.EncodeToString(sum[:]) != p.codeHash {
		return out, ErrConflict
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return out, ErrInvalid
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return out, ErrInvalid
	}
	out.keyID = keyID
	out.nonce = slices.Clone(nonce)
	out.manifest = usInvitationCodeAAD(p.identity, p.scope.ownerID, p.scope.issuer.server, p.id, p.codeHash, keyID)
	out.body = aead.Seal(nil, out.nonce, []byte(exactCode), out.manifest[:])
	return out, nil
}

type usInvitationIssuanceRequest struct {
	invitation usInvitationPrepared
	code       usInvitationProtectedCode
}
type usInvitationIssuanceCommand struct {
	request usInvitationIssuanceRequest
	digest  RequestDigest
}

func (usInvitationIssuanceRequest) String() string   { return "usInvitationIssuanceRequest{redacted}" }
func (usInvitationIssuanceRequest) GoString() string { return "usInvitationIssuanceRequest{redacted}" }
func (usInvitationIssuanceRequest) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationIssuanceRequest")
}
func (usInvitationIssuanceCommand) String() string   { return "usInvitationIssuanceCommand{redacted}" }
func (usInvitationIssuanceCommand) GoString() string { return "usInvitationIssuanceCommand{redacted}" }
func (usInvitationIssuanceCommand) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationIssuanceCommand")
}

// Freezing binds all original prepared fields AND protected output. Validation
// is structural, not AEAD/key/source proof; the trusted parent owns those proofs.
func freezeUSInvitationIssuance(p *usInvitationPrepared, code usInvitationProtectedCode) (*usInvitationIssuanceCommand, error) {
	native, err := freezeUSInvitation(p)
	if err != nil {
		return nil, err
	}
	if p.kind != usInvitationCreate || !usIdentifier(code.keyID) || len(code.nonce) != 12 || len(code.body) != 59 || code.manifest != usInvitationCodeAAD(p.identity, p.scope.ownerID, p.scope.issuer.server, p.id, p.codeHash, code.keyID) {
		return nil, ErrInvalid
	}
	code.nonce = slices.Clone(code.nonce)
	code.body = slices.Clone(code.body)
	request := usInvitationIssuanceRequest{invitation: native.request, code: code}
	digest, err := canonicalRequestDigest(p.kind, 1, p.identity, request)
	if err != nil {
		return nil, err
	}
	return &usInvitationIssuanceCommand{request: request, digest: digest}, nil
}
