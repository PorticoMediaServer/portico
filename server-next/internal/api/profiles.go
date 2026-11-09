package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Profile is a viewing profile of an account.
type Profile struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	// IsPrimary marks the account's main profile, which cannot be deleted.
	IsPrimary bool `json:"isPrimary"`
	// PINRequired says selecting this profile needs its PIN.
	PINRequired bool `json:"pinRequired"`
}

// ProfileCreate adds a profile. Send an Idempotency-Key so a repeat does not add a second one.
type ProfileCreate struct {
	Name string `json:"name"`
	// Color is one of blue, violet, mint, coral, gold, slate, rose, sky. It defaults to blue.
	Color string `json:"color,omitempty"`
	// PIN is four digits, or absent for no PIN.
	PIN string `json:"pin,omitempty"`
}

// ProfileEdit changes a profile. Fields left out stay as they are; the last edit wins.
type ProfileEdit struct {
	Name  *string `json:"name,omitempty"`
	Color *string `json:"color,omitempty"`
}

// ProfilePIN sets or changes a profile's PIN: four digits.
type ProfilePIN struct {
	PIN string `json:"pin"`
}

// ProfilePINRemove removes a profile's PIN. Whoever may manage the account sends no body.
// Anyone else on the account, such as someone who forgot the main profile's PIN, sends the
// account's password.
type ProfilePINRemove struct {
	CurrentPassword string `json:"currentPassword,omitempty"`
}

// ProfileSelect chooses the profile this session acts for.
type ProfileSelect struct {
	// PIN is needed only when the profile's pinRequired is true.
	PIN string `json:"pin,omitempty"`
}

var profileColors = []string{"blue", "violet", "mint", "coral", "gold", "slate", "rose", "sky"}

func profileOut(p db.Profile) Profile {
	return Profile{ID: p.PublicID, Name: p.Name, Color: p.Color, IsPrimary: p.Primary, PINRequired: p.PINHash != ""}
}

func profilesOut(profiles []db.Profile) []Profile {
	out := make([]Profile, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, profileOut(p))
	}
	return out
}

func (s *Server) profileRoutes() {
	Handle(s, "GET /v1/profiles", SignedIn, s.listProfiles)
	Handle(s, "POST /v1/profiles", SignedIn, s.createProfile)
	Handle(s, "GET /v1/profiles/{id}", SignedIn, s.getProfile)
	Handle(s, "PATCH /v1/profiles/{id}", SignedIn, s.editProfile)
	Handle(s, "PUT /v1/profiles/{id}/pin", SignedIn, s.setProfilePIN)
	Handle(s, "DELETE /v1/profiles/{id}/pin", SignedIn, s.removeProfilePIN)
	Handle(s, "DELETE /v1/profiles/{id}", SignedIn, s.deleteProfile)
	Handle(s, "POST /v1/profiles/{id}/select", SignedIn, s.selectProfile)
}

// manager returns the caller if this session may change the account itself. A profile other
// than the main one may not, and neither may a sign-in that has not passed the main
// profile's PIN: knowing a profile's id is never enough.
func manager(r *http.Request) (Caller, error) {
	c := CallerFrom(r.Context())
	if !c.Manage {
		return c, ErrNotPermitted()
	}
	return c, nil
}

// profileOwner says whose profiles a profile route acts on: the caller's own, or, with
// ?member=<account id>, those of an account the caller manages as owner or admin. It
// returns that account's id and the session to leave alone when a PIN change sends other
// devices back to the chooser.
func (s *Server) profileOwner(r *http.Request) (userID, sessionID int64, err error) {
	caller := CallerFrom(r.Context())
	member := r.URL.Query().Get("member")
	if member == "" {
		if !caller.Manage {
			return 0, 0, ErrNotPermitted()
		}
		return caller.UserID, caller.SessionID, nil
	}
	m, err := s.db.ManagedMember(r.Context(), caller.UserID, caller.Role, member)
	return m.ID, 0, memberError(err)
}

func (s *Server) listProfiles(r *http.Request, _ None) (Page[Profile], error) {
	userID := CallerFrom(r.Context()).UserID
	if r.URL.Query().Has("member") {
		var err error
		if userID, _, err = s.profileOwner(r); err != nil {
			return Page[Profile]{}, err
		}
	}
	profiles, err := s.db.Profiles(r.Context(), userID)
	return Page[Profile]{Items: profilesOut(profiles)}, err
}

func checkProfileFields(name, color, pin *string) error {
	switch {
	case name != nil && !auth.ValidName(*name):
		return ErrField("name", "Give the profile a name of at most 120 characters.")
	case color != nil && !slices.Contains(profileColors, *color):
		return ErrField("color", "Choose one of the profile colors.")
	case pin != nil && *pin != "" && !auth.ValidPIN(*pin):
		return ErrField("pin", "A PIN is four digits.")
	}
	return nil
}

func (s *Server) createProfile(r *http.Request, req ProfileCreate) (Profile, error) {
	ctx := r.Context()
	userID, _, err := s.profileOwner(r)
	if err != nil {
		return Profile{}, err
	}
	if req.Color == "" {
		req.Color = "blue"
	}
	if err := checkProfileFields(&req.Name, &req.Color, &req.PIN); err != nil {
		return Profile{}, err
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 128 {
		return Profile{}, ErrInvalid("The Idempotency-Key is at most 128 characters.")
	}
	n := db.NewProfile{UserID: userID, Name: req.Name, Color: req.Color, IdempotencyKey: key, RequestHash: requestHash(req), Now: s.now()}
	if req.PIN != "" {
		if n.PINHash, err = s.passwords.HashPIN(ctx, req.PIN); err != nil {
			return Profile{}, err
		}
	}
	p, err := s.db.CreateProfile(ctx, n)
	switch {
	case errors.Is(err, db.ErrProfileLimit):
		return Profile{}, ErrConflict("profile_capacity", "An account can have up to eight profiles.")
	case errors.Is(err, db.ErrKeyReused):
		return Profile{}, errKeyReused()
	}
	return profileOut(p), err
}

// requestHash identifies a request body, so an Idempotency-Key cannot be reused for a different one.
func requestHash(req any) string {
	raw, _ := json.Marshal(req)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func errKeyReused() *Error {
	return refusal(http.StatusUnprocessableEntity, "idempotency_key_reused", "This request conflicts with an earlier request.", false)
}

func (s *Server) editProfile(r *http.Request, req ProfileEdit) (Profile, error) {
	ctx := r.Context()
	userID, sessionID, err := s.profileOwner(r)
	if err != nil {
		return Profile{}, err
	}
	if err := checkProfileFields(req.Name, req.Color, nil); err != nil {
		return Profile{}, err
	}
	e := db.ProfileEdit{UserID: userID, PublicID: r.PathValue("id"), Name: req.Name, Color: req.Color, SessionID: sessionID, Now: s.now()}
	p, err := s.db.UpdateProfile(ctx, e)
	if errors.Is(err, db.ErrNotFound) {
		return Profile{}, ErrNotFound()
	}
	return profileOut(p), err
}

func (s *Server) getProfile(r *http.Request, _ None) (Profile, error) {
	userID := CallerFrom(r.Context()).UserID
	if r.URL.Query().Has("member") {
		var err error
		if userID, _, err = s.profileOwner(r); err != nil {
			return Profile{}, err
		}
	}
	p, err := s.db.Profile(r.Context(), userID, r.PathValue("id"))
	if errors.Is(err, db.ErrNotFound) {
		return Profile{}, ErrNotFound()
	}
	return profileOut(p), err
}

// setProfilePIN sets or changes a PIN. Other devices on the profile go back to the chooser.
func (s *Server) setProfilePIN(r *http.Request, req ProfilePIN) (Profile, error) {
	if !auth.ValidPIN(req.PIN) {
		return Profile{}, ErrField("pin", "A PIN is four digits.")
	}
	hash, err := s.passwords.HashPIN(r.Context(), req.PIN)
	if err != nil {
		return Profile{}, err
	}
	return s.changeProfilePIN(r, hash)
}

func (s *Server) changeProfilePIN(r *http.Request, hash string) (Profile, error) {
	userID, sessionID, err := s.profileOwner(r)
	if err != nil {
		return Profile{}, err
	}
	p, err := s.db.UpdateProfile(r.Context(), db.ProfileEdit{UserID: userID, PublicID: r.PathValue("id"), PINHash: &hash,
		SessionID: sessionID, Now: s.now()})
	if errors.Is(err, db.ErrNotFound) {
		return Profile{}, ErrNotFound()
	}
	return profileOut(p), err
}

func (s *Server) deleteProfile(r *http.Request, _ None) (None, error) {
	userID, _, err := s.profileOwner(r)
	if err != nil {
		return None{}, err
	}
	err = s.db.DeleteProfile(r.Context(), userID, r.PathValue("id"), s.now())
	if errors.Is(err, db.ErrPrimaryProfile) {
		return None{}, ErrConflict("primary_profile", "The main profile cannot be deleted.")
	}
	return None{}, err
}

func errBadPIN() *Error {
	return refusal(http.StatusForbidden, "profile_pin_invalid", "That PIN is not correct.", false)
}

func errPINWait(wait time.Duration) *Error {
	return ErrTooMany("profile_pin_locked", "Too many wrong PINs. Wait, then try again.", wait)
}

// selectProfile binds this session to one of the account's profiles and answers with an
// access token for it. The refresh token stays as it is.
func (s *Server) selectProfile(r *http.Request, req ProfileSelect) (Session, error) {
	ctx, now, caller := r.Context(), s.now(), CallerFrom(r.Context())
	p, err := s.db.Profile(ctx, caller.UserID, r.PathValue("id"))
	if errors.Is(err, db.ErrNotFound) {
		return Session{}, ErrNotFound()
	}
	if err != nil {
		return Session{}, err
	}
	sel := db.ProfileSelection{UserID: caller.UserID, SessionID: caller.SessionID, ProfileID: p.ID, CheckedPINHash: p.PINHash, Now: now}
	if p.PINHash != "" {
		if req.PIN == "" {
			return Session{}, errBadPIN()
		}
		// The count belongs to the profile, not to who is asking: a new device or sign-in
		// does not get a fresh set of guesses at four digits.
		sel.Limits = []db.Limit{{Bucket: "pin", Key: "profile:" + strconv.FormatInt(p.ID, 10), Delay: auth.PINDelay}}
		if wait, err := s.db.LimitWait(ctx, now, sel.Limits...); err != nil {
			return Session{}, err
		} else if wait > 0 {
			return Session{}, errPINWait(wait)
		}
		if sel.PINOK, err = s.passwords.Check(ctx, p.PINHash, req.PIN); err != nil {
			return Session{}, err
		}
	}
	st, err := s.db.SelectProfile(ctx, sel)
	var limited *db.LimitedError
	switch {
	case errors.As(err, &limited):
		return Session{}, errPINWait(limited.Wait)
	case errors.Is(err, db.ErrBadPIN):
		return Session{}, errBadPIN()
	case errors.Is(err, db.ErrNotFound):
		return Session{}, ErrNotFound()
	case errors.Is(err, db.ErrBadCredentials):
		return Session{}, errSignInEnded()
	case err != nil:
		return Session{}, err
	}
	return s.session(st, now, ""), nil
}

// removeProfilePIN removes a PIN. With the account's password it is also the way back in
// when a PIN is forgotten, the main profile's included; the account's sign-ins then fetch
// fresh tokens, this one too.
func (s *Server) removeProfilePIN(r *http.Request, req ProfilePINRemove) (None, error) {
	if req.CurrentPassword == "" {
		_, err := s.changeProfilePIN(r, "")
		return None{}, err
	}
	proof, err := s.passwordProof(r, req.CurrentPassword)
	if err != nil {
		return None{}, err
	}
	_, err = s.db.ClearProfilePIN(r.Context(), proof, r.PathValue("id"), s.now())
	var limited *db.LimitedError
	switch {
	case errors.As(err, &limited):
		return None{}, errTooManyAttempts(limited.Wait)
	case errors.Is(err, db.ErrBadCredentials):
		return None{}, errWrongCurrentPassword()
	case errors.Is(err, db.ErrNotFound):
		return None{}, ErrNotFound()
	}
	return None{}, err
}
