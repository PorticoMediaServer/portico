package api

import (
	"cmp"
	"errors"
	"net/http"
	"slices"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// nowWatchingLive is Now playing's cards for the live channels being watched: a stream like
// any title (area 11 rule 22; area 12 rule 6), newest start first. The channel is named only
// where the asking owner or administrator may watch it.
func (s *Server) nowWatchingLive(r *http.Request) ([]NowPlaying, error) {
	viewings := s.viewings.All()
	if len(viewings) == 0 {
		return nil, nil
	}
	slices.SortFunc(viewings, func(a, b live.Viewing) int { return cmp.Compare(b.StartedAt.UnixNano(), a.StartedAt.UnixNano()) })
	profiles := make([]int64, len(viewings))
	for n, v := range viewings {
		profiles[n] = v.ProfileID
	}
	people, err := s.db.ViewingNames(r.Context(), profiles)
	if err != nil {
		return nil, err
	}
	lv, err := s.liveViewer(r)
	mayKnow := err == nil
	out := make([]NowPlaying, 0, len(viewings))
	for _, v := range viewings {
		person := people[v.ProfileID]
		state := playback.StatePlaying
		if v.Paused {
			state = playback.StatePaused
		}
		item := NowPlaying{ID: v.ID, Kind: "live", State: state, Username: person.Username, Profile: person.Profile, DeviceName: v.Name,
			Away: v.Away, Route: playback.RouteOriginal, StartedAt: v.StartedAt.UTC()}
		if v.Play != nil {
			item.Route = v.Play.Route
		}
		if mayKnow {
			ch, err := s.db.LiveChannelByID(r.Context(), viewer(r), v.ChannelID)
			if err != nil && !errors.Is(err, db.ErrNotFound) {
				return nil, err
			}
			if err == nil && mayWatch(lv, ch) {
				item.Title = ch.Name
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// accountLiveStreams are the account's live channels being watched, as the account's other
// devices see them: the channel named only where the asking profile may watch it.
func (s *Server) accountLiveStreams(r *http.Request, c playback.Caller) ([]AccountStream, error) {
	var out []AccountStream
	lv, err := s.liveViewer(r)
	mayKnow := err == nil
	for _, v := range s.viewings.All() {
		if v.UserID != c.Viewer.UserID {
			continue
		}
		state := playback.StatePlaying
		if v.Paused {
			state = playback.StatePaused
		}
		stream := AccountStream{ID: v.ID, DeviceName: v.Name, Kind: "live", State: state, StartedAt: v.StartedAt.UTC(),
			Current: c.SignInID != 0 && v.SignInID == c.SignInID}
		if device, err := s.db.SessionDevice(r.Context(), v.SignInID); err == nil {
			stream.DeviceID = device.PublicID
		} else if !errors.Is(err, db.ErrNotFound) {
			return nil, err
		}
		if mayKnow {
			ch, err := s.db.LiveChannelByID(r.Context(), viewer(r), v.ChannelID)
			if err != nil && !errors.Is(err, db.ErrNotFound) {
				return nil, err
			}
			if err == nil && mayWatch(lv, ch) {
				stream.Title = ch.Name
			}
		}
		out = append(out, stream)
	}
	slices.SortFunc(out, func(a, b AccountStream) int { return cmp.Compare(b.StartedAt.UnixNano(), a.StartedAt.UnixNano()) })
	return out, nil
}
