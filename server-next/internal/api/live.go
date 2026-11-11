package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
)

// Channel is a channel in the guide: a source's or a Library Channel.
type Channel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Number string `json:"number"`
	// Kind is source or library.
	Kind     string `json:"kind"`
	Group    string `json:"group,omitempty"`
	Favorite bool   `json:"favorite"`
	Hidden   bool   `json:"hidden"`
}

// Programme is one programme of the guide. A Library Channel's programme names its title by
// ItemID. One the profile may not see says only NotAvailable and its times; the slate says Slate.
type Programme struct {
	ID           string    `json:"id"`
	StartsAt     time.Time `json:"startsAt"`
	EndsAt       time.Time `json:"endsAt"`
	Title        string    `json:"title,omitempty"`
	EpisodeTitle string    `json:"episodeTitle,omitempty"`
	Season       *int      `json:"season,omitempty"`
	Episode      *int      `json:"episode,omitempty"`
	Summary      string    `json:"summary,omitempty"`
	Rating       string    `json:"rating,omitempty"`
	ItemID       string    `json:"itemId,omitempty"`
	Slate        bool      `json:"slate,omitempty"`
	NotAvailable bool      `json:"notAvailable,omitempty"`
}

// GuideRow is one channel with its programmes over the stretch asked for.
type GuideRow struct {
	Channel    Channel     `json:"channel"`
	Programmes []Programme `json:"programmes"`
}

// Guide is a page of channels with their programmes.
type Guide struct {
	Items      []GuideRow `json:"items"`
	NextCursor string     `json:"nextCursor,omitempty"`
	// OutOfDate says the last refresh of a source failed and the guide shown is older.
	OutOfDate bool `json:"outOfDate"`
}

// OnNow is one channel with what is on and what is next.
type OnNow struct {
	Channel Channel    `json:"channel"`
	Now     *Programme `json:"now,omitempty"`
	Next    *Programme `json:"next,omitempty"`
}

// ChannelMarks is a profile's own favourite and hidden marks on a channel.
type ChannelMarks struct {
	Favorite bool `json:"favorite"`
	Hidden   bool `json:"hidden"`
}

func (s *Server) liveRoutes() {
	Handle(s, "GET /v1/live/channels", WithProfile, s.liveChannels)
	Handle(s, "GET /v1/live/guide", WithProfile, s.liveGuide)
	Handle(s, "GET /v1/live/now", WithProfile, s.liveNow)
	Handle(s, "PUT /v1/live/channels/{id}/state", WithProfile, s.setChannelMarks)
}

type liveCursor struct {
	Phase  int    `json:"p,omitempty"`
	Number int    `json:"n,omitempty"`
	Name   string `json:"s,omitempty"`
	ID     int64  `json:"i"`
}

// liveViewer reads what the viewer may see, and refuses a profile not allowed Live TV: to it
// Live TV is absent, as if there were none.
func (s *Server) liveViewer(r *http.Request) (db.LiveViewer, error) {
	v, err := s.db.ViewerLive(r.Context(), viewer(r))
	if err != nil {
		return v, err
	}
	if !v.LiveTV {
		return v, ErrNotFound()
	}
	return v, nil
}

// mayWatch says whether the viewer may see a channel. A Library Channel follows its
// libraries, through the one function that decides it; a source's channel follows its
// source's audience. Anything else is shown to nobody.
func mayWatch(v db.LiveViewer, c db.LiveChannel) bool {
	switch c.Kind {
	case db.ChannelFromLibrary:
		return librarychannels.Visible(v, c.Libraries)
	case db.ChannelFromSource:
		return v.LiveTV && (c.Audience == "everyone" || c.Audience == "owner" && v.Owner)
	}
	return false
}

// rated says whether a guide's programme is within the profile's rating limit. A programme
// the guide gives no rating is allowed: most guides send none.
func rated(v db.LiveViewer, age *int) bool {
	return v.MaxAge == nil || age == nil || *age <= *v.MaxAge
}

func channelOut(c db.LiveChannel) Channel {
	out := Channel{ID: c.PublicID, Name: c.Name, Kind: "library", Group: c.Group, Favorite: c.Favorite, Hidden: c.Hidden,
		Number: strconv.Itoa(c.Number)}
	if c.Kind == db.ChannelFromSource {
		out.Kind = "source"
		if minor := c.Number % 1000; minor != 0 {
			out.Number = strconv.Itoa(c.Number/1000) + "." + strconv.Itoa(minor)
		} else {
			out.Number = strconv.Itoa(c.Number / 1000)
		}
	}
	return out
}

func pageLimit(r *http.Request, fallback, most int) (int, error) {
	limit, err := limitParam(r.URL.Query())
	if err != nil {
		return 0, err
	}
	if limit == 0 {
		return fallback, nil
	}
	return min(limit, most), nil
}

// visibleChannels reads channels in order until limit the viewer may see are found. It
// answers them and the cursor after the last row read, or "" at the end.
func (s *Server) visibleChannels(ctx context.Context, v db.Viewer, lv db.LiveViewer, q db.LiveChannelQuery, c liveCursor, limit int) (
	[]db.LiveChannel, *liveCursor, error) {
	var out []db.LiveChannel
	q.AfterNumber, q.AfterName, q.AfterID, q.Limit = c.Number, c.Name, c.ID, 100
	for {
		rows, err := s.db.LiveChannels(ctx, v, q)
		if err != nil {
			return nil, nil, err
		}
		for _, row := range rows {
			q.AfterNumber, q.AfterName, q.AfterID = row.Number, row.SortName, row.ID
			if mayWatch(lv, row) {
				out = append(out, row)
				if len(out) == limit {
					return out, &liveCursor{Phase: c.Phase, Number: row.Number, Name: row.SortName, ID: row.ID}, nil
				}
			}
		}
		if len(rows) < q.Limit {
			return out, nil, nil
		}
	}
}

func readLiveCursor(r *http.Request) (liveCursor, error) {
	var c liveCursor
	if text := r.URL.Query().Get("cursor"); text != "" {
		if err := decodeOpaque(text, &c); err != nil {
			return c, ErrField("cursor", "This cursor is not one this list gave.")
		}
	}
	return c, nil
}

func (s *Server) liveChannels(r *http.Request, _ None) (Page[Channel], error) {
	lv, err := s.liveViewer(r)
	if err != nil {
		return Page[Channel]{}, err
	}
	query := r.URL.Query()
	q := db.LiveChannelQuery{ByName: query.Get("sort") == "name", Favorites: query.Get("favorites") == "true",
		WithHidden: query.Get("hidden") == "true"}
	c, err := readLiveCursor(r)
	if err != nil {
		return Page[Channel]{}, err
	}
	limit, err := pageLimit(r, 100, 200)
	if err != nil {
		return Page[Channel]{}, err
	}
	rows, next, err := s.visibleChannels(r.Context(), viewer(r), lv, q, c, limit)
	if err != nil {
		return Page[Channel]{}, err
	}
	out := Page[Channel]{Items: []Channel{}}
	for _, row := range rows {
		out.Items = append(out.Items, channelOut(row))
	}
	if next != nil {
		out.NextCursor = encodeOpaque(next)
	}
	return out, nil
}

func timeParam(r *http.Request, name string, fallback time.Time) (time.Time, error) {
	text := r.URL.Query().Get(name)
	if text == "" {
		return fallback, nil
	}
	t, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return t, ErrField(name, "Use a time such as 2026-11-10T20:00:00Z.")
	}
	return t, nil
}

// programmes reads each channel's programmes over a stretch and hides what the viewer may
// not see: a title above its rating limit, carrying a Tag blocked for it, or gone, shows
// only that it is not available, never its name.
func (s *Server) programmes(ctx context.Context, v db.Viewer, lv db.LiveViewer, channels []db.LiveChannel, from, to time.Time, most int) ([][]Programme, error) {
	all := make([][]db.GuideProgramme, len(channels))
	var ids []int64
	for n, c := range channels {
		rows, err := s.db.GuideProgrammes(ctx, c.GuideSource, c.GuideKey, from, to, most)
		if err != nil {
			return nil, err
		}
		all[n] = rows
		for _, p := range rows {
			if p.ItemID != 0 {
				ids = append(ids, p.ItemID)
			}
		}
	}
	seen, err := s.db.VisibleItems(ctx, v, ids)
	if err != nil {
		return nil, err
	}
	// A Library Channel's programme is shown from the title as it is now, and only when the row
	// is still the title the schedule named: a row id can be used again after a delete.
	var shown []int64
	for id := range seen {
		shown = append(shown, id)
	}
	current, err := s.db.ProgrammeTitles(ctx, shown)
	if err != nil {
		return nil, err
	}
	out := make([][]Programme, len(channels))
	for n, rows := range all {
		out[n] = []Programme{}
		for _, p := range rows {
			item := Programme{ID: encodeOpaque(map[string]any{"c": channels[n].PublicID, "s": p.StartsAt.UnixMilli()}),
				StartsAt: p.StartsAt.UTC(), EndsAt: p.EndsAt.UTC()}
			switch {
			case channels[n].Kind == db.ChannelFromLibrary && p.ItemID == 0:
				item.Slate = true
			case channels[n].Kind == db.ChannelFromLibrary && seen[p.ItemID] != p.ProgrammeKey, channels[n].Kind == db.ChannelFromSource && !rated(lv, p.RatingAge):
				item.NotAvailable = true
			case channels[n].Kind == db.ChannelFromLibrary:
				t := current[p.ItemID]
				item.Title, item.EpisodeTitle, item.Season, item.Episode = t.Title, t.EpisodeTitle, t.Season, t.Episode
				item.Summary, item.Rating, item.ItemID = t.Summary, t.ContentRating, t.PublicID
			default:
				item.Title, item.EpisodeTitle, item.Season, item.Episode = p.Title, p.EpisodeTitle, p.Season, p.Episode
				item.Summary, item.Rating = p.Summary, p.Rating
				if channels[n].Kind == db.ChannelFromLibrary {
					item.ItemID = p.ProgrammeKey
				}
			}
			out[n] = append(out[n], item)
		}
	}
	return out, nil
}

func (s *Server) liveGuide(r *http.Request, _ None) (Guide, error) {
	lv, err := s.liveViewer(r)
	if err != nil {
		return Guide{}, err
	}
	now := s.now()
	from, err := timeParam(r, "from", now.Truncate(30*time.Minute))
	if err != nil {
		return Guide{}, err
	}
	to, err := timeParam(r, "to", from.Add(3*time.Hour))
	if err != nil {
		return Guide{}, err
	}
	if !to.After(from) || to.Sub(from) > 24*time.Hour {
		return Guide{}, ErrField("to", "Ask for up to a day of the guide at a time.")
	}
	query := r.URL.Query()
	q := db.LiveChannelQuery{ByName: query.Get("sort") == "name", Favorites: query.Get("favorites") == "true",
		WithHidden: query.Get("hidden") == "true"}
	c, err := readLiveCursor(r)
	if err != nil {
		return Guide{}, err
	}
	limit, err := pageLimit(r, 20, 50)
	if err != nil {
		return Guide{}, err
	}
	channels, next, err := s.visibleChannels(r.Context(), viewer(r), lv, q, c, limit)
	if err != nil {
		return Guide{}, err
	}
	progs, err := s.programmes(r.Context(), viewer(r), lv, channels, from, to, 200)
	if err != nil {
		return Guide{}, err
	}
	out := Guide{Items: []GuideRow{}}
	for n, ch := range channels {
		out.Items = append(out.Items, GuideRow{Channel: channelOut(ch), Programmes: progs[n]})
	}
	if next != nil {
		out.NextCursor = encodeOpaque(next)
	}
	return out, nil
}

// liveNow lists the profile's favourite channels first, then the rest by number, each with
// what is on and what is next.
func (s *Server) liveNow(r *http.Request, _ None) (Page[OnNow], error) {
	lv, err := s.liveViewer(r)
	if err != nil {
		return Page[OnNow]{}, err
	}
	c, err := readLiveCursor(r)
	if err != nil {
		return Page[OnNow]{}, err
	}
	limit, err := pageLimit(r, 20, 50)
	if err != nil {
		return Page[OnNow]{}, err
	}
	ctx := r.Context()
	var channels []db.LiveChannel
	var next *liveCursor
	for c.Phase < 2 && len(channels) < limit {
		q := db.LiveChannelQuery{Favorites: c.Phase == 0, NotFavorites: c.Phase == 1}
		var rows []db.LiveChannel
		if rows, next, err = s.visibleChannels(ctx, viewer(r), lv, q, c, limit-len(channels)); err != nil {
			return Page[OnNow]{}, err
		}
		channels = append(channels, rows...)
		if next == nil {
			c = liveCursor{Phase: c.Phase + 1}
			if c.Phase < 2 {
				next = &c
			}
		}
	}
	now := s.now()
	progs, err := s.programmes(ctx, viewer(r), lv, channels, now, now.Add(24*time.Hour), 2)
	if err != nil {
		return Page[OnNow]{}, err
	}
	out := Page[OnNow]{Items: []OnNow{}}
	for n, ch := range channels {
		item := OnNow{Channel: channelOut(ch)}
		for k := range progs[n] {
			p := &progs[n][k]
			if !p.StartsAt.After(now) && item.Now == nil {
				item.Now = p
			} else if item.Next == nil {
				item.Next = p
			}
		}
		out.Items = append(out.Items, item)
	}
	if next != nil && next.Phase < 2 {
		out.NextCursor = encodeOpaque(next)
	}
	return out, nil
}

func (s *Server) setChannelMarks(r *http.Request, req ChannelMarks) (ChannelMarks, error) {
	lv, err := s.liveViewer(r)
	if err != nil {
		return ChannelMarks{}, err
	}
	ch, err := s.db.LiveChannelByID(r.Context(), viewer(r), r.PathValue("id"))
	if errors.Is(err, db.ErrNotFound) || err == nil && !mayWatch(lv, ch) {
		return ChannelMarks{}, ErrNotFound()
	}
	if err != nil {
		return ChannelMarks{}, err
	}
	return req, s.db.SetChannelMarks(r.Context(), viewer(r), ch.ID, req.Favorite, req.Hidden, s.now())
}

// tuneChannel finds the title a viewer joins on a Library Channel now, and where in it.
func (s *Server) tuneChannel(ctx context.Context, v db.Viewer, channelID string) (librarychannels.Tuned, error) {
	lv, err := s.db.ViewerLive(ctx, v)
	if err != nil {
		return librarychannels.Tuned{}, err
	}
	if !lv.LiveTV {
		return librarychannels.Tuned{}, ErrNotFound()
	}
	if ch, err := s.db.LiveChannelByID(ctx, v, channelID); err == nil && ch.Kind == db.ChannelFromSource {
		if !mayWatch(lv, ch) {
			return librarychannels.Tuned{}, ErrNotFound()
		}
		return librarychannels.Tuned{}, notYet("live_watching_unavailable", "This server can't play a tuner's or playlist's channels yet.")
	}
	tuned, err := s.channels.Tune(ctx, v, channelID)
	if slate, ok := librarychannels.IsSlate(err); ok {
		e := ErrConflict("channel_slate", "Nothing is playing on this channel right now.")
		if !slate.Next.IsZero() {
			e.Retry, e.RetryAfterMs = true, max(slate.Next.Sub(s.now()).Milliseconds(), 1000)
		}
		return tuned, e
	}
	if errors.Is(err, db.ErrNotFound) {
		return tuned, ErrNotFound()
	}
	return tuned, err
}
