package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

// fakeProvider answers like a playlist host, an XMLTV guide, an Xtream provider and an
// HDHomeRun tuner, all in this process. Nothing outside it is ever contacted.
func fakeProvider(t *testing.T, now time.Time) *httptest.Server {
	stamp := func(d time.Duration) string { return now.Add(d).UTC().Format("20060102150405 +0000") }
	guide := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE tv SYSTEM "xmltv.dtd">
<tv>
 <channel id="news.example"><display-name>Example News</display-name></channel>
 <channel id="film.example"><display-name>Film Four</display-name><display-name>4</display-name></channel>
 <programme channel="news.example" start="%[1]s" stop="%[2]s"><title>Morning News</title><rating><value>TV-G</value></rating><new/></programme>
 <programme channel="news.example" start="%[2]s" stop="%[3]s"><title>Late News</title></programme>
 <programme channel="film.example" start="%[1]s" stop="%[3]s"><title>A Grim Film</title><desc>Not for children.</desc>
  <episode-num system="xmltv_ns">1.4.</episode-num><rating><value>TV-MA</value></rating></programme>
 <programme channel="film.example" start="bad" stop="%[3]s"><title>Broken</title></programme>
</tv>`, stamp(-30*time.Minute), stamp(30*time.Minute), stamp(2*time.Hour))
	var packed bytes.Buffer
	z := gzip.NewWriter(&packed)
	z.Write([]byte(guide))
	z.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/playlist.m3u", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "#EXTM3U\n#EXTINF:-1 tvg-id=\"news.example\" tvg-chno=\"2.1\" group-title=\"News\",Example News\nhttp://streams.invalid/news.ts\n"+
			"#EXTINF:-1 tvg-id=\"film.example\" tvg-chno=\"4\" group-title=\"Films\",Film Four\nhttp://streams.invalid/film.ts\n"+
			"#EXTINF:-1 group-title=\"Films\",No Address\nnot an address\n")
	})
	mux.HandleFunc("/guide.xml.gz", func(w http.ResponseWriter, r *http.Request) { w.Write(packed.Bytes()) })
	mux.HandleFunc("/xtream/player_api.php", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("username") != "sam" || r.URL.Query().Get("password") != "s3cret-word" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `[{"stream_id":7,"name":"Example News","num":2,"epg_channel_id":"news.example","category_name":"News"}]`)
	})
	mux.HandleFunc("/xtream/xmltv.php", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, guide) })
	mux.HandleFunc("/hdhr/discover.json", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"TunerCount":2}`) })
	mux.HandleFunc("/hdhr/lineup.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"GuideNumber":"4","GuideName":"Film Four","URL":"http://tuner.invalid/auto/v4"},
			{"GuideNumber":"9","GuideName":"Paid","URL":"http://tuner.invalid/auto/v9","DRM":1}]`)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func addSource(t *testing.T, s *Server, owner string, save LiveSourceSave) LiveSource {
	t.Helper()
	var src LiveSource
	if w := call(t, s, "POST", "/v1/admin/live/sources", owner, save, &src); w.Code != 201 {
		t.Fatalf("adding a source answered %d: %s", w.Code, w.Body)
	}
	if err := s.sources.Refresh(context.Background(), src.ID); err != nil {
		t.Fatal(err)
	}
	return src
}

// A playlist with a guide comes in with its channels numbered as sent and its listings, a
// line it cannot use is reported and loses nothing else, a profile under a rating limit sees
// a rated programme only as not available, and an owner's edit and a profile's favourite
// survive the next refresh.
func TestAPlaylistAndItsGuideComeIn(t *testing.T) {
	s, owner, members := seededWith(t, 50, 4)
	everything, thirteen := members[0], members[3]
	fake := fakeProvider(t, s.now())

	var preview SourcePreview
	ask := LiveSourceSave{Kind: "playlist", Config: &live.Config{PlaylistURL: fake.URL + "/playlist.m3u", GuideURL: fake.URL + "/guide.xml.gz"}}
	if w := call(t, s, "POST", "/v1/admin/live/sources/preview", owner, ask, &preview); w.Code != 200 ||
		preview.Report.Channels != 2 || preview.Report.SkippedChannels != 1 || preview.Report.Programmes != 3 || preview.Report.SkippedProgrammes != 1 {
		t.Fatalf("preview answered %d %+v", w.Code, preview.Report)
	}
	if strings.Contains(fmt.Sprint(preview.Channels), "streams.invalid") {
		t.Fatalf("the preview shows a stream address: %+v", preview.Channels)
	}
	ask.Name = new("Cable")
	src := addSource(t, s, owner, ask)
	if got := get[LiveSource](t, s, owner, "/v1/admin/live/sources/"+src.ID); got.State != "up_to_date" || got.Report == nil || got.Report.Channels != 2 {
		t.Fatalf("after its refresh the source reads %+v", got)
	}

	channels := get[Page[Channel]](t, s, everything, "/v1/live/channels").Items
	if len(channels) != 2 || channels[0].Number != "2.1" || channels[1].Number != "4" {
		t.Fatalf("channels: %+v", channels)
	}
	stretch := "/v1/live/guide?from=" + s.now().Add(-time.Hour).UTC().Format(time.RFC3339) + "&to=" + s.now().Add(3*time.Hour).UTC().Format(time.RFC3339)
	check := func(token string, grimShown bool) {
		t.Helper()
		guide := get[Guide](t, s, token, stretch)
		if len(guide.Items) != 2 || len(guide.Items[0].Programmes) != 2 || len(guide.Items[1].Programmes) != 1 {
			t.Fatalf("guide: %+v", guide.Items)
		}
		grim := guide.Items[1].Programmes[0]
		if grimShown != (grim.Title == "A Grim Film") || !grimShown && (!grim.NotAvailable || grim.Summary != "" || grim.Season != nil) {
			t.Fatalf("the TV-MA programme reads %+v", grim)
		}
		if strings.Contains(fmt.Sprint(guide), "streams.invalid") {
			t.Fatal("the guide shows a stream address")
		}
	}
	check(everything, true)
	check(thirteen, false)
	if code := status(t, s, "PUT", "/v1/live/channels/"+channels[1].ID+"/state", everything, ChannelMarks{Favorite: true}); code != 200 {
		t.Fatalf("favouriting answered %d", code)
	}
	if code := status(t, s, "PATCH", "/v1/admin/live/sources/"+src.ID+"/channels", owner,
		SourceChannelEdits{Channels: []SourceChannelEdit{{ID: channels[1].ID, Name: new("Films"), Number: new("40")}}}); code != 204 {
		t.Fatalf("editing a channel answered %d", code)
	}
	if err := s.sources.Refresh(context.Background(), src.ID); err != nil {
		t.Fatal(err)
	}
	now := get[Page[OnNow]](t, s, everything, "/v1/live/now").Items
	if len(now) != 2 || now[0].Channel.Name != "Films" || now[0].Channel.Number != "40" || !now[0].Channel.Favorite || now[0].Now == nil {
		t.Fatalf("after a refresh On now reads %+v", now)
	}

	// Only me: the owner's channels leave everyone else's guide; paused, they leave the owner's too.
	if code := status(t, s, "PATCH", "/v1/admin/live/sources/"+src.ID, owner, LiveSourceSave{Audience: new("owner")}); code != 200 {
		t.Fatalf("changing the audience answered %d", code)
	}
	if n := len(get[Page[Channel]](t, s, everything, "/v1/live/channels").Items); n != 0 {
		t.Fatalf("a member is shown %d channels of an owner-only source", n)
	}
	status(t, s, "PATCH", "/v1/admin/live/sources/"+src.ID, owner, LiveSourceSave{Paused: new(true)})
	if n := len(get[Page[Channel]](t, s, owner, "/v1/live/channels").Items); n != 0 {
		t.Fatalf("a paused source's %d channels are in Live TV", n)
	}
}

// An Xtream sign-in is stored and used, never shown; a refused one says the source needs its
// sign-in again. An HDHomeRun's tuner count and channels come from the tuner itself.
func TestAnXtreamSignInIsNeverShownAndATunerSaysItsTuners(t *testing.T) {
	s, owner, _ := seededWith(t, 50, 0)
	fake := fakeProvider(t, s.now())
	src := addSource(t, s, owner, LiveSourceSave{Kind: "xtream", Name: new("Provider"), Config: &live.Config{ServerURL: fake.URL + "/xtream"},
		SignIn: &live.SignIn{Username: "sam", Password: "s3cret-word"}})
	w := call(t, s, "GET", "/v1/admin/live/sources/"+src.ID, owner, nil, nil)
	if strings.Contains(w.Body.String(), "s3cret-word") || !strings.Contains(w.Body.String(), `"hasSignIn":true`) {
		t.Fatalf("the source shows its sign-in, or not that it has one: %s", w.Body)
	}
	if w := call(t, s, "GET", "/v1/live/guide", owner, nil, nil); strings.Contains(w.Body.String(), "s3cret-word") || !strings.Contains(w.Body.String(), "Example News") {
		t.Fatalf("the guide: %s", w.Body)
	}
	status(t, s, "PATCH", "/v1/admin/live/sources/"+src.ID, owner, LiveSourceSave{SignIn: &live.SignIn{Username: "sam", Password: "wrong"}})
	if err := s.sources.Refresh(context.Background(), src.ID); err == nil {
		t.Fatal("a refused sign-in refreshed")
	}
	if got := get[LiveSource](t, s, owner, "/v1/admin/live/sources/"+src.ID); got.State != "needs_sign_in" {
		t.Fatalf("after a refused sign-in the source reads %q", got.State)
	}
	if n := len(get[Page[Channel]](t, s, owner, "/v1/live/channels").Items); n != 1 {
		t.Fatalf("a failed refresh left %d channels, want the one it had", n)
	}
	if !get[Guide](t, s, owner, "/v1/live/guide").OutOfDate {
		t.Fatal("after a failed refresh the guide does not say it is out of date")
	}

	tuner := addSource(t, s, owner, LiveSourceSave{Kind: "hdhomerun", Name: new("Antenna"), Config: &live.Config{TunerURL: fake.URL + "/hdhr",
		GuideURL: fake.URL + "/guide.xml.gz"}})
	got := get[LiveSource](t, s, owner, "/v1/admin/live/sources/"+tuner.ID)
	if got.TunerCount != 2 || got.Report == nil || got.Report.Channels != 1 || got.Report.SkippedChannels != 1 || got.Report.ChannelsWithGuide != 1 {
		t.Fatalf("the tuner reads %+v %+v", got, got.Report)
	}
}
