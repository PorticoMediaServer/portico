package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// LiveProgrammeDetail is one programme with its other airings, and whether the profile booked it
// or set a reminder. One the profile's rating limit hides says only that, and can be neither
// watched nor recorded.
type LiveProgrammeDetail struct {
	LiveProgramme
	Channel   LiveChannel   `json:"channel"`
	Airings   []LiveAiring  `json:"airings"`
	Recording *DvrRecording `json:"recording,omitempty"`
	Reminder  bool          `json:"reminder"`
	CanRecord bool          `json:"canRecord"`
}

// LiveAiring is another time a programme is on.
type LiveAiring struct {
	ID       string    `json:"id"`
	Channel  string    `json:"channel"`
	StartsAt time.Time `json:"startsAt"`
	EndsAt   time.Time `json:"endsAt"`
	// EpisodeTitle, Season and Episode say which episode airs then, where the guide says.
	EpisodeTitle string `json:"episodeTitle,omitempty"`
	Season       *int   `json:"season,omitempty"`
	Episode      *int   `json:"episode,omitempty"`
}

// LiveReminderOut is a reminder as the apps show it, two minutes before its programme starts.
type LiveReminderOut struct {
	Programme string    `json:"programme"`
	Channel   string    `json:"channel"`
	Title     string    `json:"title"`
	StartsAt  time.Time `json:"startsAt"`
	EndsAt    time.Time `json:"endsAt"`
}

func (s *Server) liveProgrammeRoutes() {
	Handle(s, "GET /v1/live/programmes/{id}", WithProfile, s.getProgramme)
	Handle(s, "GET /v1/live/reminders", WithProfile, s.listReminders)
	Handle(s, "PUT /v1/live/reminders/{id}", WithProfile, s.setReminder)
	Handle(s, "DELETE /v1/live/reminders/{id}", WithProfile, s.deleteReminder)
}

func programmeRef(channel string, at time.Time) string {
	return encodeOpaque(map[string]any{"c": channel, "s": at.UnixMilli()})
}

func (s *Server) getProgramme(r *http.Request, _ None) (LiveProgrammeDetail, error) {
	lv, err := s.liveViewer(r)
	if err != nil {
		return LiveProgrammeDetail{}, err
	}
	ctx := r.Context()
	ch, p, err := s.programme(ctx, viewer(r), lv, r.PathValue("id"))
	if err != nil {
		return LiveProgrammeDetail{}, err
	}
	rows, err := s.programmes(ctx, viewer(r), lv, []db.LiveChannel{ch}, p.StartsAt, p.StartsAt.Add(time.Millisecond), 2)
	if err != nil {
		return LiveProgrammeDetail{}, err
	}
	out := LiveProgrammeDetail{Channel: channelOut(ch), Airings: []LiveAiring{}, CanRecord: lv.Record && ch.Kind == db.ChannelFromSource}
	for _, item := range rows[0] {
		if item.StartsAt.Equal(p.StartsAt) {
			out.LiveProgramme = item
		}
	}
	if out.NotAvailable {
		out.CanRecord = false
		return out, nil
	}
	if out.Reminder, err = s.db.HasReminder(ctx, viewer(r).ProfileID, ch.ID, p.ProgrammeKey); err != nil {
		return LiveProgrammeDetail{}, err
	}
	if rec, err := s.db.AiringRecording(ctx, ch.ID, p.ProgrammeKey); err == nil {
		recOut := s.recordingOut(rec, false, nil)
		out.Recording = &recOut
	} else if !errors.Is(err, db.ErrNotFound) {
		return LiveProgrammeDetail{}, err
	}
	if ch.Kind == db.ChannelFromSource {
		// More airings, to everyone allowed Live TV, on the channels they may watch.
		// At most 200 rows of the guide, so a common title ("News") costs a page, not the guide.
		airings, err := s.db.GuideAirings(ctx, p.SeriesKey, p.Title, s.now(), s.now().AddDate(0, 0, 14), 200)
		if err != nil {
			return LiveProgrammeDetail{}, err
		}
		for _, a := range airings {
			if a.ProgrammeKey == p.ProgrammeKey && a.StartsAt.Equal(p.StartsAt) || p.SeriesKey != "" && a.EpisodeTitle != p.EpisodeTitle {
				continue
			}
			channels, err := s.db.ChannelsForListings(ctx, a.SourceID, a.GuideKey)
			if err != nil {
				return LiveProgrammeDetail{}, err
			}
			for _, c := range channels {
				full, err := s.db.LiveChannelByID(ctx, viewer(r), c.PublicID)
				if err != nil || !mayWatch(lv, full) {
					continue
				}
				out.Airings = append(out.Airings, LiveAiring{ID: programmeRef(c.PublicID, a.StartsAt), Channel: c.Name, StartsAt: a.StartsAt.UTC(),
					EndsAt: a.EndsAt.UTC(), EpisodeTitle: a.EpisodeTitle, Season: a.Season, Episode: a.Episode})
			}
			if len(out.Airings) >= 50 {
				break
			}
		}
	}
	return out, nil
}

func (s *Server) listReminders(r *http.Request, _ None) (Page[LiveReminderOut], error) {
	lv, err := s.liveViewer(r)
	if err != nil {
		return Page[LiveReminderOut]{}, err
	}
	ctx := r.Context()
	rows, err := s.db.Reminders(ctx, viewer(r).ProfileID, s.now())
	if err != nil {
		return Page[LiveReminderOut]{}, err
	}
	out := Page[LiveReminderOut]{Items: []LiveReminderOut{}}
	for _, rem := range rows {
		public, err := s.db.ChannelPublicID(ctx, rem.ChannelID)
		if err != nil {
			continue
		}
		ch, err := s.db.LiveChannelByID(ctx, viewer(r), public)
		if err != nil || !mayWatch(lv, ch) {
			continue // a channel the profile may no longer watch: its reminders are absent
		}
		out.Items = append(out.Items, LiveReminderOut{Programme: programmeRef(public, rem.StartsAt), Channel: ch.Name, Title: rem.Title,
			StartsAt: rem.StartsAt.UTC(), EndsAt: rem.EndsAt.UTC()})
	}
	return out, nil
}

func (s *Server) setReminder(r *http.Request, _ None) (None, error) {
	lv, err := s.liveViewer(r)
	if err != nil {
		return None{}, err
	}
	ctx := r.Context()
	ch, p, err := s.programme(ctx, viewer(r), lv, r.PathValue("id"))
	if err != nil {
		return None{}, err
	}
	if !p.EndsAt.After(s.now()) {
		return None{}, ErrConflict("programme_over", "This program has ended.")
	}
	title := p.Title
	if ch.Kind == db.ChannelFromLibrary {
		// A Library Channel's programme is named from the title as it is now, and only when the
		// profile may see it; otherwise the reminder says only that something is on.
		seen, err := s.db.VisibleItems(ctx, viewer(r), []int64{p.ItemID})
		if err != nil {
			return None{}, err
		}
		if seen[p.ItemID] != p.ProgrammeKey {
			return None{}, ErrNotFound()
		}
	} else if !rated(lv, p.RatingAge) {
		return None{}, ErrNotFound()
	}
	return None{}, s.db.SetReminder(ctx, viewer(r).ProfileID, db.Reminder{ChannelID: ch.ID, ProgrammeKey: p.ProgrammeKey, Title: title,
		StartsAt: p.StartsAt, EndsAt: p.EndsAt}, s.now())
}

func (s *Server) deleteReminder(r *http.Request, _ None) (None, error) {
	lv, err := s.liveViewer(r)
	if err != nil {
		return None{}, err
	}
	ch, p, err := s.programme(r.Context(), viewer(r), lv, r.PathValue("id"))
	if err != nil {
		return None{}, nil // a programme gone from the guide or never seen: nothing to remove
	}
	return None{}, s.db.DeleteReminder(r.Context(), viewer(r).ProfileID, ch.ID, p.ProgrammeKey)
}
