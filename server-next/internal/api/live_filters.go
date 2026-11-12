package api

import (
	"cmp"
	"context"
	"net/http"
	"slices"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
	"github.com/PorticoMediaServer/portico/server-next/internal/sorttext"
)

// LiveChannelPage is the answer of GET /v1/live/channels: a page of channels and, on the first
// page, the sources and groups the profile can filter the list and the guide by, drawn only
// from channels it may see.
type LiveChannelPage struct {
	Items      []LiveChannel `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
	// Sources are the sources of the channels the profile may see, by name; "library" is the
	// Library Channels. Present on the first page only, as Groups is.
	Sources []LiveSourceChoice `json:"sources,omitempty"`
	Groups  []string           `json:"groups,omitempty"`
}

// LiveSourceChoice is a source to filter the channels and the guide by: source=<id>.
type LiveSourceChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
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

// liveFilterChoices are the sources and groups of the channels the profile may see.
func (s *Server) liveFilterChoices(ctx context.Context, lv db.LiveViewer) ([]LiveSourceChoice, []string, error) {
	f, err := s.db.ReadLiveFilters(ctx, lv.Owner)
	if err != nil {
		return nil, nil, err
	}
	sources := []LiveSourceChoice{}
	for _, src := range f.Sources {
		sources = append(sources, LiveSourceChoice{ID: src.ID, Name: src.Name})
	}
	slices.SortFunc(sources, func(a, b LiveSourceChoice) int { return cmp.Or(compareText(a.Name, b.Name), cmp.Compare(a.ID, b.ID)) })
	groups := append([]string{}, f.Groups...)
	library := false
	for _, c := range f.Library {
		if librarychannels.Visible(lv, c.Libraries) {
			library = true
			if c.Group != "" && !slices.Contains(groups, c.Group) {
				groups = append(groups, c.Group)
			}
		}
	}
	if library {
		sources = append(sources, LiveSourceChoice{ID: libraryChannelsSource, Name: "Library Channels"})
	}
	slices.SortFunc(groups, compareText)
	return sources, groups, nil
}

// compareText orders names as a person expects: folded, articles aside, numbers by value.
func compareText(a, b string) int {
	return cmp.Or(cmp.Compare(sorttext.Key(a, "", ""), sorttext.Key(b, "", "")), cmp.Compare(a, b))
}
