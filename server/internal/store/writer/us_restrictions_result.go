package writer

import "crypto/sha256"

// The COMPLETE private ProfileRestrictions result. It is reconstructed only from
// its immutable receipt row plus ordered label cohort; a replay never reads the
// live profile_restrictions row and never re-runs the rating table. This is the
// whole public DTO, not a partial document: every response field is retained.
type usRestrictionsSavedResult struct {
	kind                                                                    CommandID
	ownerID                                                                 string
	account, memberIncarnation, profile, profileLifetime                    string
	revision, appliedMS                                                     int64
	ratingSystem, maximumAgeRating                                          string
	maximumAge                                                              int64
	allowUnrated, allowDownloads, allowLiveTV, allowDVR, allowWatchTogether bool
	labels                                                                  []string
}

func usRestrictionsCodec(kind CommandID) (string, error) {
	if kind != USProfileRestrictions {
		return "", ErrInvalid
	}
	return usRestrictionsResultCodec, nil
}

func validateUSRestrictionsResult(v usRestrictionsSavedResult) error {
	if _, err := usRestrictionsCodec(v.kind); err != nil {
		// A retained row under another purpose is corruption, never a live result.
		return ErrResolveRequired
	}
	if !usIdentifier(v.ownerID) || !usIdentifier(v.account) || !usHex(v.memberIncarnation, 32) || !usIdentifier(v.profile) || !usHex(v.profileLifetime, 32) ||
		v.revision < 2 || v.revision > usMaxRevision || v.appliedMS < 0 {
		return ErrResolveRequired
	}
	if !usRestrictionsRatingShape(v.ratingSystem, v.maximumAgeRating, v.maximumAge) {
		return ErrResolveRequired
	}
	return usRestrictionsValidateLabels(v.labels)
}

// Closed v1 codec, reusing W's bounded LP/integer primitives with a distinct
// semantic domain and explicit field order. Never reflection or arbitrary JSON.
func usRestrictionsResultDigest(identity CommandIdentity, request RequestDigest, v usRestrictionsSavedResult) (ResultDigest, error) {
	if identity.Incarnation == [16]byte{} || identity.OperationID == [16]byte{} || identity.Sequence <= 0 || request == (RequestDigest{}) {
		return ResultDigest{}, ErrResolveRequired
	}
	if err := validateUSRestrictionsResult(v); err != nil {
		return ResultDigest{}, err
	}
	codec, _ := usRestrictionsCodec(v.kind)
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
	e.text(v.profile)
	e.text(v.profileLifetime)
	e.number(uint64(v.revision))
	// US-07's policy result digest includes appliedMS; this sibling keeps the
	// same immutable timestamp witness in its digest.
	e.number(uint64(v.appliedMS))
	e.text(v.ratingSystem)
	e.text(v.maximumAgeRating)
	// maximumAge is signed (-1 means no ceiling); +1 keeps -1 distinct from 0.
	e.number(uint64(v.maximumAge + 1))
	for _, flag := range []bool{v.allowUnrated, v.allowDownloads, v.allowLiveTV, v.allowDVR, v.allowWatchTogether} {
		e.flag(flag)
	}
	e.number(uint64(len(v.labels)))
	for _, label := range v.labels {
		e.text(label)
	}
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	var out ResultDigest
	copy(out[:], e.h.Sum(nil))
	return out, nil
}

// Exact logical variable-column charge, NOT index/page/WAL bytes or a quota. It
// must equal the receipt header, typed restriction row and ordered label rows'
// GENERATED variable_bytes sum that the native INSERTs return. The typed result
// row contributes its OWN owner and command-incarnation columns in addition to
// the header's fixed 96 bytes.
func usRestrictionsResultVariableBytes(v usRestrictionsSavedResult) (int64, error) {
	if err := validateUSRestrictionsResult(v); err != nil {
		return 0, err
	}
	codec, _ := usRestrictionsCodec(v.kind)
	n := usRestrictionsHeaderBytes(v.ownerID, codec) +
		int64(len(v.ownerID)+16+len(v.account)+len(v.memberIncarnation)+len(v.profile)+len(v.profileLifetime)+len(v.ratingSystem)+len(v.maximumAgeRating))
	for _, label := range v.labels {
		n += int64(len(v.ownerID) + 16 + len(label))
	}
	return n, nil
}
