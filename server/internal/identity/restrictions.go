package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"sort"
	"strings"
)

var ErrRestrictionInput = requestinput.New("invalid profile restriction")
var ErrRestrictionChanged = errors.New("These restrictions changed. Refresh before trying again.")

// ProfileRestrictions is the complete per-profile restriction document. It is
// owned by whoever may manage the account (the primary profile or the owner);
// the restricted profile itself may read it but never write it.
//
// MaximumAge is the resolved admission-age ceiling for MaximumAgeRating, or -1
// when the profile has no ceiling. Storing the resolved age alongside the code
// keeps catalog reads free of rating-table lookups and keeps a ceiling stable
// if a rating table entry is ever relabelled.
type ProfileRestrictions struct {
	ProfileID          string   `json:"profileId"`
	RatingSystem       string   `json:"ratingSystem"`
	MaximumAgeRating   string   `json:"maximumAgeRating"`
	MaximumAge         int      `json:"maximumAge"`
	AllowUnrated       bool     `json:"allowUnrated"`
	BlockedLabels      []string `json:"blockedLabels"`
	AllowDownloads     bool     `json:"allowDownloads"`
	AllowLiveTV        bool     `json:"allowLiveTv"`
	AllowDVR           bool     `json:"allowDvr"`
	AllowWatchTogether bool     `json:"allowWatchTogether"`
	Revision           int64    `json:"revision"`
}

// ContentRestrictions is the catalog-visible subset: the three facts that decide
// whether a viewer may see a title at all. The remaining flags gate features,
// not visibility, and are answered by the feature that owns them.
//
// Every field is chosen so the zero value restricts nothing. That is not a
// convenience: this value is embedded in catalog request structs that dozens of
// call sites build by hand, and a zero value that happened to mean "block
// everything" would turn a forgotten field into an empty library. MaximumAge is
// therefore a pointer (nil is no ceiling, and a ceiling of 0 stays expressible),
// and the unrated rule is stated as BlockUnrated rather than AllowUnrated.
type ContentRestrictions struct {
	ProfileID     string
	MaximumAge    *int
	BlockUnrated  bool
	BlockedLabels []string
	Revision      int64
	// The account's limits (set by the owner or an administrator) narrow the
	// profile's own: Narrow folds the account's rating ceiling and unrated rule
	// into MaximumAge and BlockUnrated above, on the same age scale, so there
	// is one rating rule and it cannot disagree with itself. What stays
	// separate is the account's denied list, which matches a title's tags as
	// well as its labels, and the revision that fences open pages.
	MemberDeniedLabels []string
	MemberRevision     int64
	// BlockRecordings is the profile's Recordings switch turned off (P8).
	// Published recordings live only in the owner's private DVR library, which
	// the library gate already withholds (recordingaccess), so this is not a
	// per-row catalogue predicate and does not make Active() true: it is
	// enforced by the single-item fence (contentaccess.VisibleItemTx) for a
	// recording reached by id, and folded into RestrictionFence.
	BlockRecordings bool
}

// Active reports whether the predicate has anything to enforce. An inactive
// restriction must compile to no SQL at all so unrestricted reads keep their
// existing plans.
func (r ContentRestrictions) Active() bool {
	return r.MaximumAge != nil || r.BlockUnrated || len(r.BlockedLabels) > 0 || len(r.MemberDeniedLabels) > 0
}

// Narrow applies an account-level limit on top of these restrictions. It can
// only tighten: the lower of the two rating ceilings holds, unrated titles are
// blocked when either side blocks them, and the denied list is added. A
// negative ceiling means the account has none.
func (r ContentRestrictions) Narrow(ceiling int, blockUnrated bool, denied []string, revision int64) ContentRestrictions {
	if ceiling >= 0 && (r.MaximumAge == nil || ceiling < *r.MaximumAge) {
		age := ceiling
		r.MaximumAge = &age
	} else if r.MaximumAge != nil {
		age := *r.MaximumAge
		r.MaximumAge = &age
	}
	r.BlockUnrated = r.BlockUnrated || blockUnrated
	r.MemberDeniedLabels = append([]string{}, denied...)
	r.MemberRevision = revision
	return r
}

// Content projects the catalog-visible subset.
func (r ProfileRestrictions) Content() ContentRestrictions {
	out := ContentRestrictions{ProfileID: r.ProfileID, BlockUnrated: !r.AllowUnrated, BlockedLabels: r.BlockedLabels, Revision: r.Revision, BlockRecordings: !r.AllowDVR}
	if r.MaximumAge >= 0 {
		ceiling := r.MaximumAge
		out.MaximumAge = &ceiling
	}
	return out
}

// DefaultRestrictions is what a profile with no stored row means: nothing blocked,
// except recording. Recording uses the server's disk, so it is off until the
// server owner switches it on (Who can record); recordingDefault answers for the
// owner's own main profile, which records from the start.
func DefaultRestrictions(profile string) ProfileRestrictions {
	return ProfileRestrictions{ProfileID: profile, MaximumAge: -1, AllowUnrated: true, BlockedLabels: []string{}, AllowDownloads: true, AllowLiveTV: true, AllowDVR: false, AllowWatchTogether: true, Revision: 1}
}

type restrictionQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// defaultRestrictions is DefaultRestrictions for a profile with no stored row,
// with Recordings on for the server owner's main profile.
func defaultRestrictions(ctx context.Context, q restrictionQuerier, profile string) (ProfileRestrictions, error) {
	out := DefaultRestrictions(profile)
	e := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM direct_profiles p JOIN direct_memberships m ON m.account_id=p.account_id WHERE p.id=? AND p.is_primary=1 AND p.deleted=0 AND m.role='owner' AND m.disabled=0)`, profile).Scan(&out.AllowDVR)
	return out, e
}

// RecordingDefaultSQL is the stored-or-default Recordings value for a profile row
// p joined to its membership m and its optional restriction row r, for list
// queries that cannot call RestrictionsTx per row.
const RecordingDefaultSQL = `COALESCE(r.allow_dvr,CASE WHEN p.is_primary=1 AND m.role='owner' THEN 1 ELSE 0 END)`

// ErrRecordingGrant refuses a restriction edit that would let a profile record
// when the caller is not the server owner. Who can record is the owner's choice;
// anyone who manages the profile may still switch Recordings off.
var ErrRecordingGrant = fmt.Errorf("only the server owner can let a profile record: %w", ErrForbidden)

// ProfileRestrictionEdit is a full replacement guarded by ExpectedRevision. A
// partial patch is deliberately not offered: a restriction document read, shown
// and re-submitted as a whole cannot lose a field a newer client added.
type ProfileRestrictionEdit struct {
	ExpectedRevision   int64    `json:"expectedRevision"`
	RatingSystem       string   `json:"ratingSystem"`
	MaximumAgeRating   string   `json:"maximumAgeRating"`
	AllowUnrated       bool     `json:"allowUnrated"`
	BlockedLabels      []string `json:"blockedLabels"`
	AllowDownloads     bool     `json:"allowDownloads"`
	AllowLiveTV        bool     `json:"allowLiveTv"`
	AllowDVR           bool     `json:"allowDvr"`
	AllowWatchTogether bool     `json:"allowWatchTogether"`
}

func normalizeLabels(raw []string) ([]string, bool) {
	if len(raw) > 64 {
		return nil, false
	}
	seen := map[string]bool{}
	out := []string{}
	for _, label := range raw {
		label = strings.TrimSpace(label)
		if label == "" || len(label) > 120 || strings.ContainsAny(label, "\r\n\t\x00") {
			return nil, false
		}
		key := strings.ToLower(label)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, label)
	}
	sort.Strings(out)
	return out, true
}

func scanRestrictions(row interface{ Scan(...any) error }, profile string) (ProfileRestrictions, error) {
	out := ProfileRestrictions{ProfileID: profile}
	var labels string
	if e := row.Scan(&out.RatingSystem, &out.MaximumAgeRating, &out.MaximumAge, &out.AllowUnrated, &labels, &out.AllowDownloads, &out.AllowLiveTV, &out.AllowDVR, &out.AllowWatchTogether, &out.Revision); e != nil {
		return out, e
	}
	if e := json.Unmarshal([]byte(labels), &out.BlockedLabels); e != nil {
		return out, e
	}
	if out.BlockedLabels == nil {
		out.BlockedLabels = []string{}
	}
	return out, nil
}

const restrictionColumns = `rating_system,maximum_age_rating,maximum_age,allow_unrated,blocked_labels,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together,revision`

// RestrictionsTx reads one profile's restrictions inside a caller transaction.
// A missing row is not an error: it is the documented "nothing blocked" default.
func RestrictionsTx(ctx context.Context, tx *sql.Tx, profile string) (ProfileRestrictions, error) {
	out, e := scanRestrictions(tx.QueryRowContext(ctx, `SELECT `+restrictionColumns+` FROM profile_restrictions WHERE profile_id=?`, profile), profile)
	if errors.Is(e, sql.ErrNoRows) {
		return defaultRestrictions(ctx, tx, profile)
	}
	return out, e
}

// SetRecordingAllowedTx is Who can record: the owner switches one profile's
// Recordings on or off at the restriction document's revision, keeping every
// other restriction as it is. The caller has proved server-owner authority and,
// when allowing, that the profile's membership is current.
func SetRecordingAllowedTx(ctx context.Context, tx *sql.Tx, account, profile string, allowed bool, expectedRevision int64) (ProfileRestrictions, error) {
	var deleted int
	if e := tx.QueryRowContext(ctx, `SELECT deleted FROM direct_profiles WHERE account_id=? AND id=?`, account, profile).Scan(&deleted); e != nil || deleted != 0 {
		return ProfileRestrictions{}, ErrNotVisible
	}
	current, e := RestrictionsTx(ctx, tx, profile)
	if e != nil {
		return ProfileRestrictions{}, e
	}
	if current.Revision != expectedRevision {
		return ProfileRestrictions{}, ErrRestrictionChanged
	}
	raw, _ := json.Marshal(current.BlockedLabels)
	if _, e = tx.ExecContext(ctx, `INSERT INTO profile_restrictions(profile_id,rating_system,maximum_age_rating,maximum_age,allow_unrated,blocked_labels,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together,revision) VALUES(?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(profile_id) DO UPDATE SET allow_dvr=excluded.allow_dvr,revision=profile_restrictions.revision+1`,
		profile, current.RatingSystem, current.MaximumAgeRating, current.MaximumAge, current.AllowUnrated, string(raw), current.AllowDownloads, current.AllowLiveTV, allowed, current.AllowWatchTogether, current.Revision+1); e != nil {
		return ProfileRestrictions{}, e
	}
	return RestrictionsTx(ctx, tx, profile)
}

// API keys belong to a local account and inherit that owner's profile policy.
// Hosted profile ids live in a separate authority and can collide with local
// ids. Unknown authorities are refused rather than given an open default.
func apiKeyProfileBelongsToAccount(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, viewer Viewer) error {
	var allowed bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM direct_profiles WHERE id=? AND account_id=? AND deleted=0)`, viewer.ProfileID, viewer.AccountID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrUnauthorized
	}
	return nil
}

func RestrictionsForViewerTx(ctx context.Context, tx *sql.Tx, viewer Viewer) (ProfileRestrictions, error) {
	switch viewer.Authority {
	case "local":
		return RestrictionsTx(ctx, tx, viewer.ProfileID)
	case "api-key":
		if err := apiKeyProfileBelongsToAccount(ctx, tx, viewer); err != nil {
			return ProfileRestrictions{}, err
		}
		return RestrictionsTx(ctx, tx, viewer.ProfileID)
	default:
		return ProfileRestrictions{}, ErrUnauthorized
	}
}

// ViewerRestrictions resolves the content restrictions a viewer session carries.
// Catalog reads take this value; it never widens what a library policy allows.
func (s *Service) ViewerRestrictions(ctx context.Context, p Principal) (ContentRestrictions, error) {
	if p.Authority != "local" && p.Authority != "api-key" {
		return ContentRestrictions{}, ErrUnauthorized
	}
	if p.ProfileID == "" {
		return ContentRestrictions{}, nil
	}

	if p.Authority == "api-key" {
		if err := apiKeyProfileBelongsToAccount(ctx, s.db, p.Viewer); err != nil {
			return ContentRestrictions{}, err
		}
	}
	out, e := scanRestrictions(s.db.QueryRowContext(ctx, `SELECT `+restrictionColumns+` FROM profile_restrictions WHERE profile_id=?`, p.ProfileID), p.ProfileID)
	if errors.Is(e, sql.ErrNoRows) {
		out, e = defaultRestrictions(ctx, s.db, p.ProfileID)
	}
	if e != nil {
		return ContentRestrictions{}, e
	}
	return out.Content(), nil
}

// RestrictionRevision is folded into the viewer fence so an open cursor or a
// cached page cannot outlive a restriction change.
func (s *Service) RestrictionRevision(ctx context.Context, profile string) (int64, error) {
	var revision int64
	e := s.db.QueryRowContext(ctx, `SELECT revision FROM profile_restrictions WHERE profile_id=?`, profile).Scan(&revision)
	if errors.Is(e, sql.ErrNoRows) {
		return 0, nil
	}
	return revision, e
}

// ProfileRestrictions reads a profile's restrictions through an account session.
// The caller must be able to manage the account, or be the profile itself.
func (s *Service) ProfileRestrictions(ctx context.Context, bearer, profile string) (ProfileRestrictions, error) {
	if !validFamilyID(profile) {
		return ProfileRestrictions{}, ErrRestrictionInput
	}
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return ProfileRestrictions{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	c, e := s.directCallerTx(ctx, tx, bearer, false)
	if e != nil {
		return ProfileRestrictions{}, e
	}
	if !c.manage && c.profileID != profile {
		return ProfileRestrictions{}, ErrForbidden
	}
	var deleted int
	if e = tx.QueryRowContext(ctx, `SELECT deleted FROM direct_profiles WHERE account_id=? AND id=?`, c.account.ID, profile).Scan(&deleted); e != nil || deleted != 0 {
		return ProfileRestrictions{}, ErrNotVisible
	}
	return RestrictionsTx(ctx, tx, profile)
}

// SetProfileRestrictions replaces a profile's restriction document. Account
// management authority is carried by the ordinary session; a child profile's
// viewing session cannot manage the account.
//
// The change applies live. The profile's devices stay signed in; what they may
// see changes on their next request.
func (s *Service) SetProfileRestrictions(ctx context.Context, bearer, profile string, edit ProfileRestrictionEdit) (ProfileRestrictions, error) {
	if !validFamilyID(profile) {
		return ProfileRestrictions{}, ErrRestrictionInput
	}
	if e := edit.normalize(); e != nil {
		return ProfileRestrictions{}, e
	}
	gated2, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return ProfileRestrictions{}, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	c, e := s.directCallerTx(ctx, tx, bearer, true)
	if e != nil {
		return ProfileRestrictions{}, e
	}
	var owner bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM direct_memberships WHERE account_id=? AND role='owner' AND disabled=0)`, c.account.ID).Scan(&owner); e != nil {
		return ProfileRestrictions{}, e
	}
	out, e := writeRestrictionsTx(ctx, tx, c.account.ID, profile, edit, owner)
	if e != nil {
		return out, e
	}
	return out, gated2.Commit()
}

// normalize checks an edit and puts it in its stored spelling.
func (edit *ProfileRestrictionEdit) normalize() error {
	if edit.ExpectedRevision < 1 {
		return ErrRestrictionInput
	}
	labels, ok := normalizeLabels(edit.BlockedLabels)
	if !ok {
		return ErrRestrictionInput
	}
	edit.BlockedLabels = labels
	edit.RatingSystem, edit.MaximumAgeRating = strings.TrimSpace(edit.RatingSystem), strings.TrimSpace(edit.MaximumAgeRating)
	if edit.MaximumAgeRating == "" {
		edit.RatingSystem = ""
	}
	if _, ok = RatingCeiling(edit.RatingSystem, edit.MaximumAgeRating); !ok {
		return ErrRestrictionInput
	}
	return nil
}

// writeRestrictionsTx replaces one live profile's restrictions for the account
// that owns it. The caller has normalized edit and proved its authority over
// account in tx.
//
// The change applies live. The profile's devices stay signed in; what they may
// see changes on their next request.
//
// ownerGrant says the caller is the server owner, the only one who may switch
// a profile's Recordings on (Who can record); switching it off is open to anyone
// who manages the profile.
func writeRestrictionsTx(ctx context.Context, tx *sql.Tx, account, profile string, edit ProfileRestrictionEdit, ownerGrant bool) (ProfileRestrictions, error) {
	var deleted int
	if e := tx.QueryRowContext(ctx, `SELECT deleted FROM direct_profiles WHERE account_id=? AND id=?`, account, profile).Scan(&deleted); e != nil || deleted != 0 {
		return ProfileRestrictions{}, ErrNotVisible
	}
	current, e := RestrictionsTx(ctx, tx, profile)
	if e != nil {
		return ProfileRestrictions{}, e
	}
	if current.Revision != edit.ExpectedRevision {
		return ProfileRestrictions{}, ErrRestrictionChanged
	}
	if edit.AllowDVR && !current.AllowDVR && !ownerGrant {
		return ProfileRestrictions{}, ErrRecordingGrant
	}
	ceiling, _ := RatingCeiling(edit.RatingSystem, edit.MaximumAgeRating)
	raw, _ := json.Marshal(edit.BlockedLabels)
	if _, e = tx.ExecContext(ctx, `INSERT INTO profile_restrictions(profile_id,rating_system,maximum_age_rating,maximum_age,allow_unrated,blocked_labels,allow_downloads,allow_live_tv,allow_dvr,allow_watch_together,revision) VALUES(?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(profile_id) DO UPDATE SET rating_system=excluded.rating_system,maximum_age_rating=excluded.maximum_age_rating,maximum_age=excluded.maximum_age,allow_unrated=excluded.allow_unrated,blocked_labels=excluded.blocked_labels,allow_downloads=excluded.allow_downloads,allow_live_tv=excluded.allow_live_tv,allow_dvr=excluded.allow_dvr,allow_watch_together=excluded.allow_watch_together,revision=profile_restrictions.revision+1`,
		profile, edit.RatingSystem, edit.MaximumAgeRating, ceiling, edit.AllowUnrated, string(raw), edit.AllowDownloads, edit.AllowLiveTV, edit.AllowDVR, edit.AllowWatchTogether, current.Revision+1); e != nil {
		return ProfileRestrictions{}, e
	}
	// Nobody is signed out. The new restrictions apply to the profile's next
	// request: every catalogue read and every playback start reads this row,
	// and its revision is part of the fence that expires open pages, cursors
	// and cached counts (catalog.RestrictionFence).
	return RestrictionsTx(ctx, tx, profile)
}

// ErrContentRestricted is the single refusal a restricted viewer sees, whether the
// title was filtered from a list, opened by id, or asked to play. One code means a
// client can say the same clear thing everywhere, and a probe cannot distinguish
// "blocked for you" from "blocked for a different reason".
var ErrContentRestricted = errors.New("This title is outside the limits set for this profile.")

// WithProfileRestrictions exposes the complete current policy to route admission.
func (s *Service) WithProfileRestrictions(ctx context.Context, p Principal, check func(ProfileRestrictions) error) error {
	if p.Authority != "local" && p.Authority != "api-key" {
		return ErrUnauthorized
	}
	if p.ProfileID == "" {
		return ErrUnauthorized
	}

	if p.Authority == "api-key" {
		if err := apiKeyProfileBelongsToAccount(ctx, s.db, p.Viewer); err != nil {
			return err
		}
	}
	out, err := scanRestrictions(s.db.QueryRowContext(ctx, `SELECT `+restrictionColumns+` FROM profile_restrictions WHERE profile_id=?`, p.ProfileID), p.ProfileID)
	if errors.Is(err, sql.ErrNoRows) {
		out, err = defaultRestrictions(ctx, s.db, p.ProfileID)
	}
	if err != nil {
		return err
	}
	return check(out)
}
