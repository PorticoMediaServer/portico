package writer

import (
	"crypto/sha256"
	"fmt"
)

// usMemberProfileImage owns the complete native identity.MemberProfile DTO at
// source 74837e5d6. member_profiles.go:memberProfileTx constructs DirectProfile
// (direct_profiles.go), then RestrictionsTx (restrictions.go:scanRestrictions or
// defaultRestrictions), then the optional ProfileAvatar (avatars.go). The edit,
// restriction, PIN-clear and picture setters all return memberProfileTx;
// MemberProfilesTx appends those same images in captured position,id order.
// No bearer, PIN body/hash, avatar digest or image bytes belong to this DTO.
// Flags are historical data, never current permission grants.
type usMemberProfileImage struct {
	id, name              string
	primary               bool
	position              int
	art                   string
	revision, pinRevision int64
	pinRequired           bool
	allowedLibraries      []string
	restrictions          usMemberProfileRestrictionsImage
	avatar                *usMemberProfileAvatarImage
}

type usMemberProfileRestrictionsImage struct {
	profileID, ratingSystem, maximumAgeRating                 string
	maximumAge                                                int
	allowUnrated                                              bool
	blockedLabels                                             []string
	allowDownloads, allowLiveTV, allowDVR, allowWatchTogether bool
	revision                                                  int64
}

type usMemberProfileAvatarImage struct {
	profileID                  string
	version                    int64
	updatedAt, sourceMIME, url string
}

func (usMemberProfileImage) String() string   { return "usMemberProfileImage{redacted}" }
func (usMemberProfileImage) GoString() string { return "usMemberProfileImage{redacted}" }
func (usMemberProfileImage) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usMemberProfileImage")
}
func (usMemberProfileRestrictionsImage) String() string {
	return "usMemberProfileRestrictionsImage{redacted}"
}
func (usMemberProfileRestrictionsImage) GoString() string {
	return "usMemberProfileRestrictionsImage{redacted}"
}
func (usMemberProfileRestrictionsImage) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usMemberProfileRestrictionsImage")
}
func (usMemberProfileAvatarImage) String() string   { return "usMemberProfileAvatarImage{redacted}" }
func (usMemberProfileAvatarImage) GoString() string { return "usMemberProfileAvatarImage{redacted}" }
func (usMemberProfileAvatarImage) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usMemberProfileAvatarImage")
}

func cloneUSMemberProfileImage(v usMemberProfileImage) usMemberProfileImage {
	if v.allowedLibraries != nil {
		v.allowedLibraries = append(make([]string, 0, len(v.allowedLibraries)), v.allowedLibraries...)
	}
	if v.restrictions.blockedLabels != nil {
		v.restrictions.blockedLabels = append(make([]string, 0, len(v.restrictions.blockedLabels)), v.restrictions.blockedLabels...)
	}
	if v.avatar != nil {
		a := *v.avatar
		v.avatar = &a
	}
	return v
}

// Manual native-image manifest, NOT an accepted result or registered purpose.
// Fixed field order, LP raw strings and 64-bit integers bind every native fact,
// identity, cohort ordinal and optional/container presence. Raw strings preserve
// even invalid UTF-8 native returned facts; JSON's replacement semantics must
// happen at capture if a caller instead captures JSON. Equal repaired labels
// remain separate ordinals. No normalization, deduplication or rating lookup.
// memberProfileTx repairs nil libraries to []; scanRestrictions repairs nil
// labels to []. This helper preserves presence exactly, including nil supplied
// by a future capture path. Library inheritance is not inferred from [] here.
// No edit-input caps are read invariants. The shared encoder's 32 MiB staging
// bound can reject a legal large read; root must arrange larger staging before
// using such an image, rather than misclassifying it as invalid product data.
func usMemberProfileImageManifest(identity CommandIdentity, ordinal uint64, v usMemberProfileImage) ([32]byte, error) {
	e := requestEncoder{h: sha256.New()}
	e.bytes([]byte("portico-us-member-profile-native-image-sha256-v1\x00"))
	e.number(1)
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.number(ordinal)
	text := func(s string) { e.number(uint64(len(s))); e.bytes([]byte(s)) }
	strings := func(v []string) {
		e.flag(v != nil)
		e.number(uint64(len(v)))
		for i, s := range v {
			e.number(uint64(i))
			text(s)
		}
	}
	text(v.id)
	text(v.name)
	e.flag(v.primary)
	e.number(uint64(int64(v.position)))
	text(v.art)
	e.number(uint64(v.revision))
	e.number(uint64(v.pinRevision))
	e.flag(v.pinRequired)
	strings(v.allowedLibraries)
	r := v.restrictions
	text(r.profileID)
	text(r.ratingSystem)
	text(r.maximumAgeRating)
	e.number(uint64(int64(r.maximumAge)))
	e.flag(r.allowUnrated)
	strings(r.blockedLabels)
	e.flag(r.allowDownloads)
	e.flag(r.allowLiveTV)
	e.flag(r.allowDVR)
	e.flag(r.allowWatchTogether)
	e.number(uint64(r.revision))
	e.flag(v.avatar != nil)
	if a := v.avatar; a != nil {
		text(a.profileID)
		e.number(uint64(a.version))
		text(a.updatedAt)
		text(a.sourceMIME)
		text(a.url)
	}
	if e.err != nil {
		return [32]byte{}, e.err
	}
	var out [32]byte
	copy(out[:], e.h.Sum(nil))
	return out, nil
}
