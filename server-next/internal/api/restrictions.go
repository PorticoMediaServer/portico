package api

import (
	"errors"
	"net/http"
	"slices"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A profile's limits, the order of an account's profiles, and what deleting a profile or a
// person would remove. Limits only ever narrow what the account itself may see, and the
// server applies them to every list and title: an app shows these settings, it never hides
// anything by them itself.

// ProfileRestrictions are what a profile is kept from seeing.
type ProfileRestrictions struct {
	// MaxRatingAge is the highest age rating shown (0 to 21), or null for no limit.
	MaxRatingAge *int `json:"maxRatingAge"`
	// AllowUnrated shows titles with no rating although a limit is set. Without it a
	// profile with a limit never sees an unrated title.
	AllowUnrated bool `json:"allowUnrated"`
	// BlockedLabels are the ids of labels whose titles are never shown.
	BlockedLabels []string `json:"blockedLabels"`
	// AllLibraries, or else Libraries, says which of the account's libraries the profile is given.
	AllLibraries bool     `json:"allLibraries"`
	Libraries    []string `json:"libraries"`
	// AccountAllLibraries and AccountLibraries are what the account itself is given. A
	// library the account lacks stays out of reach whatever the profile's own list says, so
	// a settings screen can say where the account is the stricter of the two.
	AccountAllLibraries bool     `json:"accountAllLibraries"`
	AccountLibraries    []string `json:"accountLibraries"`
}

// ProfileRestrictionsEdit replaces a profile's limits: send all of them. The later save wins.
type ProfileRestrictionsEdit struct {
	MaxRatingAge  *int     `json:"maxRatingAge"`
	AllowUnrated  bool     `json:"allowUnrated"`
	BlockedLabels []string `json:"blockedLabels"`
	// AllLibraries must be true, or Libraries must be sent, so that leaving both out cannot
	// take every library away by accident. An empty list is a profile with no libraries.
	AllLibraries bool     `json:"allLibraries"`
	Libraries    []string `json:"libraries"`
}

// ProfileOrder is every profile of the account, each once, in the order to show them. The
// main profile is always shown first.
type ProfileOrder struct {
	ProfileIDs []string `json:"profileIds"`
}

// DeletionPreview counts what deleting a profile, or a person with all their profiles,
// removes for good. Nothing is deleted by asking.
type DeletionPreview struct {
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

func (s *Server) restrictionRoutes() {
	Handle(s, "GET /v1/profiles/{id}/restrictions", SignedIn, s.getRestrictions)
	Handle(s, "PUT /v1/profiles/{id}/restrictions", SignedIn, s.setRestrictions)
	Handle(s, "PUT /v1/profiles/order", SignedIn, s.orderProfiles)
	Handle(s, "GET /v1/profiles/{id}/deletion-preview", SignedIn, s.profileDeletionPreview)
	Handle(s, "GET /v1/admin/members/{id}/deletion-preview", Admin, s.memberDeletionPreview)
}

func restrictionsOut(r db.Restrictions) ProfileRestrictions {
	return ProfileRestrictions{MaxRatingAge: r.MaxRatingAge, AllowUnrated: r.AllowUnrated, BlockedLabels: r.BlockedLabels,
		AllLibraries: r.AllLibraries, Libraries: r.Libraries, AccountAllLibraries: r.AccountAllLibraries, AccountLibraries: r.AccountLibraries}
}

// getRestrictions is for whoever manages the profile, and for the profile itself: an app
// on a limited profile may read its own limits, never another profile's.
func (s *Server) getRestrictions(r *http.Request, _ None) (ProfileRestrictions, error) {
	ctx, caller := r.Context(), CallerFrom(r.Context())
	userID := caller.UserID
	if own, err := s.db.Profile(ctx, caller.UserID, r.PathValue("id")); r.URL.Query().Has("member") || err != nil || own.ID != caller.ProfileID {
		if userID, _, err = s.profileOwner(r); err != nil {
			return ProfileRestrictions{}, err
		}
	}
	out, err := s.db.ProfileRestrictions(ctx, userID, r.PathValue("id"))
	if errors.Is(err, db.ErrNotFound) {
		return ProfileRestrictions{}, ErrNotFound()
	}
	return restrictionsOut(out), err
}

func (s *Server) setRestrictions(r *http.Request, req ProfileRestrictionsEdit) (ProfileRestrictions, error) {
	userID, _, err := s.profileOwner(r)
	if err != nil {
		return ProfileRestrictions{}, err
	}
	switch {
	case req.MaxRatingAge != nil && (*req.MaxRatingAge < 0 || *req.MaxRatingAge > 21):
		return ProfileRestrictions{}, ErrField("maxRatingAge", "A rating limit is an age from 0 to 21, or null for no limit.")
	case len(req.BlockedLabels) > 64:
		return ProfileRestrictions{}, ErrField("blockedLabels", "A profile can hide at most 64 labels.")
	case !req.AllLibraries && req.Libraries == nil:
		return ProfileRestrictions{}, ErrField("libraries", "Send allLibraries as true, or the list of libraries this profile is given.")
	case len(req.Libraries) > 1000:
		return ProfileRestrictions{}, ErrField("libraries", "That is too many libraries.")
	}
	out, err := s.db.SetProfileRestrictions(r.Context(), userID, r.PathValue("id"), db.Restrictions{MaxRatingAge: req.MaxRatingAge,
		AllowUnrated: req.AllowUnrated, BlockedLabels: req.BlockedLabels, AllLibraries: req.AllLibraries, Libraries: req.Libraries}, s.now())
	switch {
	case errors.Is(err, db.ErrNotFound):
		return ProfileRestrictions{}, ErrNotFound()
	case errors.Is(err, db.ErrUnknownLibrary):
		return ProfileRestrictions{}, ErrField("libraries", "One of these libraries does not exist.")
	case errors.Is(err, db.ErrUnknownLabel):
		return ProfileRestrictions{}, ErrField("blockedLabels", "One of these labels does not exist.")
	}
	return restrictionsOut(out), err
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

func deletionOut(d db.Deletion) DeletionPreview {
	return DeletionPreview{Profiles: d.Profiles, History: d.History, InProgress: d.InProgress, Watched: d.Watched, Favorites: d.Favorites,
		MyList: d.MyList, Ratings: d.Ratings, Lists: d.Lists, Devices: d.Devices, SignIns: d.SignIns, Keys: d.Keys}
}

func (s *Server) profileDeletionPreview(r *http.Request, _ None) (DeletionPreview, error) {
	userID, _, err := s.profileOwner(r)
	if err != nil {
		return DeletionPreview{}, err
	}
	out, err := s.db.ProfileDeletion(r.Context(), userID, r.PathValue("id"))
	if errors.Is(err, db.ErrNotFound) {
		return DeletionPreview{}, ErrNotFound()
	}
	return deletionOut(out), err
}

func (s *Server) memberDeletionPreview(r *http.Request, _ None) (DeletionPreview, error) {
	caller := CallerFrom(r.Context())
	out, err := s.db.MemberDeletion(r.Context(), caller.UserID, caller.Role, r.PathValue("id"))
	return deletionOut(out), memberError(err)
}
