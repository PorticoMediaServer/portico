package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The COMPLETE private PIN-primitive output (532/533). It is reconstructed only
// from the immutable receipt; no PIN or hash is ever a field here. The public
// profile adapter still owes its whole parent response image; the packet pins
// that image to the merged US-04 MemberProfile, captured below in the SAME frame
// as the accepted original and never rebuilt from live source on replay.
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
	// A PIN edit always bumps BOTH revisions from at least 1, including clearing
	// an already absent PIN, exactly as the pinned API does.
	if !usIdentifier(v.owner) || !usIdentifier(v.account) || !usHex(v.member, 32) || !usIdentifier(v.profile) || !usHex(v.lifetime, 32) ||
		v.revision < 2 || v.revision > usMaxRevision || v.pinRevision < 2 || v.pinRevision > usMaxRevision || v.appliedMS < 0 ||
		v.pinRequired != (v.kind == usPINSet) {
		return ErrResolveRequired
	}
	return nil
}

// Closed v1 codec, reusing W's bounded LP/integer primitives, with a distinct
// semantic domain and explicit field order. Never reflection or arbitrary JSON.
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
	// The receipt DDL forces result_changed=1 for both PIN purposes: the edit
	// always advances both revisions, so the semantic "changed" flag is a
	// constant part of the codec rather than a caller-supplied value.
	e.flag(true)
	e.number(uint64(v.appliedMS))
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	var out ResultDigest
	copy(out[:], e.h.Sum(nil))
	return out, nil
}

// Exact logical variable-column charge, NOT index/page/WAL bytes or a quota.
// Native INSERT RETURNING bytes must equal this complete prepared image.
func usPINResultVariableBytes(v usPINSavedResult) (int64, error) {
	if err := validateUSPINSavedResult(v); err != nil {
		return 0, err
	}
	codec, _ := usPINCodec(v.kind)
	return int64(len(v.owner) + 96 + len(codec) + len(v.account) + len(v.member) + len(v.profile) + len(v.lifetime)), nil
}

// usPINPublicImage is the frozen public shape of a fresh PIN original: the
// complete MemberProfile of the edited profile, exactly memberProfileTx's
// response after the edit, as the packet (US-04) specifies for this command.
// The own-account 532/533 result is being changed to a retained DirectSnapshot
// by the follow-up US-09b per SOL-USERS; only the admin clear of another
// account's profile keeps this MemberProfile closed result. It is a response
// image captured in the SAME frame, never a receipt field, and a replay never
// reconstructs it from live source.
type usPINPublicImage struct {
	Profile *USMemberProfileImage
}

func (usPINPublicImage) String() string   { return "[us pin public image redacted]" }
func (usPINPublicImage) GoString() string { return "[us pin public image redacted]" }
func (usPINPublicImage) Format(f fmt.State, _ rune) {
	_, _ = f.Write([]byte("[us pin public image redacted]"))
}

// The loader's own finite statement set. Every variable column first appears as
// an INTEGER-only octet/count point; no TEXT/BLOB Scan, JSON decode or slice
// allocation may precede the charged preflight.
const (
	usPINImageProfileOctets     stmt.ID = "us.pin.image.profile_octets"
	usPINImageProfile           stmt.ID = "us.pin.image.profile"
	usPINImageRestrictionOctets stmt.ID = "us.pin.image.restriction_octets"
	usPINImageRestriction       stmt.ID = "us.pin.image.restriction"
	usPINImageAvatarOctets      stmt.ID = "us.pin.image.avatar_octets"
	usPINImageAvatar            stmt.ID = "us.pin.image.avatar"
	usPINImageDefaultDVR        stmt.ID = "us.pin.image.default_dvr"
)

// usPINImageMaxLabels is the closed stored-label cohort this loader decodes.
// It mirrors the restrictions write cap; a larger stored cohort is a named
// refusal, never a truncated image.
const usPINImageMaxLabels = 64

func usPINImageDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	profilePoint := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_direct_profiles_2"}, Seeks: []string{"account_id=? AND id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{{Table: "direct_profiles", Name: "sqlite_autoindex_direct_profiles_2", Origin: "u",
			Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}, {Name: "id", Collation: "BINARY"}}}}}
	restrictionPoint := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_profile_restrictions_1"}, Seeks: []string{"profile_id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{{Table: "profile_restrictions", Name: "sqlite_autoindex_profile_restrictions_1", Origin: "u",
			Columns: []stmt.IndexColumn{{Name: "profile_id", Collation: "BINARY"}}}}}
	avatarPoint := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_profile_avatars_1"}, Seeks: []string{"profile_id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{{Table: "profile_avatars", Name: "sqlite_autoindex_profile_avatars_1", Origin: "u",
			Columns: []stmt.IndexColumn{{Name: "profile_id", Collation: "BINARY"}}}}}
	defaultDVRPoint := stmt.Plan{Hot: true,
		Indexes: []string{"sqlite_autoindex_direct_profiles_2", "sqlite_autoindex_direct_memberships_1"},
		Seeks:   []string{"account_id=? AND id=?", "account_id=?"},
		AutoIndexes: []stmt.AutoIndexBinding{
			{Table: "direct_profiles", Name: "sqlite_autoindex_direct_profiles_2", Origin: "u",
				Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}, {Name: "id", Collation: "BINARY"}}},
			{Table: "direct_memberships", Name: "sqlite_autoindex_direct_memberships_1", Origin: "pk",
				Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}}},
		}}
	defs := []stmt.Definition{
		{ID: usPINImageProfileOctets, SQL: `SELECT octet_length(id),octet_length(name),octet_length(art),coalesce(octet_length(allowed_libraries),0),revision,pin_revision,length(pin_hash),is_primary,position
FROM direct_profiles INDEXED BY sqlite_autoindex_direct_profiles_2
WHERE account_id=? AND id=? AND deleted=0`,
			Params: []stmt.Param{text("account"), text("profile")}, Cohorts: []stmt.Cohort{{Name: "exact_profile", Bindings: []any{"us-pin-account", "us-pin-profile"}}}, Plan: profilePoint},
		{ID: usPINImageProfile, SQL: `SELECT id,name,art,allowed_libraries,revision,pin_revision,length(pin_hash),is_primary,position
FROM direct_profiles INDEXED BY sqlite_autoindex_direct_profiles_2
WHERE account_id=? AND id=? AND deleted=0`,
			Params: []stmt.Param{text("account"), text("profile")}, Cohorts: []stmt.Cohort{{Name: "exact_profile", Bindings: []any{"us-pin-account", "us-pin-profile"}}}, Plan: profilePoint},
		{ID: usPINImageRestrictionOctets, SQL: `SELECT octet_length(rating_system),octet_length(maximum_age_rating),octet_length(blocked_labels),maximum_age,allow_unrated,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together,revision
FROM profile_restrictions INDEXED BY sqlite_autoindex_profile_restrictions_1
WHERE profile_id=?`,
			Params: []stmt.Param{text("profile")}, Cohorts: []stmt.Cohort{{Name: "exact_profile", Bindings: []any{"us-pin-profile"}}}, Plan: restrictionPoint},
		{ID: usPINImageRestriction, SQL: `SELECT rating_system,maximum_age_rating,blocked_labels,maximum_age,allow_unrated,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together,revision
FROM profile_restrictions INDEXED BY sqlite_autoindex_profile_restrictions_1
WHERE profile_id=?`,
			Params: []stmt.Param{text("profile")}, Cohorts: []stmt.Cohort{{Name: "exact_profile", Bindings: []any{"us-pin-profile"}}}, Plan: restrictionPoint},
		{ID: usPINImageAvatarOctets, SQL: `SELECT version,octet_length(updated_at),octet_length(source_mime)
FROM profile_avatars INDEXED BY sqlite_autoindex_profile_avatars_1
WHERE profile_id=?`,
			Params: []stmt.Param{text("profile")}, Cohorts: []stmt.Cohort{{Name: "exact_profile", Bindings: []any{"us-pin-profile"}}}, Plan: avatarPoint},
		{ID: usPINImageAvatar, SQL: `SELECT version,updated_at,source_mime
FROM profile_avatars INDEXED BY sqlite_autoindex_profile_avatars_1
WHERE profile_id=?`,
			Params: []stmt.Param{text("profile")}, Cohorts: []stmt.Cohort{{Name: "exact_profile", Bindings: []any{"us-pin-profile"}}}, Plan: avatarPoint},
		// RestrictionsTx's documented "nothing blocked" default needs the
		// profile's recording entitlement: a primary profile of an owner records
		// from the start. One INTEGER point, no materialization.
		{ID: usPINImageDefaultDVR, SQL: `SELECT 1 FROM direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=p.account_id
WHERE p.account_id=? AND p.id=? AND p.is_primary=1 AND p.deleted=0 AND m.role='owner' AND m.disabled=0`,
			Params: []stmt.Param{text("account"), text("profile")}, Cohorts: []stmt.Cohort{{Name: "owner_primary", Bindings: []any{"us-pin-account", "us-pin-profile"}}}, Plan: defaultDVRPoint},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		defs[n].Owner = "store/writer US PIN image"
		defs[n].CallSites = []string{"store/writer/us_pin_result.go"}
	}
	return defs
}

// usMemberProfileImageOctets is the image's exact variable-string charge: every
// string the public DTO carries, counted once. Integers, flags and presence are
// encoded but cost no variable source bytes.
func usMemberProfileImageOctets(v USMemberProfileImage) (int64, error) {
	total := int64(0)
	for _, s := range []string{v.ID, v.Name, v.Art, v.Restrictions.ProfileID, v.Restrictions.RatingSystem, v.Restrictions.MaximumAgeRating} {
		n, err := usPolicyOctetsSum(total, int64(len(s)))
		if err != nil {
			return 0, err
		}
		total = n
	}
	for _, list := range [][]string{v.AllowedLibraries, v.Restrictions.BlockedLabels} {
		for _, s := range list {
			n, err := usPolicyOctetsSum(total, int64(len(s)))
			if err != nil {
				return 0, err
			}
			total = n
		}
	}
	if a := v.Avatar; a != nil {
		for _, s := range []string{a.ProfileID, a.UpdatedAt, a.SourceMIME, a.URL} {
			n, err := usPolicyOctetsSum(total, int64(len(s)))
			if err != nil {
				return 0, err
			}
			total = n
		}
	}
	return total, nil
}

// usPINProfileURL mirrors memberProfileTx's avatar URL exactly, from validated
// native facts. It is a public response fact, never a bearer or object path.
func usPINProfileURL(account, profile string, version int64) string {
	return "/v1/admin/access/members/" + account + "/profiles/" + profile + "/avatar?v=" + strconv.FormatInt(version, 10)
}

// decodeUSPINLabels decodes one stored blocked-label cohort after its octet
// preflight. A malformed array, an absurd cohort or a non-string element is a
// resolve-required refusal, never a repaired image.
func decodeUSPINLabels(raw string) ([]string, error) {
	if raw == "" || len(raw) > usLibraryPolicyByteLimit {
		return nil, ErrResolveRequired
	}
	var labels []string
	if err := json.Unmarshal([]byte(raw), &labels); err != nil {
		return nil, errors.Join(ErrResolveRequired, err)
	}
	if len(labels) > usPINImageMaxLabels {
		return nil, &Refusal{Code: RefusalCapacity}
	}
	if labels == nil {
		labels = []string{}
	}
	return labels, nil
}

// captureUSPINProfileImage freezes the edited profile's complete POST-EFFECT
// MemberProfile for a fresh 532/533 original, exactly memberProfileTx's shape.
//
// Resource order: INTEGER-only octet/count preflights over every string the
// image will carry (including the two repeated profile IDs, the avatar URL and
// the JSON cohorts), overflow-checked sum, funded-grant charge, and only then
// any TEXT/BLOB Scan, JSON decode, slice or DTO allocation.
func captureUSPINProfileImage(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, target usPINFenceScope, v usPINSavedResult) (out *USMemberProfileImage, charged int64, err error) {
	var done struct {
		image   *USMemberProfileImage
		charged int64
	}
	defer func() {
		value, e := finishCommand(ctx, commandLifetime(turn), done, err)
		out, charged, err = value.image, value.charged, e
	}()
	if turn == nil || turn.transaction == nil || a == nil {
		return nil, 0, ErrInvalid
	}
	issuer := a.proof.witness.issuer
	if !usIdentifier(target.account) || !usIdentifier(target.profile) || !usHex(target.lifetime, 32) {
		return nil, 0, ErrInvalid
	}
	// The image is the caller's own account profile. A cross-account image is a
	// different product operation and is not admitted here.
	if target.account != issuer.account || v.account != target.account || v.profile != target.profile || v.lifetime != target.lifetime {
		return nil, 0, ErrDisclosureDenied
	}

	// Integer-only preflight. No variable column is materialized yet.
	row, err := usPolicyRow(ctx, turn, usPINImageProfileOctets, target.account, target.profile)
	if err != nil {
		return nil, 0, err
	}
	var idN, nameN, artN, librariesN, pinLength, primary sql.NullInt64
	var revision, pinRevision, position int64
	if err = row.Scan(&idN, &nameN, &artN, &librariesN, &revision, &pinRevision, &pinLength, &primary, &position); err != nil {
		return nil, 0, errors.Join(ErrResolveRequired, err)
	}
	var profileN [4]int64
	for n, value := range []sql.NullInt64{idN, nameN, artN, librariesN} {
		if profileN[n], err = usPolicyOctet(value); err != nil {
			return nil, 0, err
		}
	}
	if revision != v.revision || pinRevision != v.pinRevision || !primary.Valid || (primary.Int64 != 0 && primary.Int64 != 1) ||
		pinLength.Valid != v.pinRequired || position < 0 {
		return nil, 0, ErrResolveRequired
	}

	restrictionPresent := true
	row, err = usPolicyRow(ctx, turn, usPINImageRestrictionOctets, target.profile)
	if err != nil {
		return nil, 0, err
	}
	var ratingN, maximumRatingN, labelsN sql.NullInt64
	var maximumAge, allowUnrated, allowDownloads, allowLiveTV, allowDVR, allowWatchTogether, restrictionRevision int64
	switch scan := row.Scan(&ratingN, &maximumRatingN, &labelsN, &maximumAge, &allowUnrated, &allowDownloads, &allowLiveTV, &allowDVR, &allowWatchTogether, &restrictionRevision); {
	case errors.Is(scan, sql.ErrNoRows):
		restrictionPresent = false
	case scan != nil:
		return nil, 0, errors.Join(ErrResolveRequired, scan)
	}
	var restrictionN [3]int64
	if restrictionPresent {
		for n, value := range []sql.NullInt64{ratingN, maximumRatingN, labelsN} {
			if restrictionN[n], err = usPolicyOctet(value); err != nil {
				return nil, 0, err
			}
		}
		if maximumAge < -1 || !usPINFlag(allowUnrated) || !usPINFlag(allowDownloads) || !usPINFlag(allowLiveTV) ||
			!usPINFlag(allowDVR) || !usPINFlag(allowWatchTogether) || restrictionRevision < 1 {
			return nil, 0, ErrResolveRequired
		}
	} else {
		// DefaultRestrictions: nothing blocked; a primary owner profile records
		// from the start. This is one INTEGER existence point.
		row, err = usPolicyRow(ctx, turn, usPINImageDefaultDVR, target.account, target.profile)
		if err != nil {
			return nil, 0, err
		}
		var present int64
		switch scan := row.Scan(&present); {
		case scan == nil && present == 1:
			allowDVR = 1
		case scan == nil, errors.Is(scan, sql.ErrNoRows):
		default:
			return nil, 0, errors.Join(ErrResolveRequired, scan)
		}
	}

	avatarPresent := true
	row, err = usPolicyRow(ctx, turn, usPINImageAvatarOctets, target.profile)
	if err != nil {
		return nil, 0, err
	}
	var version, updatedN, mimeN sql.NullInt64
	switch scan := row.Scan(&version, &updatedN, &mimeN); {
	case errors.Is(scan, sql.ErrNoRows):
		avatarPresent = false
	case scan != nil:
		return nil, 0, errors.Join(ErrResolveRequired, scan)
	}
	var avatarN [2]int64
	avatarURL := ""
	if avatarPresent {
		if !version.Valid || version.Int64 < 1 {
			return nil, 0, ErrResolveRequired
		}
		for n, value := range []sql.NullInt64{updatedN, mimeN} {
			if avatarN[n], err = usPolicyOctet(value); err != nil {
				return nil, 0, err
			}
		}
		avatarURL = usPINProfileURL(target.account, target.profile, version.Int64)
	}

	// The image repeats the profile ID in Restrictions.ProfileID and, when
	// present, Avatar.ProfileID; the avatar URL also carries the account and
	// profile. Every copy is counted on the raw side before the comparison, so
	// an ordinary PIN edit can never exceed its own preflighted source.
	raw := int64(0)
	for copy := 0; copy < 3; copy++ {
		if raw, err = usPolicyOctetsSum(raw, profileN[0]); err != nil {
			return nil, 0, err
		}
	}
	for _, n := range []int64{profileN[1], profileN[2], profileN[3]} {
		if raw, err = usPolicyOctetsSum(raw, n); err != nil {
			return nil, 0, err
		}
	}
	for _, n := range restrictionN {
		if raw, err = usPolicyOctetsSum(raw, n); err != nil {
			return nil, 0, err
		}
	}
	if avatarPresent {
		for _, n := range avatarN {
			if raw, err = usPolicyOctetsSum(raw, n); err != nil {
				return nil, 0, err
			}
		}
		if raw, err = usPolicyOctetsSum(raw, int64(len(avatarURL))); err != nil {
			return nil, 0, err
		}
	}
	// Both the retained native copy and the decoded DTO copy are resident while
	// the image is built; slice overhead and the image header are additive.
	charged = raw
	if charged, err = usPolicyOctetsSum(charged, raw); err != nil {
		return nil, 0, err
	}
	if charged, err = usPolicyOctetsSum(charged, usPolicyImageProfileBytes); err != nil {
		return nil, 0, err
	}
	if charged, err = usPolicyOctetsSum(charged, usPolicyImageHeaderBytes); err != nil {
		return nil, 0, err
	}
	if err = a.budget.check(ctx, turn, charged); err != nil {
		return nil, 0, err
	}
	if err = usPINDispatchFund(a, charged); err != nil {
		return nil, 0, err
	}
	done.charged = charged

	// Charged. Only now may variable source become Go memory.
	row, err = usPolicyRow(ctx, turn, usPINImageProfile, target.account, target.profile)
	if err != nil {
		return nil, 0, err
	}
	var id, name, art string
	var libraries sql.NullString
	var pinPresent sql.NullInt64
	var profilePosition int64
	if err = row.Scan(&id, &name, &art, &libraries, &revision, &pinRevision, &pinPresent, &primary, &profilePosition); err != nil {
		return nil, 0, errors.Join(ErrResolveRequired, err)
	}
	if id != target.profile || revision != v.revision || pinRevision != v.pinRevision || pinPresent.Valid != v.pinRequired ||
		profilePosition != position || int64(len(id)) != profileN[0] || int64(len(name)) != profileN[1] || int64(len(art)) != profileN[2] {
		return nil, 0, ErrResolveRequired
	}
	image := USMemberProfileImage{
		ID: id, Name: name, Primary: primary.Int64 == 1, Position: int(position), Art: art,
		Revision: revision, PINRevision: pinRevision, PINRequired: pinPresent.Valid,
		AllowedLibraries: []string{},
	}
	if libraries.Valid {
		if int64(len(libraries.String)) != profileN[3] {
			return nil, 0, ErrResolveRequired
		}
		ids, e := decodeUSLibraryIDs(libraries.String)
		if e != nil {
			return nil, 0, e
		}
		if ids != nil {
			image.AllowedLibraries = ids
		}
	}

	if restrictionPresent {
		row, err = usPolicyRow(ctx, turn, usPINImageRestriction, target.profile)
		if err != nil {
			return nil, 0, err
		}
		var ratingSystem, maximumAgeRating, labels string
		if err = row.Scan(&ratingSystem, &maximumAgeRating, &labels, &maximumAge, &allowUnrated, &allowDownloads,
			&allowLiveTV, &allowDVR, &allowWatchTogether, &restrictionRevision); err != nil {
			return nil, 0, errors.Join(ErrResolveRequired, err)
		}
		if int64(len(ratingSystem)) != restrictionN[0] || int64(len(maximumAgeRating)) != restrictionN[1] || int64(len(labels)) != restrictionN[2] {
			return nil, 0, ErrResolveRequired
		}
		blocked, e := decodeUSPINLabels(labels)
		if e != nil {
			return nil, 0, e
		}
		image.Restrictions = USMemberProfileRestrictionsImage{
			ProfileID: target.profile, RatingSystem: ratingSystem, MaximumAgeRating: maximumAgeRating,
			MaximumAge: int(maximumAge), AllowUnrated: allowUnrated == 1, BlockedLabels: blocked,
			AllowDownloads: allowDownloads == 1, AllowLiveTV: allowLiveTV == 1, AllowDVR: allowDVR == 1,
			AllowWatchTogether: allowWatchTogether == 1, Revision: restrictionRevision,
		}
	} else {
		// DefaultRestrictions(profile), exactly, with the owner-primary recording
		// default from the existence point above.
		image.Restrictions = USMemberProfileRestrictionsImage{
			ProfileID: target.profile, MaximumAge: -1, AllowUnrated: true, BlockedLabels: []string{},
			AllowDownloads: true, AllowLiveTV: true, AllowDVR: allowDVR == 1, AllowWatchTogether: true, Revision: 1,
		}
	}

	if avatarPresent {
		row, err = usPolicyRow(ctx, turn, usPINImageAvatar, target.profile)
		if err != nil {
			return nil, 0, err
		}
		var updatedAt, sourceMIME string
		var avatarVersion int64
		if err = row.Scan(&avatarVersion, &updatedAt, &sourceMIME); err != nil {
			return nil, 0, errors.Join(ErrResolveRequired, err)
		}
		if avatarVersion != version.Int64 || int64(len(updatedAt)) != avatarN[0] || int64(len(sourceMIME)) != avatarN[1] {
			return nil, 0, ErrResolveRequired
		}
		image.Avatar = &USMemberProfileAvatarImage{
			ProfileID: target.profile, Version: avatarVersion, UpdatedAt: updatedAt,
			SourceMIME: sourceMIME, URL: usPINProfileURL(target.account, target.profile, avatarVersion),
		}
	}

	// Frozen carrier contract on the fresh image: the image can never cost more
	// variable bytes than the preflighted source it was decoded from.
	imageOctets, e := usMemberProfileImageOctets(image)
	if e != nil || imageOctets > raw {
		return nil, 0, errors.Join(ErrResolveRequired, e)
	}
	done.image = &image
	return &image, charged, nil
}

// usPINFlag accepts one stored 0/1 integer flag.
func usPINFlag(v int64) bool { return v == 0 || v == 1 }
