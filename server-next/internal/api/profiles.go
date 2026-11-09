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

// ProfilePIN sets or changes a profile's PIN: four digits. From the main profile that is
// all it takes. A forgotten PIN is replaced from any profile, or from the profile chooser,
// by sending the account's password as well; the device stays on the profile it is on.
type ProfilePIN struct {
	PIN             string `json:"pin"`
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

// manager returns the caller if this session may change the account itself: only a session
// on the account's main profile may. A sign-in still at the profile chooser may not, whether
// or not the main profile has a PIN.
func manager(r *http.Request) (Caller, error) {
	c := CallerFrom(r.Context())
	if !c.Manage {
		return c, ErrNotPermitted()
	}
	return c, nil
}

func (s *Server) listProfiles(r *http.Request, _ None) (Page[Profile], error) {
	profiles, err := s.db.Profiles(r.Context(), CallerFrom(r.Context()).UserID)
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
	caller, err := manager(r)
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
	n := db.NewProfile{UserID: caller.UserID, Name: req.Name, Color: req.Color, IdempotencyKey: key, RequestHash: requestHash(req), Now: s.now()}
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
	caller, err := manager(r)
	if err != nil {
		return Profile{}, err
	}
	if err := checkProfileFields(req.Name, req.Color, nil); err != nil {
		return Profile{}, err
	}
	e := db.ProfileEdit{UserID: caller.UserID, PublicID: r.PathValue("id"), Name: req.Name, Color: req.Color, SessionID: caller.SessionID, Now: s.now()}
	p, err := s.db.UpdateProfile(ctx, e)
	if errors.Is(err, db.ErrNotFound) {
		return Profile{}, ErrNotFound()
	}
	return profileOut(p), err
}

func (s *Server) getProfile(r *http.Request, _ None) (Profile, error) {
	p, err := s.db.Profile(r.Context(), CallerFrom(r.Context()).UserID, r.PathValue("id"))
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
	var proof *db.PasswordProof
	if req.CurrentPassword != "" {
		p, err := s.passwordProof(r, req.CurrentPassword)
		if err != nil {
			return Profile{}, err
		}
		proof = &p
	}
	hash, err := s.passwords.HashPIN(r.Context(), req.PIN)
	if err != nil {
		return Profile{}, err
	}
	return s.changeProfilePIN(r, hash, proof)
}

func (s *Server) removeProfilePIN(r *http.Request, _ None) (None, error) {
	_, err := s.changeProfilePIN(r, "", nil)
	return None{}, err
}

// changeProfilePIN is for the main profile, or for anyone on the account who proves its password.
func (s *Server) changeProfilePIN(r *http.Request, hash string, proof *db.PasswordProof) (Profile, error) {
	caller := CallerFrom(r.Context())
	if proof == nil && !caller.Manage {
		return Profile{}, ErrNotPermitted()
	}
	p, err := s.db.UpdateProfile(r.Context(), db.ProfileEdit{UserID: caller.UserID, PublicID: r.PathValue("id"), PINHash: &hash,
		SessionID: caller.SessionID, Proof: proof, Now: s.now()})
	if errors.Is(err, db.ErrNotFound) {
		return Profile{}, ErrNotFound()
	}
	return profileOut(p), proofError(err)
}

func (s *Server) deleteProfile(r *http.Request, _ None) (None, error) {
	caller, err := manager(r)
	if err != nil {
		return None{}, err
	}
	err = s.db.DeleteProfile(r.Context(), caller.UserID, r.PathValue("id"), s.now())
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
		// Wrong PINs are counted for this profile on this device, so nobody can make the
		// profile wait on another device, and signing in again on this one starts no new count.
		session, err := s.db.SessionState(ctx, caller.SessionID, now)
		if errors.Is(err, db.ErrNotFound) {
			return Session{}, errSignInEnded()
		}
		if err != nil {
			return Session{}, err
		}
		sel.Limits = append([]db.Limit{{Bucket: "pin", Key: "profile:" + strconv.FormatInt(p.ID, 10) + "|" + session.DevicePublicID,
			Delay: auth.StandardPINs.Delay, Lasting: true}}, sourceLimit(r)...)
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
