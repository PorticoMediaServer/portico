package writer

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
)

// DATA only: immutable native password post-state. No password proof or authority.
type usPasswordSavedResult struct {
	owner, account, memberIncarnation, profile, profileLifetime            string
	memberRevision, profileRevision, previousEpoch, resultEpoch, appliedMS int64
}

func validateUSPasswordResult(v usPasswordSavedResult) error {
	if !usIdentifier(v.owner) || !usIdentifier(v.account) || !usHex(v.memberIncarnation, 32) || !usIdentifier(v.profile) || !usHex(v.profileLifetime, 32) || v.memberRevision <= 0 || v.profileRevision <= 0 || v.previousEpoch <= 0 || v.previousEpoch == int64(^uint64(0)>>1) || v.resultEpoch != v.previousEpoch+1 || v.appliedMS < 0 {
		return ErrResolveRequired
	}
	return nil
}
func cloneUSPasswordResult(v usPasswordSavedResult) usPasswordSavedResult {
	v.owner = strings.Clone(v.owner)
	v.account = strings.Clone(v.account)
	v.memberIncarnation = strings.Clone(v.memberIncarnation)
	v.profile = strings.Clone(v.profile)
	v.profileLifetime = strings.Clone(v.profileLifetime)
	return v
}

// Display/source text is raw byte-length-prefixed, including invalid UTF-8.
func usNativeResultText(e *requestEncoder, s string) { e.number(uint64(len(s))); e.bytes([]byte(s)) }
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
func usPasswordResultDigest(identity CommandIdentity, request RequestDigest, v usPasswordSavedResult) (ResultDigest, error) {
	if err := validateUSPasswordResult(v); err != nil {
		return ResultDigest{}, err
	}
	e, err := usCredentialResultEncoder(identity, request, usLocalPasswordReplace, "password_replace_v1")
	if err != nil {
		return ResultDigest{}, err
	}
	for _, s := range []string{v.owner, v.account, v.memberIncarnation, v.profile, v.profileLifetime} {
		usNativeResultText(&e, s)
	}
	for _, n := range []int64{v.memberRevision, v.profileRevision, v.previousEpoch, v.resultEpoch, v.appliedMS} {
		e.number(uint64(n))
	}
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	var out ResultDigest
	copy(out[:], e.h.Sum(nil))
	return out, nil
}
func usPasswordResultVariableBytes(v usPasswordSavedResult) (int64, error) {
	if err := validateUSPasswordResult(v); err != nil {
		return 0, err
	}
	return int64(len(v.owner) + 96 + len("password_replace_v1") + len(v.owner) + 16 + len(v.account) + len(v.memberIncarnation) + len(v.profile) + len(v.profileLifetime)), nil
}
func (usPasswordSavedResult) String() string   { return "usPasswordSavedResult{redacted}" }
func (usPasswordSavedResult) GoString() string { return "usPasswordSavedResult{redacted}" }
func (usPasswordSavedResult) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usPasswordSavedResult")
}
