package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A device may remember a profile's PIN for as long as the owner allows (Settings › Sign-in).
// A main profile entered with a remembered PIN reaches every page it has a right to, owner
// and administrator pages included: a TV left on the owner's profile shows the owner's pages.
// What it cannot do is change a sensitive setting until the PIN is typed, which then counts
// for an hour on that device. Each person can turn that extra check off for themselves, which
// itself needs the PIN typed.

// pinTypedFor is how long a typed PIN counts as just typed on a device that remembers PINs.
const pinTypedFor = time.Hour

// pinRecheckSetting is a person's own switch for the check. It is on unless they turn it off.
const pinRecheckSetting = "security.askPinForSensitiveSettings"

// pinMemory is what a device keeps to show that it was allowed to remember a profile's PIN.
// It is good on that device only, and only while the PIN is the one it was made for.
type pinMemory struct {
	Profile int64  `json:"p"`
	Device  string `json:"d"`
	PIN     string `json:"h"`
	At      int64  `json:"t"`
}

func pinFingerprint(hash string) string { return auth.Hash("pin|" + hash)[:16] }

// errPINAgain asks the app for the profile's PIN: it chooses the profile again with the PIN
// and repeats what it was doing.
func errPINAgain() *Error {
	return refusal(http.StatusForbidden, "profile_pin_required", "Enter this profile's PIN to change this.", false)
}

// sensitiveRoutes are the routes that change something a person would not want changed from
// a device left on their profile: a profile's PIN and limits, people, a change of owner, the sign-in, network
// and remote access settings, keys, tokens and webhooks, restoring or deleting a backup, and removing a
// library. A function narrows a route to some of its requests. Changing the password or
// two-step sign-in is not here: both already ask for the account's password.
var sensitiveRoutes = map[string]func(*http.Request) bool{
	"DELETE /v1/profiles/{id}/pin":                     nil,
	"PUT /v1/profiles/{id}/restrictions":               nil,
	"DELETE /v1/profiles/{id}":                         nil,
	"POST /v1/admin/members":                           nil,
	"PATCH /v1/admin/members/{id}":                     nil,
	"DELETE /v1/admin/members/{id}":                    nil,
	"POST /v1/admin/members/{id}/password-reset":       nil,
	"POST /v1/admin/owner":                             nil,
	"DELETE /v1/admin/members/{id}/devices/{deviceId}": nil,
	"POST /v1/admin/invitations":                       nil,
	"PATCH /v1/admin/invitations/{id}":                 nil,
	"DELETE /v1/admin/invitations/{id}":                nil,
	"PUT /v1/admin/settings/network":                   nil,
	"PUT /v1/admin/settings/remote-access":             nil,
	"PUT /v1/admin/settings/{name}":                    func(r *http.Request) bool { return r.PathValue("name") == "sign-in" },
	"POST /v1/admin/api-keys":                          nil,
	"DELETE /v1/admin/api-keys/{id}":                   nil,
	"POST /v1/me/tokens":                               nil,
	"POST /v1/me/app-passwords":                        nil,
	"POST /v1/admin/webhooks":                          nil,
	"PATCH /v1/admin/webhooks/{id}":                    nil,
	"DELETE /v1/admin/webhooks/{id}":                   nil,
	"POST /v1/admin/webhooks/{id}/secret":              nil,
	"POST /v1/admin/backups/restore":                   nil,
	"DELETE /v1/admin/backups/{id}":                    nil,
	"DELETE /v1/admin/libraries/{id}":                  nil,
}

// pinRecheck refuses a sensitive route to a main profile that was entered with a remembered
// PIN and has not typed it lately. Setting a PIN is checked in its own handler, because
// Forgot PIN (with the account's password) must still work.
func (s *Server) pinRecheck(r *http.Request, route *Route, c Caller) error {
	if !c.PINStale {
		return nil
	}
	narrow, listed := sensitiveRoutes[route.Pattern]
	if !listed || narrow != nil && !narrow(r) {
		return nil
	}
	return s.pinStale(r, c)
}

// pinStale is the check itself: errPINAgain for a caller whose PIN is stale, unless that
// person turned the check off.
func (s *Server) pinStale(r *http.Request, c Caller) error {
	if !c.PINStale {
		return nil
	}
	off, err := s.db.ProfilePreference(r.Context(), c.ProfileID, pinRecheckSetting)
	if err != nil || off == "false" {
		return err
	}
	return errPINAgain()
}

// ProfileOrder is the account's profiles in the order its picker should show them.
type ProfileOrder struct {
	// IDs names every profile of the account once. The main profile stays first.
	IDs []string `json:"ids"`
}

func (s *Server) orderProfiles(r *http.Request, req ProfileOrder) (Page[Profile], error) {
	userID, _, err := s.profileOwner(r)
	if err != nil {
		return Page[Profile]{}, err
	}
	if len(req.IDs) > db.MaxProfiles {
		return Page[Profile]{}, ErrField("ids", "Send every profile of the account once, in the order wanted.")
	}
	profiles, err := s.db.OrderProfiles(r.Context(), userID, req.IDs, s.now())
	if errors.Is(err, db.ErrInvalid) {
		return Page[Profile]{}, ErrField("ids", "Send every profile of the account once, in the order wanted.")
	}
	return Page[Profile]{Items: profilesOut(profiles)}, err
}
