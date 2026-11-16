package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

// Programme-only matches continue past the first channel batch and page normally. Listings
// later than now/next, adult listings and owner-only sources never leak into the count.
func TestSearchFindsOnlyVisibleNowAndNextProgrammes(t *testing.T) {
	s, owner, members := seededWith(t, 50, 4)
	viewer, child := members[0], members[3]
	fake := fakeProvider(t, s.now())
	playlist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "#EXTM3U")
		for n := 1; n <= 105; n++ {
			fmt.Fprintf(w, "#EXTINF:-1 tvg-id=\"film.example\" tvg-chno=\"%d\",Station %03d\nhttp://streams.invalid/%d.ts\n", n, n, n)
		}
	}))
	t.Cleanup(playlist.Close)
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Stations"),
		Config: &live.LiveSourceConfig{PlaylistURL: playlist.URL + "/list.m3u", GuideURL: fake.URL + "/guide.xml.gz"}})
	find := func(token, query, cursor string) SearchGroup {
		t.Helper()
		result := get[SearchResults](t, s, token, "/v1/search?kind=channel&limit=50&q="+url.QueryEscape(query)+"&cursor="+url.QueryEscape(cursor))
		if len(result.Groups) != 1 {
			t.Fatalf("channel group: %+v", result)
		}
		return result.Groups[0]
	}
	first := find(viewer, "grim", "")
	if first.Total == nil || *first.Total != 105 || len(first.Channels) != 50 || first.NextCursor == "" {
		t.Fatalf("programme matches beyond a channel batch: %+v", first)
	}
	seen := map[string]bool{}
	page := first
	for {
		for _, channel := range page.Channels {
			if seen[channel.ID] || channel.MatchedProgramme == nil || channel.MatchedProgramme.Title != "A Grim Film" {
				t.Fatalf("repeated channel or missing match: %+v", channel)
			}
			seen[channel.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		page = find(viewer, "grim", page.NextCursor)
	}
	if len(seen) != 105 {
		t.Fatalf("paged %d of 105 channels", len(seen))
	}
	if got := find(child, "grim", ""); got.Total == nil || *got.Total != 0 || len(got.Channels) != 0 {
		t.Fatalf("adult listings leaked into search: %+v", got)
	}
	if got := find(viewer, "night", ""); got.Total == nil || *got.Total != 105 || got.Channels[0].MatchedProgramme.Title != "Night Film" {
		t.Fatalf("next programme did not match: %+v", got)
	}
	// Add a third programme through the guide store, without a real provider or database edit.
	ctx := context.Background()
	channels, err := s.db.LiveChannels(ctx, viewerOfToken(t, s, viewer), db.LiveChannelQuery{Limit: 200})
	if err != nil || len(channels) != 105 {
		t.Fatalf("channels: %d %v", len(channels), err)
	}
	c := channels[0]
	rows, err := s.db.GuideProgrammes(ctx, c.GuideSource, c.GuideKey, s.now(), s.now().Add(24*time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	// A long off-air gap does not make the cached next programme disappear.
	for n := range rows {
		if rows[n].Title == "Night Film" {
			rows[n].StartsAt, rows[n].EndsAt = s.now().Add(36*time.Hour), s.now().Add(37*time.Hour)
		}
	}
	rows = append(rows, db.GuideProgramme{GuideKey: c.GuideKey, StartsAt: s.now().Add(38 * time.Hour), EndsAt: s.now().Add(39 * time.Hour),
		ProgrammeKey: "later", Title: "Unrelated Later"})
	if err := s.db.ReplaceSourceGuide(ctx, c.GuideSource, func(add func(db.GuideProgramme) error) error {
		for _, p := range rows {
			if err := add(p); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := find(viewer, "night", ""); got.Total == nil || *got.Total != 105 {
		t.Fatalf("next after a long gap did not match: %+v", got)
	}
	if got := find(viewer, "unrelated", ""); got.Total == nil || *got.Total != 0 {
		t.Fatalf("a later programme matched now/next: %+v", got)
	}
}
