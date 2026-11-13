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
 <programme channel="film.example" start="%[4]s" stop="%[5]s"><title>Night Film</title></programme>
</tv>`, stamp(-30*time.Minute), stamp(30*time.Minute), stamp(2*time.Hour), stamp(4*time.Hour), stamp(5*time.Hour))
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
	mux.HandleFunc("/tuned.m3u", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "#EXTM3U\n#EXTINF:-1 tvg-chno=\"1\",One\nhttp://%[1]s/stream/1.ts\n#EXTINF:-1 tvg-chno=\"2\",Two\nhttp://%[1]s/stream/2.ts\n", r.Host)
	})
	stream := func(w http.ResponseWriter, r *http.Request) {
		for n := 0; r.Context().Err() == nil; n++ {
			p := make([]byte, 188)
			p[0], p[1] = 0x47, byte(n)
			if _, err := w.Write(p); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(2 * time.Millisecond)
		}
	}
	mux.HandleFunc("/stream/", stream)
	mux.HandleFunc("/recordable.m3u", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "#EXTM3U\n#EXTINF:-1 tvg-id=\"news.example\" tvg-chno=\"2\",Example News\nhttp://%[1]s/stream/2.ts\n"+
			"#EXTINF:-1 tvg-id=\"film.example\" tvg-chno=\"4\",Film Four\nhttp://%[1]s/stream/4.ts\n", r.Host)
	})
	mux.HandleFunc("/xtream/live/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/xtream/live/sam/s3cret-word/7.ts" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		stream(w, r)
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

	var preview LiveSourcePreview
	ask := LiveSourceSave{Kind: "playlist", Config: &live.LiveSourceConfig{PlaylistURL: fake.URL + "/playlist.m3u", GuideURL: fake.URL + "/guide.xml.gz"}}
	if w := call(t, s, "POST", "/v1/admin/live/sources/preview", owner, ask, &preview); w.Code != 200 ||
		preview.Report.Channels != 2 || preview.Report.SkippedChannels != 1 || preview.Report.Programmes != 4 || preview.Report.SkippedProgrammes != 1 {
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

	var current LiveSource
	response := call(t, s, "GET", "/v1/admin/live/sources/"+src.ID, owner, nil, &current)
	if current.Revision == "" || response.Header().Get("ETag") != "\""+current.Revision+"\"" {
		t.Fatal("source revision must be its actual ETag")
	}
	filtered := get[Page[LiveSourceChannel]](t, s, owner, "/v1/admin/live/sources/"+src.ID+"/channels?q=film")
	if len(filtered.Items) != 1 || filtered.Items[0].Name != "Film Four" {
		t.Fatalf("searching the owner's channel map: %+v", filtered)
	}
	channels := get[Page[LiveChannel]](t, s, everything, "/v1/live/channels").Items
	if len(channels) != 2 || channels[0].Number != "2.1" || channels[1].Number != "4" {
		t.Fatalf("channels: %+v", channels)
	}
	stretch := "/v1/live/guide?from=" + s.now().Add(-time.Hour).UTC().Format(time.RFC3339) + "&to=" + s.now().Add(3*time.Hour).UTC().Format(time.RFC3339)
	check := func(token string, grimShown bool) {
		t.Helper()
		guide := get[LiveGuide](t, s, token, stretch)
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
	if code := status(t, s, "PUT", "/v1/live/channels/"+channels[1].ID+"/state", everything, LiveChannelMarks{Favorite: true}); code != 200 {
		t.Fatalf("favouriting answered %d", code)
	}
	if code := status(t, s, "PATCH", "/v1/admin/live/sources/"+src.ID+"/channels", owner,
		LiveSourceChannelEdits{Channels: []LiveSourceChannelEdit{{ID: channels[1].ID, Name: new("Films"), Number: new("40")}}}); code != 204 {
		t.Fatalf("editing a channel answered %d", code)
	}
	if err := s.sources.Refresh(context.Background(), src.ID); err != nil {
		t.Fatal(err)
	}
	now := get[Page[LiveOnNow]](t, s, everything, "/v1/live/now").Items
	if len(now) != 2 || now[0].Channel.Name != "Films" || now[0].Channel.Number != "40" || !now[0].Channel.Favorite || now[0].Now == nil {
		t.Fatalf("after a refresh On now reads %+v", now)
	}

	listings := addSource(t, s, owner, LiveSourceSave{Kind: "guide", Name: new("Other listings"), Config: &live.LiveSourceConfig{GuideURL: fake.URL + "/guide.xml.gz"}})
	cfg := get[LiveSource](t, s, owner, "/v1/admin/live/sources/"+src.ID).Config
	cfg.GuideSource = listings.ID
	cfg.Numbering, cfg.FirstNumber = "sequential", 0
	if code := status(t, s, "PATCH", "/v1/admin/live/sources/"+src.ID, owner, LiveSourceSave{Config: &cfg}); code != 200 {
		t.Fatalf("choosing listings: %d", code)
	}
	if got := get[LiveSource](t, s, owner, "/v1/admin/live/sources/"+src.ID); got.Config.FirstNumber != 1 {
		t.Fatalf("the implicit starting number: %+v", got.Config)
	}
	if err := s.sources.Refresh(context.Background(), src.ID); err != nil {
		t.Fatal(err)
	}
	mapped := get[Page[LiveSourceChannel]](t, s, owner, "/v1/admin/live/sources/"+src.ID+"/channels")
	if len(mapped.Items) != 2 || mapped.Items[0].GuideSource != listings.ID || mapped.Items[0].SourceKey == "" {
		t.Fatalf("chosen listings and source keys: %+v", mapped)
	}

	cfg.PlaylistURL, cfg.Uploaded = "", true
	replacement := "#EXTM3U\n#EXTINF:-1 tvg-id=\"replacement\",Uploaded replacement\nhttp://streams.invalid/replacement.ts\n"
	if code := status(t, s, "PATCH", "/v1/admin/live/sources/"+src.ID, owner, LiveSourceSave{Config: &cfg, Playlist: replacement}); code != 200 {
		t.Fatalf("replacing an uploaded playlist: %d", code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		items := get[Page[LiveChannel]](t, s, everything, "/v1/live/channels").Items
		if len(items) == 1 && items[0].Name == "Uploaded replacement" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the replacement was not imported: %+v", items)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Only me: the owner's channels leave everyone else's guide; paused, they leave the owner's too.
	if code := status(t, s, "PATCH", "/v1/admin/live/sources/"+src.ID, owner, LiveSourceSave{Audience: new("owner")}); code != 200 {
		t.Fatalf("changing the audience answered %d", code)
	}
	if n := len(get[Page[LiveChannel]](t, s, everything, "/v1/live/channels").Items); n != 0 {
		t.Fatalf("a member is shown %d channels of an owner-only source", n)
	}
	status(t, s, "PATCH", "/v1/admin/live/sources/"+src.ID, owner, LiveSourceSave{Paused: new(true)})
	if n := len(get[Page[LiveChannel]](t, s, owner, "/v1/live/channels").Items); n != 0 {
		t.Fatalf("a paused source's %d channels are in Live TV", n)
	}
}

// An Xtream sign-in is stored and used, never shown; a refused one says the source needs its
// sign-in again. An HDHomeRun's tuner count and channels come from the tuner itself.
func TestAnXtreamSignInIsNeverShownAndATunerSaysItsTuners(t *testing.T) {
	s, owner, _ := seededWith(t, 50, 0)
	fake := fakeProvider(t, s.now())
	src := addSource(t, s, owner, LiveSourceSave{Kind: "xtream", Name: new("Provider"), Config: &live.LiveSourceConfig{ServerURL: fake.URL + "/xtream"},
		SignIn: &live.LiveSignIn{Username: "sam", Password: "s3cret-word"}})
	w := call(t, s, "GET", "/v1/admin/live/sources/"+src.ID, owner, nil, nil)
	if strings.Contains(w.Body.String(), "s3cret-word") || !strings.Contains(w.Body.String(), `"hasSignIn":true`) {
		t.Fatalf("the source shows its sign-in, or not that it has one: %s", w.Body)
	}
	if w := call(t, s, "GET", "/v1/live/guide", owner, nil, nil); strings.Contains(w.Body.String(), "s3cret-word") || !strings.Contains(w.Body.String(), "Example News") {
		t.Fatalf("the guide: %s", w.Body)
	}
	status(t, s, "PATCH", "/v1/admin/live/sources/"+src.ID, owner, LiveSourceSave{SignIn: &live.LiveSignIn{Username: "sam", Password: "wrong"}})
	if err := s.sources.Refresh(context.Background(), src.ID); err == nil {
		t.Fatal("a refused sign-in refreshed")
	}
	if got := get[LiveSource](t, s, owner, "/v1/admin/live/sources/"+src.ID); got.State != "needs_sign_in" {
		t.Fatalf("after a refused sign-in the source reads %q", got.State)
	}
	if n := len(get[Page[LiveChannel]](t, s, owner, "/v1/live/channels").Items); n != 1 {
		t.Fatalf("a failed refresh left %d channels, want the one it had", n)
	}
	if !get[LiveGuide](t, s, owner, "/v1/live/guide").OutOfDate {
		t.Fatal("after a failed refresh the guide does not say it is out of date")
	}

	tuner := addSource(t, s, owner, LiveSourceSave{Kind: "hdhomerun", Name: new("Antenna"), Config: &live.LiveSourceConfig{TunerURL: fake.URL + "/hdhr",
		GuideURL: fake.URL + "/guide.xml.gz"}})
	got := get[LiveSource](t, s, owner, "/v1/admin/live/sources/"+tuner.ID)
	if got.TunerCount != 2 || got.Report == nil || got.Report.Channels != 1 || got.Report.SkippedChannels != 1 || got.Report.ChannelsWithGuide != 1 {
		t.Fatalf("the tuner reads %+v %+v", got, got.Report)
	}
}

// Watching a tuner's channel shares one connection among its viewers; a second channel on a
// one-tuner source is refused naming what uses the tuner; the stream plays from live or from
// a moment in the rewind stretch, and its address stops working when the session ends.
func TestWatchingASourcesChannelSharesItsTuner(t *testing.T) {
	s, owner, members := seededWith(t, 50, 2)
	fake := fakeProvider(t, s.now())
	t.Cleanup(s.tuners.Close) // before the fake source closes, which waits for its streams
	src := addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Antenna"), TunerCount: new(1), Config: &live.LiveSourceConfig{PlaylistURL: fake.URL + "/tuned.m3u"}})
	channels := get[Page[LiveChannel]](t, s, members[0], "/v1/live/channels").Items
	if len(channels) != 2 {
		t.Fatalf("channels: %+v", channels)
	}
	var first, second PlaybackSession
	if w := call(t, s, "POST", "/v1/playback/sessions", members[0], PlaybackStart{Channel: channels[0].ID}, &first); w.Code != 201 ||
		first.Kind != "live" || first.Live == nil || first.Presentation.URL == "" {
		t.Fatalf("watching answered %d: %s", w.Code, w.Body)
	}
	if w := call(t, s, "POST", "/v1/playback/sessions", members[1], PlaybackStart{Channel: channels[0].ID}, &second); w.Code != 201 {
		t.Fatalf("a second viewer of the same channel answered %d: %s", w.Code, w.Body)
	}
	tuners := get[DvrTuners](t, s, owner, "/v1/admin/dvr/tuners")
	found := false
	for _, row := range tuners.Sources {
		if row.SourceID == src.ID {
			found = true
			if row.TunerCount != 1 || row.InUse != 1 || len(row.Active) != 2 {
				t.Fatalf("two viewers must share one tuner input: %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("the tuner snapshot lost its source")
	}
	var refused struct {
		Error Error `json:"error"`
	}
	if w := call(t, s, "POST", "/v1/playback/sessions", owner, PlaybackStart{Channel: channels[1].ID}, &refused); w.Code != 409 ||
		refused.Error.Code != "no_tuner" || !strings.Contains(refused.Error.Message, "Another viewer") {
		t.Fatalf("a second channel on a one-tuner source answered %d %+v", w.Code, refused.Error)
	}

	read := func(address string) int {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		r := newRequest("GET", address, "", nil).WithContext(ctx)
		w := serve(s, r)
		if w.Code != 200 {
			t.Logf("%s answered %d: %s", address, w.Code, w.Body)
		}
		return w.Body.Len()
	}
	time.Sleep(200 * time.Millisecond)
	if n := read(first.Presentation.URL); n < 188*4 || n%188 != 0 {
		t.Fatalf("reading live sent %d bytes", n)
	}
	if n := read(first.Presentation.URL + "?at=" + first.Live.OldestAt.Format(time.RFC3339Nano)); n < 188*4 {
		t.Fatalf("reading from the oldest point sent %d bytes", n)
	}
	if code := status(t, s, "PUT", "/v1/playback/sessions/"+first.ID+"/progress", members[0], PlaybackProgress{State: "paused"}); code != 200 {
		t.Fatalf("a live session's report answered %d", code)
	}
	status(t, s, "DELETE", "/v1/playback/sessions/"+first.ID, members[0], nil)
	if code := status(t, s, "GET", first.Presentation.URL, "", nil); code != 404 {
		t.Fatalf("a stopped session's stream answered %d", code)
	}
}

// An Xtream channel plays from an address made at tune time from the sealed sign-in, which
// no stored row and no answer ever holds.
func TestAnXtreamChannelPlaysWithItsSealedSignIn(t *testing.T) {
	s, owner, _ := seededWith(t, 50, 0)
	fake := fakeProvider(t, s.now())
	t.Cleanup(s.tuners.Close)
	addSource(t, s, owner, LiveSourceSave{Kind: "xtream", Name: new("Provider"), Config: &live.LiveSourceConfig{ServerURL: fake.URL + "/xtream"},
		SignIn: &live.LiveSignIn{Username: "sam", Password: "s3cret-word"}})
	channels := get[Page[LiveChannel]](t, s, owner, "/v1/live/channels").Items
	var session PlaybackSession
	if w := call(t, s, "POST", "/v1/playback/sessions", owner, PlaybackStart{Channel: channels[0].ID}, &session); w.Code != 201 ||
		strings.Contains(w.Body.String(), "s3cret-word") {
		t.Fatalf("watching answered %d: %s", w.Code, w.Body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	time.Sleep(100 * time.Millisecond)
	if w := serve(s, newRequest("GET", session.Presentation.URL, "", nil).WithContext(ctx)); w.Body.Len() < 188*4 {
		t.Fatalf("the stream sent %d bytes", w.Body.Len())
	}
}
