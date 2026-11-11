package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/live"
)

func TestReviewALeftOutChannelStaysLeftOutAfterARefresh(t *testing.T) {
	s, owner, _ := seededWith(t, 50, 0)
	fake := fakeProvider(t, s.now())
	src := addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Cable"),
		Config: &live.Config{PlaylistURL: fake.URL + "/playlist.m3u", GuideURL: fake.URL + "/guide.xml.gz"}})
	chans := get[Page[SourceChannel]](t, s, owner, "/v1/admin/live/sources/"+src.ID+"/channels").Items
	if code := status(t, s, "PATCH", "/v1/admin/live/sources/"+src.ID+"/channels", owner,
		SourceChannelEdits{Channels: []SourceChannelEdit{{ID: chans[0].ID, BroughtIn: new(false)}}}); code != 204 {
		t.Fatalf("leaving a channel out answered %d", code)
	}
	if err := s.sources.Refresh(context.Background(), src.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range get[Page[SourceChannel]](t, s, owner, "/v1/admin/live/sources/"+src.ID+"/channels").Items {
		if c.ID == chans[0].ID && c.BroughtIn {
			t.Fatal("the channel the owner left out came back in after a refresh")
		}
	}
}

func TestReviewAStoredSignInIsNotSentToANewServerAddress(t *testing.T) {
	s, owner, _ := seededWith(t, 50, 0)
	fake := fakeProvider(t, s.now())
	src := addSource(t, s, owner, LiveSourceSave{Kind: "xtream", Name: new("Provider"), Config: &live.Config{ServerURL: fake.URL + "/xtream"},
		SignIn: &live.SignIn{Username: "sam", Password: "s3cret-word"}})
	var got atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("password") == "s3cret-word" {
			got.Store(true)
		}
		w.WriteHeader(404)
	}))
	defer other.Close()
	code := status(t, s, "PATCH", "/v1/admin/live/sources/"+src.ID, owner, LiveSourceSave{Config: &live.Config{ServerURL: other.URL}})
	if code == 200 {
		s.sources.Refresh(context.Background(), src.ID)
	}
	if got.Load() {
		t.Fatalf("the stored sign-in went to a new address the owner set without typing it again (PATCH answered %d)", code)
	}
}
