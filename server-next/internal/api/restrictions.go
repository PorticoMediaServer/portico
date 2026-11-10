package api

import (
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Restrictions are what a profile is kept from seeing. They only narrow what the account
// may see. PUT replaces all of it, so an editor sends back what it read with its changes.
type Restrictions struct {
	// MaxRatingAge is the highest age rating shown (13 shows PG-13 and below), or null for
	// no limit. Under a limit, a title with no rating is not shown.
	MaxRatingAge *int `json:"maxRatingAge"`
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
}

// The most labels or libraries one profile's restrictions can name, and the highest age limit.
const (
	maxRestrictionEntries = 200
	maxRatingAge          = 21
)

func restrictionsOut(r db.Restrictions) Restrictions {
	return Restrictions{MaxRatingAge: r.MaxRatingAge, BlockedLabels: r.BlockedLabels, AllLibraries: r.AllLibraries, Libraries: r.Libraries,
		AccountAllLibraries: r.AccountAllLibraries, AccountLibraries: r.AccountLibraries}
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
	Handle(s, "PUT /v1/profiles/{id}/restrictions", SignedIn, func(r *http.Request, req Restrictions) (Restrictions, error) {
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
			BlockedLabels: req.BlockedLabels, AllLibraries: req.AllLibraries, Libraries: req.Libraries}, s.now())
		switch {
		case errors.Is(err, db.ErrUnknownLibrary):
			return Restrictions{}, ErrField("libraries", "One of these libraries does not exist.")
		case errors.Is(err, db.ErrUnknownLabel):
			return Restrictions{}, ErrField("blockedLabels", "One of these labels does not exist.")
		}
		return restrictionsOut(out), refuse(err)
	})
}
