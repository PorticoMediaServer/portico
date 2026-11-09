package writer

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"slices"
	"strings"
)

// The COMPLETE saved native result of one top-level API-credential issue (521)
// or revoke (522), copied from the unpublished protected credential-result
// draft. It captures the immutable public DTO, the exact scope cohort and, for
// 521 only, the protected credential wire. It never holds the plaintext secret:
// key generation, AEAD and entropy happen OUTSIDE the writer turn and only the
// sealed artifact (key id, nonce, body, AAD digest) plus the one-way hash enter
// here. The plaintext first response is the caller's, never this component's.
type usCredentialSavedResult struct {
	purpose                                                                                                                                                  CommandID
	owner, id, kind, name, hint, state, endedReason, account, memberIncarnation, username, profile, profileLifetime, profileName, family, device, secretHash string
	familyIncarnation                                                                                                                                        [16]byte
	scopes                                                                                                                                                   []string
	createdMS, expiresMS, lastUsedMS, revokedMS, appliedMS                                                                                                   int64
	secret                                                                                                                                                   usCredentialResultSecret
}

// Nil means SQL NULL; present empty values remain distinct and are rejected
// where the exact native purpose requires absence or a fixed wire width. This
// is the protected (sealed) artifact, never a plaintext credential.
type usCredentialResultSecret struct {
	keyID                  *string
	nonce, body, aadDigest []byte
}

func (s usCredentialResultSecret) present() bool {
	return s.keyID != nil || s.nonce != nil || s.body != nil || s.aadDigest != nil
}

func usCredentialResultCodec(p CommandID) (string, error) {
	switch p {
	case usAPICredentialIssue:
		return "credential_issue_v1", nil
	case usAPICredentialRevoke:
		return "credential_revoke_v1", nil
	}
	return "", ErrInvalid
}

func validateUSCredentialResult(v usCredentialSavedResult) error {
	if _, err := usCredentialResultCodec(v.purpose); err != nil {
		return err
	}
	if !usIdentifier(v.owner) || !usIdentifier(v.id) || !usIdentifier(v.account) || !usHex(v.memberIncarnation, 32) || !usIdentifier(v.profile) || !usHex(v.profileLifetime, 32) || !usIdentifier(v.family) || !usIdentifier(v.device) || v.familyIncarnation == [16]byte{} || !usCredentialDigest(v.secretHash) || v.createdMS < 0 || v.expiresMS < 0 || v.lastUsedMS < 0 || v.revokedMS < 0 || v.appliedMS < 0 {
		return ErrResolveRequired
	}
	width := 0
	switch v.kind {
	case "key":
		width = 62
	case "token":
		width = 63
	case "app_password":
		width = 64
	default:
		return ErrResolveRequired
	}
	if v.kind == "app_password" && v.hint != "" || v.kind != "app_password" && len(v.hint) != 6 {
		return ErrResolveRequired
	}
	switch v.state {
	case "active", "expired", "revoked", "ended":
	default:
		return ErrResolveRequired
	}
	switch v.endedReason {
	case "", "member_disabled", "member_deleted", "profile_deleted", "permission_removed", "unavailable":
	default:
		return ErrResolveRequired
	}
	if (v.state == "ended") != (v.endedReason != "") {
		return ErrResolveRequired
	}
	if len(v.scopes) < 1 || len(v.scopes) > 4 {
		return ErrResolveRequired
	}
	seen := map[string]bool{}
	for _, s := range v.scopes {
		switch s {
		case "read", "playback", "library", "admin":
		default:
			return ErrResolveRequired
		}
		if seen[s] {
			return ErrResolveRequired
		}
		seen[s] = true
	}
	if v.purpose == usAPICredentialIssue {
		if v.state != "active" || v.revokedMS != 0 || v.lastUsedMS != 0 || v.secret.keyID == nil || len(*v.secret.keyID) < 1 || len(*v.secret.keyID) > 128 || len(v.secret.nonce) != 12 || len(v.secret.body) != width || len(v.secret.aadDigest) != 32 {
			return ErrResolveRequired
		}
	} else if v.secret.present() {
		return ErrResolveRequired
	}
	return nil
}

// Display/source text is raw byte-length-prefixed, including invalid UTF-8.
func usNativeResultText(e *requestEncoder, s string) {
	e.number(uint64(len(s)))
	e.bytes([]byte(s))
}

// Closed v1 result header shared by the credential purposes. The frozen request
// digest is the ORIGINAL identity; no later source read contributes.
func usCredentialResultEncoder(identity CommandIdentity, request RequestDigest, kind CommandID, codec string) (requestEncoder, error) {
	if identity.Incarnation == [16]byte{} || identity.OperationID == [16]byte{} || identity.Sequence <= 0 || request == (RequestDigest{}) {
		return requestEncoder{}, ErrResolveRequired
	}
	e := requestEncoder{h: sha256.New()}
	e.bytes([]byte(canonicalResultCodec))
	var header [4]byte
	binary.BigEndian.PutUint16(header[:2], uint16(kind))
	binary.BigEndian.PutUint16(header[2:], 1)
	e.bytes(header[:])
	usNativeResultText(&e, "users_security")
	usNativeResultText(&e, codec)
	usNativeResultText(&e, "applied")
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	return e, nil
}

// Structural evidence only. The parent owes outside-writer crypto verification,
// original secret/hash/manifest binding, current-key proof and disclosure grant.
// The sealed body is bound one-way; this codec defines no AEAD AAD contract and
// never reproduces a plaintext secret.
func usCredentialResultDigest(identity CommandIdentity, request RequestDigest, v usCredentialSavedResult) (ResultDigest, error) {
	if err := validateUSCredentialResult(v); err != nil {
		return ResultDigest{}, err
	}
	codec, _ := usCredentialResultCodec(v.purpose)
	e, err := usCredentialResultEncoder(identity, request, v.purpose, codec)
	if err != nil {
		return ResultDigest{}, err
	}
	for _, s := range []string{v.owner, v.id, v.kind, v.name, v.hint, v.state, v.endedReason, v.account, v.memberIncarnation, v.username, v.profile, v.profileLifetime, v.profileName, v.family, v.device, v.secretHash} {
		usNativeResultText(&e, s)
	}
	e.bytes(v.familyIncarnation[:])
	for _, n := range []int64{v.createdMS, v.expiresMS, v.lastUsedMS, v.revokedMS, v.appliedMS} {
		e.number(uint64(n))
	}
	e.flag(v.scopes != nil)
	e.number(uint64(len(v.scopes)))
	for _, s := range v.scopes {
		usNativeResultText(&e, s)
	}
	e.flag(v.secret.keyID != nil)
	if v.secret.keyID != nil {
		usNativeResultText(&e, *v.secret.keyID)
	}
	for _, b := range [][]byte{v.secret.nonce, v.secret.body, v.secret.aadDigest} {
		e.flag(b != nil)
		e.number(uint64(len(b)))
		e.bytes(b)
	}
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	var out ResultDigest
	copy(out[:], e.h.Sum(nil))
	return out, nil
}

func cloneUSCredentialResult(v usCredentialSavedResult) usCredentialSavedResult {
	v.owner = strings.Clone(v.owner)
	v.id = strings.Clone(v.id)
	v.kind = strings.Clone(v.kind)
	v.name = strings.Clone(v.name)
	v.hint = strings.Clone(v.hint)
	v.state = strings.Clone(v.state)
	v.endedReason = strings.Clone(v.endedReason)
	v.account = strings.Clone(v.account)
	v.memberIncarnation = strings.Clone(v.memberIncarnation)
	v.username = strings.Clone(v.username)
	v.profile = strings.Clone(v.profile)
	v.profileLifetime = strings.Clone(v.profileLifetime)
	v.profileName = strings.Clone(v.profileName)
	v.family = strings.Clone(v.family)
	v.device = strings.Clone(v.device)
	v.secretHash = strings.Clone(v.secretHash)
	v.scopes = slices.Clone(v.scopes)
	for i := range v.scopes {
		v.scopes[i] = strings.Clone(v.scopes[i])
	}
	if v.secret.keyID != nil {
		s := strings.Clone(*v.secret.keyID)
		v.secret.keyID = &s
	}
	v.secret.nonce = slices.Clone(v.secret.nonce)
	v.secret.body = slices.Clone(v.secret.body)
	v.secret.aadDigest = slices.Clone(v.secret.aadDigest)
	return v
}

// Exact logical variable-byte charge of the receipt header row, its typed
// credential-result child and its ordered scope children. It is exactly the sum
// of the three generated variable_bytes columns, never index/page/WAL bytes.
func usCredentialResultVariableBytes(v usCredentialSavedResult) (int64, error) {
	if err := validateUSCredentialResult(v); err != nil {
		return 0, err
	}
	codec, _ := usCredentialResultCodec(v.purpose)
	n := len(v.owner) + 96 + len(codec) + 32
	n += len(v.owner)
	n += len(v.id)
	n += len(v.kind)
	n += len(v.name)
	n += len(v.hint)
	n += len(v.state)
	n += len(v.endedReason)
	n += len(v.account)
	n += len(v.memberIncarnation)
	n += len(v.username)
	n += len(v.profile)
	n += len(v.profileLifetime)
	n += len(v.profileName)
	n += len(v.family)
	n += len(v.device)
	n += len(v.secretHash)
	if v.secret.keyID != nil {
		n += len(*v.secret.keyID)
	}
	n += len(v.secret.nonce) + len(v.secret.body) + len(v.secret.aadDigest)
	for _, s := range v.scopes {
		n += len(v.owner) + 16 + len(s)
	}
	return int64(n), nil
}

func (usCredentialSavedResult) String() string   { return "usCredentialSavedResult{redacted}" }
func (usCredentialSavedResult) GoString() string { return "usCredentialSavedResult{redacted}" }
func (usCredentialSavedResult) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usCredentialSavedResult")
}

func (usCredentialResultSecret) String() string   { return "usCredentialResultSecret{redacted}" }
func (usCredentialResultSecret) GoString() string { return "usCredentialResultSecret{redacted}" }
func (usCredentialResultSecret) Format(state fmt.State, _ rune) {
	formatUSPreparation(state, "usCredentialResultSecret")
}
