package api

import (
	"database/sql"
	"github.com/PorticoMediaServer/portico/server-next/internal/live"
	"path/filepath"
	"testing"
)

func TestLiveSessionCannotBeReadOrRenewedByAnotherProfile(t *testing.T) {
	th := newTheatre(t)
	s, owner := th.s, th.owner.AccessToken
	fake := fakeProvider(t, s.now())
	t.Cleanup(s.tuners.Close)
	addSource(t, s, owner, LiveSourceSave{Kind: "playlist", Name: new("Antenna"), TunerCount: new(2), Config: &live.LiveSourceConfig{PlaylistURL: fake.URL + "/tuned.m3u"}})
	channels := get[Page[LiveChannel]](t, s, owner, "/v1/live/channels").Items
	var watching PlaybackSession
	if w := call(t, s, "POST", "/v1/playback/sessions", owner, PlaybackStart{Channel: channels[0].ID}, &watching); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	var selected Session
	call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, s, 5).Session.AccessToken, ProfileSelect{}, &selected)
	conn, err := sql.Open("sqlite", filepath.Join(s.dataDir, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(`UPDATE profiles SET allow_live_tv = 0 WHERE id = ?`, viewerOfToken(t, s, selected.AccessToken).ProfileID); err != nil {
		t.Fatal(err)
	}
	if w := call(t, s, "GET", "/v1/live/channels", selected.AccessToken, nil, nil); w.Code != 404 {
		t.Fatal("Live TV not disabled", w.Code, w.Body)
	}
	for _, method := range []string{"GET", "PATCH", "PUT"} {
		path := "/v1/playback/sessions/" + watching.ID
		var body any
		if method == "PATCH" {
			body = map[string]any{"paused": true}
		}
		if method == "PUT" {
			path += "/progress"
			body = PlaybackProgress{State: "playing"}
		}
		w := call(t, s, method, path, selected.AccessToken, body, nil)
		if w.Code != 404 {
			t.Errorf("another profile %s session: %d %s", method, w.Code, w.Body)
		}
	}
}

func TestPlaybackStartReplayCannotCrossProfiles(t *testing.T) {
	th := newTheatre(t)
	owner := th.owner.AccessToken
	req := PlaybackStart{ItemID: th.film}
	if code, _ := th.start(owner, "profile-replay", req); code != 201 {
		t.Fatal("start", code)
	}
	var kid Profile
	call(t, th.s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	call(t, th.s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", owner, map[string]any{"maxRatingAge": 6}, nil)
	var selected Session
	call(t, th.s, "POST", "/v1/profiles/"+kid.ID+"/select", owner, ProfileSelect{}, &selected)
	if code, _ := th.start(selected.AccessToken, "fresh", req); code != 404 {
		t.Fatal("fresh start", code)
	}
	if code, session := th.start(selected.AccessToken, "profile-replay", req); code != 404 {
		t.Errorf("replay returned protected session: %d %+v", code, session)
	}
}

func TestPlaybackStartReplayUsesTheCurrentDeviceAndAccess(t *testing.T) {
	th := newTheatre(t)
	owner := th.owner.AccessToken
	req := PlaybackStart{ItemID: th.film}
	code, first := th.start(owner, "same-key", req)
	if code != 201 {
		t.Fatal(code)
	}
	if code, repeated := th.start(owner, "same-key", req); code != 200 || repeated.ID != first.ID {
		t.Fatal("own replay changed", code)
	}
	other := onMain(t, th.s, signIn(t, th.s, 7)).AccessToken
	if code, second := th.start(other, "same-key", req); code != 201 || second.ID == first.ID {
		t.Fatal("another device reused a session", code)
	}
	main := get[Page[Profile]](t, th.s, owner, "/v1/profiles").Items[0].ID
	if code := status(t, th.s, "PUT", "/v1/profiles/"+main+"/restrictions", owner, map[string]any{"maxRatingAge": 6}); code != 200 {
		t.Fatal(code)
	}
	if code, _ := th.start(owner, "same-key", req); code != 404 {
		t.Errorf("own replay after losing access: HTTP %d", code)
	}
}
