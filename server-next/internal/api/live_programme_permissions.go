package api

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

// programmeAllowed is the permission shared by live actions and saved reminders.
// Guide slots remain visible as unavailable, as the product definition requires.
func (s *Server) programmeAllowed(ctx context.Context, v db.Viewer, lv db.LiveViewer, p db.GuideProgramme) (bool, error) {
	if !rated(lv, p.RatingAge) {
		return false, nil
	}
	if p.ItemID != 0 {
		return s.db.Visible(ctx, v, p.ItemID)
	}
	return true, nil
}

func (s *Server) sourceProgrammeAllowed(ctx context.Context, v db.Viewer, lv db.LiveViewer, ch db.LiveChannel) (bool, error) {
	if !mayWatch(lv, ch) {
		return false, nil
	}
	if ch.GuideSource == 0 || ch.GuideKey == "" {
		return true, nil
	}
	now := s.now()
	rows, err := s.db.GuideProgrammes(ctx, ch.GuideSource, ch.GuideKey, now, now.Add(time.Millisecond), 2)
	if err != nil {
		return false, err
	}
	for _, p := range rows {
		if ok, err := s.programmeAllowed(ctx, v, lv, p); err != nil || !ok {
			return ok, err
		}
	}
	return true, nil
}

// liveViewingAllowed also runs between programmes and after limits change.
func (s *Server) liveViewingAllowed(ctx context.Context, view live.Viewing) (bool, error) {
	v := db.Viewer{UserID: view.UserID, ProfileID: view.ProfileID}
	lv, err := s.db.ViewerLive(ctx, v)
	if err != nil {
		return false, err
	}
	ch, err := s.db.LiveChannelByID(ctx, v, view.ChannelID)
	if errors.Is(err, db.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return s.sourceProgrammeAllowed(ctx, v, lv, ch)
}
