package api

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

// A search has a Live TV group, last, for a profile allowed Live TV: channels whose names hold
// what was typed, best match first, counted, a page at a time on its own, and only those the
// profile may see. A profile not allowed Live TV has no such group and cannot ask for one.
func TestSearchFindsChannelsForAProfileAllowedLiveTV(t *testing.T) {
	s, owner, members := seededWith(t, 600, 4)
	everything, other := members[0], members[1]
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Cable"), Config: &live.LiveSourceConfig{PlaylistURL: playlistOf(t,
		"1|News|World News Tonight", "2|News|News", "3|News|Newsround", "4|Films|Film Four", "5|News|Évening News")}})
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Mine"), Audience: new("owner"),
		Config: &live.LiveSourceConfig{PlaylistURL: playlistOf(t, "9|News|Owner News")}})

	s.db.StartSearch(filepath.Join(t.TempDir(), "search.db"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := s.db.SearchCaughtUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	channelGroup := func(token, query string) *SearchGroup {
		t.Helper()
		res := get[SearchResults](t, s, token, "/v1/search?q="+query)
		for n, g := range res.Groups {
			if g.Kind == "channel" {
				if n != len(res.Groups)-1 {
					t.Fatalf("the channel group is not last: %+v", res.Groups)
				}
				return &g
			}
		}
		return nil
	}
	g := channelGroup(everything, "news")
	if g == nil || g.Total == nil || *g.Total != 4 || !slices.Equal(channelNames(g.Channels), []string{"News", "Newsround", "World News Tonight", "Évening News"}) {
		t.Fatalf("searching news: %+v", g)
	}
	if g := channelGroup(everything, "evening"); g == nil || !slices.Equal(channelNames(g.Channels), []string{"Évening News"}) {
		t.Fatalf("an accent is folded away: %+v", g)
	}
	if g := channelGroup(owner, "owner"); g == nil || !slices.Equal(channelNames(g.Channels), []string{"Owner News"}) {
		t.Fatalf("the owner's search: %+v", g)
	}
	if g := channelGroup(everything, "owner"); g == nil || len(g.Channels) != 0 || *g.Total != 0 {
		t.Fatalf("a member finds the owner's own channel: %+v", g)
	}
	// One kind, a page at a time, and by name.
	one := get[SearchResults](t, s, everything, "/v1/search?q=news&kind=channel&limit=2&sort=title")
	if len(one.Groups) != 1 || !slices.Equal(channelNames(one.Groups[0].Channels), []string{"Évening News", "News"}) || one.Groups[0].NextCursor == "" {
		t.Fatalf("the channel group by name: %+v", one.Groups)
	}
	rest := get[SearchResults](t, s, everything, "/v1/search?q=news&kind=channel&limit=2&sort=title&cursor="+one.Groups[0].NextCursor)
	if !slices.Equal(channelNames(rest.Groups[0].Channels), []string{"Newsround", "World News Tonight"}) || rest.Groups[0].Total != nil {
		t.Fatalf("its second page: %+v", rest.Groups)
	}

	// Live TV turned off for a profile: no group, and kind=channel is an unknown kind.
	conn, err := sql.Open("sqlite", filepath.Join(s.dataDir, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`UPDATE profiles SET allow_live_tv = 0 WHERE id = ?`, viewerOfToken(t, s, other).ProfileID); err != nil {
		t.Fatal(err)
	}
	if g := channelGroup(other, "news"); g != nil {
		t.Fatalf("a profile not allowed Live TV has a channel group: %+v", g)
	}
	if a, b := status(t, s, "GET", "/v1/search?q=news&kind=channel", other, nil), status(t, s, "GET", "/v1/search?q=news&kind=nothing", other, nil); a != b || a < 400 {
		t.Fatalf("kind=channel for a profile not allowed Live TV answered %d, an unknown kind %d", a, b)
	}
}
