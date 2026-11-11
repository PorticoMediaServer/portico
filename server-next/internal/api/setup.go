package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// First-run setup. A server with no owner shows setup to anyone who reaches it, from any
// network, and asks every one of them for the setup code: the server prints it in its log and
// writes it to setup-code.txt in its data folder, so only someone who can see the server's
// own files or log can become its owner. There is no "at home" shortcut, because a reverse
// proxy or a container makes every visitor look local. Wrong codes slow that network down and
// never lock setup.
//
// Setup holds nothing until it finishes. Its three steps are the server's name and kind of
// sign-in, the owner, and the first library; the first two finish in one request (or, for
// Portico Accounts, in the request that brings the approval back), and the first library is
// added with the ordinary library routes once the owner is signed in. Whoever finishes first
// is the owner; anyone after is told the server is already set up.

// SetupOwnerRequest creates the owner account on a new server that uses local sign-in.
type SetupOwnerRequest struct {
	// SetupCode is the code the server wrote to its log and to setup-code.txt in its data
	// folder. It shows the person can reach the server itself.
	SetupCode string `json:"setupCode"`
	// ServerName names the server. Left out, it keeps the name it has ("Portico").
	ServerName string `json:"serverName,omitempty"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	// ProfileName names the owner's main profile. It defaults to the username.
	ProfileName string     `json:"profileName,omitempty"`
	Device      DeviceInfo `json:"device"`
}

// SetupPorticoRequest starts setting up a new server with Portico Accounts.
type SetupPorticoRequest struct {
	SetupCode  string `json:"setupCode"`
	ServerName string `json:"serverName,omitempty"`
	// ReturnURL is the page the person's browser comes back to with the approval.
	ReturnURL string `json:"returnUrl"`
}

// SetupPorticoApproval is where to send the person to approve this server with their
// Portico Account, and until when that address works.
type SetupPorticoApproval struct {
	ApprovalURL string    `json:"approvalUrl"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// SetupPorticoFinish brings the approval back. The approving Portico Account becomes the
// owner and this device is signed in to it.
type SetupPorticoFinish struct {
	Approval string     `json:"approval"`
	Device   DeviceInfo `json:"device"`
}

const setupCodeFile = "setup-code.txt"

func (s *Server) setupRoutes() {
	Handle(s, "POST /v1/setup/owner", Public, s.setupOwner)
	Handle(s, "POST /v1/setup/portico", Public, s.setupPortico)
	Handle(s, "POST /v1/setup/portico/finish", Public, s.setupPorticoFinish)
}

// loadSetupCode makes the setup code of a server with no owner, or reads back the one it made
// before a restart: the code stays the same until setup finishes, so a code someone already
// copied from the log keeps working. A server that has an owner has no setup code, and a file
// left from a setup that finished just before a crash is removed.
func (s *Server) loadSetupCode(ctx context.Context, dataDir string) error {
	s.setupFile = filepath.Join(dataDir, setupCodeFile)
	if set, err := s.db.HasUsers(ctx); err != nil {
		return err
	} else if set {
		os.Remove(s.setupFile)
		return nil
	}
	if raw, err := os.ReadFile(s.setupFile); err == nil && validSetupCode(strings.TrimSpace(string(raw))) {
		s.setupCode = strings.TrimSpace(string(raw))
	} else {
		s.setupCode = auth.NewCode(4)
		if err := os.WriteFile(s.setupFile, []byte(s.setupCode+"\n"), 0o600); err != nil {
			return err
		}
	}
	s.log.Info("this server has no owner yet: set it up with this setup code", "setupCode", s.setupCode, "file", s.setupFile)
	return nil
}

func validSetupCode(code string) bool {
	key := auth.CodeKey(code)
	return len(key) == 16 && strings.Trim(key, "ABCDEFGHJKMNPQRSTUVWXYZ23456789") == ""
}

func errSetupDone() *Error {
	return ErrConflict("setup_complete", "This server is already set up. Sign in instead.")
}

// checkSetupCode refuses a server that already has an owner, then a wrong setup code. The right
// code always gets through, whatever wait is in force: behind a reverse proxy every visitor is
// one source, and a stranger sending wrong codes must not keep the owner out. Wrong codes count
// against the network they came from, at home too (a proxy can make everyone look local), and
// are slowed as passwords are, a minute at most; they never feed the day-long block of a
// network away from home. With waits no longer bounding guesses, the code itself carries the
// weight: 16 characters, about 79 bits.
func (s *Server) checkSetupCode(r *http.Request, code string) error {
	ctx, now := r.Context(), s.now()
	if done, err := s.db.HasUsers(ctx); err != nil {
		return err
	} else if done {
		return errSetupDone()
	}
	source := ""
	if addr, ok := s.networks().Client(r); ok {
		source = auth.SourceKey(addr)
	}
	limits := []db.Limit{{Bucket: "setup", Key: source, Delay: auth.StandardPasswords.Delay}}
	if s.setupCode != "" && subtle.ConstantTimeCompare([]byte(auth.CodeKey(code)), []byte(auth.CodeKey(s.setupCode))) == 1 {
		return nil
	}
	if wait, err := s.db.LimitWait(ctx, now, limits...); err != nil {
		return err
	} else if wait > 0 {
		return errTooManyAttempts(wait)
	}
	if err := s.db.RecordFailure(ctx, now, limits...); err != nil {
		return err
	}
	return refusal(http.StatusForbidden, "setup_code_invalid",
		"That setup code is not correct. It is in setup-code.txt in the server's data folder; the server's log names that folder when it starts.", false)
}

// checkServerName checks a name setup was given with Settings › General's own rule, so the
// name setup saves is one the console accepts. "" keeps the name the server has.
func checkServerName(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", nil
	}
	checked, err := settings.General.Check(settings.GeneralSettings{Name: name})
	var invalid *settings.Invalid
	if errors.As(err, &invalid) {
		return "", ErrField("serverName", invalid.Message)
	}
	return checked.Name, err
}

// saveServerName stores the name setup was given in Settings › General, where every reader of
// the server's name finds it and the owner renames it later. It is saved by the new owner once
// their account exists: a setup refused at the last moment changes nothing. If this save fails
// the server keeps its default name, which the owner can change.
func (s *Server) saveServerName(ctx context.Context, ownerID int64, name string) {
	if name == "" {
		return
	}
	change, _ := json.Marshal(map[string]string{"name": name})
	if _, err := s.settings.Write(ctx, ownerID, settings.General.Name(), change, -1, s.now()); err != nil {
		s.log.Error("the name chosen in setup was not saved; the server keeps its default name", "error", err)
	}
}

// setupDone deletes the setup code once an owner exists; it is never asked for again.
func (s *Server) setupDone() {
	os.Remove(s.setupFile)
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
	serverName, err := checkServerName(req.ServerName)
	if err != nil {
		return SignInResponse{}, err
	}
	session, refresh, err := s.newSession(r, req.Device, now)
	if err != nil {
		return SignInResponse{}, err
	}
	// Anyone who can reach a new server could otherwise make themselves its owner. This is
	// checked before the password is hashed, so a set-up server spends nothing on it.
	if err := s.checkSetupCode(r, req.SetupCode); err != nil {
		return SignInResponse{}, err
	}
	hash, err := s.passwords.Hash(ctx, req.Password)
	if err != nil {
		return SignInResponse{}, err
	}
	st, err := s.db.CreateOwner(ctx, db.NewOwner{Username: username, PasswordHash: hash, ProfileName: req.ProfileName,
		Session: session, Now: now})
	if errors.Is(err, db.ErrSetupDone) {
		return SignInResponse{}, errSetupDone()
	}
	if err != nil {
		return SignInResponse{}, err
	}
	s.saveServerName(ctx, st.UserID, serverName)
	s.setupDone()
	return s.signedIn(ctx, st, now, refresh)
}

// hostedError answers a failure to reach Portico Services, or an approval it did not give.
func hostedError(err error) error {
	switch {
	case errors.Is(err, hosted.ErrNotConnected):
		e := refusal(http.StatusServiceUnavailable, "portico_unavailable", "Portico Services can't be reached. Try again later.", true)
		e.RetryAfterMs = 30_000
		return e
	case errors.Is(err, hosted.ErrDeclined):
		return ErrConflict("approval_declined", "The approval was declined or has expired. Nothing has changed; start again.")
	}
	return err
}

// setupPortico checks the setup code and asks Portico Services where the person approves this
// server. The server saves nothing: if the approval never comes back, it is as it was.
func (s *Server) setupPortico(r *http.Request, req SetupPorticoRequest) (SetupPorticoApproval, error) {
	serverName, err := checkServerName(req.ServerName)
	if err != nil {
		return SetupPorticoApproval{}, err
	}
	if u, err := url.Parse(req.ReturnURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || len(req.ReturnURL) > 2000 {
		return SetupPorticoApproval{}, ErrField("returnUrl", "Send the full address of the page to come back to.")
	}
	if err := s.checkSetupCode(r, req.SetupCode); err != nil {
		return SetupPorticoApproval{}, err
	}
	if serverName == "" {
		serverName = s.serverName()
	}
	approval, err := s.hosted.StartApproval(r.Context(), hosted.ApprovalRequest{ServerID: s.id, ServerName: serverName,
		Purpose: hosted.PurposeSetup, ReturnURL: req.ReturnURL})
	if err != nil {
		return SetupPorticoApproval{}, hostedError(err)
	}
	return SetupPorticoApproval{ApprovalURL: approval.URL, ExpiresAt: approval.ExpiresAt.UTC()}, nil
}

// setupPorticoFinish makes the Portico Account that approved this server its owner, with no
// password on the server, and records that the server uses Portico Accounts, in one commit.
// The setup code is not asked again: the approval could only be started with it, and the
// person may be back in a new page or after a restart.
func (s *Server) setupPorticoFinish(r *http.Request, req SetupPorticoFinish) (SignInResponse, error) {
	ctx, now := r.Context(), s.now()
	if req.Approval == "" || len(req.Approval) > 8192 {
		return SignInResponse{}, ErrField("approval", "Send the approval exactly as it came back.")
	}
	session, refresh, err := s.newSession(r, req.Device, now)
	if err != nil {
		return SignInResponse{}, err
	}
	if done, err := s.db.HasUsers(ctx); err != nil {
		return SignInResponse{}, err
	} else if done {
		return SignInResponse{}, errSetupDone()
	}
	decision, err := s.hosted.FinishApproval(ctx, s.id, req.Approval)
	if err == nil && (decision.Purpose != hosted.PurposeSetup || decision.Account.ID == "") {
		err = hosted.ErrDeclined
	}
	if err != nil {
		return SignInResponse{}, hostedError(err)
	}
	username, ok := auth.Username(decision.Account.Username)
	if !ok {
		return SignInResponse{}, errors.New("portico services answered a username this server cannot use")
	}
	profileName := strings.TrimSpace(decision.Account.DisplayName)
	if !auth.ValidName(profileName) || profileName == "" {
		profileName = decision.Account.Username
	}
	serverName, err := checkServerName(decision.ServerName)
	if err != nil {
		serverName = ""
	}
	st, err := s.db.CreateOwner(ctx, db.NewOwner{Username: username, PorticoAccountID: decision.Account.ID, ProfileName: profileName,
		Session: session, Now: now})
	if errors.Is(err, db.ErrSetupDone) {
		return SignInResponse{}, errSetupDone()
	}
	if err != nil {
		return SignInResponse{}, err
	}
	s.saveServerName(ctx, st.UserID, serverName)
	s.setupDone()
	return s.signedIn(ctx, st, now, refresh)
}

// signInKind is the server's kind of sign-in: "portico" when it uses Portico Accounts,
// otherwise "local".
func (s *Server) signInKind(ctx context.Context) (string, error) {
	kind, _, err := s.db.Setting(ctx, db.SignInKindKey)
	if kind == db.SignInPortico {
		return "portico", err
	}
	return "local", err
}
