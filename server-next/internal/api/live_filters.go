package api

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// LiveChannelPage is the answer of GET /v1/live/channels: a page of channels and, on the first
// page, the sources the profile can filter the list and the guide by (source=<id>), each with
// its groups (group=<name>), drawn only from channels it may see.
type LiveChannelPage struct {
	Items      []LiveChannel `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
	// Sources is present on the first page only: the sources by name, then Library Channels.
	Sources []LiveChannelSource `json:"sources,omitempty"`
}

// LiveChannelSource is one source of the filter menu. Counts are of the channels the list
// would show for it: the profile's hidden ones only with hidden=true.
type LiveChannelSource struct {
	// ID is what source= takes; "library" is the Library Channels.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind is live (a source's channels) or library (the Library Channels).
	Kind      string `json:"kind"`
	Channels  int    `json:"channels"`
	Favorites int    `json:"favorites"`
	// Groups are this source's groups by name, each with how many channels it holds. A name
	// can repeat across sources.
	Groups []LiveChannelGroup `json:"groups"`
	// GuideThrough is how far the guide reaches for these channels, when it is known: the end
	// of the source's last listing, or for Library Channels the earliest end of a schedule.
	GuideThrough *time.Time `json:"guideThrough,omitempty"`
	// GuideFrom is the earliest programme currently held for this source.
	GuideFrom *time.Time `json:"guideFrom,omitempty"`
	// OutOfDate says the source's last refresh, or that of the source its listings come from,
	// failed: the guide shown is older.
	OutOfDate bool `json:"outOfDate,omitempty"`
}

// LiveChannelGroup is one group of a source, and how many channels it holds.
type LiveChannelGroup struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// libraryChannelsSource is the source=<id> of the Library Channels.
const libraryChannelsSource = "library"

// aroundWindow is how many channels the mini guide shows on each side of one channel, and
// aroundRead how many are read on each side to find them, in one statement: a Library Channel
// the profile may not see is passed over within it.
const aroundWindow, aroundRead = 5, 20

// liveChannelQuery reads the filters the channel list and the guide share: sort=name,
// favorites, hidden, source and group. none says the source asked for is not one the
// profile may see, which answers an empty list, as a source that does not exist does.
func (s *Server) liveChannelQuery(r *http.Request, lv db.LiveViewer) (q db.LiveChannelQuery, none bool, err error) {
	query := r.URL.Query()
	q = db.LiveChannelQuery{ByName: query.Get("sort") == "name", Favorites: query.Get("favorites") == "true",
		WithHidden: query.Get("hidden") == "true", Group: query.Get("group"), Owner: lv.Owner}
	switch source := query.Get("source"); source {
	case "":
	case libraryChannelsSource:
		q.LibraryOnly = true
	default:
		id, ok, err := s.db.LiveSourceFor(r.Context(), source, lv.Owner)
		if err != nil || !ok {
			return q, true, err
		}
		q.SourceID = id
	}
	return q, false, nil
}

// channelsAround answers the channels either side of one channel the profile may see, in the
// query's order: up to aroundWindow before it, the channel, and up to aroundWindow after it.
// A channel the profile may not see, or that does not exist, is not found.
func (s *Server) channelsAround(ctx context.Context, v db.Viewer, lv db.LiveViewer, q db.LiveChannelQuery, id string) ([]db.LiveChannel, error) {
	at, err := s.db.LiveChannelByID(ctx, v, id)
	if err != nil || !mayWatch(lv, at) {
		return nil, ErrNotFound()
	}
	before, after, err := s.db.LiveChannelsAround(ctx, v, q, at, aroundRead)
	if err != nil {
		return nil, err
	}
	var out []db.LiveChannel
	for _, c := range before {
		if len(out) < aroundWindow && mayWatch(lv, c) {
			out = append(out, c)
		}
	}
	slices.Reverse(out)
	shown := 0
	for _, c := range after {
		if c.ID == at.ID {
			continue
		}
		if shown < aroundWindow && mayWatch(lv, c) {
			out = append(out, c)
			shown++
		}
	}
	// The channel itself, between the two, even when the list's filters would leave it out.
	place := len(out) - shown
	return slices.Insert(out, place, at), nil
}

// liveFilterSources makes the filter menu: each source the profile may see that has channels
// in use, with its counts and groups, then the Library Channels when it may see any.
func (s *Server) liveFilterSources(ctx context.Context, v db.Viewer, lv db.LiveViewer, withHidden bool) ([]LiveChannelSource, error) {
	f, err := s.db.ReadLiveFilters(ctx, v, lv.Owner)
	if err != nil {
		return nil, err
	}
	sources, err := s.db.LiveSources(ctx) // a handful of rows
	if err != nil {
		return nil, err
	}
	known := map[int64]db.LiveSource{}
	for _, src := range sources {
		known[src.ID] = src
	}
	failed := func(id int64) bool {
		src, ok := known[id]
		return ok && !src.Paused && src.State != live.StateUpToDate && src.State != live.StateNew
	}
	reach := func(id int64) *time.Time {
		var report live.LiveSourceReport
		if src, ok := known[id]; ok && json.Unmarshal([]byte(src.Report), &report) == nil {
			return report.GuideThrough
		}
		return nil
	}
	type counted struct {
		out    LiveChannelSource
		groups map[string]int
		guides map[int64]bool
	}
	bySource := map[int64]*counted{}
	of := func(id int64) *counted {
		c := bySource[id]
		if c == nil {
			c = &counted{out: LiveChannelSource{ID: known[id].PublicID, Name: known[id].Name, Kind: "live"},
				groups: map[string]int{}, guides: map[int64]bool{}}
			bySource[id] = c
		}
		return c
	}
	for _, g := range f.Counts {
		c := of(g.SourceID)
		c.out.Channels += g.Channels
		if g.Group != "" {
			c.groups[g.Group] += g.Channels
		}
		c.guides[g.MinGuide], c.guides[g.MaxGuide] = true, true
	}
	for _, m := range f.Marks {
		c := of(m.SourceID)
		switch {
		case m.Hidden && !withHidden:
			c.out.Channels--
			if m.Group != "" {
				c.groups[m.Group]--
			}
		case m.Favorite:
			c.out.Favorites++
		}
	}
	finish := func(c *counted) LiveChannelSource {
		c.out.Groups = []LiveChannelGroup{}
		for name, n := range c.groups {
			if n > 0 {
				c.out.Groups = append(c.out.Groups, LiveChannelGroup{Name: name, Count: n})
			}
		}
		slices.SortFunc(c.out.Groups, func(a, b LiveChannelGroup) int { return compareText(a.Name, b.Name) })
		return c.out
	}
	out := []LiveChannelSource{}
	for id, c := range bySource {
		if c.out.Channels <= 0 {
			continue
		}
		for guide := range c.guides {
			if guide == 0 {
				continue
			}
			from, err := s.db.GuideStart(ctx, guide)
			if err != nil {
				return nil, err
			}
			if !from.IsZero() && (c.out.GuideFrom == nil || from.Before(*c.out.GuideFrom)) {
				c.out.GuideFrom = &from
			}
			if through := reach(guide); through != nil && (c.out.GuideThrough == nil || through.After(*c.out.GuideThrough)) {
				c.out.GuideThrough = through
			}
			c.out.OutOfDate = c.out.OutOfDate || failed(guide)
		}
		c.out.OutOfDate = c.out.OutOfDate || failed(id)
		out = append(out, finish(c))
	}
	slices.SortFunc(out, func(a, b LiveChannelSource) int { return cmp.Or(compareText(a.Name, b.Name), cmp.Compare(a.ID, b.ID)) })
	library := &counted{out: LiveChannelSource{ID: libraryChannelsSource, Name: "Library Channels", Kind: "library"}, groups: map[string]int{}}
	for _, e := range f.Library {
		if !librarychannels.Visible(lv, e.Libraries) || e.Hidden && !withHidden {
			continue
		}
		from := e.ScheduledFrom
		if !from.IsZero() && (library.out.GuideFrom == nil || from.Before(*library.out.GuideFrom)) {
			library.out.GuideFrom = &from
		}
		library.out.Channels++
		if e.Favorite {
			library.out.Favorites++
		}
		if e.Group != "" {
			library.groups[e.Group]++
		}
		if !e.ScheduledThrough.IsZero() && (library.out.GuideThrough == nil || e.ScheduledThrough.Before(*library.out.GuideThrough)) {
			through := e.ScheduledThrough.UTC()
			library.out.GuideThrough = &through
		}
	}
	if library.out.Channels > 0 {
		out = append(out, finish(library))
	}
	return out, nil
}

// compareText orders names as a person expects: folded, articles aside, numbers by value.
func compareText(a, b string) int {
	return cmp.Or(cmp.Compare(sorttext.Key(a, "", ""), sorttext.Key(b, "", "")), cmp.Compare(a, b))
}
