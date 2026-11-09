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

// present distinguishes the sealed create artifact from the zero value carried
// by edit/revoke invitations.
func (c usInvitationProtectedCode) present() bool {
	return c.keyID != "" || c.nonce != nil || c.body != nil
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

// prepareUSInvitationCode seals the caller-generated 43-character code after
// proving the exact code-hash binding. The plaintext code is never retained in
// the artifact, error or any formatting; only the sealed body leaves here.
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
	if len(out.body) != 43+16 {
		return usInvitationProtectedCode{}, ErrInvalid
	}
	return out, nil
}

// structural check only: AEAD opening and key custody belong to the parent.
func (c usInvitationProtectedCode) valid(p *usInvitationPrepared) bool {
	return p != nil && p.kind == usInvitationCreate && usIdentifier(c.keyID) && len(c.nonce) == 12 && len(c.body) == 43+16 &&
		c.manifest == usInvitationCodeAAD(p.identity, p.scope.ownerID, p.scope.issuer.server, p.id, p.codeHash, c.keyID)
}
