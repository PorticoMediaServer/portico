package livechannels

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A playlist source with a full guide saves. The preview snapshot holds the fetched guide, and a
// stale 128 KiB bound on stored payloads refused any real one: the preview listed the channels
// and programmes, then the save answered guide_unavailable (seen on the demo with 3,499 programmes).
func TestRemoteSourceWithALargeGuideSaves(t *testing.T) {
	var m3u, guide strings.Builder
	m3u.WriteString("#EXTM3U\n")
	guide.WriteString(`<?xml version="1.0" encoding="UTF-8"?><tv>`)
	start := time.Now().UTC().Truncate(time.Hour)
	for c := 0; c < 12; c++ {
		fmt.Fprintf(&m3u, "#EXTINF:-1 tvg-id=\"ch%d\" tvg-chno=\"%d\" tvg-name=\"Channel %d\",Channel %d\nhttps://streams.example/ch%d/index.m3u8\n", c, 100+c, c, c, c)
		fmt.Fprintf(&guide, `<channel id="ch%d"><display-name>Channel %d</display-name></channel>`, c, c)
	}
	for c := 0; c < 12; c++ {
		for h := 0; h < 72; h++ {
			a, b := start.Add(time.Duration(h)*time.Hour), start.Add(time.Duration(h+1)*time.Hour)
			fmt.Fprintf(&guide, `<programme start="%s" stop="%s" channel="ch%d"><title>Programme %d on %d</title><desc>%s</desc></programme>`,
				a.Format("20060102150405 -0700"), b.Format("20060102150405 -0700"), c, h, c, strings.Repeat("A long description of this hour. ", 6))
		}
	}
	guide.WriteString(`</tv>`)
	if guide.Len() < 256<<10 {
		t.Fatalf("the guide must exceed the old bound: %d bytes", guide.Len())
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/channels.m3u":
			_, _ = w.Write([]byte(m3u.String()))
		case "/guide.xml":
			_, _ = w.Write([]byte(guide.String()))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := testStore(t)
	ctx := context.Background()
	a := testAuthority("fence", true, true)
	draft := RemoteDraft{ID: strings.Repeat("ab", 24), Name: "Playlist", Kind: "m3u", Locator: srv.URL + "/channels.m3u", GuideURL: srv.URL + "/guide.xml",
		ConfirmedLANRoots: []string{srv.URL + "/"}, Mappings: []ChannelMapping{}, RefreshMs: 21600000, ViewerAccess: "server-members"}
	preview, e := s.PreviewRemote(ctx, a, SourceFetcher{}, draft)
	if e != nil {
		t.Fatalf("preview: %v", e)
	}
	if preview.Preview.Channels != 12 || preview.Preview.Programmes != 12*72 {
		t.Fatalf("preview: %+v", preview.Preview)
	}
	source, e := s.SaveRemote(ctx, a, preview.ID, strings.Repeat("34", 24))
	if e != nil {
		t.Fatalf("save: %v", e)
	}
	if source.ID != draft.ID {
		t.Fatalf("saved %+v", source)
	}
}

func TestRemoteInitialSelectionKeepsOnlyEnabledChannels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\" group-title=\"Keep\",One\nhttps://streams.example/one\n#EXTINF:-1 tvg-id=\"two\" group-title=\"Drop\",Two\nhttps://streams.example/two\n")
	}))
	defer srv.Close()
	s := testStore(t)
	ctx := context.Background()
	a := testAuthority("fence", true, true)
	draft := RemoteDraft{ID: strings.Repeat("ab", 24), Name: "Selected", Kind: "m3u", Locator: srv.URL + "/channels.m3u", ConfirmedLANRoots: []string{srv.URL + "/"}, RefreshMs: 21600000, ViewerAccess: "server-members", EnabledGroups: []string{"Keep"}}
	p, e := s.PreviewRemote(ctx, a, SourceFetcher{}, draft)
	if e != nil {
		t.Fatal(e)
	}
	if p.Preview.Channels != 1 || p.Preview.Names[0] != "One" {
		t.Fatalf("%+v", p)
	}
	source, e := s.SaveRemote(ctx, a, p.ID, strings.Repeat("cd", 24))
	if e != nil {
		t.Fatal(e)
	}
	if source.Channels != 1 {
		t.Fatalf("%+v", source)
	}
}
