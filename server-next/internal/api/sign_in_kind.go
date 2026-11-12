package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
)

// Changing the kind of sign-in, Start over, Change owner, and the waiting people coming back.
// Only the owner, from the main profile, changes the kind or the owner. Every change ends the
// sign-ins it makes wrong in the same commit, so nobody keeps a power they no longer hold.
// A switch to local sign-in and Start over to local both work with Portico Services out of
// reach; anything that needs a Portico approval goes through internal/hosted, which is not
// connected yet, and answers that Portico Services can't be reached.

// moveCodeLife is how long a move code or link works.
const moveCodeLife = 7 * 24 * time.Hour

// SignInState is the owner's Sign-in page.
type SignInState struct {
	// Kind is "local" or "portico".
	Kind  string  `json:"kind"`
	Owner Account `json:"owner"`
	// Move is a switch that still has people waiting; absent when there is none.
	Move *MoveState `json:"move,omitempty"`
	// Waiting are the accounts waiting to move, by username.
	Waiting []WaitingAccount `json:"waiting"`
}

// MoveState is a switch with people still waiting.
type MoveState struct {
	// To is "local" or "portico".
	To    string    `json:"to"`
	Since time.Time `json:"since"`
}

// WaitingAccount is one person who has not moved yet.
type WaitingAccount struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	// CodeExpiresAt is when the code last made for them stops working; absent with none.
	CodeExpiresAt *time.Time `json:"codeExpiresAt,omitempty"`
}

// SignInPreviewRequest asks what a switch or Start over would do.
type SignInPreviewRequest struct {
	// To is "local" or "portico".
	To        string `json:"to"`
	StartOver bool   `json:"startOver,omitempty"`
	// ReturnURL, for a switch to Portico Accounts, is the page the approval comes back to.
	ReturnURL string `json:"returnUrl,omitempty"`
}

// SignInPreview is what a switch or Start over would do, with this server's real numbers.
// Nothing changes until the owner confirms.
type SignInPreview struct {
	To        string `json:"to"`
	StartOver bool   `json:"startOver"`
	// SignInAgain are the people who must sign in again (a switch); Removed the accounts
	// that go with their profiles and everything they saved (Start over).
	SignInAgain int `json:"signInAgain"`
	Removed     int `json:"removed"`
	// ProfilesRemoved go on Start over; ProfilesKept stay.
	ProfilesRemoved int `json:"profilesRemoved"`
	ProfilesKept    int `json:"profilesKept"`
	// SignedOut are the sign-ins that end, the owner's own included.
	SignedOut int `json:"signedOut"`
	// InvitationsCancelled are unused invitations; KeysPaused or KeysRemoved other people's keys.
	InvitationsCancelled int `json:"invitationsCancelled"`
	KeysPaused           int `json:"keysPaused"`
	KeysRemoved          int `json:"keysRemoved"`
	// PorticoAddressRemoved: moving to local sign-in removes the Portico address and certificate.
	PorticoAddressRemoved bool `json:"porticoAddressRemoved"`
	// ApprovalURL is where the owner approves this server with their Portico Account, for a
	// switch to Portico Accounts when a ReturnURL was sent and Portico Services answered.
	ApprovalURL string `json:"approvalUrl,omitempty"`
	// PorticoUnavailable says Portico Services could not be reached for the approval.
	PorticoUnavailable bool `json:"porticoUnavailable,omitempty"`
}

// SignInSwitchRequest confirms a switch or Start over.
type SignInSwitchRequest struct {
	To        string `json:"to"`
	StartOver bool   `json:"startOver,omitempty"`
	// ServerName is typed by the owner to confirm Start over.
	ServerName string `json:"serverName,omitempty"`
	// CurrentPassword (and Code, with two-step sign-in) prove the owner, typed fresh, on a
	// server with local sign-in: a switch or Start over replaces the owner's password and can
	// remove everyone else, so a sign-in token alone must never be enough. A server using
	// Portico Accounts has no password to ask; there the PIN check of sensitive writes is all.
	CurrentPassword string `json:"currentPassword,omitempty"`
	Code            string `json:"code,omitempty"`
	// Username and Password are the owner's new ones on a server moving to local sign-in.
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// Approval is what came back from approving the server with a Portico Account.
	Approval string     `json:"approval,omitempty"`
	Device   DeviceInfo `json:"device"`
}

// FinishedMove says how many accounts were turned off as "Didn't move".
type FinishedMove struct {
	NotMoved int `json:"notMoved"`
}

// MoveCodeOut is a code or link for one waiting person, shown once.
type MoveCodeOut struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// MoveCheckRequest is a waiting person proving who they are: a move code, or (for a while
// after a switch to Portico Accounts) their old username and password.
type MoveCheckRequest struct {
	Code     string `json:"code,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// MoveCheck is who they are and what to do next: "choose_password" (pick a username, their
// current one filled in, and a password) or "portico_account" (sign in with a Portico Account).
type MoveCheck struct {
	Username string `json:"username"`
	Next     string `json:"next"`
}

// MoveCompleteRequest finishes a move: to local sign-in the code with the chosen username and
// password; to Portico Accounts the code or old password with the Portico approval.
type MoveCompleteRequest struct {
	Code     string `json:"code,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// Challenge and Identity prove the Portico Account a waiting person moves to: the
	// challenge from POST /v1/auth/portico/challenge and the sign-in note Portico Services
	// answered it with. A move is a sign-in, never an approval: an approval names an owner.
	Challenge string          `json:"challenge,omitempty"`
	Identity  json.RawMessage `json:"identity,omitempty"`
	Device    DeviceInfo      `json:"device"`
}

// OwnerChangeRequest hands the server to someone else.
type OwnerChangeRequest struct {
	// Password (and Code, with two-step sign-in) prove the owner on a local server.
	Password string `json:"password,omitempty"`
	Code     string `json:"code,omitempty"`
	// AccountID is an existing account to make the owner, or NewAccount one to make.
	AccountID  string        `json:"accountId,omitempty"`
	NewAccount *MemberCreate `json:"newAccount,omitempty"`
	Approval   string        `json:"approval,omitempty"`
	Previous   string        `json:"previousOwner"`
}

// LeaveRequest is a member leaving a Portico Accounts server.
type LeaveRequest struct {
	// DeleteData also deletes their profiles and history from this server.
	DeleteData bool `json:"deleteData"`
}

func moveHash(typed string) string { return auth.Hash("move|" + auth.CodeKey(typed)) }

func (s *Server) signInKindRoutes() {
	Handle(s, "GET /v1/admin/sign-in", Owner, s.signInState)
	Handle(s, "POST /v1/admin/sign-in/preview", Owner, s.signInPreview)
	Handle(s, "POST /v1/admin/sign-in/switch", Owner, s.signInSwitch).Scope = ScopeSession
	// Turning people off is for the owner in person, never a key.
	Handle(s, "POST /v1/admin/sign-in/finish", Owner, s.finishMove).Scope = ScopeSession
	Handle(s, "POST /v1/admin/members/{id}/move-code", Admin, s.moveCode).Scope = ScopeSession
	Handle(s, "POST /v1/admin/sign-in/move-codes", Admin, s.moveCodes).Scope = ScopeSession
	Handle(s, "POST /v1/admin/owner", Owner, s.changeOwner).Scope = ScopeSession
	Handle(s, "POST /v1/auth/move/check", Public, s.moveCheck)
	Handle(s, "POST /v1/auth/move/complete", Public, s.moveComplete)
	Handle(s, "POST /v1/me/leave", SignedIn, s.leave)
}

func (s *Server) owner(r *http.Request) (db.Account, error) {
	return s.db.Account(r.Context(), CallerFrom(r.Context()).UserID)
}

func (s *Server) signInState(r *http.Request, _ None) (SignInState, error) {
	ctx, now := r.Context(), s.now()
	kind, err := s.signInKind(ctx)
	if err != nil {
		return SignInState{}, err
	}
	owner, err := s.owner(r)
	if err != nil {
		return SignInState{}, err
	}
	out := SignInState{Kind: kind, Owner: accountOut(owner), Waiting: []WaitingAccount{}}
	if move, moving, err := s.db.CurrentMove(ctx); err != nil {
		return SignInState{}, err
	} else if moving {
		out.Move = &MoveState{To: move.To, Since: move.At.UTC()}
	}
	waiting, err := s.db.WaitingAccounts(ctx, now)
	for _, w := range waiting {
		a := WaitingAccount{ID: w.PublicID, Username: w.Username}
		if !w.CodeExpiresAt.IsZero() {
			at := w.CodeExpiresAt.UTC()
			a.CodeExpiresAt = &at
		}
		out.Waiting = append(out.Waiting, a)
	}
	return out, err
}

// checkDestination refuses a switch to the kind the server already uses, except Start over,
// which on its own is a fresh start of the same kind's accounts.
func (s *Server) checkDestination(r *http.Request, to string, startOver bool) error {
	if to != "local" && to != "portico" {
		return ErrField("to", "Choose local or portico.")
	}
	kind, err := s.signInKind(r.Context())
	if err != nil {
		return err
	}
	if kind == to && !startOver {
		return ErrConflict("already_this_kind", "This server already uses this kind of sign-in.")
	}
	return nil
}

func (s *Server) signInPreview(r *http.Request, req SignInPreviewRequest) (SignInPreview, error) {
	if err := s.checkDestination(r, req.To, req.StartOver); err != nil {
		return SignInPreview{}, err
	}
	caller := CallerFrom(r.Context())
	c, err := s.db.SignInCounts(r.Context(), caller.UserID, s.now())
	if err != nil {
		return SignInPreview{}, err
	}
	kind, _ := s.signInKind(r.Context())
	out := SignInPreview{To: req.To, StartOver: req.StartOver, SignedOut: c.SignedIn, InvitationsCancelled: c.Invitations,
		PorticoAddressRemoved: kind == "portico" && req.To == "local"}
	if req.StartOver {
		out.Removed, out.ProfilesRemoved, out.ProfilesKept, out.KeysRemoved = c.OtherAccounts, c.OtherProfiles, c.Profiles-c.OtherProfiles, c.Keys
	} else {
		out.SignInAgain, out.ProfilesKept, out.KeysPaused = c.People, c.Profiles, c.Keys
	}
	if req.To == "portico" && req.ReturnURL != "" {
		if u, err := url.Parse(req.ReturnURL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return SignInPreview{}, ErrField("returnUrl", "Send the full address of the page to come back to.")
		}
		name := s.serverName()
		approval, err := s.hosted.StartApproval(r.Context(), hosted.ApprovalRequest{ServerID: s.id, ServerName: name,
			Purpose: hosted.PurposeSwitch, ReturnURL: req.ReturnURL})
		if err != nil {
			out.PorticoUnavailable = true
		} else {
			out.ApprovalURL = approval.URL
		}
	}
	return out, nil
}

// approved checks a Portico approval brought back for purpose, through Portico Services.
func (s *Server) approved(r *http.Request, approval string, purpose hosted.Purpose) (hosted.Decision, error) {
	if approval == "" || len(approval) > 8192 {
		return hosted.Decision{}, ErrField("approval", "Send the approval exactly as it came back.")
	}
	decision, err := s.hosted.FinishApproval(r.Context(), s.id, approval)
	if err == nil && (decision.Purpose != purpose || decision.Account.ID == "") {
		err = hosted.ErrDeclined
	}
	if err != nil {
		return hosted.Decision{}, hostedError(err)
	}
	return decision, nil
}

func (s *Server) signInSwitch(r *http.Request, req SignInSwitchRequest) (SignInResponse, error) {
	ctx, now, caller := r.Context(), s.now(), CallerFrom(r.Context())
	if err := s.checkDestination(r, req.To, req.StartOver); err != nil {
		return SignInResponse{}, err
	}
	if req.StartOver {
		name := s.serverName()
		if !strings.EqualFold(strings.TrimSpace(req.ServerName), name) {
			return SignInResponse{}, ErrField("serverName", "Type the server's name to confirm. This cannot be undone.")
		}
	}
	session, refresh, err := s.newSession(r, req.Device, now)
	if err != nil {
		return SignInResponse{}, err
	}
	sw := db.Switch{OwnerID: caller.UserID, To: req.To, StartOver: req.StartOver, Session: session, Now: now}
	if kind, err := s.signInKind(ctx); err != nil {
		return SignInResponse{}, err
	} else if kind == "local" {
		proof, err := s.passwordProof(r, req.CurrentPassword, req.Code)
		if err != nil {
			return SignInResponse{}, proofError(err)
		}
		sw.Proof = &proof
	}
	if req.To == "local" {
		username, ok := auth.Username(req.Username)
		if !ok {
			return SignInResponse{}, ErrField("username", "Choose a username of 3 to 64 characters with no spaces or @.")
		}
		if problem := auth.PasswordProblem(req.Password); problem != "" {
			return SignInResponse{}, ErrField("password", problem)
		}
		hash, err := s.passwords.Hash(ctx, req.Password)
		if err != nil {
			return SignInResponse{}, err
		}
		sw.Username, sw.PasswordHash = username, hash
	} else {
		decision, err := s.approved(r, req.Approval, hosted.PurposeSwitch)
		if err != nil {
			return SignInResponse{}, err
		}
		sw.PorticoAccountID, sw.ApprovedAt = decision.Account.ID, decision.IssuedAt
	}
	st, err := s.db.SwitchSignIn(ctx, sw)
	switch {
	case errors.Is(err, db.ErrConflict) && req.To == "local":
		return SignInResponse{}, ErrConflict("username_taken", "That username is already in use.")
	case errors.Is(err, db.ErrNotPermitted):
		return SignInResponse{}, ErrNotPermitted()
	case errors.Is(err, db.ErrStaleApproval):
		return SignInResponse{}, porticoError(hosted.ErrDeclined)
	case errors.Is(err, db.ErrNotTheLinkedAccount), errors.Is(err, db.ErrNotLinkOwner):
		return SignInResponse{}, refusal(http.StatusForbidden, "not_the_linked_account",
			"This server belongs to another Portico Account. Approve with the Portico Account that owns it.", false)
	case err != nil:
		return SignInResponse{}, proofError(s.porticoTaken(r, err))
	}
	if req.To == "portico" {
		s.twoStepOffForPortico(ctx, st.UserID, now)
	}
	return s.signedIn(ctx, st, now, refresh)
}

func (s *Server) finishMove(r *http.Request, _ None) (FinishedMove, error) {
	n, err := s.db.FinishMove(r.Context(), CallerFrom(r.Context()).UserID, s.now())
	return FinishedMove{NotMoved: n}, err
}

// issueMoveCodes makes a new code for each waiting account named, replacing any earlier one.
func (s *Server) issueMoveCodes(r *http.Request, waiting []db.Waiting) ([]MoveCodeOut, error) {
	caller, now := CallerFrom(r.Context()), s.now()
	expires := now.Add(moveCodeLife)
	out := make([]MoveCodeOut, 0, len(waiting))
	codes := make([]db.MoveCode, 0, len(waiting))
	for _, w := range waiting {
		code := auth.NewCode(3)
		out = append(out, MoveCodeOut{ID: w.PublicID, Username: w.Username, Code: code, ExpiresAt: expires.UTC()})
		codes = append(codes, db.MoveCode{PublicID: w.PublicID, Hash: moveHash(code)})
	}
	err := s.db.IssueMoveCodes(r.Context(), caller.UserID, caller.Role, codes, expires, now)
	if errors.Is(err, db.ErrConflict) {
		return nil, ErrConflict("not_waiting", "This person is not waiting to move.")
	}
	return out, memberError(err)
}

func (s *Server) moveCode(r *http.Request, _ None) (MoveCodeOut, error) {
	member, err := s.db.Member(r.Context(), r.PathValue("id"))
	if err != nil {
		return MoveCodeOut{}, memberError(err)
	}
	out, err := s.issueMoveCodes(r, []db.Waiting{{PublicID: member.PublicID, Username: member.Username}})
	if err != nil {
		return MoveCodeOut{}, err
	}
	return out[0], nil
}

func (s *Server) moveCodes(r *http.Request, _ None) (Page[MoveCodeOut], error) {
	waiting, err := s.db.WaitingAccounts(r.Context(), s.now())
	if err != nil {
		return Page[MoveCodeOut]{}, err
	}
	out, err := s.issueMoveCodes(r, waiting)
	return Page[MoveCodeOut]{Items: out}, err
}

// moveLimits count wrong move codes and old passwords against the network they came from.
func (s *Server) moveLimits(r *http.Request) []db.Limit {
	source := ""
	if addr, ok := s.networks().Client(r); ok {
		source = auth.SourceKey(addr)
	}
	return append([]db.Limit{{Bucket: "move", Key: source, Delay: auth.StandardPasswords.Delay}}, s.sourceLimit(r)...)
}

func errBadMoveCode() *Error {
	return refusal(http.StatusUnauthorized, "move_code_invalid", "That code or link doesn't work. Ask the server's owner for a new one.", false)
}

func (s *Server) moveCheck(r *http.Request, req MoveCheckRequest) (MoveCheck, error) {
	ctx, now := r.Context(), s.now()
	move, moving, err := s.db.CurrentMove(ctx)
	if err != nil {
		return MoveCheck{}, err
	}
	limits := s.moveLimits(r)
	if wait, err := s.db.LimitWait(ctx, now, limits...); err != nil {
		return MoveCheck{}, err
	} else if wait > 0 {
		return MoveCheck{}, errTooManyAttempts(wait)
	}
	next := "choose_password"
	if move.To == "portico" {
		next = "portico_account"
	}
	if req.Code != "" {
		account, err := s.db.MoveCodeAccount(ctx, moveHash(req.Code), now)
		if errors.Is(err, db.ErrBadCredentials) || err == nil && !moving {
			if err := s.db.RecordFailure(ctx, now, limits...); err != nil {
				return MoveCheck{}, err
			}
			return MoveCheck{}, errBadMoveCode()
		}
		if err != nil {
			return MoveCheck{}, err
		}
		return MoveCheck{Username: account.Username, Next: next}, nil
	}
	creds, limits, err := s.oldPassword(r, req.Username, req.Password)
	if err != nil {
		return MoveCheck{}, err
	}
	if creds.UserID != 0 && moving && move.To == "portico" && now.Sub(move.At) < db.OldPasswordWorks {
		if account, err := s.db.Account(ctx, creds.UserID); err == nil && account.State == "waiting" && !account.Disabled {
			return MoveCheck{Username: account.Username, Next: next}, nil
		}
	}
	if err := s.db.RecordFailure(ctx, now, limits...); err != nil {
		return MoveCheck{}, err
	}
	return MoveCheck{}, errBadMoveCode()
}

// oldPassword checks a waiting person's old password as strictly as a sign-in: counted against
// the account as well as the network, and waited out first. An account with two-step sign-in
// never moves on its password alone: it needs a code from the owner. Credentials come back
// with UserID 0 unless the password is right and may be used; the limits are what a wrong
// guess counts against.
func (s *Server) oldPassword(r *http.Request, username, password string) (db.Credentials, []db.Limit, error) {
	ctx, now := r.Context(), s.now()
	key, _ := auth.Username(username)
	limits := s.passwordLimits(r, key)
	if wait, err := s.db.LimitWait(ctx, now, limits...); err != nil {
		return db.Credentials{}, limits, err
	} else if wait > 0 {
		return db.Credentials{}, limits, errTooManyAttempts(wait)
	}
	creds, err := s.db.CredentialsByUsername(ctx, key)
	if err != nil {
		return db.Credentials{}, limits, err
	}
	ok, err := s.passwords.Check(ctx, creds.PasswordHash, password)
	if err != nil || !ok {
		return db.Credentials{}, limits, err
	}
	account, err := s.db.Account(ctx, creds.UserID)
	if err != nil {
		return db.Credentials{}, limits, err
	}
	if member, err := s.db.Member(ctx, account.PublicID); err != nil {
		return db.Credentials{}, limits, err
	} else if member.TwoFactor {
		return db.Credentials{}, limits, ErrConflict("move_code_required",
			"This account uses two-step sign-in, so it moves with a code from the server's owner, not its old password.")
	}
	return creds, limits, nil
}

func (s *Server) moveComplete(r *http.Request, req MoveCompleteRequest) (SignInResponse, error) {
	ctx, now := r.Context(), s.now()
	move, moving, err := s.db.CurrentMove(ctx)
	if err != nil {
		return SignInResponse{}, err
	}
	if !moving {
		return SignInResponse{}, errBadMoveCode()
	}
	if move.To == "portico" {
		return s.moveToPortico(r, req)
	}
	username, ok := auth.Username(req.Username)
	if !ok {
		return SignInResponse{}, ErrField("username", "Choose a username of 3 to 64 characters with no spaces or @.")
	}
	if problem := auth.PasswordProblem(req.Password); problem != "" {
		return SignInResponse{}, ErrField("password", problem)
	}
	session, refresh, err := s.newSession(r, req.Device, now)
	if err != nil {
		return SignInResponse{}, err
	}
	limits := s.moveLimits(r)
	if wait, err := s.db.LimitWait(ctx, now, limits...); err != nil {
		return SignInResponse{}, err
	} else if wait > 0 {
		return SignInResponse{}, errTooManyAttempts(wait)
	}
	hash, err := s.passwords.Hash(ctx, req.Password)
	if err != nil {
		return SignInResponse{}, err
	}
	st, err := s.db.CompleteMoveToLocal(ctx, db.MoveToLocal{CodeHash: moveHash(req.Code), Username: username, PasswordHash: hash,
		Limits: limits, Session: session, Now: now})
	switch {
	case errors.Is(err, db.ErrBadCredentials):
		return SignInResponse{}, errBadMoveCode()
	case errors.Is(err, db.ErrConflict):
		return SignInResponse{}, ErrConflict("username_taken", "That username is already in use.")
	case err != nil:
		return SignInResponse{}, err
	}
	return s.signedIn(ctx, st, now, refresh)
}

// porticoTaken answers a Portico Account that is already on this server as another account.
func (s *Server) porticoTaken(r *http.Request, err error) error {
	var taken *db.PorticoAccountTaken
	if !errors.As(err, &taken) {
		return err
	}
	name := s.serverName()
	return ErrConflict("portico_account_taken", "That Portico Account is already on "+name+" as "+taken.Username+".")
}

// moveToPortico links a waiting person's account to the Portico Account they approved with,
// proven by their move code or, within 30 days of the switch, their old password.
func (s *Server) moveToPortico(r *http.Request, req MoveCompleteRequest) (SignInResponse, error) {
	ctx, now := r.Context(), s.now()
	session, refresh, err := s.newSession(r, req.Device, now)
	if err != nil {
		return SignInResponse{}, err
	}
	limits := s.moveLimits(r)
	if wait, err := s.db.LimitWait(ctx, now, limits...); err != nil {
		return SignInResponse{}, err
	} else if wait > 0 {
		return SignInResponse{}, errTooManyAttempts(wait)
	}
	account, _, err := s.porticoProven(r, req.Challenge, req.Identity, now)
	if err != nil {
		return SignInResponse{}, err
	}
	m := db.MoveToPortico{PorticoAccountID: account.ID, Limits: limits, Session: session, Now: now}
	m.Username, _ = auth.Username(account.Username)
	if req.Code != "" {
		m.CodeHash = moveHash(req.Code)
	} else {
		creds, passwordLimits, err := s.oldPassword(r, req.Username, req.Password)
		if err != nil {
			return SignInResponse{}, err
		}
		// A wrong password is counted by the transaction, against the account too; a hash
		// nobody has never stands.
		m.UserID, m.CheckedHash, m.Limits = creds.UserID, creds.PasswordHash, passwordLimits
		if creds.UserID == 0 {
			m.CheckedHash = "-"
		}
	}
	st, err := s.db.CompleteMoveToPortico(ctx, m)
	switch {
	case errors.Is(err, db.ErrBadCredentials):
		return SignInResponse{}, errBadMoveCode()
	case err != nil:
		return SignInResponse{}, s.porticoTaken(r, err)
	}
	return s.signedIn(ctx, st, now, refresh)
}

func (s *Server) changeOwner(r *http.Request, req OwnerChangeRequest) (None, error) {
	ctx, now, caller := r.Context(), s.now(), CallerFrom(r.Context())
	if req.Previous != "admin" && req.Previous != "member" && req.Previous != "removed" {
		return None{}, ErrField("previousOwner", "Choose admin, member or removed.")
	}
	if (req.AccountID == "") == (req.NewAccount == nil) {
		return None{}, ErrField("accountId", "Choose an existing account or make a new one, not both.")
	}
	kind, err := s.signInKind(ctx)
	if err != nil {
		return None{}, err
	}
	change := db.OwnerChange{OwnerID: caller.UserID, Previous: req.Previous, Now: now}
	if kind == "portico" {
		// The new owner approves with their Portico Account; nothing else proves them.
		decision, err := s.approved(r, req.Approval, hosted.PurposeChangeOwner)
		if err != nil {
			return None{}, err
		}
		account := decision.Account
		username, ok := auth.Username(account.Username)
		if !ok {
			return None{}, errors.New("portico services answered a username this server cannot use")
		}
		change.New = &db.NewAccount{Username: username, ProfileName: account.Username, PorticoAccountID: account.ID}
	} else {
		proof, err := s.passwordProof(r, req.Password, req.Code)
		if err != nil {
			return None{}, proofError(err)
		}
		change.Proof = &proof
		if req.NewAccount != nil {
			username, ok := auth.Username(req.NewAccount.Username)
			if !ok {
				return None{}, ErrField("newAccount.username", "Choose a username of 3 to 64 characters with no spaces or @.")
			}
			if problem := auth.PasswordProblem(req.NewAccount.Password); problem != "" {
				return None{}, ErrField("newAccount.password", problem)
			}
			name := req.NewAccount.ProfileName
			if name == "" {
				name = strings.TrimSpace(req.NewAccount.Username)
			}
			if !auth.ValidName(name) {
				return None{}, ErrField("newAccount.profileName", "Give the profile a name of at most 120 characters.")
			}
			hash, err := s.passwords.Hash(ctx, req.NewAccount.Password)
			if err != nil {
				return None{}, err
			}
			change.New = &db.NewAccount{Username: username, PasswordHash: hash, ProfileName: name}
		} else {
			member, err := s.db.Member(ctx, req.AccountID)
			if err != nil {
				return None{}, memberError(err)
			}
			change.TargetID = member.ID
		}
	}
	_, err = s.db.ChangeOwner(ctx, change)
	switch {
	case errors.Is(err, db.ErrNotActive):
		return None{}, ErrConflict("account_not_active", "That account cannot be given the server: it is turned off or has not moved.")
	case errors.Is(err, db.ErrConflict) && kind == "portico":
		return None{}, ErrConflict("portico_account_taken", "That Portico Account is already on this server as another account.")
	case err != nil:
		return None{}, proofError(memberError(err))
	}
	return None{}, nil
}

func (s *Server) leave(r *http.Request, req LeaveRequest) (None, error) {
	kind, err := s.signInKind(r.Context())
	if err != nil {
		return None{}, err
	}
	if kind != "portico" {
		return None{}, ErrConflict("not_portico_accounts", "On a server with local sign-in, sign out, or ask the owner to delete your account.")
	}
	caller := CallerFrom(r.Context())
	if !caller.Manage {
		return None{}, ErrConflict("main_profile_required", "Leave the server from your main profile.")
	}
	// Portico Services keeps the list of which accounts belong to which servers. Telling it
	// is not built yet: see internal/hosted.
	err = s.db.LeaveServer(r.Context(), caller.UserID, req.DeleteData, s.now())
	if errors.Is(err, db.ErrNotPermitted) {
		return None{}, ErrConflict("owner_cannot_leave", "The owner cannot leave. Change the owner first.")
	}
	return None{}, err
}
