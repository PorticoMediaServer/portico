package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

// playlistOf serves a playlist of the channels given as "number|group|name".
func playlistOf(t *testing.T, channels ...string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "#EXTM3U\n")
		for n, c := range channels {
			f := strings.SplitN(c, "|", 3)
			fmt.Fprintf(w, "#EXTINF:-1 tvg-chno=%q group-title=%q,%s\nhttp://streams.invalid/%d.ts\n", f[0], f[1], f[2], n)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL + "/playlist.m3u"
}

func channelNames(page []LiveChannel) []string {
	var out []string
	for _, c := range page {
		out = append(out, c.Name)
	}
	return out
}

// The channel list and the guide filter by source and by group, and the first page of the
// list says which sources and groups there are. Both come only from channels the profile may
// see: an owner-only source, and a Library Channel of a library the profile may not open, are
// neither offered nor told apart from a source that does not exist.
func TestTheGuideFiltersBySourceAndGroupWithinWhatTheProfileMaySee(t *testing.T) {
	s, owner, members := seededWith(t, 600, 4)
	everything, filmsAndMusic := members[0], members[2]
	fake := fakeProvider(t, s.now())
	cable := addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Cable"),
		Config: &live.LiveSourceConfig{PlaylistURL: fake.URL + "/playlist.m3u", GuideURL: fake.URL + "/guide.xml.gz"}})
	mine := addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Mine"), Audience: new("owner"),
		Config: &live.LiveSourceConfig{PlaylistURL: playlistOf(t, "7|Sports|Owner Sports")}})
	shows := libraryOfKind(t, s, owner, "shows")
	tv := makeChannel(t, s, owner, "TV", librarychannels.LibraryChannelBuilder{LibraryChannelSelection: librarychannels.LibraryChannelSelection{Libraries: []string{shows},
		Kinds: []string{"show"}}})

	page := func(token, query string) LiveChannelPage {
		t.Helper()
		return get[LiveChannelPage](t, s, token, "/v1/live/channels"+query)
	}
	// menu writes the filter menu as one line per source: name, kind, channels, favourites,
	// groups with their counts, whether the guide's reach is known, and out of date.
	menu := func(p LiveChannelPage) []string {
		var out []string
		for _, src := range p.Sources {
			line := fmt.Sprintf("%s %s %d/%d", src.Name, src.Kind, src.Channels, src.Favorites)
			for _, g := range src.Groups {
				line += fmt.Sprintf(" %s:%d", g.Name, g.Count)
			}
			if src.GuideThrough != nil {
				line += " through"
			}
			if src.OutOfDate {
				line += " out-of-date"
			}
			out = append(out, line)
		}
		return out
	}
	want := func(token, query string, lines ...string) {
		t.Helper()
		if got := menu(page(token, query)); !slices.Equal(got, lines) {
			t.Errorf("the menu%s reads %q, want %q", query, got, lines)
		}
	}
	want(everything, "", "Cable live 2/0 Films:1 News:1 through", "Library Channels library 1/0 through")
	want(owner, "", "Cable live 2/0 Films:1 News:1 through", "Mine live 1/0 Sports:1", "Library Channels library 1/0 through")
	want(filmsAndMusic, "", "Cable live 2/0 Films:1 News:1 through")
	// A favourite is counted; a hidden channel leaves the counts unless hidden ones are asked for.
	cableChannels := page(everything, "?source="+cable.ID).Items
	status(t, s, "PUT", "/v1/live/channels/"+cableChannels[0].ID+"/state", everything, LiveChannelMarks{Hidden: true})
	status(t, s, "PUT", "/v1/live/channels/"+cableChannels[1].ID+"/state", everything, LiveChannelMarks{Favorite: true})
	status(t, s, "PUT", "/v1/live/channels/"+tv.ID+"/state", everything, LiveChannelMarks{Favorite: true})
	want(everything, "", "Cable live 1/1 Films:1 through", "Library Channels library 1/1 through")
	want(everything, "?hidden=true", "Cable live 2/1 Films:1 News:1 through", "Library Channels library 1/1 through")
	status(t, s, "PUT", "/v1/live/channels/"+cableChannels[0].ID+"/state", everything, LiveChannelMarks{})

	// A source whose last refresh failed says its guide is out of date.
	var down atomic.Bool
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, "#EXTM3U\n#EXTINF:-1 tvg-chno=\"30\",Weather\nhttp://streams.invalid/w.ts\n")
	}))
	t.Cleanup(flaky.Close)
	weather := addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Weather"), Config: &live.LiveSourceConfig{PlaylistURL: flaky.URL}})
	down.Store(true)
	s.sources.Refresh(context.Background(), weather.ID) // fails: the channels stay, the guide is out of date
	want(owner, "?source="+weather.ID, "Cable live 2/0 Films:1 News:1 through", "Mine live 1/0 Sports:1", "Weather live 1/0 out-of-date",
		"Library Channels library 1/0 through")

	for _, c := range []struct {
		token, query string
		want         []string
	}{
		{everything, "?source=" + cable.ID, []string{"Example News", "Film Four"}},
		{everything, "?group=News", []string{"Example News"}},
		{everything, "?source=" + cable.ID + "&group=Films", []string{"Film Four"}},
		{everything, "?source=library", []string{"TV"}},
		{filmsAndMusic, "?source=library", nil},
		{owner, "?source=" + mine.ID, []string{"Owner Sports"}},
		// Another's owner-only source reads exactly as one that does not exist.
		{everything, "?source=" + mine.ID, nil},
		{everything, "?source=src_nope", nil},
		{everything, "?group=Sports", nil},
	} {
		got := page(c.token, c.query)
		if names := channelNames(got.Items); !slices.Equal(names, c.want) {
			t.Errorf("%s: channels %v, want %v", c.query, names, c.want)
		}
		if c.token == everything && strings.Contains(fmt.Sprint(got), "Mine") || strings.Contains(fmt.Sprint(got), "Sports") && c.token != owner {
			t.Errorf("%s: the answer names the owner's own source: %+v", c.query, got)
		}
	}
	if a, b := page(everything, "?source="+mine.ID), page(everything, "?source=src_nope"); fmt.Sprint(a) != fmt.Sprint(b) {
		t.Errorf("an owner-only source and one that does not exist read differently: %+v and %+v", a, b)
	}
	if got := page(everything, "?source=library&cursor="+page(everything, "?limit=1").NextCursor); got.Sources != nil {
		t.Errorf("a later page repeats the sources: %+v", got)
	}

	stretch := "&from=" + s.now().Add(-time.Hour).UTC().Format(time.RFC3339) + "&to=" + s.now().Add(3*time.Hour).UTC().Format(time.RFC3339)
	guide := get[LiveGuide](t, s, everything, "/v1/live/guide?group=Films"+stretch)
	if len(guide.Items) != 1 || guide.Items[0].Channel.Name != "Film Four" || len(guide.Items[0].Programmes) != 1 {
		t.Fatalf("the guide filtered to Films: %+v", guide.Items)
	}
	if guide := get[LiveGuide](t, s, filmsAndMusic, "/v1/live/guide?source=library"+stretch); len(guide.Items) != 0 {
		t.Fatalf("a profile without the shows library is shown its Library Channel in the guide: %+v", guide.Items)
	}
}

// around=<channel> answers the channels either side of one channel, for the player's mini
// guide: five before and five after where there are that many, in the list's order and
// filters, in the channel list and in the guide. A channel the profile may not see is not
// found, as one that does not exist is not.
func TestAroundAChannelGivesItsNeighbours(t *testing.T) {
	s, owner, members := seededWith(t, 600, 4)
	everything, filmsAndMusic := members[0], members[2]
	var lineup []string
	for n := 1; n <= 20; n++ {
		group := "Odd"
		if n%2 == 0 {
			group = "Even"
		}
		lineup = append(lineup, fmt.Sprintf("%d|%s|Channel %02d", n, group, n))
	}
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Big"), Config: &live.LiveSourceConfig{PlaylistURL: playlistOf(t, lineup...)}})
	byName := map[string]string{}
	for _, c := range get[LiveChannelPage](t, s, everything, "/v1/live/channels").Items {
		byName[c.Name] = c.ID
	}
	numbers := func(page []LiveChannel) []string {
		var out []string
		for _, c := range page {
			out = append(out, c.Number)
		}
		return out
	}
	for _, c := range []struct {
		query string
		want  []string
	}{
		{"?around=" + byName["Channel 10"], []string{"5", "6", "7", "8", "9", "10", "11", "12", "13", "14", "15"}},
		{"?around=" + byName["Channel 02"], []string{"1", "2", "3", "4", "5", "6", "7"}},
		{"?around=" + byName["Channel 19"], []string{"14", "15", "16", "17", "18", "19", "20"}},
		{"?group=Even&around=" + byName["Channel 10"], []string{"2", "4", "6", "8", "10", "12", "14", "16", "18", "20"}},
	} {
		if got := numbers(get[LiveChannelPage](t, s, everything, "/v1/live/channels"+c.query).Items); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.query, got, c.want)
		}
	}
	if got := numbers(rowChannels(get[LiveGuide](t, s, everything, "/v1/live/guide?around="+byName["Channel 10"]).Items)); len(got) != 11 || got[5] != "10" {
		t.Errorf("the guide around channel 10: %v", got)
	}

	shows := libraryOfKind(t, s, owner, "shows")
	tv := makeChannel(t, s, owner, "TV", librarychannels.LibraryChannelBuilder{LibraryChannelSelection: librarychannels.LibraryChannelSelection{Libraries: []string{shows},
		Kinds: []string{"show"}}})
	if code := status(t, s, "GET", "/v1/live/channels?around="+tv.ID, filmsAndMusic, nil); code != 404 {
		t.Errorf("around a channel the profile may not see answered %d", code)
	}
	if code := status(t, s, "GET", "/v1/live/channels?around=chn_nope", filmsAndMusic, nil); code != 404 {
		t.Errorf("around a channel that does not exist answered %d", code)
	}
	if got := get[LiveChannelPage](t, s, filmsAndMusic, "/v1/live/channels?around="+byName["Channel 20"]).Items; slices.ContainsFunc(got,
		func(c LiveChannel) bool { return c.ID == tv.ID }) {
		t.Errorf("the mini guide of a profile without the shows library holds its Library Channel: %v", channelNames(got))
	}
}

func rowChannels(rows []LiveGuideRow) []LiveChannel {
	var out []LiveChannel
	for _, r := range rows {
		out = append(out, r.Channel)
	}
	return out
}
