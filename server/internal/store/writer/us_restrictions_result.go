package writer

import (
	"crypto/sha256"
	"fmt"
	"unicode/utf8"
)

// Complete immutable purpose-542 DTO image. maximumAge is the saved resolved
// value, never a lookup in today's rating table. Labels retain response order
// and equal JSON-repaired spellings. This type confers no current authority.
type usRestrictionsSavedResult struct {
	kind                                                                    CommandID
	version                                                                 int
	ownerID, account, memberIncarnation, profile, profileLifetime           string
	appliedMS, revision                                                     int64
	ratingSystem, maximumAgeRating                                          string
	maximumAge                                                              int
	allowUnrated, allowDownloads, allowLiveTV, allowDVR, allowWatchTogether bool
	labels                                                                  []string
}

func (usRestrictionsSavedResult) String() string   { return "usRestrictionsSavedResult{redacted}" }
func (usRestrictionsSavedResult) GoString() string { return "usRestrictionsSavedResult{redacted}" }
func (usRestrictionsSavedResult) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usRestrictionsSavedResult")
}

func cloneUSRestrictionsResult(v usRestrictionsSavedResult) usRestrictionsSavedResult {
	if v.labels != nil {
		v.labels = append(make([]string, 0, len(v.labels)), v.labels...)
	}
	return v
}

func validateUSRestrictionsResult(v usRestrictionsSavedResult) error {
	if v.kind != usProfileRestrictionsEdit || v.version != 1 || !usIdentifier(v.ownerID) || !usIdentifier(v.account) || !usHex(v.memberIncarnation, 32) || !usIdentifier(v.profile) || !usHex(v.profileLifetime, 32) || v.appliedMS < 0 || v.revision < 2 || v.revision > usMaxRevision || v.maximumAge < -1 || !utf8.ValidString(v.ratingSystem) || !utf8.ValidString(v.maximumAgeRating) || len(v.labels) > 64 {
		return ErrResolveRequired
	}
	for _, label := range v.labels {
		if len(label) < 1 || len(label) > 360 || !utf8.ValidString(label) {
			return ErrResolveRequired
		}
	}
	return nil
}

// Closed LP/integer full result encoding, including nil versus empty labels.
func usRestrictionsResultDigest(identity CommandIdentity, request RequestDigest, v usRestrictionsSavedResult) (ResultDigest, error) {
	if identity.Incarnation == [16]byte{} || identity.OperationID == [16]byte{} || identity.Sequence <= 0 || request == (RequestDigest{}) {
		return ResultDigest{}, ErrResolveRequired
	}
	if err := validateUSRestrictionsResult(v); err != nil {
		return ResultDigest{}, err
	}
	e := requestEncoder{h: sha256.New()}
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("users_security")
	e.text("profile_restrictions_v1")
	e.text("applied")
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.bytes(request[:])
	e.number(uint64(v.kind))
	e.number(uint64(v.version))
	e.text(v.ownerID)
	e.text(v.account)
	e.text(v.memberIncarnation)
	e.text(v.profile)
	e.text(v.profileLifetime)
	e.number(uint64(v.appliedMS))
	e.number(uint64(v.revision))
	e.text(v.ratingSystem)
	e.text(v.maximumAgeRating)
	e.number(uint64(int64(v.maximumAge)))
	e.flag(v.allowUnrated)
	e.flag(v.allowDownloads)
	e.flag(v.allowLiveTV)
	e.flag(v.allowDVR)
	e.flag(v.allowWatchTogether)
	e.flag(v.labels != nil)
	e.number(uint64(len(v.labels)))
	for ordinal, label := range v.labels {
		e.number(uint64(ordinal))
		e.text(label)
	}
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	var out ResultDigest
	copy(out[:], e.h.Sum(nil))
	return out, nil
}

// Exact draft logical variable columns: header owner+96+codec; typed child
// owner+16+account+member incarnation+profile+lifetime+system+rating; each label
// owner+16+label. Integers add zero. No physical-storage or quota claim.
func usRestrictionsResultVariableBytes(v usRestrictionsSavedResult) (int64, error) {
	if err := validateUSRestrictionsResult(v); err != nil {
		return 0, err
	}
	n := len(v.ownerID) + 96 + len("profile_restrictions_v1")
	n += len(v.ownerID) + 16 + len(v.account) + len(v.memberIncarnation) + len(v.profile) + len(v.profileLifetime) + len(v.ratingSystem) + len(v.maximumAgeRating)
	for _, label := range v.labels {
		n += len(v.ownerID) + 16 + len(label)
	}
	return int64(n), nil
}
