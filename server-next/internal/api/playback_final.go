package api

import (
	"context"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// A signed token binds a final observation to its original play without retaining the session.
// It still requires a current sign-in on that account/device and current item access.
type playbackProgressClaim struct {
	Viewer    db.Viewer
	Account   string
	Profile   string
	DeviceID  int64
	Device    string
	PlayID    string
	ItemID    string
	StartedAt int64
	Away      bool
	Delivery  db.PlayDelivery
	OwnerOnly bool
}

func (s *Server) progressToken(v playback.Session) string {
	if v.ID == "" || v.Device.ID == 0 || v.ProfileID == 0 {
		return ""
	}
	return s.tokens.Remember("playback-progress", playbackProgressClaim{Viewer: v.Viewer(), DeviceID: v.Device.ID,
		Device: v.Device.PublicID, Account: v.Device.UserPublicID, Profile: v.ProfilePublicID, PlayID: v.ID, ItemID: v.Item.PublicID, StartedAt: v.StartedAt.UnixMilli(),
		Away: v.Network != playback.NetworkLocal, Delivery: db.ParsePlayDelivery(v.Plan.Route), OwnerOnly: v.Channel != nil})
}

func (s *Server) saveFinalPlayback(r *http.Request, req PlaybackStop) error {
	if req.ProgressToken == "" {
		if req.MadeAgoMs != 0 {
			return ErrField("progressToken", "Send this play's progress token with a delayed final position.")
		}
		return nil
	}
	if req.PositionMs == nil {
		return ErrField("positionMs", "Send the final observed position.")
	}
	if req.MadeAgoMs < 0 || req.MadeAgoMs > db.MaxEditAge.Milliseconds() {
		return ErrField("madeAgoMs", "A position must be between now and 35 days old.")
	}
	if req.PlayClockMs < 0 {
		return ErrField("playClockMs", "Played time cannot be negative.")
	}
	var claim playbackProgressClaim
	c := CallerFrom(r.Context())
	if !s.tokens.Recall("playback-progress", req.ProgressToken, &claim) || claim.PlayID != r.PathValue("id") || claim.Viewer.UserID != c.UserID {
		return ErrNotFound()
	}
	device, err := s.db.SessionDevice(r.Context(), c.SessionID)
	if err != nil {
		return refuse(err)
	}
	if device.ID != claim.DeviceID || device.PublicID != claim.Device || device.UserPublicID != claim.Account {
		return ErrNotFound()
	}
	at := s.now().Add(-time.Duration(req.MadeAgoMs) * time.Millisecond)
	elapsed := at.UnixMilli() - claim.StartedAt
	if elapsed < 0 {
		return ErrField("madeAgoMs", "The position must have been observed after this play started.")
	}
	_, err = s.db.SaveProgress(context.WithoutCancel(r.Context()), db.User, []db.Progress{{Viewer: claim.Viewer,
		ProfilePublicID: claim.Profile, DeviceID: claim.DeviceID, ItemID: claim.ItemID, PlayID: claim.PlayID, At: at, PositionMS: *req.PositionMs,
		WatchedMS: min(req.PlayClockMs, elapsed), Away: claim.Away, Delivery: claim.Delivery, OwnerOnly: claim.OwnerOnly}})
	return err
}
