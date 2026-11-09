package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// DeviceInfo is what an app says about the install it runs on when it signs in.
type DeviceInfo struct {
	// InstallationID is an id the app makes once and keeps: 32 to 128 letters, digits, "_" or "-".
	InstallationID string `json:"installationId"`
	Name           string `json:"name"`
	Platform       string `json:"platform,omitempty"`
	App            string `json:"app,omitempty"`
	AppVersion     string `json:"appVersion,omitempty"`
}

// SignInRequest is a password sign-in.
type SignInRequest struct {
	Username string     `json:"username"`
	Password string     `json:"password"`
	Device   DeviceInfo `json:"device"`
}

// SetupOwnerRequest creates the owner account on a new server.
type SetupOwnerRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// ProfileName names the owner's main profile. It defaults to the username.
	ProfileName string     `json:"profileName,omitempty"`
	Device      DeviceInfo `json:"device"`
}

// RefreshRequest renews a sign-in. The app saves RequestID before sending and sends the
// same one again if the reply is lost, and is then given the same tokens again.
type RefreshRequest struct {
	RefreshToken   string `json:"refreshToken"`
	InstallationID string `json:"installationId"`
	RequestID      string `json:"requestId"`
}

// PasswordRequest changes the caller's password.
type PasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// Session is the tokens of one sign-in on one device.
type Session struct {
	// AccessToken goes in "Authorization: Bearer" and works until ExpiresAt, 15 minutes at most.
	AccessToken string    `json:"accessToken"`
	ExpiresAt   time.Time `json:"expiresAt"`
	// RefreshToken renews the session and is replaced by every renewal. Selecting a profile
	// does not replace it, so that reply leaves it out.
	RefreshToken string `json:"refreshToken,omitempty"`
	SessionID    string `json:"sessionId"`
	// TokenGeneration counts renewals, as a decimal string. A renewed session's is always higher.
	TokenGeneration string `json:"tokenGeneration"`
	// SessionExpiresAt is when the sign-in ends if it is not renewed again.
	SessionExpiresAt time.Time `json:"sessionExpiresAt"`
	DeviceID         string    `json:"deviceId"`
	InstallationID   string    `json:"installationId"`
	Viewer           Viewer    `json:"viewer"`
}

// Viewer is who a session acts for.
type Viewer struct {
	AccountID string `json:"accountId"`
	// ProfileID is absent until a profile has been selected.
	ProfileID string `json:"profileId,omitempty"`
	ServerID  string `json:"serverId"`
	// Role is owner, admin or member, or account before a profile has been selected.
	Role string `json:"role"`
}

// Account is a person's account on this server.
type Account struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
	// Role is the account's own role. A session's role can be lower: see Viewer.
	Role string `json:"role"`
}

// SignInResponse is the answer to a sign-in: the new session, the account and its profiles.
// For an account with two-step sign-in the password gets a challenge instead, and
// POST /v1/auth/two-factor with the challenge and a code gets the session.
type SignInResponse struct {
	Session   Session    `json:"session,omitzero"`
	Account   Account    `json:"account,omitzero"`
	Profiles  []Profile  `json:"profiles,omitempty"`
	Challenge *Challenge `json:"challenge,omitempty"`
}

// Challenge is a password sign-in waiting for its second step.
type Challenge struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Me is who the caller is.
type Me struct {
	Account Account `json:"account"`
	// Profile is absent until one has been selected.
	Profile *Profile `json:"profile,omitempty"`
	// Role is what this session may do on the server; see Viewer.
	Role string `json:"role"`
	// CanManageAccount says this session may change the account's profiles, devices and sessions.
	CanManageAccount bool `json:"canManageAccount"`
}

func errBadSignIn() *Error {
	return refusal(http.StatusUnauthorized, "unauthorized", "The username or password is not correct.", false)
}

func errSignInEnded() *Error {
	return refusal(http.StatusUnauthorized, "unauthorized", "This sign-in has ended. Sign in again.", false)
}

func errTooManyAttempts(wait time.Duration) *Error {
	return ErrTooMany("rate_limited", "Too many attempts. Wait a moment, then try again.", wait)
}

func (s *Server) authRoutes() {
	Handle(s, "POST /v1/setup/owner", Public, s.setupOwner)
	Handle(s, "POST /v1/auth/sign-in", Public, s.signIn)
	Handle(s, "POST /v1/auth/refresh", Public, s.refresh)
	Handle(s, "DELETE /v1/auth/session", Public, s.signOut)
	Handle(s, "POST /v1/auth/password", SignedIn, s.changePassword)
	// A tool with a key can ask who it is acting for.
	Handle(s, "GET /v1/me", SignedIn, s.me).Scope = ScopeRead
	Handle(s, "POST /v1/auth/password-reset/confirm", Public, s.resetPassword)
	s.twoFactorRoutes()
	s.codeRoutes()
}

// passwordProof compares the caller's current password and returns the proof for the
// transaction that will act on it. A wait already in force is refused here.
func (s *Server) passwordProof(r *http.Request, password string) (db.PasswordProof, error) {
	ctx, caller := r.Context(), CallerFrom(r.Context())
	p := db.PasswordProof{UserID: caller.UserID,
		Limits: guessLimits(r, "password", "user:"+strconv.FormatInt(caller.UserID, 10), auth.PasswordDelay)}
	if wait, err := s.db.LimitWait(ctx, s.now(), p.Limits...); err != nil {
		return p, err
	} else if wait > 0 {
		return p, errTooManyAttempts(wait)
	}
	creds, err := s.db.CredentialsByID(ctx, caller.UserID)
	if err != nil {
		return p, err
	}
	p.CheckedHash = creds.PasswordHash
	p.OK, err = s.passwords.Check(ctx, creds.PasswordHash, password)
	return p, err
}

func errWrongCurrentPassword() *Error {
	return refusal(http.StatusForbidden, "current_password_incorrect", "The current password is not correct.", false)
}

// session mints the tokens that say what st says. issued is when they count from.
func (s *Server) session(st db.SessionState, issued time.Time, refreshToken string) Session {
	// Before a profile is selected the session can only choose one and, unless the main
	// profile has a PIN, manage the account. Only the main profile carries the account's
	// role; any other profile is a member, whoever owns the account.
	role, manage := "account", !st.PrimaryHasPIN
	if st.ProfileID != 0 {
		role, manage = "member", false
		if st.ProfilePrimary {
			role, manage = st.UserRole, true
		}
	}
	access, expires := s.tokens.Access(auth.Claims{SessionID: st.ID, UserID: st.UserID, ProfileID: st.ProfileID, Role: role, Manage: manage,
		Boot: st.Stamp.Boot, Seq: st.Stamp.Seq},
		issued, st.ExpiresAt)
	return Session{
		AccessToken: access, ExpiresAt: expires.UTC(), RefreshToken: refreshToken,
		SessionID: st.PublicID, TokenGeneration: strconv.FormatInt(st.Generation, 10), SessionExpiresAt: st.ExpiresAt.UTC(),
		DeviceID: st.DevicePublicID, InstallationID: st.InstallationID,
		Viewer: Viewer{AccountID: st.UserPublicID, ProfileID: st.ProfilePublicID, ServerID: s.id, Role: role},
	}
}

// checkDevice checks what an app said about the install it runs on.
func checkDevice(device DeviceInfo) (db.DeviceInfo, error) {
	switch {
	case !auth.ValidToken(device.InstallationID, 32):
		return db.DeviceInfo{}, ErrField("device.installationId", "Send the id this app made for itself: 32 to 128 letters, digits, _ or -.")
	case !auth.ValidName(device.Name):
		return db.DeviceInfo{}, ErrField("device.name", "Give the device a name of at most 120 characters.")
	case len(device.Platform) > 64 || len(device.App) > 64 || len(device.AppVersion) > 64:
		return db.DeviceInfo{}, ErrField("device", "Platform, app and version are at most 64 characters each.")
	}
	return db.DeviceInfo{InstallationID: device.InstallationID, Name: device.Name, Platform: device.Platform, App: device.App, AppVersion: device.AppVersion}, nil
}

// mintSession prepares a session and its first refresh token. The device is filled in by
// the caller, or by the stored request the session answers.
func (s *Server) mintSession(r *http.Request, now time.Time) (db.NewSession, string) {
	id := db.NewSessionID()
	refresh := s.tokens.FirstRefresh(id)
	return db.NewSession{ID: id, RefreshHash: auth.Hash(refresh), ExpiresAt: now.Add(auth.SessionIdle), RemoteAddr: remoteHost(r)}, refresh
}

// newSession checks what the app said about its device and prepares a session for it.
func (s *Server) newSession(r *http.Request, device DeviceInfo, now time.Time) (db.NewSession, string, error) {
	checked, err := checkDevice(device)
	if err != nil {
		return db.NewSession{}, "", err
	}
	session, refresh := s.mintSession(r, now)
	session.Device = checked
	return session, refresh, nil
}

// remoteHost is the address a request came from, without the port.
func remoteHost(r *http.Request) string {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	return ap.Addr().Unmap().String()
}

// guessLimits are the counts a wrong guess at subject's secret adds to: this source's
// guesses at this subject, all sources' guesses at it, and everything this source gets wrong.
func guessLimits(r *http.Request, bucket, subject string, delay func(int) time.Duration) []db.Limit {
	source, blockable := auth.Source(r.RemoteAddr)
	limits := []db.Limit{{Bucket: bucket, Key: source + "|" + subject, FloorKey: "all|" + subject, Delay: delay}}
	if blockable {
		limits = append(limits, db.Limit{Bucket: "source", Key: source, Delay: auth.SourceDelay})
	}
	return limits
}

// signedIn builds the sign-in answer for a session that has just started.
func (s *Server) signedIn(ctx context.Context, st db.SessionState, now time.Time, refresh string) (SignInResponse, error) {
	account, err := s.db.Account(ctx, st.UserID)
	if err != nil {
		return SignInResponse{}, err
	}
	profiles, err := s.db.Profiles(ctx, st.UserID)
	if err != nil {
		return SignInResponse{}, err
	}
	return SignInResponse{Session: s.session(st, now, refresh), Account: accountOut(account), Profiles: profilesOut(profiles)}, nil
}

func accountOut(a db.Account) Account {
	return Account{ID: a.PublicID, Username: a.Username, Email: a.Email, Role: a.Role}
}

func (s *Server) setupOwner(r *http.Request, req SetupOwnerRequest) (SignInResponse, error) {
	ctx, now := r.Context(), s.now()
	key, ok := auth.UsernameKey(req.Username)
	if !ok {
		return SignInResponse{}, ErrField("username", "Choose a username of 3 to 64 characters with no spaces or @.")
	}
	if !auth.ValidPassword(req.Password) {
		return SignInResponse{}, ErrField("password", "Choose a password of at least 8 characters.")
	}
	if req.ProfileName == "" {
		req.ProfileName = strings.TrimSpace(req.Username)
	}
	if !auth.ValidName(req.ProfileName) {
		return SignInResponse{}, ErrField("profileName", "Give the profile a name of at most 120 characters.")
	}
	session, refresh, err := s.newSession(r, req.Device, now)
	if err != nil {
		return SignInResponse{}, err
	}
	// Do not spend a password hash on a server that is already set up.
	if done, err := s.db.HasUsers(ctx); err != nil {
		return SignInResponse{}, err
	} else if done {
		return SignInResponse{}, errSetupDone()
	}
	hash, err := s.passwords.Hash(ctx, req.Password)
	if err != nil {
		return SignInResponse{}, err
	}
	st, err := s.db.CreateOwner(ctx, db.NewOwner{Username: strings.TrimSpace(req.Username), UsernameKey: key, PasswordHash: hash,
		ProfileName: req.ProfileName, Session: session, Now: now})
	if errors.Is(err, db.ErrSetupDone) {
		return SignInResponse{}, errSetupDone()
	}
	if err != nil {
		return SignInResponse{}, err
	}
	return s.signedIn(ctx, st, now, refresh)
}

func errSetupDone() *Error {
	return ErrConflict("setup_complete", "This server already has an owner. Sign in instead.")
}

func (s *Server) signIn(r *http.Request, req SignInRequest) (SignInResponse, error) {
	ctx, now := r.Context(), s.now()
	session, refresh, err := s.newSession(r, req.Device, now)
	if err != nil {
		return SignInResponse{}, err
	}
	// From here on, a username nobody has is treated exactly like one with a wrong password:
	// the same work, the same counting, the same answer.
	key, _ := auth.UsernameKey(req.Username)
	limits := guessLimits(r, "password", key, auth.PasswordDelay)
	if wait, err := s.db.LimitWait(ctx, now, limits...); err != nil {
		return SignInResponse{}, err
	} else if wait > 0 {
		return SignInResponse{}, errTooManyAttempts(wait)
	}
	creds, err := s.db.CredentialsByUsername(ctx, key)
	if err != nil {
		return SignInResponse{}, err
	}
	ok, err := s.passwords.Check(ctx, creds.PasswordHash, req.Password)
	if err != nil {
		return SignInResponse{}, err
	}
	// If the account has two-step sign-in, this challenge is stored in place of a session.
	device, _ := json.Marshal(session.Device)
	challenge := Challenge{Token: auth.NewSecret(), ExpiresAt: now.Add(challengeLife).UTC()}
	st, challenged, err := s.db.SignIn(ctx, db.SignInAttempt{UserID: creds.UserID, CheckedHash: creds.PasswordHash, PasswordOK: ok,
		Limits: limits, Session: session, Now: now,
		Challenge: db.Code{Kind: "two_factor", Hash: auth.Hash(challenge.Token), Payload: string(device), ExpiresAt: challenge.ExpiresAt}})
	var limited *db.LimitedError
	switch {
	case errors.As(err, &limited):
		return SignInResponse{}, errTooManyAttempts(limited.Wait)
	case errors.Is(err, db.ErrBadCredentials):
		return SignInResponse{}, errBadSignIn()
	case err != nil:
		return SignInResponse{}, err
	case challenged:
		return SignInResponse{Challenge: &challenge}, nil
	}
	return s.signedIn(ctx, st, now, refresh)
}

func (s *Server) refresh(r *http.Request, req RefreshRequest) (Session, error) {
	now := s.now()
	if !auth.ValidToken(req.InstallationID, 32) {
		return Session{}, ErrField("installationId", "Send the id this app made for itself.")
	}
	if !auth.ValidToken(req.RequestID, 1) {
		return Session{}, ErrField("requestId", "Send the request id saved for this renewal: 1 to 128 letters, digits, _ or -.")
	}
	ref, err := s.tokens.CheckRefresh(req.RefreshToken)
	if err != nil {
		return Session{}, errSignInEnded()
	}
	next := s.tokens.NextRefresh(req.RefreshToken, ref, req.RequestID)
	st, replayed, err := s.db.RenewSession(r.Context(), db.Renewal{SessionID: ref.SessionID, Generation: ref.Generation,
		Hash: auth.Hash(req.RefreshToken), NextHash: auth.Hash(next), RequestID: req.RequestID, InstallationID: req.InstallationID,
		ExpiresAt: now.Add(auth.SessionIdle), RemoteAddr: remoteHost(r), Now: now})
	if errors.Is(err, db.ErrBadCredentials) {
		return Session{}, errSignInEnded()
	}
	if err != nil {
		return Session{}, err
	}
	// The same renewal sent again gets the same answer: the same refresh token, and an
	// access token counted from the first time, even if that one has run out by now.
	issued := now
	if replayed {
		issued = st.RenewedAt
	}
	return s.session(st, issued, next), nil
}

// signOut ends the session of the token it is sent with. It is safe to repeat, and it takes
// a token that has run out, so an app that was closed for a while can still sign out.
func (s *Server) signOut(r *http.Request, _ None) (None, error) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return None{}, ErrUnauthorized()
	}
	claims, err := s.tokens.ExpiredAccess(token)
	if err != nil {
		return None{}, ErrUnauthorized()
	}
	return None{}, s.db.RevokeSession(r.Context(), claims.SessionID, "signed_out", s.now())
}

func (s *Server) changePassword(r *http.Request, req PasswordRequest) (None, error) {
	ctx, now := r.Context(), s.now()
	if !auth.ValidPassword(req.NewPassword) {
		return None{}, ErrField("newPassword", "Choose a password of at least 8 characters.")
	}
	proof, err := s.passwordProof(r, req.CurrentPassword)
	if err != nil {
		return None{}, err
	}
	var hash string
	if proof.OK {
		if hash, err = s.passwords.Hash(ctx, req.NewPassword); err != nil {
			return None{}, err
		}
	}
	err = s.db.ChangePassword(ctx, db.PasswordChange{UserID: proof.UserID, CheckedHash: proof.CheckedHash, CurrentOK: proof.OK,
		NewHash: hash, Limits: proof.Limits, Now: now})
	var limited *db.LimitedError
	switch {
	case errors.As(err, &limited):
		return None{}, errTooManyAttempts(limited.Wait)
	case errors.Is(err, db.ErrBadCredentials):
		return None{}, errWrongCurrentPassword()
	}
	return None{}, err
}

func (s *Server) me(r *http.Request, _ None) (Me, error) {
	ctx, caller := r.Context(), CallerFrom(r.Context())
	account, err := s.db.Account(ctx, caller.UserID)
	if err != nil {
		return Me{}, err
	}
	out := Me{Account: accountOut(account), Role: caller.Role, CanManageAccount: caller.Manage}
	if caller.ProfileID != 0 {
		p, err := s.db.ProfileByID(ctx, caller.UserID, caller.ProfileID)
		if err != nil {
			return Me{}, err
		}
		profile := profileOut(p)
		out.Profile = &profile
	}
	return out, nil
}
