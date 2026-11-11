package api

import (
	"errors"
	"net/http"
	"slices"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Restrictions are what a profile is kept from seeing. They only narrow what the account
// may see. PUT replaces all of it, so an editor sends back what it read with its changes.
type Restrictions struct {
	// MaxRatingAge is the highest age rating shown (13 shows PG-13 and below), or null for
	// no limit. Under a limit, a title with no rating is not shown unless AllowUnrated is set.
	MaxRatingAge *int `json:"maxRatingAge"`
	// AllowUnrated shows titles with no rating although a limit is set. It is off by default.
	AllowUnrated bool `json:"allowUnrated"`
	// BlockedLabels are the ids of labels whose titles are never shown.
	BlockedLabels []string `json:"blockedLabels"`
	// AllLibraries gives the profile every library its account has. Otherwise it is given
	// only Libraries, by id.
	AllLibraries bool     `json:"allLibraries"`
	Libraries    []string `json:"libraries"`
	// AccountAllLibraries and AccountLibraries say what the account itself is given, which
	// the profile can never go beyond. They are read-only.
	AccountAllLibraries bool     `json:"accountAllLibraries"`
	AccountLibraries    []string `json:"accountLibraries"`
	// AccountMaxRatingAge, AccountAllowUnrated and AccountBlockedLabels are what the server's
	// owner holds the whole account to. They apply as well as the profile's own: the lower
	// age wins, an unrated title needs both to allow it, and a label blocked by either is
	// hidden. They are read-only here, so a screen can say where the account is stricter.
	AccountMaxRatingAge  *int     `json:"accountMaxRatingAge"`
	AccountAllowUnrated  bool     `json:"accountAllowUnrated"`
	AccountBlockedLabels []string `json:"accountBlockedLabels"`
}

// MemberLimits are what the owner or an admin holds a person's whole account to: every
// profile of it, whatever that person sets on their own profiles. Which libraries the
// account is given is part of the member edit. PUT replaces all of it.
type MemberLimits struct {
	// MaxRatingAge is the highest age rating any profile of the account is shown, or null
	// for no limit. Under a limit, a title with no rating is hidden unless AllowUnrated is set.
	MaxRatingAge *int `json:"maxRatingAge"`
	AllowUnrated bool `json:"allowUnrated"`
	// BlockedLabels are the ids of labels whose titles no profile of the account is shown.
	BlockedLabels []string `json:"blockedLabels"`
}

func memberLimitsOut(l db.AccountLimits) MemberLimits {
	return MemberLimits{MaxRatingAge: l.MaxRatingAge, AllowUnrated: l.AllowUnrated, BlockedLabels: l.BlockedLabels}
}

func (s *Server) memberLimitRoutes() {
	Handle(s, "GET /v1/admin/members/{id}/limits", Admin, func(r *http.Request, _ None) (MemberLimits, error) {
		caller := CallerFrom(r.Context())
		out, err := s.db.MemberLimits(r.Context(), caller.UserID, caller.Role, r.PathValue("id"))
		return memberLimitsOut(out), memberError(err)
	})
	sensitive(Handle(s, "PUT /v1/admin/members/{id}/limits", Admin, func(r *http.Request, req MemberLimits) (MemberLimits, error) {
		caller := CallerFrom(r.Context())
		switch {
		case req.MaxRatingAge != nil && (*req.MaxRatingAge < 0 || *req.MaxRatingAge > maxRatingAge):
			return MemberLimits{}, ErrField("maxRatingAge", "Use an age from 0 to 21, or null for no limit.")
		case len(req.BlockedLabels) > maxRestrictionEntries:
			return MemberLimits{}, ErrField("blockedLabels", "That is too many labels.")
		}
		out, err := s.db.SetMemberLimits(r.Context(), caller.UserID, caller.Role, r.PathValue("id"),
			db.AccountLimits{MaxRatingAge: req.MaxRatingAge, AllowUnrated: req.AllowUnrated, BlockedLabels: req.BlockedLabels}, s.now())
		if errors.Is(err, db.ErrUnknownLabel) {
			return MemberLimits{}, ErrField("blockedLabels", "One of these labels does not exist.")
		}
		return memberLimitsOut(out), memberError(err)
	}))
}

// The most labels or libraries one profile's restrictions can name, and the highest age limit.
const (
	maxRestrictionEntries = 200
	maxRatingAge          = 21
)

func restrictionsOut(r db.Restrictions) Restrictions {
	return Restrictions{MaxRatingAge: r.MaxRatingAge, AllowUnrated: r.AllowUnrated, BlockedLabels: r.BlockedLabels, AllLibraries: r.AllLibraries, Libraries: r.Libraries,
		AccountAllLibraries: r.AccountAllLibraries, AccountLibraries: r.AccountLibraries,
		AccountMaxRatingAge: r.Account.MaxRatingAge, AccountAllowUnrated: r.Account.AllowUnrated, AccountBlockedLabels: r.Account.BlockedLabels}
}

// restrictionRoutes are for the account's main profile, or an owner or admin acting on a
// member with ?member=. A profile can never read or lift its own limits from inside itself.
func (s *Server) restrictionRoutes() {
	s.memberLimitRoutes()
	Handle(s, "GET /v1/profiles/{id}/restrictions", SignedIn, func(r *http.Request, _ None) (Restrictions, error) {
		userID, _, err := s.profileOwner(r)
		if err != nil {
			return Restrictions{}, err
		}
		out, err := s.db.ProfileRestrictions(r.Context(), userID, r.PathValue("id"))
		return restrictionsOut(out), refuse(err)
	})
	sensitive(Handle(s, "PUT /v1/profiles/{id}/restrictions", SignedIn, func(r *http.Request, req Restrictions) (Restrictions, error) {
		userID, _, err := s.profileOwner(r)
		switch {
		case err != nil:
			return Restrictions{}, err
		case req.MaxRatingAge != nil && (*req.MaxRatingAge < 0 || *req.MaxRatingAge > maxRatingAge):
			return Restrictions{}, ErrField("maxRatingAge", "Use an age from 0 to 21, or null for no limit.")
		case len(req.BlockedLabels) > maxRestrictionEntries:
			return Restrictions{}, ErrField("blockedLabels", "That is too many labels.")
		case len(req.Libraries) > maxRestrictionEntries:
			return Restrictions{}, ErrField("libraries", "That is too many libraries.")
		}
		out, err := s.db.SetProfileRestrictions(r.Context(), userID, r.PathValue("id"), db.Restrictions{MaxRatingAge: req.MaxRatingAge,
			AllowUnrated: req.AllowUnrated, BlockedLabels: req.BlockedLabels, AllLibraries: req.AllLibraries, Libraries: req.Libraries}, s.now())
		switch {
		case errors.Is(err, db.ErrUnknownLibrary):
			return Restrictions{}, ErrField("libraries", "One of these libraries does not exist.")
		case errors.Is(err, db.ErrUnknownLabel):
			return Restrictions{}, ErrField("blockedLabels", "One of these labels does not exist.")
		}
		return restrictionsOut(out), refuse(err)
	}))
	Handle(s, "PUT /v1/profiles/order", SignedIn, s.orderProfiles)
	Handle(s, "GET /v1/profiles/{id}/deletion-preview", SignedIn, s.profileDeletionPreview)
	Handle(s, "GET /v1/admin/members/{id}/deletion-preview", Admin, s.memberDeletionPreview)
}

// ProfileOrder is every profile of the account, each once, in the order to show them. The
// main profile is always shown first.
type ProfileOrder struct {
	ProfileIDs []string `json:"profileIds"`
}

// ProfileDeletionPreview counts what deleting a profile, or a person with all their profiles,
// removes for good. Nothing is deleted by asking.
type ProfileDeletionPreview struct {
	// Profiles is how many profiles go: 1 for a profile.
	Profiles   int `json:"profiles"`
	History    int `json:"history"`
	InProgress int `json:"inProgress"`
	Watched    int `json:"watched"`
	Favorites  int `json:"favorites"`
	MyList     int `json:"myList"`
	Ratings    int `json:"ratings"`
	Lists      int `json:"lists"`
	// Devices, SignIns and Keys are counted only for a person.
	Devices int `json:"devices"`
	SignIns int `json:"signIns"`
	Keys    int `json:"keys"`
}

func (s *Server) orderProfiles(r *http.Request, req ProfileOrder) (Page[Profile], error) {
	userID, _, err := s.profileOwner(r)
	if err != nil {
		return Page[Profile]{}, err
	}
	ids := slices.Clone(req.ProfileIDs)
	slices.Sort(ids)
	if len(ids) > db.MaxProfiles || len(slices.Compact(ids)) != len(req.ProfileIDs) {
		return Page[Profile]{}, errProfileOrder()
	}
	if err := s.db.OrderProfiles(r.Context(), userID, req.ProfileIDs); errors.Is(err, db.ErrConflict) {
		return Page[Profile]{}, errProfileOrder()
	} else if err != nil {
		return Page[Profile]{}, err
	}
	profiles, err := s.db.Profiles(r.Context(), userID)
	return Page[Profile]{Items: profilesOut(profiles)}, err
}

func errProfileOrder() *Error {
	return ErrField("profileIds", "Send every profile of the account once, in the order to show them.")
}

func deletionOut(d db.ProfileDeletion) ProfileDeletionPreview {
	return ProfileDeletionPreview{Profiles: d.Profiles, History: d.History, InProgress: d.InProgress, Watched: d.Watched, Favorites: d.Favorites,
		MyList: d.MyList, Ratings: d.Ratings, Lists: d.Lists, Devices: d.Devices, SignIns: d.SignIns, Keys: d.Keys}
}

func (s *Server) profileDeletionPreview(r *http.Request, _ None) (ProfileDeletionPreview, error) {
	userID, _, err := s.profileOwner(r)
	if err != nil {
		return ProfileDeletionPreview{}, err
	}
	out, err := s.db.ProfileDeletion(r.Context(), userID, r.PathValue("id"))
	return deletionOut(out), refuse(err)
}

func (s *Server) memberDeletionPreview(r *http.Request, _ None) (ProfileDeletionPreview, error) {
	caller := CallerFrom(r.Context())
	out, err := s.db.MemberDeletion(r.Context(), caller.UserID, caller.Role, r.PathValue("id"))
	return deletionOut(out), memberError(err)
}
