package api

import (
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// MemberLimits are what the owner or an administrator holds a person's whole account to:
// every profile of it, the main one included, whatever that person sets on their own
// profiles. Only someone who manages the account can change them (PATCH on the member with
// "limits"), so nobody lifts their own. A PATCH that carries limits replaces all of them: an
// editor sends back what it read with its changes. Which libraries the account is given is
// beside them on the member.
type MemberLimits struct {
	// HomeOnly: the account works on the home network only. Away from home it cannot sign in
	// and a device already signed in gets no new token; it keeps its sign-in for when it is back.
	HomeOnly bool `json:"homeOnly"`
	// ViewingHours are the times of the week the account may be used, or null for any time.
	// Outside them it is refused as away from home is, and keeps its sign-in for later.
	ViewingHours *auth.Hours `json:"viewingHours"`
	// MaxRatingAge is the highest age rating any profile is shown (13 shows PG-13 and below),
	// or null for no limit. Under a limit a title with no rating is hidden unless
	// AllowUnrated is set. A profile's own limit applies as well: the lower one wins.
	MaxRatingAge *int `json:"maxRatingAge"`
	AllowUnrated bool `json:"allowUnrated"`
	// BlockedLabels are the ids of labels whose titles no profile of the account is shown.
	BlockedLabels []string `json:"blockedLabels"`
	// AllowConversion false: the account plays only what its device plays without converting.
	AllowConversion bool `json:"allowConversion"`
	// RemoteMaxBitrateKbps is the most video bitrate the account gets away from home, or
	// null for no cap of its own.
	RemoteMaxBitrateKbps *int `json:"remoteMaxBitrateKbps"`
	// MaxProfiles is how many profiles the account may have, 1 to 8, or null for 8. Lowering
	// it removes no profile.
	MaxProfiles *int `json:"maxProfiles"`
	// What the account may do at all. A profile's own switch can only narrow these.
	AllowDownloads     bool `json:"allowDownloads"`
	AllowWatchTogether bool `json:"allowWatchTogether"`
	// AllowTokens: the account's people may make personal tokens and app passwords.
	AllowTokens bool `json:"allowTokens"`
}

func memberLimitsOut(l db.AccountLimits) MemberLimits {
	labels := l.BlockedLabels
	if labels == nil {
		labels = []string{}
	}
	return MemberLimits{HomeOnly: l.HomeOnly, ViewingHours: l.Hours, MaxRatingAge: l.MaxRatingAge, AllowUnrated: l.AllowUnrated,
		BlockedLabels: labels, AllowConversion: l.AllowConversion, RemoteMaxBitrateKbps: l.RemoteMaxBitrateKbps, MaxProfiles: l.MaxProfiles,
		AllowDownloads: l.AllowDownloads, AllowWatchTogether: l.AllowWatchTogether, AllowTokens: l.AllowTokens}
}

// memberLimitsIn checks limits sent with a member edit. Hours with no windows mean any time.
func memberLimitsIn(l *MemberLimits) (*db.AccountLimits, error) {
	switch {
	case l == nil:
		return nil, nil
	case l.ViewingHours != nil && !l.ViewingHours.Valid():
		return nil, ErrField("limits.viewingHours", "Give a known timezone and up to 64 windows, each with days 0 to 6 and a start and end in minutes after midnight.")
	case l.MaxRatingAge != nil && (*l.MaxRatingAge < 0 || *l.MaxRatingAge > maxRatingAge):
		return nil, ErrField("limits.maxRatingAge", "Use an age from 0 to 21, or null for no limit.")
	case len(l.BlockedLabels) > maxRestrictionEntries:
		return nil, ErrField("limits.blockedLabels", "That is too many labels.")
	case l.RemoteMaxBitrateKbps != nil && (*l.RemoteMaxBitrateKbps < 100 || *l.RemoteMaxBitrateKbps > 1_000_000):
		return nil, ErrField("limits.remoteMaxBitrateKbps", "Use 100 to 1,000,000 kbps, or null for no cap.")
	case l.MaxProfiles != nil && (*l.MaxProfiles < 1 || *l.MaxProfiles > db.MaxProfiles):
		return nil, ErrField("limits.maxProfiles", "Use 1 to 8, or null for 8.")
	}
	return &db.AccountLimits{HomeOnly: l.HomeOnly, Hours: l.ViewingHours, MaxRatingAge: l.MaxRatingAge, AllowUnrated: l.AllowUnrated,
		BlockedLabels: l.BlockedLabels, AllowConversion: l.AllowConversion, RemoteMaxBitrateKbps: l.RemoteMaxBitrateKbps, MaxProfiles: l.MaxProfiles,
		AllowDownloads: l.AllowDownloads, AllowWatchTogether: l.AllowWatchTogether, AllowTokens: l.AllowTokens}, nil
}

// accountLimitRefusal turns the two ways an account's limits stop a sign-in, a renewal, a
// choice of profile or a key into their answers, wherever they came from. Both keep the
// sign-in the device holds, so an app shows the reason and tries again later.
func (s *Server) accountLimitRefusal(r *http.Request, err error) error {
	switch {
	case errors.Is(err, db.ErrOutsideHours):
		return refusal(http.StatusForbidden, "outside_viewing_hours", "Viewing hours have ended for this account.", false)
	case errors.Is(err, db.ErrAwayFromHome):
		message := "This account can only be used on the home network."
		if s.networks().ThroughUnlistedProxy(r) {
			// At home behind a proxy the owner has not listed, everyone counts as away.
			message += " If you are at home, ask the server's owner to add its proxy under Remote access."
		}
		return refusal(http.StatusForbidden, "away_from_home", message, false)
	}
	return err
}
