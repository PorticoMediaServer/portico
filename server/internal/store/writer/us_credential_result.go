package writer

import (
	"fmt"
	"slices"
	"strings"
)

// Complete captured Credential (also AppPassword projection), native lifetime
// facts and exact per-purpose protected wire. No current clock/source lookup.
type usCredentialSavedResult struct {
	purpose                                                                                                                                                  CommandID
	owner, id, kind, name, hint, state, endedReason, account, memberIncarnation, username, profile, profileLifetime, profileName, family, device, secretHash string
	familyIncarnation                                                                                                                                        [16]byte
	scopes                                                                                                                                                   []string
	createdMS, expiresMS, lastUsedMS, revokedMS, appliedMS                                                                                                   int64
	secret                                                                                                                                                   usCredentialResultSecret
}

// Nil means SQL NULL; present empty values remain distinct and are rejected
// where the exact native purpose requires absence or a fixed wire width.
type usCredentialResultSecret struct {
	keyID                  *string
	nonce, body, aadDigest []byte
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
	} else if v.secret.keyID != nil || v.secret.nonce != nil || v.secret.body != nil || v.secret.aadDigest != nil {
		return ErrResolveRequired
	}
	return nil
}

// Structural evidence only. Parent owes outside-writer crypto verification,
// original secret/hash/manifest binding, current-key proof and disclosure grant.
// This codec binds saved AAD bytes; it defines no AEAD AAD contract.
func usCredentialResultDigest(identity CommandIdentity, request RequestDigest, v usCredentialSavedResult) (ResultDigest, error) {
	if err := validateUSCredentialResult(v); err != nil {
		return ResultDigest{}, err
	}
	codec, _ := usCredentialResultCodec(v.purpose)
	e, err := usCredentialResultEncoder(identity, request, v.purpose, codec)
	if err != nil {
		return ResultDigest{}, err
	}
	usNativeResultText(&e, v.owner)
	usNativeResultText(&e, v.id)
	usNativeResultText(&e, v.kind)
	usNativeResultText(&e, v.name)
	usNativeResultText(&e, v.hint)
	usNativeResultText(&e, v.state)
	usNativeResultText(&e, v.endedReason)
	usNativeResultText(&e, v.account)
	usNativeResultText(&e, v.memberIncarnation)
	usNativeResultText(&e, v.username)
	usNativeResultText(&e, v.profile)
	usNativeResultText(&e, v.profileLifetime)
	usNativeResultText(&e, v.profileName)
	usNativeResultText(&e, v.family)
	usNativeResultText(&e, v.device)
	usNativeResultText(&e, v.secretHash)
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
func (usCredentialSavedResult) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usCredentialSavedResult")
}
func (usCredentialResultSecret) String() string   { return "usCredentialResultSecret{redacted}" }
func (usCredentialResultSecret) GoString() string { return "usCredentialResultSecret{redacted}" }
func (usCredentialResultSecret) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usCredentialResultSecret")
}
