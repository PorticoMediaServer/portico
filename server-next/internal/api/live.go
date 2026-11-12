package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

// LiveChannel is a channel in the guide: a source's or a Library Channel.
type LiveChannel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Number string `json:"number"`
	// Kind is source or library.
	Kind  string `json:"kind"`
	Group string `json:"group,omitempty"`
	// Logo is a Library Channel's uploaded logo, or absent for the monogram.
	Logo     string `json:"logo,omitempty"`
	Favorite bool   `json:"favorite"`
	Hidden   bool   `json:"hidden"`
}

// LiveProgramme is one programme of the guide. A Library Channel's programme names its title by
// ItemID. One the profile may not see says only NotAvailable and its times; the slate says Slate.
type LiveProgramme struct {
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

// LiveGuideRow is one channel with its programmes over the stretch asked for.
type LiveGuideRow struct {
	Channel    LiveChannel     `json:"channel"`
	Programmes []LiveProgramme `json:"programmes"`
}

// LiveGuide is a page of channels with their programmes.
type LiveGuide struct {
	Items      []LiveGuideRow `json:"items"`
	NextCursor string         `json:"nextCursor,omitempty"`
	// OutOfDate says the last refresh of a source failed and the guide shown is older.
	OutOfDate bool `json:"outOfDate"`
}

// LiveOnNow is one channel with what is on and what is next.
type LiveOnNow struct {
	Channel LiveChannel    `json:"channel"`
	Now     *LiveProgramme `json:"now,omitempty"`
	Next    *LiveProgramme `json:"next,omitempty"`
}

// LiveChannelMarks is a profile's own favourite and hidden marks on a channel.
type LiveChannelMarks struct {
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

func channelOut(c db.LiveChannel) LiveChannel {
	out := LiveChannel{ID: c.PublicID, Name: c.Name, Kind: "library", Group: c.Group, Favorite: c.Favorite, Hidden: c.Hidden,
		Number: strconv.Itoa(c.Number)}
	if c.LogoHash != "" {
		out.Logo = "/v1/images/" + c.LogoHash
	}
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
	q.AfterNumber, q.AfterName, q.AfterID, q.Limit, q.Owner = c.Number, c.Name, c.ID, 100, lv.Owner
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

// liveChannels lists the channels the profile may watch, filtered by source, group and
// favourites as the guide is (see liveChannelQuery). around=<channel id> answers instead the
// channels either side of that channel, for the player's mini guide (channelsAround). The
// first page also carries the sources and groups to filter by.
func (s *Server) liveChannels(r *http.Request, _ None) (LiveChannelPage, error) {
	lv, err := s.liveViewer(r)
	if err != nil {
		return LiveChannelPage{}, err
	}
	q, none, err := s.liveChannelQuery(r, lv)
	if err != nil {
		return LiveChannelPage{}, err
	}
	out := LiveChannelPage{Items: []LiveChannel{}}
	if around := r.URL.Query().Get("around"); around != "" {
		rows, err := s.channelsAround(r.Context(), viewer(r), lv, q, around)
		if err != nil {
			return LiveChannelPage{}, err
		}
		for _, row := range rows {
			out.Items = append(out.Items, channelOut(row))
		}
		return out, nil
	}
	c, err := readLiveCursor(r)
	if err != nil {
		return LiveChannelPage{}, err
	}
	limit, err := pageLimit(r, 100, 200)
	if err != nil {
		return LiveChannelPage{}, err
	}
	if r.URL.Query().Get("cursor") == "" {
		if out.Sources, out.Groups, err = s.liveFilterChoices(r.Context(), lv); err != nil {
			return LiveChannelPage{}, err
		}
	}
	if none {
		return out, nil
	}
	rows, next, err := s.visibleChannels(r.Context(), viewer(r), lv, q, c, limit)
	if err != nil {
		return LiveChannelPage{}, err
	}
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
func (s *Server) programmes(ctx context.Context, v db.Viewer, lv db.LiveViewer, channels []db.LiveChannel, from, to time.Time, most int) ([][]LiveProgramme, error) {
	all := make([][]db.GuideProgramme, len(channels))
	var ids []int64
	for n, c := range channels {
		if c.Kind == db.ChannelFromSource && (c.GuideSource == 0 || c.GuideKey == "") {
			continue // no listings: source 0 is the Library Channels' and is never read for a source's channel
		}
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
	out := make([][]LiveProgramme, len(channels))
	for n, rows := range all {
		out[n] = []LiveProgramme{}
		for _, p := range rows {
			item := LiveProgramme{ID: encodeOpaque(map[string]any{"c": channels[n].PublicID, "s": p.StartsAt.UnixMilli()}),
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

func (s *Server) liveGuide(r *http.Request, _ None) (LiveGuide, error) {
	lv, err := s.liveViewer(r)
	if err != nil {
		return LiveGuide{}, err
	}
	now := s.now()
	from, err := timeParam(r, "from", now.Truncate(30*time.Minute))
	if err != nil {
		return LiveGuide{}, err
	}
	to, err := timeParam(r, "to", from.Add(3*time.Hour))
	if err != nil {
		return LiveGuide{}, err
	}
	if !to.After(from) || to.Sub(from) > 24*time.Hour {
		return LiveGuide{}, ErrField("to", "Ask for up to a day of the guide at a time.")
	}
	q, none, err := s.liveChannelQuery(r, lv)
	if err != nil {
		return LiveGuide{}, err
	}
	c, err := readLiveCursor(r)
	if err != nil {
		return LiveGuide{}, err
	}
	limit, err := pageLimit(r, 20, 50)
	if err != nil {
		return LiveGuide{}, err
	}
	var channels []db.LiveChannel
	var next *liveCursor
	switch around := r.URL.Query().Get("around"); {
	case around != "":
		// The mini guide's channels with what is on them.
		if channels, err = s.channelsAround(r.Context(), viewer(r), lv, q, around); err != nil {
			return LiveGuide{}, err
		}
	case !none:
		if channels, next, err = s.visibleChannels(r.Context(), viewer(r), lv, q, c, limit); err != nil {
			return LiveGuide{}, err
		}
	}
	progs, err := s.programmes(r.Context(), viewer(r), lv, channels, from, to, 200)
	if err != nil {
		return LiveGuide{}, err
	}
	out := LiveGuide{Items: []LiveGuideRow{}}
	for n, ch := range channels {
		out.Items = append(out.Items, LiveGuideRow{Channel: channelOut(ch), Programmes: progs[n]})
	}
	if out.OutOfDate, err = s.guideOutOfDate(r.Context(), channels); err != nil {
		return LiveGuide{}, err
	}
	if next != nil {
		out.NextCursor = encodeOpaque(next)
	}
	return out, nil
}

// liveNow lists the profile's favourite channels first, then the rest by number, each with
// what is on and what is next.
func (s *Server) liveNow(r *http.Request, _ None) (Page[LiveOnNow], error) {
	lv, err := s.liveViewer(r)
	if err != nil {
		return Page[LiveOnNow]{}, err
	}
	c, err := readLiveCursor(r)
	if err != nil {
		return Page[LiveOnNow]{}, err
	}
	limit, err := pageLimit(r, 20, 50)
	if err != nil {
		return Page[LiveOnNow]{}, err
	}
	ctx := r.Context()
	var channels []db.LiveChannel
	var next *liveCursor
	for c.Phase < 2 && len(channels) < limit {
		q := db.LiveChannelQuery{Favorites: c.Phase == 0, NotFavorites: c.Phase == 1}
		var rows []db.LiveChannel
		if rows, next, err = s.visibleChannels(ctx, viewer(r), lv, q, c, limit-len(channels)); err != nil {
			return Page[LiveOnNow]{}, err
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
		return Page[LiveOnNow]{}, err
	}
	out := Page[LiveOnNow]{Items: []LiveOnNow{}}
	for n, ch := range channels {
		item := LiveOnNow{Channel: channelOut(ch)}
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

func (s *Server) setChannelMarks(r *http.Request, req LiveChannelMarks) (LiveChannelMarks, error) {
	lv, err := s.liveViewer(r)
	if err != nil {
		return LiveChannelMarks{}, err
	}
	ch, err := s.db.LiveChannelByID(r.Context(), viewer(r), r.PathValue("id"))
	if errors.Is(err, db.ErrNotFound) || err == nil && !mayWatch(lv, ch) {
		return LiveChannelMarks{}, ErrNotFound()
	}
	if err != nil {
		return LiveChannelMarks{}, err
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

// guideOutOfDate says whether the last refresh of a source whose channels are on this page,
// or of the source their listings come from, failed: the guide shown is then older.
func (s *Server) guideOutOfDate(ctx context.Context, channels []db.LiveChannel) (bool, error) {
	sources, err := s.db.LiveSources(ctx) // a handful of rows
	if err != nil {
		return false, err
	}
	failed := map[int64]bool{}
	for _, src := range sources {
		failed[src.ID] = !src.Paused && src.State != live.StateUpToDate && src.State != live.StateNew
	}
	for _, c := range channels {
		if c.Kind == db.ChannelFromSource && (failed[c.SourceID] || failed[c.GuideSource]) {
			return true, nil
		}
	}
	return false, nil
}
