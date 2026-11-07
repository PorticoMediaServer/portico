package writer

import "crypto/sha256"

// Complete private PIN primitive output. The public profile-edit adapter still
// owes its complete parent response image. Neither PIN nor hash leaves here.
type usPINSavedResult struct {
	kind                                      CommandID
	owner, account, member, profile, lifetime string
	revision, pinRevision, appliedMS          int64
	pinRequired                               bool
}

func usPINCodec(kind CommandID) (string, error) {
	switch kind {
	case usPINSet:
		return "pin_set_v1", nil
	case usPINClear:
		return "pin_clear_v1", nil
	default:
		return "", ErrInvalid
	}
}
func validateUSPINSavedResult(v usPINSavedResult) error {
	if _, err := usPINCodec(v.kind); err != nil {
		return err
	}
	if !usIdentifier(v.owner) || !usIdentifier(v.account) || !usHex(v.member, 32) || !usIdentifier(v.profile) || !usHex(v.lifetime, 32) || v.revision < 2 || v.revision > usMaxRevision || v.pinRevision < 2 || v.pinRevision > usMaxRevision || v.appliedMS < 0 || v.pinRequired != (v.kind == usPINSet) {
		return ErrResolveRequired
	}
	return nil
}
func usPINResultDigest(identity CommandIdentity, request RequestDigest, v usPINSavedResult) (ResultDigest, error) {
	if identity.Incarnation == [16]byte{} || identity.OperationID == [16]byte{} || identity.Sequence <= 0 || request == (RequestDigest{}) {
		return ResultDigest{}, ErrResolveRequired
	}
	if err := validateUSPINSavedResult(v); err != nil {
		return ResultDigest{}, err
	}
	codec, _ := usPINCodec(v.kind)
	e := requestEncoder{h: sha256.New()}
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("users_security")
	e.text(codec)
	e.text("applied")
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	e.text(v.owner)
	e.text(v.account)
	e.text(v.member)
	e.text(v.profile)
	e.text(v.lifetime)
	e.number(uint64(v.revision))
	e.number(uint64(v.pinRevision))
	e.flag(v.pinRequired)
	e.flag(true)
	e.number(uint64(v.appliedMS))
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	var out ResultDigest
	copy(out[:], e.h.Sum(nil))
	return out, nil
}
func usPINResultVariableBytes(v usPINSavedResult) (int64, error) {
	if err := validateUSPINSavedResult(v); err != nil {
		return 0, err
	}
	codec, _ := usPINCodec(v.kind)
	return int64(len(v.owner) + 96 + len(codec) + len(v.account) + len(v.member) + len(v.profile) + len(v.lifetime)), nil
}
