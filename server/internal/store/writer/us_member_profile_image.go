package writer

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// USMemberProfileImage owns the complete public identity.MemberProfile response
// shape at source 74837e5d63b0490003984e0ae9aaee1f4ab535a7. There
// member_profiles.go:MemberProfile embeds direct_profiles.go:DirectProfile and
// carries restrictions.go:ProfileRestrictions plus an optional
// avatars.go:ProfileAvatar. memberProfileTx builds that image; the edit,
// restriction, PIN-clear and picture setters all return it, and
// MemberProfilesTx appends those same images in captured position,id order.
//
// This is historical public response data, never a receipt, result authority or
// current capability grant. PINRequired and the restrictions flags are captured
// facts. No bearer, PIN body/hash, avatar digest or image bytes belong here.
//
// A snapshot carrier still needs its own authority, same-snapshot octet
// preflight and memory accounting. Input edit caps are not stored-source
// invariants: the container cardinality below is unbounded here.
type USMemberProfileImage struct {
	ID               string
	Name             string
	Primary          bool
	Position         int
	Art              string
	Revision         int64
	PINRevision      int64
	PINRequired      bool
	AllowedLibraries []string
	Restrictions     USMemberProfileRestrictionsImage
	Avatar           *USMemberProfileAvatarImage
}

type USMemberProfileRestrictionsImage struct {
	ProfileID          string
	RatingSystem       string
	MaximumAgeRating   string
	MaximumAge         int
	AllowUnrated       bool
	BlockedLabels      []string
	AllowDownloads     bool
	AllowLiveTV        bool
	AllowDVR           bool
	AllowWatchTogether bool
	Revision           int64
}

type USMemberProfileAvatarImage struct {
	ProfileID  string
	Version    int64
	UpdatedAt  string
	SourceMIME string
	URL        string
}

func (USMemberProfileImage) String() string   { return "[member profile image redacted]" }
func (USMemberProfileImage) GoString() string { return "[member profile image redacted]" }
func (USMemberProfileImage) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("[member profile image redacted]"))
}

func (USMemberProfileRestrictionsImage) String() string {
	return "[member profile restrictions image redacted]"
}
func (USMemberProfileRestrictionsImage) GoString() string {
	return "[member profile restrictions image redacted]"
}
func (USMemberProfileRestrictionsImage) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("[member profile restrictions image redacted]"))
}

func (USMemberProfileAvatarImage) String() string {
	return "[member profile avatar image redacted]"
}
func (USMemberProfileAvatarImage) GoString() string {
	return "[member profile avatar image redacted]"
}
func (USMemberProfileAvatarImage) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("[member profile avatar image redacted]"))
}

// Clone takes ownership of every mutable container and string backing without
// normalizing anything: nil and empty stay distinct, order is unchanged and no
// label is repaired, deduplicated or looked up.
func (v USMemberProfileImage) Clone() USMemberProfileImage {
	v.ID = strings.Clone(v.ID)
	v.Name = strings.Clone(v.Name)
	v.Art = strings.Clone(v.Art)
	v.AllowedLibraries = cloneUSMemberProfileStrings(v.AllowedLibraries)
	v.Restrictions = v.Restrictions.Clone()
	if v.Avatar != nil {
		a := v.Avatar.Clone()
		v.Avatar = &a
	}
	return v
}

func cloneUSMemberProfileStrings(v []string) []string {
	if v == nil {
		return nil
	}
	out := make([]string, len(v))
	for i := range v {
		out[i] = strings.Clone(v[i])
	}
	return out
}

func (v USMemberProfileRestrictionsImage) Clone() USMemberProfileRestrictionsImage {
	v.ProfileID = strings.Clone(v.ProfileID)
	v.RatingSystem = strings.Clone(v.RatingSystem)
	v.MaximumAgeRating = strings.Clone(v.MaximumAgeRating)
	v.BlockedLabels = cloneUSMemberProfileStrings(v.BlockedLabels)
	return v
}

func (v USMemberProfileAvatarImage) Clone() USMemberProfileAvatarImage {
	v.ProfileID = strings.Clone(v.ProfileID)
	v.UpdatedAt = strings.Clone(v.UpdatedAt)
	v.SourceMIME = strings.Clone(v.SourceMIME)
	v.URL = strings.Clone(v.URL)
	return v
}

// ManifestSHA256 is an image-only manual LP/integer manifest, version 1. It is
// not an accepted result or a registered purpose and performs no acceptance.
//
// Every field has an explicit ordinal so a reordered or retyped image cannot
// silently keep the same digest. Slices include presence, length and element
// ordinals, so nil, empty and reordered cohorts differ. Strings are raw LP bytes:
// native returned facts are bound even when invalid UTF-8, and JSON replacement
// semantics belong to the capture that chooses JSON. Equal repaired labels stay
// separate ordinals. Integers are signed two's-complement uint64.
//
// The shared encoder's 32 MiB staging bound is an encoding refusal, never
// truncation or a product user/profile ceiling. A legal large read needs a
// larger staging arrangement at the caller, not reclassification as invalid.
func (v USMemberProfileImage) ManifestSHA256() ([32]byte, error) {
	e := requestEncoder{h: sha256.New()}
	e.bytes([]byte("portico-us-member-profile-image\x00"))
	e.number(1)
	encodeUSMemberProfileImage(&e, v)
	if e.err != nil {
		return [32]byte{}, e.err
	}
	var out [32]byte
	copy(out[:], e.h.Sum(nil))
	return out, nil
}

// usMemberProfileImageManifest binds one image to the result identity and its
// captured cohort ordinal. MemberProfilesTx returns a position,id-ordered
// cohort, so two equal images at different ordinals are different facts. It is
// the same image encoding as ManifestSHA256 with an identity/ordinal prefix.
func usMemberProfileImageManifest(identity CommandIdentity, ordinal uint64, v USMemberProfileImage) ([32]byte, error) {
	e := requestEncoder{h: sha256.New()}
	e.bytes([]byte("portico-us-member-profile-native-image-sha256-v1\x00"))
	e.number(1)
	e.bytes(identity.Incarnation[:])
	e.number(uint64(identity.Sequence))
	e.bytes(identity.OperationID[:])
	e.number(ordinal)
	encodeUSMemberProfileImage(&e, v)
	if e.err != nil {
		return [32]byte{}, e.err
	}
	var out [32]byte
	copy(out[:], e.h.Sum(nil))
	return out, nil
}

// usMemberProfileImageText writes raw LP bytes, unlike requestEncoder.text,
// because native returned profile facts may hold invalid UTF-8 that this image
// must bind unchanged.
func usMemberProfileImageText(e *requestEncoder, s string) {
	e.number(uint64(len(s)))
	e.bytes([]byte(s))
}

func encodeUSMemberProfileStrings(e *requestEncoder, v []string) {
	e.flag(v != nil)
	if v == nil {
		return
	}
	e.number(uint64(len(v)))
	for i, s := range v {
		if e.err != nil {
			return
		}
		e.number(uint64(i))
		usMemberProfileImageText(e, s)
	}
}

func encodeUSMemberProfileImage(e *requestEncoder, v USMemberProfileImage) {
	e.number(11)
	e.number(0) // ID
	usMemberProfileImageText(e, v.ID)
	e.number(1) // Name
	usMemberProfileImageText(e, v.Name)
	e.number(2) // Primary
	e.flag(v.Primary)
	e.number(3) // Position
	e.number(uint64(int64(v.Position)))
	e.number(4) // Art
	usMemberProfileImageText(e, v.Art)
	e.number(5) // Revision
	e.number(uint64(v.Revision))
	e.number(6) // PINRevision
	e.number(uint64(v.PINRevision))
	e.number(7) // PINRequired
	e.flag(v.PINRequired)
	e.number(8) // AllowedLibraries
	encodeUSMemberProfileStrings(e, v.AllowedLibraries)
	e.number(9) // Restrictions
	encodeUSMemberProfileRestrictionsImage(e, v.Restrictions)
	e.number(10) // Avatar
	e.flag(v.Avatar != nil)
	if a := v.Avatar; a != nil {
		encodeUSMemberProfileAvatarImage(e, *a)
	}
}

func encodeUSMemberProfileRestrictionsImage(e *requestEncoder, v USMemberProfileRestrictionsImage) {
	e.number(11)
	e.number(0) // ProfileID
	usMemberProfileImageText(e, v.ProfileID)
	e.number(1) // RatingSystem
	usMemberProfileImageText(e, v.RatingSystem)
	e.number(2) // MaximumAgeRating
	usMemberProfileImageText(e, v.MaximumAgeRating)
	e.number(3) // MaximumAge
	e.number(uint64(int64(v.MaximumAge)))
	e.number(4) // AllowUnrated
	e.flag(v.AllowUnrated)
	e.number(5) // BlockedLabels
	encodeUSMemberProfileStrings(e, v.BlockedLabels)
	e.number(6) // AllowDownloads
	e.flag(v.AllowDownloads)
	e.number(7) // AllowLiveTV
	e.flag(v.AllowLiveTV)
	e.number(8) // AllowDVR
	e.flag(v.AllowDVR)
	e.number(9) // AllowWatchTogether
	e.flag(v.AllowWatchTogether)
	e.number(10) // Revision
	e.number(uint64(v.Revision))
}

func encodeUSMemberProfileAvatarImage(e *requestEncoder, v USMemberProfileAvatarImage) {
	e.number(5)
	e.number(0) // ProfileID
	usMemberProfileImageText(e, v.ProfileID)
	e.number(1) // Version
	e.number(uint64(v.Version))
	e.number(2) // UpdatedAt
	usMemberProfileImageText(e, v.UpdatedAt)
	e.number(3) // SourceMIME
	usMemberProfileImageText(e, v.SourceMIME)
	e.number(4) // URL
	usMemberProfileImageText(e, v.URL)
}
