package api

import (
	"net/http"
	"time"
)

type homeNowCursor struct {
	At      time.Time `json:"at"`
	Profile int64     `json:"profile"`
	Next    string    `json:"next"`
}

func (s *Server) onNowHomeRow(r *http.Request) (HomeRow, error) {
	row := HomeRow{ID: "on_now", Title: "On now", Shape: "landscape", Items: []Item{}}
	c := homeNowCursor{At: s.now().UTC(), Profile: viewer(r).ProfileID}
	if text := r.URL.Query().Get("cursor"); text != "" {
		if decodeOpaque(text, &c) != nil || c.Next == "" || c.Profile != viewer(r).ProfileID ||
			c.At.After(s.now()) || !s.now().Before(c.At.Add(time.Minute)) {
			return row, ErrField("cursor", "This On now page has expired. Read the row again.")
		}
	}
	request := r.Clone(r.Context())
	url := *r.URL
	query := url.Query()
	query.Set("cursor", c.Next)
	url.RawQuery = query.Encode()
	request.URL = &url
	page, err := s.liveNowAt(request, c.At)
	if err != nil {
		return row, err
	}
	row.Live = &HomeLive{Revision: c.At.Format(time.RFC3339Nano), ObservedAt: c.At, ExpiresAt: c.At.Add(time.Minute),
		Sort: "favorites", Groups: []LiveChannelGroup{}, Channels: []HomeChannel{}}
	for _, entry := range page.Items {
		channel := homeLiveChannel(entry, c.At)
		row.Live.Channels = append(row.Live.Channels, channel)
		if entry.Now != nil && entry.Now.EndsAt.Before(row.Live.ExpiresAt) {
			row.Live.ExpiresAt = entry.Now.EndsAt
		}
	}
	if page.NextCursor != "" {
		c.Next = page.NextCursor
		row.NextCursor = encodeOpaque(c)
	}
	return row, nil
}

// Match the rebuilt guide's identity bridge: a live source uses server/1; a Library
// Channel is its own source. These are player identities, never viewer-facing names.
func homeLiveChannel(entry LiveOnNow, at time.Time) HomeChannel {
	ch := entry.Channel
	source := HomeLiveSource{ID: "server", Generation: "1", Provenance: "live-source", PublishedAt: at, RefreshState: "ready"}
	if ch.Kind == "library" {
		source.ID, source.Provenance = ch.ID, "library-channel"
	}
	mark := ch.Number
	if mark == "" {
		mark = "TV"
	}
	channel := HomeLiveChannel{ID: ch.ID, SourceID: source.ID, Provenance: source.Provenance, Generation: source.Generation,
		Name: ch.Name, Number: ch.Number, Group: ch.Group, LogoPath: ch.Logo, Favorite: ch.Favorite,
		Mark: HomeLiveMark{Text: mark, Seed: ch.ID}, Programmes: []HomeLiveProgramme{}, TuneAvailable: true, Guide: "none"}
	if entry.Now != nil || entry.Next != nil {
		channel.Guide = "full"
	}
	if p := entry.Now; p != nil {
		channel.Programmes = append(channel.Programmes, HomeLiveProgramme{ID: p.ID, ChannelID: ch.ID, Title: p.Title,
			Start: p.StartsAt, End: p.EndsAt, Lineage: "interval-only", Description: p.Summary, Subtitle: p.EpisodeTitle})
		programme := &channel.Programmes[0]
		if ch.Kind == "library" {
			programme.Lineage = "generated"
		}
		if p.Season != nil || p.Episode != nil {
			programme.Episode = &HomeLiveEpisode{Season: p.Season, Number: p.Episode}
		}
		if p.Rating != "" {
			programme.Rating = &HomeLiveRating{Value: p.Rating}
		}
		if p.Slate {
			channel.TuneAvailable, channel.TuneUnavailableReason = false, "no-schedule"
		}
		if p.NotAvailable {
			channel.TuneAvailable, channel.TuneUnavailableReason = false, "permission-denied"
		}
	}
	return HomeChannel{Source: source, Channel: channel, Actions: HomeLiveActions{Play: channel.TuneAvailable, Favorite: true}}
}

func (s *Server) initialOnNowHomeRow(r *http.Request) (HomeRow, error) {
	allowed, err := s.db.ViewerLive(r.Context(), viewer(r))
	if err != nil {
		return HomeRow{}, err
	}
	if !allowed.LiveTV {
		return HomeRow{}, nil
	}
	request := r.Clone(r.Context())
	url := *r.URL
	url.RawQuery = ""
	request.URL = &url
	return s.onNowHomeRow(request)
}
