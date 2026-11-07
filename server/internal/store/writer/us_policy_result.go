package writer

import "crypto/sha256"

// The COMPLETE private policy-primitive result, reconstructed only from its
// immutable receipt/cohort. Public profile-edit adapters still owe their whole
// parent response image; this is not a partial DirectSnapshot replacement.
type usPolicySavedResult struct {
	kind                                                 CommandID
	ownerID                                              string
	account, memberIncarnation, profile, profileLifetime string
	revision, appliedMS                                  int64
	inherits, changed                                    bool
	libraries                                            []string
}

func usPolicyCodec(kind CommandID) (string, error) {
	switch kind {
	case USMemberLibraries:
		return "member_libraries_v1", nil
	case USProfileLibraries:
		return "profile_libraries_v1", nil
	default:
		return "", ErrInvalid
	}
}

func validateUSPolicyResult(v usPolicySavedResult) error {
	if _, err := usPolicyCodec(v.kind); err != nil {
		return err
	}
	if !usIdentifier(v.ownerID) || !usIdentifier(v.account) || !usHex(v.memberIncarnation, 32) || v.revision < 1 || v.revision > usMaxRevision || v.appliedMS < 0 || len(v.libraries) > usMaxPolicyLibraries {
		return ErrResolveRequired
	}
	if v.kind == USMemberLibraries && (v.profile != "" || v.profileLifetime != "" || v.inherits) ||
		v.kind == USProfileLibraries && (!usIdentifier(v.profile) || !usHex(v.profileLifetime, 32)) || v.inherits && len(v.libraries) != 0 {
		return ErrResolveRequired
	}
	seen := make(map[string]struct{}, len(v.libraries))
	for _, id := range v.libraries {
		if !usIdentifier(id) {
			return ErrResolveRequired
		}
		if _, exists := seen[id]; exists {
			return ErrResolveRequired
		}
		seen[id] = struct{}{}
	}
	return nil
}

// Closed v1 codec, reusing W's bounded LP/integer primitives, with a distinct
// semantic domain and explicit field order. Never reflection or arbitrary JSON.
func usPolicyResultDigest(identity CommandIdentity, request RequestDigest, v usPolicySavedResult) (ResultDigest, error) {
	if identity.Incarnation == [16]byte{} || identity.OperationID == [16]byte{} || identity.Sequence <= 0 || request == (RequestDigest{}) {
		return ResultDigest{}, ErrResolveRequired
	}
	if err := validateUSPolicyResult(v); err != nil {
		return ResultDigest{}, err
	}
	codec, _ := usPolicyCodec(v.kind)
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
	e.text(v.ownerID)
	e.text(v.account)
	e.text(v.memberIncarnation)
	e.flag(v.kind == USProfileLibraries)
	if v.kind == USProfileLibraries {
		e.text(v.profile)
		e.text(v.profileLifetime)
	}
	e.number(uint64(v.revision))
	e.number(uint64(v.appliedMS))
	e.flag(v.inherits)
	e.flag(v.changed)
	e.number(uint64(len(v.libraries)))
	for _, id := range v.libraries {
		e.text(id)
	}
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	var out ResultDigest
	copy(out[:], e.h.Sum(nil))
	return out, nil
}

// Exact logical variable-column charge, NOT index/page/WAL bytes or a quota.
// Native INSERT RETURNING bytes must equal this complete prepared image. W
// separately admits fixed rows/index effects and total physical footprints.
func usPolicyResultVariableBytes(v usPolicySavedResult) (int64, error) {
	if err := validateUSPolicyResult(v); err != nil {
		return 0, err
	}
	codec, _ := usPolicyCodec(v.kind)
	n := len(v.ownerID) + 96 + len(codec) + len(v.account) + len(v.memberIncarnation) + len(v.profile) + len(v.profileLifetime)
	for _, id := range v.libraries {
		n += len(v.ownerID) + 16 + len("policy") + len(id)
	}
	return int64(n), nil
}
