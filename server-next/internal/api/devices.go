package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// SessionInfo is one sign-in in a person's session list.
type SessionInfo struct {
	ID         string `json:"id"`
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	// ProfileID is the profile the session is on, if one is selected.
	ProfileID  string    `json:"profileId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastUsedAt time.Time `json:"lastUsedAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	// EndedAt and EndReason are present once the session is over. Ended sessions are listed for 30 days.
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	EndReason string     `json:"endReason,omitempty"`
	// Address is the network address the session was last used from.
	Address string `json:"address,omitempty"`
	// Current marks the session this request was made with.
	Current bool `json:"current"`
}

// Device is an app install or browser the person has signed in on.
type Device struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Platform   string    `json:"platform,omitempty"`
	App        string    `json:"app,omitempty"`
	AppVersion string    `json:"appVersion,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	// SignedIn is true while the person has a live session on the device.
	SignedIn bool `json:"signedIn"`
	// Current marks the device this request was made from.
	Current bool `json:"current"`
}

// DeviceEdit renames a device.
type DeviceEdit struct {
	Name string `json:"name"`
}

func (s *Server) deviceRoutes() {
	Handle(s, "GET /v1/me/sessions", SignedIn, s.listSessions)
	Handle(s, "DELETE /v1/me/sessions", SignedIn, s.endOtherSessions)
	Handle(s, "DELETE /v1/me/sessions/{id}", SignedIn, s.endSession)
	Handle(s, "GET /v1/devices", SignedIn, s.listDevices)
	Handle(s, "PATCH /v1/devices/{id}", SignedIn, s.renameDevice)
	Handle(s, "DELETE /v1/devices/{id}", SignedIn, s.forgetDevice)
}

func (s *Server) listSessions(r *http.Request, _ None) (Page[SessionInfo], error) {
	caller, err := manager(r)
	if err != nil {
		return Page[SessionInfo]{}, err
	}
	sessions, err := s.db.Sessions(r.Context(), caller.UserID, s.now())
	out := Page[SessionInfo]{Items: make([]SessionInfo, 0, len(sessions))}
	for _, v := range sessions {
		info := SessionInfo{ID: v.PublicID, DeviceID: v.DevicePublicID, DeviceName: v.DeviceName, ProfileID: v.ProfilePublicID,
			CreatedAt: v.CreatedAt.UTC(), LastUsedAt: v.LastUsedAt.UTC(), ExpiresAt: v.ExpiresAt.UTC(),
			EndReason: v.EndReason, Address: v.RemoteAddr, Current: v.ID == caller.SessionID}
		if !v.EndedAt.IsZero() {
			ended := v.EndedAt.UTC()
			info.EndedAt = &ended
		}
		out.Items = append(out.Items, info)
	}
	return out, err
}

// endSession ends one of the caller's own sessions. Ending one that is already over, or
// that was never theirs, changes nothing.
func (s *Server) endSession(r *http.Request, _ None) (None, error) {
	caller, err := manager(r)
	if err != nil {
		return None{}, err
	}
	return None{}, s.db.RevokeUserSession(r.Context(), caller.UserID, r.PathValue("id"), "ended_by_user", s.now())
}

// endOtherSessions signs the caller out everywhere except on the device asking.
func (s *Server) endOtherSessions(r *http.Request, _ None) (None, error) {
	caller, err := manager(r)
	if err != nil {
		return None{}, err
	}
	return None{}, s.db.RevokeOtherSessions(r.Context(), caller.UserID, caller.SessionID, "ended_by_user", s.now())
}

func (s *Server) devices(r *http.Request, caller Caller) ([]Device, error) {
	devices, err := s.db.Devices(r.Context(), caller.UserID, caller.SessionID, s.now())
	out := make([]Device, 0, len(devices))
	for _, v := range devices {
		out = append(out, Device{ID: v.PublicID, Name: v.Name, Platform: v.Platform, App: v.App, AppVersion: v.AppVersion,
			CreatedAt: v.CreatedAt.UTC(), LastSeenAt: v.LastSeenAt.UTC(), SignedIn: v.SignedIn, Current: v.Current})
	}
	return out, err
}

func (s *Server) listDevices(r *http.Request, _ None) (Page[Device], error) {
	caller, err := manager(r)
	if err != nil {
		return Page[Device]{}, err
	}
	devices, err := s.devices(r, caller)
	return Page[Device]{Items: devices}, err
}

func (s *Server) renameDevice(r *http.Request, req DeviceEdit) (Device, error) {
	caller, err := manager(r)
	if err != nil {
		return Device{}, err
	}
	if !auth.ValidName(req.Name) {
		return Device{}, ErrField("name", "Give the device a name of at most 120 characters.")
	}
	id := r.PathValue("id")
	if err := s.db.RenameDevice(r.Context(), caller.UserID, id, req.Name); errors.Is(err, db.ErrNotFound) {
		return Device{}, ErrNotFound()
	} else if err != nil {
		return Device{}, err
	}
	devices, err := s.devices(r, caller)
	for _, d := range devices {
		if d.ID == id {
			return d, err
		}
	}
	return Device{}, errors.Join(err, ErrNotFound())
}

// forgetDevice ends the caller's sessions on a device and takes it off their list.
func (s *Server) forgetDevice(r *http.Request, _ None) (None, error) {
	caller, err := manager(r)
	if err != nil {
		return None{}, err
	}
	return None{}, s.db.ForgetDevice(r.Context(), caller.UserID, r.PathValue("id"), s.now())
}
