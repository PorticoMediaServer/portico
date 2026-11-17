package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/librarychannels"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

func TestOnNowHomePagesPermittedChannelsAndFollowsLayout(t *testing.T) {
	s, owner, members := seededWith(t, 600, 4)
	for _, row := range get[Home](t, s, members[0], "/v1/home").Rows {
		if row.ID == "on_now" {
			t.Fatal("On now without source")
		}
	}
	playlist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "#EXTM3U")
		for n := 10; n <= 60; n++ {
			fmt.Fprintf(w, "#EXTINF:-1 tvg-chno=\"%d\",Channel %d\nhttp://streams.invalid/%d.ts\n", n, n, n)
		}
	}))
	defer playlist.Close()
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Antenna"), Config: &live.LiveSourceConfig{PlaylistURL: playlist.URL}})
	guide := fakeProvider(t, s.now())
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Guide"),
		Config: &live.LiveSourceConfig{PlaylistURL: guide.URL + "/playlist.m3u", GuideURL: guide.URL + "/guide.xml.gz"}})
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Private"), Audience: new("owner"),
		Config: &live.LiveSourceConfig{PlaylistURL: guide.URL + "/playlist.m3u"}})
	hiddenLibrary := makeChannel(t, s, owner, "Shows", librarychannels.LibraryChannelBuilder{LibraryChannelSelection: librarychannels.LibraryChannelSelection{
		Libraries: []string{libraryOfKind(t, s, owner, "shows")}, Kinds: []string{"show"}}})
	all := get[Page[LiveOnNow]](t, s, members[0], "/v1/live/now?limit=50")
	favorite, hidden := "", ""
	for _, entry := range all.Items {
		if entry.Channel.Number == "10" {
			favorite = entry.Channel.ID
		}
		if entry.Channel.Number == "11" {
			hidden = entry.Channel.ID
		}
	}
	if favorite == "" || hidden == "" {
		t.Fatal("fixture channels not imported")
	}
	for _, who := range members {
		if status(t, s, "PUT", "/v1/live/channels/"+favorite+"/state", who, LiveChannelMarks{Favorite: true}) != 200 {
			t.Fatal("favorite")
		}
		if status(t, s, "PUT", "/v1/live/channels/"+hidden+"/state", who, LiveChannelMarks{Hidden: true}) != 200 {
			t.Fatal("hide channel")
		}
		ids := []string{}
		var observation time.Time
		last := -1.0
		for cursor := ""; ; {
			row := get[HomeRow](t, s, who, "/v1/home/rows/on_now?limit=7&cursor="+url.QueryEscape(cursor))
			if row.Live == nil {
				t.Fatal("missing HomeLive")
			}
			if row.Title != "On now" || len(row.Items) != 0 || row.Total != nil {
				t.Fatal("live row used catalogue cards/count")
			}
			if observation.IsZero() {
				observation = row.Live.ObservedAt
			}
			if row.Live.ObservedAt != observation || row.Live.Revision != observation.Format(time.RFC3339Nano) || row.Live.Sort != "favorites" || !row.Live.ExpiresAt.After(observation) {
				t.Fatal("unstable live observation")
			}
			for _, entry := range row.Live.Channels {
				if entry.Source.ID != entry.Channel.SourceID || entry.Source.Generation != entry.Channel.Generation || entry.Channel.Mark.Seed == "" || len(entry.Channel.Programmes) > 1 {
					t.Fatal("invalid guide identity")
				}
				c := entry.Channel
				if slices.Contains(ids, c.ID) || c.ID == hidden {
					t.Fatal("hidden/repeated channel")
				}
				if len(ids) == 0 {
					if c.ID != favorite {
						t.Fatal("favourite not first")
					}
				} else {
					n, _ := strconv.ParseFloat(c.Number, 64)
					if n < last {
						t.Fatal("channel number order")
					}
					last = n
				}
				ids = append(ids, c.ID)
				if who == members[2] && c.ID == hiddenLibrary.ID {
					t.Fatal("channel crossed library limit")
				}
				if who == members[3] && c.Name == "Film Four" && len(c.Programmes) > 0 && (c.Programmes[0].Title != "" || entry.Actions.Play) {
					t.Fatal("blocked programme name exposed")
				}
			}
			if row.NextCursor == "" {
				break
			}
			cursor = row.NextCursor
			s.setClock(func() time.Time { return observation.Add(2 * time.Second) })
			if len(ids) > 60 {
				t.Fatal("On now did not end")
			}
		}
		want := 53
		if who == members[2] {
			want = 52
		}
		if len(ids) != want {
			t.Fatalf("visible channel count %d want %d", len(ids), want)
		}
		preview := HomeRow{}
		for _, row := range get[Home](t, s, who, "/v1/home").Rows {
			if row.ID == "on_now" {
				preview = row
			}
		}
		combined := []string{}
		for _, entry := range preview.Live.Channels {
			combined = append(combined, entry.Channel.ID)
		}
		rest := get[HomeRow](t, s, who, "/v1/home/rows/on_now?limit=50&cursor="+url.QueryEscape(preview.NextCursor))
		for _, entry := range rest.Live.Channels {
			combined = append(combined, entry.Channel.ID)
		}
		if rest.Live.ObservedAt != preview.Live.ObservedAt || rest.Live.Revision != preview.Live.Revision {
			t.Fatal("Home changed observation")
		}
		if !slices.Equal(combined, ids) {
			t.Fatal("Home continuation lost channel order")
		}
		if len(preview.Live.Channels) != 20 || preview.NextCursor == "" || len(ids) < 50 {
			t.Fatal("On now preview/source cap")
		}
		oldNow := *s.clock.Load()
		s.setClock(func() time.Time { return preview.Live.ObservedAt.Add(2 * time.Minute) })
		if status(t, s, "GET", "/v1/home/rows/on_now?cursor="+url.QueryEscape(preview.NextCursor), who, nil) != 400 {
			t.Fatal("expired cursor accepted")
		}
		s.setClock(oldNow)
	}
	edit := PreferencesEdit{Scope: "profile-server", DeviceClass: "web", Values: map[string]any{
		"home.rowOrder": []string{"on_now"}, "home.hiddenRowIds": []string{"on_now"}}}
	if status(t, s, "PATCH", "/v1/preferences", members[0], edit) != 200 {
		t.Fatal("save layout")
	}
	editor := get[Home](t, s, members[0], "/v1/home?hidden=include").Rows
	if editor[0].ID != "on_now" || !editor[0].Hidden || editor[0].Live != nil {
		t.Fatal("hidden On now entries in editor")
	}
	conn, err := sql.Open("sqlite", filepath.Join(filepath.Dir(s.setupFile), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`UPDATE profiles SET allow_live_tv=0 WHERE id=?`, viewerOfToken(t, s, members[0]).ProfileID); err != nil {
		t.Fatal(err)
	}
	if status(t, s, "GET", "/v1/home/rows/on_now", members[0], nil) != 404 {
		t.Fatal("profile without Live TV opened row")
	}
	for _, row := range get[Home](t, s, members[0], "/v1/home?hidden=include").Rows {
		if row.ID == "on_now" {
			t.Fatal("profile without Live TV named row")
		}
	}
}
