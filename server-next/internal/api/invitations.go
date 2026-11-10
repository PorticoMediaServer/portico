package api

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Invitation lets one person create their own account on this server.
type Invitation struct {
	ID string `json:"id"`
	// Code is what the invited person types, and what a link or QR code carries. It is
	// present while the invitation is open, so it can be copied again.
	Code string `json:"code,omitempty"`
	Role string `json:"role"`
	// State is open, accepted, revoked or expired.
	State        string     `json:"state"`
	AllLibraries bool       `json:"allLibraries"`
	Libraries    []string   `json:"libraries"`
	Note         string     `json:"note,omitempty"`
	CreatedBy    string     `json:"createdBy,omitempty"`
	AcceptedBy   string     `json:"acceptedBy,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	ExpiresAt    time.Time  `json:"expiresAt"`
	AcceptedAt   *time.Time `json:"acceptedAt,omitempty"`
}

// InvitationCreate makes an invitation.
type InvitationCreate struct {
	// Role is member (the default) or admin. Only the owner can invite an admin.
	Role string `json:"role,omitempty"`
	// AllLibraries defaults to true. Set it false and list Libraries to share only some.
	AllLibraries *bool    `json:"allLibraries,omitempty"`
	Libraries    []string `json:"libraries,omitempty"`
	// Note is the admins' private note. The invited person never sees it.
	Note string `json:"note,omitempty"`
	// ExpiresInDays is 1, 3, 7 (the default) or 30.
	ExpiresInDays int `json:"expiresInDays,omitempty"`
}

// InvitationEdit changes an invitation that is still open. Fields left out stay as they
// are, and the code stays the same. ExpiresInDays counts from now.
type InvitationEdit struct {
	Role          *string   `json:"role,omitempty"`
	AllLibraries  *bool     `json:"allLibraries,omitempty"`
	Libraries     *[]string `json:"libraries,omitempty"`
	Note          *string   `json:"note,omitempty"`
	ExpiresInDays *int      `json:"expiresInDays,omitempty"`
}

// InvitationPreview is what the holder of a code sees before accepting: the server, the
// role and what is shared. It never says who invited them, and never the note.
type InvitationPreview struct {
	ServerName string `json:"serverName"`
	Role       string `json:"role"`
	// AllLibraries, or else the names in Libraries, is what the new account will see.
	AllLibraries bool      `json:"allLibraries"`
	Libraries    []string  `json:"libraries"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// InvitationAccept creates the invited person's account and signs them in.
type InvitationAccept struct {
	Username    string     `json:"username"`
	Password    string     `json:"password"`
	ProfileName string     `json:"profileName,omitempty"`
	Device      DeviceInfo `json:"device"`
}

func (s *Server) invitationOut(i db.Invitation) Invitation {
	out := Invitation{ID: i.PublicID, Role: i.Role, State: i.State, AllLibraries: i.AllLibraries, Libraries: i.Libraries, Note: i.Note,
		CreatedBy: i.CreatedBy, AcceptedBy: i.AcceptedBy, CreatedAt: i.CreatedAt.UTC(), ExpiresAt: i.ExpiresAt.UTC()}
	if out.Libraries == nil {
		out.Libraries = []string{}
	}
	if i.State == "open" {
		out.Code = s.tokens.InvitationCode(i.PublicID)
	}
	if !i.AcceptedAt.IsZero() {
		at := i.AcceptedAt.UTC()
		out.AcceptedAt = &at
	}
	return out
}

func errNoInvitation() *Error {
	return refusal(http.StatusNotFound, "invitation_not_found", "This invitation is no longer valid.", false)
}

func (s *Server) invitationRoutes() {
	Handle(s, "GET /v1/admin/invitations", Admin, s.listInvitations)
	Handle(s, "POST /v1/admin/invitations", Admin, s.createInvitation)
	Handle(s, "PATCH /v1/admin/invitations/{id}", Admin, s.editInvitation)
	Handle(s, "DELETE /v1/admin/invitations/{id}", Admin, s.revokeInvitation)
	// For the person invited, the id in the path is the code they were given.
	Handle(s, "GET /v1/auth/invitations/{id}", Public, s.previewInvitation)
	Handle(s, "POST /v1/auth/invitations/{id}/accept", Public, s.acceptInvitation)
}

func invitationHash(typed string) string { return auth.Hash("invitation|" + auth.CodeKey(typed)) }

func (s *Server) listInvitations(r *http.Request, _ None) (Page[Invitation], error) {
	invitations, err := s.db.Invitations(r.Context(), s.now())
	out := Page[Invitation]{Items: make([]Invitation, 0, len(invitations))}
	for _, i := range invitations {
		out.Items = append(out.Items, s.invitationOut(i))
	}
	return out, err
}

// invitationDays are how long an invitation can be made to last.
var invitationDays = []int{1, 3, 7, 30}

func checkInvitation(role *string, days *int, libraries *[]string, note *string) error {
	switch {
	case role != nil && !validRole(*role):
		return ErrField("role", "A role is member or admin.")
	case days != nil && !slices.Contains(invitationDays, *days):
		return ErrField("expiresInDays", "An invitation lasts 1, 3, 7 or 30 days.")
	case libraries != nil && len(*libraries) > 256:
		return ErrField("libraries", "That is too many libraries.")
	case note != nil && len(*note) > 4000:
		return ErrField("note", "A note is at most 4,000 characters.")
	}
	return nil
}

func (s *Server) createInvitation(r *http.Request, req InvitationCreate) (Invitation, error) {
	caller, now := CallerFrom(r.Context()), s.now()
	if req.Role == "" {
		req.Role = "member"
	}
	if req.ExpiresInDays == 0 {
		req.ExpiresInDays = 7
	}
	if err := checkInvitation(&req.Role, &req.ExpiresInDays, &req.Libraries, &req.Note); err != nil {
		return Invitation{}, err
	}
	id := db.NewID("inv")
	i, err := s.db.CreateInvitation(r.Context(), db.NewInvitation{ActorID: caller.UserID, ActorRole: caller.Role,
		PublicID: id, CodeHash: invitationHash(s.tokens.InvitationCode(id)),
		Role: req.Role, AllLibraries: req.AllLibraries == nil || *req.AllLibraries, Libraries: req.Libraries, Note: req.Note,
		ExpiresAt: now.AddDate(0, 0, req.ExpiresInDays), Now: now})
	if errors.Is(err, db.ErrInvitationLimit) {
		return Invitation{}, ErrConflict("invitation_capacity", "Too many invitations are waiting. Revoke some first.")
	}
	return s.invitationOut(i), memberError(err)
}

func (s *Server) editInvitation(r *http.Request, req InvitationEdit) (Invitation, error) {
	caller, now := CallerFrom(r.Context()), s.now()
	if err := checkInvitation(req.Role, req.ExpiresInDays, req.Libraries, req.Note); err != nil {
		return Invitation{}, err
	}
	e := db.InvitationEdit{ActorID: caller.UserID, ActorRole: caller.Role, PublicID: r.PathValue("id"),
		Role: req.Role, AllLibraries: req.AllLibraries, Libraries: req.Libraries, Note: req.Note, Now: now}
	if req.ExpiresInDays != nil {
		expires := now.AddDate(0, 0, *req.ExpiresInDays)
		e.ExpiresAt = &expires
	}
	i, err := s.db.UpdateInvitation(r.Context(), e)
	if errors.Is(err, db.ErrNotFound) {
		return Invitation{}, errNoInvitation()
	}
	return s.invitationOut(i), memberError(err)
}

func (s *Server) revokeInvitation(r *http.Request, _ None) (None, error) {
	return None{}, s.db.RevokeInvitation(r.Context(), CallerFrom(r.Context()).UserID, r.PathValue("id"), s.now())
}

// openInvitation finds the invitation behind a typed code, counting a wrong one against the source.
func (s *Server) openInvitation(r *http.Request, code string) (db.Invitation, error) {
	limits := s.sourceLimit(r)
	if wait, err := s.db.LimitWait(r.Context(), s.now(), limits...); err != nil {
		return db.Invitation{}, err
	} else if wait > 0 {
		return db.Invitation{}, errTooManyAttempts(wait)
	}
	i, err := s.db.InvitationByCode(r.Context(), invitationHash(code), s.now())
	if errors.Is(err, db.ErrNotFound) {
		if err := s.db.RecordFailure(r.Context(), s.now(), limits...); err != nil {
			return i, err
		}
		return i, errNoInvitation()
	}
	return i, err
}

func (s *Server) previewInvitation(r *http.Request, _ None) (InvitationPreview, error) {
	i, err := s.openInvitation(r, r.PathValue("id"))
	if err != nil {
		return InvitationPreview{}, err
	}
	name, err := s.serverName(r)
	out := InvitationPreview{ServerName: name, Role: i.Role, AllLibraries: i.AllLibraries, Libraries: i.LibraryNames, ExpiresAt: i.ExpiresAt.UTC()}
	if out.Libraries == nil {
		out.Libraries = []string{}
	}
	return out, err
}

func (s *Server) acceptInvitation(r *http.Request, req InvitationAccept) (SignInResponse, error) {
	ctx, now := r.Context(), s.now()
	session, refresh, err := s.newSession(r, req.Device, now)
	if err != nil {
		return SignInResponse{}, err
	}
	// Look before hashing a password, so a wrong code costs the server little and is counted.
	if _, err := s.openInvitation(r, r.PathValue("id")); err != nil {
		if e, ok := asError(err); ok && e.Code == errNoInvitation().Code {
			return s.acceptedAgain(r, req)
		}
		return SignInResponse{}, err
	}
	username, hash, err := s.newAccount(r, req.Username, req.Password, &req.ProfileName)
	if err != nil {
		return SignInResponse{}, err
	}
	st, err := s.db.AcceptInvitation(ctx, db.Acceptance{CodeHash: invitationHash(r.PathValue("id")), Username: username,
		PasswordHash: hash, ProfileName: req.ProfileName, Session: session, Now: now})
	switch {
	case errors.Is(err, db.ErrNotFound):
		// Another acceptance of this invitation committed first: perhaps this one's first try.
		return s.acceptedAgain(r, req)
	case err != nil:
		return SignInResponse{}, memberError(err)
	}
	return s.signedIn(ctx, st, now, refresh)
}

// acceptedAgain answers an acceptance of an invitation that is no longer open. When it is
// the same acceptance sent again after its answer was lost, the person must not be stranded
// with an account they cannot reach: if the invitation created an account with this username,
// the request is answered as a sign-in to it, with the password checked and counted as any
// sign-in is. Anything else, a wrong password included, is told the invitation is used up,
// so the answer says no more than the sign-in route would.
func (s *Server) acceptedAgain(r *http.Request, req InvitationAccept) (SignInResponse, error) {
	username, err := s.db.InvitationAccount(r.Context(), invitationHash(r.PathValue("id")))
	if errors.Is(err, db.ErrNotFound) {
		return SignInResponse{}, errNoInvitation()
	} else if err != nil {
		return SignInResponse{}, err
	}
	if key, _ := auth.Username(req.Username); key != username {
		return SignInResponse{}, errNoInvitation()
	}
	out, err := s.signIn(r, SignInRequest{Username: req.Username, Password: req.Password, Device: req.Device})
	if _, refused := asError(err); refused {
		return SignInResponse{}, errNoInvitation()
	}
	return out, err
}
