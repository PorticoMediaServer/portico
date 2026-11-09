package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/netip"
	"os"
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
	// SetupCode is the code the server wrote to its log and to setup-code.txt in its data
	// folder when it first started. It shows the person can reach the server itself.
	SetupCode string `json:"setupCode"`
	Username  string `json:"username"`
	Password  string `json:"password"`
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
type SignInResponse struct {
	Session  Session   `json:"session"`
	Account  Account   `json:"account"`
	Profiles []Profile `json:"profiles"`
}

// Me is who the caller is.
type Me struct {
	Account Account `json:"account"`
	// Profile is absent until one has been selected.
	Profile *Profile `json:"profile,omitempty"`
	// Role is what this session may do on the server; see Viewer.
	Role string `json:"role"`
	// CanManageAccount says this session is on the account's main profile, and so may change
	// the account's profiles, devices, sessions and password.
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
	Handle(s, "GET /v1/me", SignedIn, s.me)
}

// session mints the tokens that say what st says. issued is when they count from.
func (s *Server) session(st db.SessionState, issued time.Time, refreshToken string) Session {
	// Before a profile is selected the session can only choose one. Only the main profile
	// carries the account's role and may manage the account; any other profile is a member,
	// whoever owns the account.
	role, manage := "account", false
	if st.ProfileID != 0 {
		role = "member"
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

// newSession checks what the app said about its device and prepares a session for it.
func (s *Server) newSession(r *http.Request, device DeviceInfo, now time.Time) (db.NewSession, string, error) {
	switch {
	case !auth.ValidToken(device.InstallationID, 32):
		return db.NewSession{}, "", ErrField("device.installationId", "Send the id this app made for itself: 32 to 128 letters, digits, _ or -.")
	case !auth.ValidName(device.Name):
		return db.NewSession{}, "", ErrField("device.name", "Give the device a name of at most 120 characters.")
	case len(device.Platform) > 64 || len(device.App) > 64 || len(device.AppVersion) > 64:
		return db.NewSession{}, "", ErrField("device", "Platform, app and version are at most 64 characters each.")
	}
	id := db.NewSessionID()
	refresh := s.tokens.FirstRefresh(id)
	return db.NewSession{
		ID: id, RefreshHash: auth.Hash(refresh), ExpiresAt: now.Add(auth.SessionIdle), RemoteAddr: remoteHost(r),
		Device: db.DeviceInfo{InstallationID: device.InstallationID, Name: device.Name, Platform: device.Platform, App: device.App, AppVersion: device.AppVersion},
	}, refresh, nil
}

// remoteHost is the address a request came from, without the port.
func remoteHost(r *http.Request) string {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	return ap.Addr().Unmap().String()
}

// sourceLimit counts everything one network away from home gets wrong: passwords, PINs and
// codes of every kind. At 30 the network is blocked for 15 minutes, then for twice as long
// each time it happens again, up to a day. Every proof takes it, so a block refuses them
// all. The home network is never blocked.
func sourceLimit(r *http.Request) []db.Limit {
	source, home := auth.Source(r.RemoteAddr)
	if home {
		return nil
	}
	return []db.Limit{{Bucket: "source", Key: source, Delay: auth.SourceDelay, Doubling: true}}
}

// passwordLimits are the counts a wrong password adds to. The wait belongs to the network
// it came from, whichever username was tried, so the same account can still be signed in
// to from elsewhere. Wrong guesses at one account from many networks are also counted
// against the account, and each new network that guesses wrong starts from that count; a
// right password from a new network still works at once.
func passwordLimits(r *http.Request, account string) []db.Limit {
	source, _ := auth.Source(r.RemoteAddr)
	return append([]db.Limit{{Bucket: "password", Key: source, FloorKey: "account|" + account, Delay: auth.StandardPasswords.Delay}}, sourceLimit(r)...)
}

// passwordProof compares the caller's current password and returns the proof for the
// transaction that will act on it. A wait already in force is refused here.
func (s *Server) passwordProof(r *http.Request, password string) (db.PasswordProof, error) {
	ctx, caller := r.Context(), CallerFrom(r.Context())
	p := db.PasswordProof{UserID: caller.UserID, Limits: passwordLimits(r, "user:"+strconv.FormatInt(caller.UserID, 10))}
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

// proofError turns the refusals of a password proof into their answers.
func proofError(err error) error {
	var limited *db.LimitedError
	switch {
	case errors.As(err, &limited):
		return errTooManyAttempts(limited.Wait)
	case errors.Is(err, db.ErrBadCredentials):
		return refusal(http.StatusForbidden, "current_password_incorrect", "The current password is not correct.", false)
	case errors.Is(err, db.ErrBadSecondStep):
		return refusal(http.StatusForbidden, "two_factor_invalid", "That code is not correct.", false)
	}
	return err
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
	username, ok := auth.Username(req.Username)
	if !ok {
		return SignInResponse{}, ErrField("username", "Choose a username of 3 to 64 characters with no spaces or @.")
	}
	if problem := auth.PasswordProblem(req.Password); problem != "" {
		return SignInResponse{}, ErrField("password", problem)
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
	// Anyone who can reach a new server could otherwise make themselves its owner.
	if s.setupCode == "" || subtle.ConstantTimeCompare([]byte(auth.CodeKey(req.SetupCode)), []byte(auth.CodeKey(s.setupCode))) != 1 {
		return SignInResponse{}, refusal(http.StatusForbidden, "setup_code_invalid",
			"That setup code is not correct. It is in the server's log and in setup-code.txt in its data folder.", false)
	}
	hash, err := s.passwords.Hash(ctx, req.Password)
	if err != nil {
		return SignInResponse{}, err
	}
	st, err := s.db.CreateOwner(ctx, db.NewOwner{Username: username, PasswordHash: hash, ProfileName: req.ProfileName, Session: session, Now: now})
	if errors.Is(err, db.ErrSetupDone) {
		return SignInResponse{}, errSetupDone()
	}
	if err != nil {
		return SignInResponse{}, err
	}
	os.Remove(s.setupFile)
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
	key, _ := auth.Username(req.Username)
	limits := passwordLimits(r, key)
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
	st, err := s.db.SignIn(ctx, db.SignInAttempt{UserID: creds.UserID, CheckedHash: creds.PasswordHash, PasswordOK: ok,
		Limits: limits, Session: session, Now: now})
	var limited *db.LimitedError
	switch {
	case errors.As(err, &limited):
		return SignInResponse{}, errTooManyAttempts(limited.Wait)
	case errors.Is(err, db.ErrBadCredentials):
		return SignInResponse{}, errBadSignIn()
	case err != nil:
		return SignInResponse{}, err
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

// changePassword needs the main profile, the current password and, for an account with
// two-step sign-in, its code. The account's other devices are signed out; this one stays.
func (s *Server) changePassword(r *http.Request, req PasswordRequest) (None, error) {
	ctx, now := r.Context(), s.now()
	caller, err := manager(r)
	if err != nil {
		return None{}, err
	}
	if problem := auth.PasswordProblem(req.NewPassword); problem != "" {
		return None{}, ErrField("newPassword", problem)
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
	return None{}, proofError(s.db.ChangePassword(ctx, proof, hash, caller.SessionID, now))
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
