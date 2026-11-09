package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Invitation lets one person create their own account on this server.
type Invitation struct {
	ID   string `json:"id"`
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
	// ExpiresInHours is 1 to 720 and defaults to 168, a week.
	ExpiresInHours int `json:"expiresInHours,omitempty"`
}

// InvitationCreated carries the code, which is shown once. To send it again, make a new invitation.
type InvitationCreated struct {
	Invitation Invitation `json:"invitation"`
	Code       string     `json:"code"`
}

// InvitationPreview is what the holder of a code sees before accepting.
type InvitationPreview struct {
	ServerName string    `json:"serverName"`
	InvitedBy  string    `json:"invitedBy,omitempty"`
	Role       string    `json:"role"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

// InvitationAccept creates the invited person's account and signs them in.
type InvitationAccept struct {
	Username    string     `json:"username"`
	Password    string     `json:"password"`
	ProfileName string     `json:"profileName,omitempty"`
	Device      DeviceInfo `json:"device"`
}

func invitationOut(i db.Invitation) Invitation {
	out := Invitation{ID: i.PublicID, Role: i.Role, State: i.State, AllLibraries: i.AllLibraries, Libraries: i.Libraries, Note: i.Note,
		CreatedBy: i.CreatedBy, AcceptedBy: i.AcceptedBy, CreatedAt: i.CreatedAt.UTC(), ExpiresAt: i.ExpiresAt.UTC()}
	if out.Libraries == nil {
		out.Libraries = []string{}
	}
	if !i.AcceptedAt.IsZero() {
		at := i.AcceptedAt.UTC()
		out.AcceptedAt = &at
	}
	return out
}

func errNoInvitation() *Error {
	return refusal(http.StatusNotFound, "invitation_not_found", "This invitation is not valid. It may have expired or been used.", false)
}

func (s *Server) invitationRoutes() {
	Handle(s, "GET /v1/admin/invitations", Admin, s.listInvitations)
	Handle(s, "POST /v1/admin/invitations", Admin, s.createInvitation)
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
		out.Items = append(out.Items, invitationOut(i))
	}
	return out, err
}

func (s *Server) createInvitation(r *http.Request, req InvitationCreate) (InvitationCreated, error) {
	caller, now := CallerFrom(r.Context()), s.now()
	if req.Role == "" {
		req.Role = "member"
	}
	if req.ExpiresInHours == 0 {
		req.ExpiresInHours = 168
	}
	switch {
	case !validRole(req.Role):
		return InvitationCreated{}, ErrField("role", "A role is member or admin.")
	case req.ExpiresInHours < 1 || req.ExpiresInHours > 720:
		return InvitationCreated{}, ErrField("expiresInHours", "An invitation lasts 1 to 720 hours.")
	case len(req.Libraries) > 256:
		return InvitationCreated{}, ErrField("libraries", "That is too many libraries.")
	case len(req.Note) > 4000:
		return InvitationCreated{}, ErrField("note", "A note is at most 4,000 characters.")
	}
	code := auth.NewCode(4)
	i, err := s.db.CreateInvitation(r.Context(), db.NewInvitation{ActorID: caller.UserID, ActorRole: caller.Role, CodeHash: invitationHash(code),
		Role: req.Role, AllLibraries: req.AllLibraries == nil || *req.AllLibraries, Libraries: req.Libraries, Note: req.Note,
		ExpiresAt: now.Add(time.Duration(req.ExpiresInHours) * time.Hour), Now: now})
	if errors.Is(err, db.ErrInvitationLimit) {
		return InvitationCreated{}, ErrConflict("invitation_capacity", "Too many invitations are waiting. Revoke some first.")
	}
	return InvitationCreated{Invitation: invitationOut(i), Code: code}, memberError(err)
}

func (s *Server) revokeInvitation(r *http.Request, _ None) (None, error) {
	return None{}, s.db.RevokeInvitation(r.Context(), CallerFrom(r.Context()).UserID, r.PathValue("id"), s.now())
}

// openInvitation finds the invitation behind a typed code, counting a wrong one against the source.
func (s *Server) openInvitation(r *http.Request, code string) (db.Invitation, error) {
	limits := codeLimits(r)
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
	return InvitationPreview{ServerName: name, InvitedBy: i.CreatedBy, Role: i.Role, ExpiresAt: i.ExpiresAt.UTC()}, err
}

func (s *Server) acceptInvitation(r *http.Request, req InvitationAccept) (SignInResponse, error) {
	ctx, now := r.Context(), s.now()
	session, refresh, err := s.newSession(r, req.Device, now)
	if err != nil {
		return SignInResponse{}, err
	}
	// Look before hashing a password, so a wrong code costs the server little and is counted.
	if _, err := s.openInvitation(r, r.PathValue("id")); err != nil {
		return SignInResponse{}, err
	}
	key, hash, err := s.newAccount(r, req.Username, req.Password, &req.ProfileName)
	if err != nil {
		return SignInResponse{}, err
	}
	st, err := s.db.AcceptInvitation(ctx, db.Acceptance{CodeHash: invitationHash(r.PathValue("id")), Username: strings.TrimSpace(req.Username),
		UsernameKey: key, PasswordHash: hash, ProfileName: req.ProfileName, Session: session, Now: now})
	switch {
	case errors.Is(err, db.ErrNotFound):
		return SignInResponse{}, errNoInvitation()
	case err != nil:
		return SignInResponse{}, memberError(err)
	}
	return s.signedIn(ctx, st, now, refresh)
}
