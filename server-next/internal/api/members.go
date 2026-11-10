package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// What the owner and admins may do to other people's accounts. The owner manages admins
// and members; an admin manages members; nobody manages the owner, or themselves here.
// A member's profiles are managed through the profile routes with ?member=<account id>.

// resetCodeLife is how long a password reset code can be used.
const resetCodeLife = time.Hour

// Member is an account as the owner and admins see it.
type Member struct {
	Account
	Disabled  bool      `json:"disabled"`
	CreatedAt time.Time `json:"createdAt"`
	// AllLibraries, or else Libraries, says what the member may see.
	AllLibraries     bool     `json:"allLibraries"`
	Libraries        []string `json:"libraries"`
	TwoFactorEnabled bool     `json:"twoFactorEnabled"`
	// Notes are the admins' private notes about the member. The member never sees them.
	Notes string `json:"notes,omitempty"`
}

// MemberCreate makes an account directly, with a first password to pass on to its person.
type MemberCreate struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// Role is member (the default) or admin.
	Role        string `json:"role,omitempty"`
	ProfileName string `json:"profileName,omitempty"`
}

// MemberEdit changes an account. Fields left out stay as they are.
type MemberEdit struct {
	Role         *string   `json:"role,omitempty"`
	Disabled     *bool     `json:"disabled,omitempty"`
	Notes        *string   `json:"notes,omitempty"`
	AllLibraries *bool     `json:"allLibraries,omitempty"`
	Libraries    *[]string `json:"libraries,omitempty"`
}

// PasswordResetCreate asks for a reset code for a member.
type PasswordResetCreate struct {
	// ResetTwoFactor also turns the member's two-step sign-in off when the code is used.
	ResetTwoFactor bool `json:"resetTwoFactor,omitempty"`
}

// PasswordResetCode is shown once, for the admin to pass on to the member.
type PasswordResetCode struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// PasswordResetRequest is a member choosing a new password with the code they were given.
type PasswordResetRequest struct {
	Username    string `json:"username"`
	Code        string `json:"code"`
	NewPassword string `json:"newPassword"`
}

func memberOut(m db.Member) Member {
	libraries := m.Libraries
	if libraries == nil {
		libraries = []string{}
	}
	return Member{Account: accountOut(m.Account), Disabled: m.Disabled, CreatedAt: m.CreatedAt.UTC(), AllLibraries: m.AllLibraries,
		Libraries: libraries, TwoFactorEnabled: m.TwoFactor, Notes: m.Notes}
}

// memberError maps what the data layer says about a managed account to a refusal.
func memberError(err error) error {
	switch {
	case errors.Is(err, db.ErrNotFound):
		return ErrNotFound()
	case errors.Is(err, db.ErrNotPermitted):
		return ErrNotPermitted()
	case errors.Is(err, db.ErrUnknownLibrary):
		return ErrField("libraries", "One of these libraries does not exist.")
	case errors.Is(err, db.ErrConflict):
		return ErrConflict("username_taken", "That username is already in use.")
	case errors.Is(err, db.ErrKeyReused):
		return errKeyReused()
	}
	return err
}

func (s *Server) memberRoutes() {
	Handle(s, "GET /v1/admin/members", Admin, s.listMembers)
	Handle(s, "POST /v1/admin/members", Admin, s.createMember)
	Handle(s, "GET /v1/admin/members/{id}", Admin, s.getMember)
	Handle(s, "PATCH /v1/admin/members/{id}", Admin, s.editMember)
	Handle(s, "DELETE /v1/admin/members/{id}", Admin, s.deleteMember)
	// Passing a code that takes over an account is for a signed-in person, never a key.
	Handle(s, "POST /v1/admin/members/{id}/password-reset", Admin, s.issuePasswordReset).Scope = ScopeSession
}

func (s *Server) listMembers(r *http.Request, _ None) (Page[Member], error) {
	members, err := s.db.Members(r.Context())
	out := Page[Member]{Items: make([]Member, 0, len(members))}
	for _, m := range members {
		out.Items = append(out.Items, memberOut(m))
	}
	return out, err
}

func (s *Server) getMember(r *http.Request, _ None) (Member, error) {
	m, err := s.db.Member(r.Context(), r.PathValue("id"))
	return memberOut(m), memberError(err)
}

func validRole(role string) bool { return role == "member" || role == "admin" }

// newAccount checks the fields every way of creating an account shares, and hashes the password.
func (s *Server) newAccount(r *http.Request, username, password string, profileName *string) (stored, hash string, err error) {
	stored, ok := auth.Username(username)
	if !ok {
		return "", "", ErrField("username", "Choose a username of 3 to 64 characters with no spaces or @.")
	}
	if problem := auth.PasswordProblem(password); problem != "" {
		return "", "", ErrField("password", problem)
	}
	if *profileName == "" {
		*profileName = strings.TrimSpace(username)
	}
	if !auth.ValidName(*profileName) {
		return "", "", ErrField("profileName", "Give the profile a name of at most 120 characters.")
	}
	hash, err = s.passwords.Hash(r.Context(), password)
	return stored, hash, err
}

func (s *Server) createMember(r *http.Request, req MemberCreate) (Member, error) {
	caller := CallerFrom(r.Context())
	if req.Role == "" {
		req.Role = "member"
	}
	if !validRole(req.Role) {
		return Member{}, ErrField("role", "A role is member or admin.")
	}
	if !db.Manages(caller.Role, req.Role) {
		return Member{}, ErrNotPermitted()
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return Member{}, err
	}
	// The password is left out of what identifies the request: the hash is kept for 35 days
	// and a plain digest of a password is easy to reverse.
	withoutPassword := req
	withoutPassword.Password = ""
	username, hash, err := s.newAccount(r, req.Username, req.Password, &req.ProfileName)
	if err != nil {
		return Member{}, err
	}
	m, err := s.db.CreateMember(r.Context(), db.NewMember{ActorID: caller.UserID, ActorRole: caller.Role, Username: username,
		PasswordHash: hash, Role: req.Role, ProfileName: req.ProfileName, IdempotencyKey: key, RequestHash: requestHash(withoutPassword), Now: s.now()})
	return memberOut(m), memberError(err)
}

func (s *Server) editMember(r *http.Request, req MemberEdit) (Member, error) {
	caller := CallerFrom(r.Context())
	switch {
	case req.Role != nil && !validRole(*req.Role):
		return Member{}, ErrField("role", "A role is member or admin.")
	case req.Notes != nil && len(*req.Notes) > 4000:
		return Member{}, ErrField("notes", "Notes are at most 4,000 characters.")
	case req.Libraries != nil && len(*req.Libraries) > 1000:
		return Member{}, ErrField("libraries", "That is too many libraries.")
	}
	m, err := s.db.UpdateMember(r.Context(), db.MemberEdit{ActorID: caller.UserID, ActorRole: caller.Role, PublicID: r.PathValue("id"),
		Role: req.Role, Disabled: req.Disabled, Notes: req.Notes, AllLibraries: req.AllLibraries, Libraries: req.Libraries, Now: s.now()})
	return memberOut(m), memberError(err)
}

func (s *Server) deleteMember(r *http.Request, _ None) (None, error) {
	caller := CallerFrom(r.Context())
	return None{}, memberError(s.db.DeleteMember(r.Context(), caller.UserID, caller.Role, r.PathValue("id"), s.now()))
}

func resetHash(typed string) string { return auth.Hash("reset|" + auth.CodeKey(typed)) }

func (s *Server) issuePasswordReset(r *http.Request, req PasswordResetCreate) (PasswordResetCode, error) {
	caller, now := CallerFrom(r.Context()), s.now()
	out := PasswordResetCode{Code: auth.NewCode(3), ExpiresAt: now.Add(resetCodeLife).UTC()}
	err := s.db.IssuePasswordReset(r.Context(), db.PasswordReset{ActorID: caller.UserID, ActorRole: caller.Role, PublicID: r.PathValue("id"),
		CodeHash: resetHash(out.Code), ResetTwoFactor: req.ResetTwoFactor, ExpiresAt: out.ExpiresAt, Now: now})
	return out, memberError(err)
}

// resetPassword does the one thing a reset code allows: choosing a new password. It answers
// a wrong code, a used code and a username nobody has the same way. Five wrong codes cancel
// the account's code, and every wrong one counts against the network it came from.
func (s *Server) resetPassword(r *http.Request, req PasswordResetRequest) (None, error) {
	ctx, now := r.Context(), s.now()
	if problem := auth.PasswordProblem(req.NewPassword); problem != "" {
		return None{}, ErrField("newPassword", problem)
	}
	key, _ := auth.Username(req.Username)
	limits := sourceLimit(r)
	if wait, err := s.db.LimitWait(ctx, now, limits...); err != nil {
		return None{}, err
	} else if wait > 0 {
		return None{}, errTooManyAttempts(wait)
	}
	hash, err := s.passwords.Hash(ctx, req.NewPassword)
	if err != nil {
		return None{}, err
	}
	err = s.db.RedeemPasswordReset(ctx, db.ResetRedemption{Username: key, CodeHash: resetHash(req.Code), NewHash: hash, Limits: limits, Now: now})
	var limited *db.LimitedError
	switch {
	case errors.As(err, &limited):
		return None{}, errTooManyAttempts(limited.Wait)
	case errors.Is(err, db.ErrBadCredentials):
		return None{}, refusal(http.StatusUnauthorized, "unauthorized", "That username or reset code is not correct.", false)
	}
	return None{}, err
}
