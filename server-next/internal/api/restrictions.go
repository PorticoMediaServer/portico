package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Restrictions are what a profile is kept from seeing. They only narrow what the account
// may see. A save (ProfileRestrictionsEdit) changes only the fields it names.
type Restrictions struct {
	// MaxRatingAge is the highest age rating shown (13 shows PG-13 and below), or null for
	// no limit. Under a limit, a title with no rating is not shown unless AllowUnrated is set.
	MaxRatingAge *int `json:"maxRatingAge"`
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
	// AllowDownloads lets the profile download, within the account's own switch
	// (AccountAllowDownloads, read-only). Left out of a change, it stays as it is. Turned
	// off, the profile's devices remove its downloads the next time they reach the server.
	AllowDownloads        *bool `json:"allowDownloads,omitempty"`
	AccountAllowDownloads bool  `json:"accountAllowDownloads"`
	// AllowWatchTogether lets the profile start and join Watch Together groups. Left out of
	// a PUT, it stays as it is. Turning it off takes the profile out of its groups at once.
	// AccountAllowWatchTogether is the account's own switch, read-only here, which the
	// profile's cannot lift.
	AllowWatchTogether        *bool `json:"allowWatchTogether,omitempty"`
	AccountAllowWatchTogether bool  `json:"accountAllowWatchTogether"`
	// AccountMaxRatingAge, AccountAllowUnrated and AccountBlockedLabels are what the owner or
	// an administrator holds the whole account to (see MemberLimits). They apply as well as
	// the profile's own: the lower age wins, an unrated title needs both to allow it, and a
	// label blocked by either is hidden. They are read-only here, so a screen can say where
	// the account is the stricter.
	AccountMaxRatingAge  *int     `json:"accountMaxRatingAge"`
	AccountAllowUnrated  bool     `json:"accountAllowUnrated"`
	AccountBlockedLabels []string `json:"accountBlockedLabels"`
}

// ProfileRestrictionsEdit is a save of a profile's restrictions. Only the fields sent change:
// one left out stays as it is, so a sheet opened earlier on another device cannot undo what
// was saved since by sending back what it read. maxRatingAge null lifts the rating limit;
// blockedLabels and libraries, when sent, are the whole list. The later save of a field wins.
type ProfileRestrictionsEdit struct {
	MaxRatingAge   NullableInt `json:"maxRatingAge,omitempty"`
	AllowUnrated   *bool       `json:"allowUnrated,omitempty"`
	BlockedLabels  *[]string   `json:"blockedLabels,omitempty"`
	AllLibraries   *bool       `json:"allLibraries,omitempty"`
	Libraries      *[]string   `json:"libraries,omitempty"`
	AllowDownloads *bool       `json:"allowDownloads,omitempty"`
	// AllowWatchTogether turned off takes the profile out of its groups at once.
	AllowWatchTogether *bool `json:"allowWatchTogether,omitempty"`

	// sent is the save as it arrived, which alone tells a field left out from one sent as null.
	sent json.RawMessage
}

func (e *ProfileRestrictionsEdit) UnmarshalJSON(raw []byte) error {
	type fields ProfileRestrictionsEdit
	if err := json.Unmarshal(raw, (*fields)(e)); err != nil {
		return err
	}
	e.sent = append(json.RawMessage(nil), raw...)
	return nil
}

// over lays the save over the profile's restrictions as they are and checks the result.
func (e *ProfileRestrictionsEdit) over(current db.Restrictions) (db.Restrictions, error) {
	// Only what a save may change is carried over: what the account is held to is not the
	// profile's to send.
	next := struct {
		MaxRatingAge       *int     `json:"maxRatingAge"`
		AllowUnrated       bool     `json:"allowUnrated"`
		BlockedLabels      []string `json:"blockedLabels"`
		AllLibraries       bool     `json:"allLibraries"`
		Libraries          []string `json:"libraries"`
		AllowDownloads     *bool    `json:"allowDownloads"`
		AllowWatchTogether *bool    `json:"allowWatchTogether"`
	}{current.MaxRatingAge, current.AllowUnrated, current.BlockedLabels, current.AllLibraries, current.Libraries, nil, nil}
	if err := json.Unmarshal(e.sent, &next); err != nil {
		return current, ErrInvalid("These restrictions could not be read.")
	}
	switch {
	case next.MaxRatingAge != nil && (*next.MaxRatingAge < 0 || *next.MaxRatingAge > maxRatingAge):
		return current, ErrField("maxRatingAge", "Use an age from 0 to 21, or null for no limit.")
	case len(next.BlockedLabels) > maxRestrictionEntries:
		return current, ErrField("blockedLabels", "That is too many labels.")
	case len(next.Libraries) > maxRestrictionEntries:
		return current, ErrField("libraries", "That is too many libraries.")
	}
	return db.Restrictions{MaxRatingAge: next.MaxRatingAge, AllowUnrated: next.AllowUnrated, BlockedLabels: next.BlockedLabels,
		AllLibraries: next.AllLibraries, Libraries: next.Libraries, AllowDownloads: next.AllowDownloads,
		AllowWatchTogether: next.AllowWatchTogether}, nil
}

// The most labels or libraries one profile's restrictions can name, and the highest age limit.
const (
	maxRestrictionEntries = 200
	maxRatingAge          = 21
)

func restrictionsOut(r db.Restrictions) Restrictions {
	return Restrictions{MaxRatingAge: r.MaxRatingAge, AllowUnrated: r.AllowUnrated, BlockedLabels: r.BlockedLabels, AllLibraries: r.AllLibraries,
		Libraries: r.Libraries, AccountAllLibraries: r.AccountAllLibraries, AccountLibraries: r.AccountLibraries,
		AllowDownloads: r.AllowDownloads, AccountAllowDownloads: r.AccountAllowDownloads,
		AllowWatchTogether: r.AllowWatchTogether, AccountAllowWatchTogether: r.AccountAllowWatchTogether,
		AccountMaxRatingAge: r.AccountMaxRatingAge, AccountAllowUnrated: r.AccountAllowUnrated, AccountBlockedLabels: r.AccountBlockedLabels}
}

// restrictionRoutes are for the account's main profile, or an owner or admin acting on a
// member with ?member=. A profile can never read or lift its own limits from inside itself.
func (s *Server) restrictionRoutes() {
	Handle(s, "GET /v1/profiles/{id}/restrictions", SignedIn, func(r *http.Request, _ None) (Restrictions, error) {
		userID, _, err := s.profileOwner(r)
		if err != nil {
			return Restrictions{}, err
		}
		out, err := s.db.ProfileRestrictions(r.Context(), userID, r.PathValue("id"))
		return restrictionsOut(out), refuse(err)
	})
	Handle(s, "PUT /v1/profiles/{id}/restrictions", SignedIn, func(r *http.Request, req ProfileRestrictionsEdit) (Restrictions, error) {
		userID, _, err := s.profileOwner(r)
		if err != nil {
			return Restrictions{}, err
		}
		out, err := s.db.ChangeProfileRestrictions(r.Context(), userID, r.PathValue("id"), req.over, s.now())
		if err == nil {
			if p, err := s.db.Profile(r.Context(), userID, r.PathValue("id")); err == nil {
				if req.AllowWatchTogether != nil && !*req.AllowWatchTogether {
					s.groups.LeaveAll(p.ID)
				}
				// What the profile may see changed: its groups wait for it, or go ahead
				// without it, by what it may see now.
				for _, g := range s.groups.List(p.ID) {
					s.checkCantSee(r.Context(), g)
				}
			}
		}
		switch {
		case errors.Is(err, db.ErrUnknownLibrary):
			return Restrictions{}, ErrField("libraries", "One of these libraries does not exist.")
		case errors.Is(err, db.ErrUnknownLabel):
			return Restrictions{}, ErrField("blockedLabels", "One of these labels does not exist.")
		}
		return restrictionsOut(out), refuse(err)
	})
}
