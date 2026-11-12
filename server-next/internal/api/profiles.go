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
	// AccessMarker changes whenever what the profile may see changes: its libraries, its
	// limits or its account's role. It means nothing else. An app keeps it with whatever it
	// has cached for the profile and throws the cache away when it differs.
	AccessMarker string `json:"accessMarker"`
	// Avatar is the address of the profile's picture, absent when it has none. It is fetched
	// signed in, like any other request, with size=small (the default) or size=large; the
	// address changes when the picture does, so an app can keep what it fetched.
	Avatar string `json:"avatar,omitempty"`
	// AvatarRef is for a server whose people sign in with a Portico Account: there a picture
	// belongs to the person's Portico Account and is kept by Portico's hosted service, never
	// by this server, and this is the reference an app uses to load it from there. Nothing
	// fills it in yet: Portico Account sign-in and the hosted side are not built in this
	// server, and what the reference holds is theirs to define.
	AvatarRef string `json:"avatarRef,omitempty"`
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
	// Code is the authenticator or recovery code, needed with the password when the
	// account has two-step sign-in.
	Code string `json:"code,omitempty"`
	// Challenge and Identity, on a server that uses Portico Accounts, prove the account in
	// place of the password: a challenge from POST /v1/auth/portico/challenge and the sign-in
	// note Portico Services signed for it, carrying passwordChecked after checking the
	// password typed with that note request (and two-step if on).
	Challenge string          `json:"challenge,omitempty"`
	Identity  json.RawMessage `json:"identity,omitempty"`
}

// ProfileSelect chooses the profile this session acts for.
type ProfileSelect struct {
	// PIN is needed only when the profile's pinRequired is true.
	PIN string `json:"pin,omitempty"`
	// RememberPIN asks that this device need not ask for the PIN again for as long as the
	// owner allows. The answer then carries a pinToken to keep.
	RememberPIN bool `json:"rememberPin,omitempty"`
	// PINToken is the pinToken kept from an earlier answer, sent in place of the PIN.
	PINToken string `json:"pinToken,omitempty"`
}

var profileColors = []string{"blue", "violet", "mint", "coral", "gold", "slate", "rose", "sky"}

func profileOut(p db.Profile) Profile {
	out := Profile{ID: p.PublicID, Name: p.Name, Color: p.Color, IsPrimary: p.Primary, PINRequired: p.PINHash != "",
		AccessMarker: strconv.FormatInt(p.AccessChangedAt, 36)}
	if p.Picture != 0 {
		out.Avatar = "/v1/profiles/" + p.PublicID + "/avatar?v=" + strconv.FormatInt(p.Picture, 36)
	}
	return out
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
	Handle(s, "PUT /v1/profiles/order", SignedIn, s.orderProfiles)
	s.restrictionRoutes()
	s.profilePictureRoutes()
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

// errProfileCapacity says how many profiles the account may have: eight, or fewer where
// the owner or an administrator lowered it.
func errProfileCapacity(err error) *Error {
	var limit *db.ProfileLimitError
	if errors.As(err, &limit) && limit.Limit == 1 {
		return ErrConflict("profile_capacity", "This account can have only its main profile.")
	} else if limit != nil && limit.Limit < db.MaxProfiles {
		return ErrConflict("profile_capacity", "This account already has "+strconv.Itoa(limit.Limit)+" profiles, the most it may have.")
	}
	return ErrConflict("profile_capacity", "An account can have up to eight profiles.")
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
		return Profile{}, errProfileCapacity(err)
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

// idempotencyKey reads the Idempotency-Key a creating request may carry.
func idempotencyKey(r *http.Request) (string, error) {
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 128 {
		return "", ErrInvalid("The Idempotency-Key is at most 128 characters.")
	}
	return key, nil
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
	if len(req.Identity) > 0 {
		return s.porticoForgotPIN(r, req)
	}
	var proof *db.PasswordProof
	if req.CurrentPassword != "" {
		p, err := s.passwordProof(r, req.CurrentPassword, req.Code)
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

// changeProfilePIN is for the main profile, for an owner or admin acting on a member, or
// for anyone on the account who proves its password (and two-step code).
func (s *Server) changeProfilePIN(r *http.Request, hash string, proof *db.PasswordProof) (Profile, error) {
	caller := CallerFrom(r.Context())
	userID, sessionID := caller.UserID, caller.SessionID
	if proof == nil {
		var err error
		if userID, sessionID, err = s.profileOwner(r); err != nil {
			return Profile{}, err
		}
		// Without the password, a PIN is changed only by someone who has just typed one.
		if err := s.pinStale(r, caller); err != nil {
			return Profile{}, err
		}
	}
	p, err := s.db.UpdateProfile(r.Context(), db.ProfileEdit{UserID: userID, PublicID: r.PathValue("id"), PINHash: &hash,
		SessionID: sessionID, Proof: proof, Now: s.now()})
	if errors.Is(err, db.ErrNotFound) {
		return Profile{}, ErrNotFound()
	}
	return profileOut(p), proofError(err)
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

// pinSourceLimit is the network's count of wrong guesses as a PIN takes it. Behind a proxy
// on this machine or a private network that the owner has not listed, every visitor is one
// address, so one stranger's wrong passwords block that address for everybody; and the
// owner, already signed in, must still be able to enter their profile to list the proxy.
// So there a PIN does not take the network's count. Nothing is opened by that: a PIN is
// only ever tried by a sign-in that already proved the account's password, and it is still
// held by the profile's own counts on this device and across all devices, which do not
// depend on the address. Passwords, codes and keys from that address stay blocked.
func (s *Server) pinSourceLimit(r *http.Request) []db.Limit {
	if s.networks().UnlistedLocalProxy(r) {
		s.noteUnlistedProxy(peerAddr(r))
		return nil
	}
	return s.sourceLimit(r)
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
	sel := db.ProfileSelection{UserID: caller.UserID, SessionID: caller.SessionID, ProfileID: p.ID, CheckedPINHash: p.PINHash, AtHome: s.fromHome(r), Now: now}
	remember, pinToken := time.Duration(s.signInSettings().PINRememberDays)*24*time.Hour, ""
	var kept pinMemory
	if p.PINHash != "" && req.PIN == "" && req.PINToken != "" {
		// A remembered PIN: good on the device it was given to, until the PIN changes or the
		// owner's time for remembering runs out. It cannot be guessed, so a stale one counts
		// against nobody; the app asks for the PIN.
		session, err := s.db.SessionState(ctx, caller.SessionID, now)
		if errors.Is(err, db.ErrNotFound) {
			return Session{}, errSignInEnded()
		}
		if err != nil {
			return Session{}, err
		}
		sel.Remembered = true
		sel.PINOK = s.tokens.Recall("pin", req.PINToken, &kept) && kept.Profile == p.ID && kept.Device == session.DevicePublicID &&
			kept.PIN == pinFingerprint(p.PINHash) && now.Before(time.UnixMilli(kept.At).Add(remember))
		if !sel.PINOK {
			return Session{}, refusal(http.StatusForbidden, "profile_pin_reentry", "Enter this profile's PIN.", false)
		}
	} else if p.PINHash != "" {
		if req.PIN == "" {
			return Session{}, errBadPIN()
		}
		// Wrong PINs are counted for this profile on this device, so signing in again on it
		// starts no new count, and for the profile on all devices together, so a count
		// cannot be escaped by making new devices. See auth.StandardProfilePINs.
		session, err := s.db.SessionState(ctx, caller.SessionID, now)
		if errors.Is(err, db.ErrNotFound) {
			return Session{}, errSignInEnded()
		}
		if err != nil {
			return Session{}, err
		}
		profile := "profile:" + strconv.FormatInt(p.ID, 10)
		sel.Limits = append([]db.Limit{
			{Bucket: "pin", Key: profile + "|" + session.DevicePublicID, Delay: auth.StandardPINs.Delay, Lasting: true},
			{Bucket: db.ProfilePINBucket, Key: profile, Delay: auth.StandardProfilePINs.Delay, Lasting: true},
		}, s.pinSourceLimit(r)...)
		if wait, err := s.db.LimitWait(ctx, now, sel.Limits...); err != nil {
			return Session{}, err
		} else if wait > 0 {
			return Session{}, errPINWait(wait)
		}
		if sel.PINOK, err = s.passwords.Check(ctx, p.PINHash, req.PIN); err != nil {
			return Session{}, err
		}
		if req.RememberPIN && remember > 0 {
			sel.TypedFor = pinTypedFor
			pinToken = s.tokens.Remember("pin", pinMemory{Profile: p.ID, Device: session.DevicePublicID, PIN: pinFingerprint(p.PINHash), At: now.UnixMilli()})
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
	out := s.session(st, now, "")
	if pinToken != "" {
		expires := now.Add(remember).UTC()
		out.PINToken, out.PINTokenExpiresAt = pinToken, &expires
	}
	return out, nil
}
