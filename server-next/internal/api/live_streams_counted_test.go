package api

import (
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/live"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// A live channel watched is a stream like any title (area 11 rule 22): it is in the owner's
// Now playing and the Dashboard's count, it counts against the server's stream limit for a
// title and for another channel, the owner can stop it with a message the viewer is told,
// and a title started on the same device replaces it.
func TestALiveChannelWatchedIsAStream(t *testing.T) {
	th := newTheatre(t)
	fake := fakeProvider(t, th.s.now())
	t.Cleanup(th.s.tuners.Close) // before the fake source closes, which waits for its streams
	owner := th.owner.AccessToken
	addSource(t, th.s, owner, LiveSourceSave{Kind: "playlist", Name: new("Antenna"), TunerCount: new(2), Config: &live.LiveSourceConfig{PlaylistURL: fake.URL + "/tuned.m3u"}})
	channels := get[Page[LiveChannel]](t, th.s, owner, "/v1/live/channels").Items
	if len(channels) != 2 {
		t.Fatalf("channels: %+v", channels)
	}
	tv := onMain(t, th.s, signIn(t, th.s, 2)).AccessToken
	var watching PlaybackSession
	if w := call(t, th.s, "POST", "/v1/playback/sessions", tv, PlaybackStart{Channel: channels[0].ID}, &watching); w.Code != 201 || watching.Kind != "live" {
		t.Fatalf("watching answered %d: %s", w.Code, w.Body)
	}

	playing := get[Page[NowPlaying]](t, th.s, owner, "/v1/admin/sessions").Items
	if len(playing) != 1 || playing[0].ID != watching.ID || playing[0].Kind != "live" || playing[0].Title != channels[0].Name || playing[0].DeviceName == "" {
		t.Fatalf("Now playing reads %+v", playing)
	}
	if got := get[Dashboard](t, th.s, owner, "/v1/admin/dashboard"); got.NowPlaying.Streams != 1 {
		t.Fatalf("the Dashboard counts %d streams", got.NowPlaying.Streams)
	}

	// At a server limit of one, the channel watched leaves no room for a title or another channel.
	if code := status(t, th.s, "PATCH", "/v1/admin/playback/stream-limits", owner, StreamLimits{Total: 1}); code != 200 {
		t.Fatalf("setting the limit answered %d", code)
	}
	if code, _ := th.start(owner, "", PlaybackStart{ItemID: th.film}); code == 201 || code == 202 {
		t.Fatalf("a title past the server's limit started (%d)", code)
	}
	if code := status(t, th.s, "POST", "/v1/playback/sessions", owner, PlaybackStart{Channel: channels[1].ID}); code == 201 {
		t.Fatal("a second channel past the server's limit started")
	}
	if code := status(t, th.s, "PATCH", "/v1/admin/playback/stream-limits", owner, map[string]int{"total": 0}); code != 200 {
		t.Fatalf("lifting the limit answered %d", code)
	}

	// Stopped by the owner, the viewer is told so with the message.
	if code := status(t, th.s, "DELETE", "/v1/admin/sessions/"+watching.ID, owner, StreamStop{Message: "Dinner is ready"}); code != 204 {
		t.Fatalf("stopping the stream answered %d", code)
	}
	if got := get[PlaybackSession](t, th.s, tv, "/v1/playback/sessions/"+watching.ID); got.State != playback.StateEnded || got.End == nil ||
		got.End.Reason != playback.EndTerminated || got.End.Message != "Dinner is ready" {
		t.Fatalf("the stopped viewer reads %+v", got)
	}

	// A title started on the device replaces what it watched live.
	call(t, th.s, "POST", "/v1/playback/sessions", tv, PlaybackStart{Channel: channels[0].ID}, &watching)
	if code, _ := th.start(tv, "", PlaybackStart{ItemID: th.film}); code != 201 && code != 202 {
		t.Fatalf("a title on the device answered %d", code)
	}
	if code := status(t, th.s, "GET", "/v1/playback/sessions/"+watching.ID, tv, nil); code != 404 {
		t.Fatalf("the channel replaced by a title answered %d", code)
	}
	if got := get[Dashboard](t, th.s, owner, "/v1/admin/dashboard"); got.NowPlaying.Streams != 1 {
		t.Fatalf("after the title replaced the channel the Dashboard counts %d streams", got.NowPlaying.Streams)
	}
}

// At a limit of one, two starts at once (two channels, or a channel and a title) let exactly
// one in: a channel's place is taken when it is counted, before it reaches its source.
func TestTwoStartsAtOnceAtALimitOfOneLetOneIn(t *testing.T) {
	th := newTheatre(t)
	fake := fakeProvider(t, th.s.now())
	t.Cleanup(th.s.tuners.Close)
	owner := th.owner.AccessToken
	addSource(t, th.s, owner, LiveSourceSave{Kind: "playlist", Name: new("Antenna"), TunerCount: new(2), Config: &live.LiveSourceConfig{PlaylistURL: fake.URL + "/tuned.m3u"}})
	channels := get[Page[LiveChannel]](t, th.s, owner, "/v1/live/channels").Items
	if code := status(t, th.s, "PATCH", "/v1/admin/playback/stream-limits", owner, StreamLimits{Total: 1}); code != 200 {
		t.Fatalf("setting the limit answered %d", code)
	}
	tv, den := onMain(t, th.s, signIn(t, th.s, 2)).AccessToken, onMain(t, th.s, signIn(t, th.s, 3)).AccessToken
	stop := func() {
		for _, p := range get[Page[NowPlaying]](t, th.s, owner, "/v1/admin/sessions").Items {
			status(t, th.s, "DELETE", "/v1/admin/sessions/"+p.ID, owner, StreamStop{})
		}
	}
	for round := range 5 {
		var codes [2]int
		var wg sync.WaitGroup
		wg.Go(func() { codes[0] = status(t, th.s, "POST", "/v1/playback/sessions", tv, PlaybackStart{Channel: channels[0].ID}) })
		wg.Go(func() { codes[1] = status(t, th.s, "POST", "/v1/playback/sessions", den, PlaybackStart{Channel: channels[1].ID}) })
		wg.Wait()
		if (codes[0] == 201) == (codes[1] == 201) {
			t.Fatalf("round %d: two channel starts at once at a limit of one answered %v", round, codes)
		}
		stop()
		wg.Go(func() { codes[0] = status(t, th.s, "POST", "/v1/playback/sessions", tv, PlaybackStart{Channel: channels[0].ID}) })
		wg.Go(func() { codes[1], _ = th.start(den, "", PlaybackStart{ItemID: th.film}) })
		wg.Wait()
		titleIn := codes[1] == 201 || codes[1] == 202
		if (codes[0] == 201) == titleIn {
			t.Fatalf("round %d: a channel and a title at once at a limit of one answered %v", round, codes)
		}
		stop()
	}
}

// At a person's limit, the account's streams listed for them to stop include a channel being
// watched on another device, named, and stopping it from there makes room.
func TestAChannelIsInTheListShownAtAStreamLimit(t *testing.T) {
	th := newTheatre(t)
	fake := fakeProvider(t, th.s.now())
	t.Cleanup(th.s.tuners.Close)
	owner := th.owner.AccessToken
	addSource(t, th.s, owner, LiveSourceSave{Kind: "playlist", Name: new("Antenna"), TunerCount: new(2), Config: &live.LiveSourceConfig{PlaylistURL: fake.URL + "/tuned.m3u"}})
	channels := get[Page[LiveChannel]](t, th.s, owner, "/v1/live/channels").Items
	if code := status(t, th.s, "PATCH", "/v1/admin/playback/stream-limits", owner, StreamLimits{PerPerson: 1}); code != 200 {
		t.Fatalf("setting the limit answered %d", code)
	}
	tv, phone := onMain(t, th.s, signIn(t, th.s, 2)).AccessToken, onMain(t, th.s, signIn(t, th.s, 3)).AccessToken
	var watching PlaybackSession
	if w := call(t, th.s, "POST", "/v1/playback/sessions", tv, PlaybackStart{Channel: channels[0].ID}, &watching); w.Code != 201 {
		t.Fatalf("watching answered %d: %s", w.Code, w.Body)
	}
	if code, _ := th.start(phone, "", PlaybackStart{ItemID: th.film}); code == 201 || code == 202 {
		t.Fatalf("a title past the person's limit started (%d)", code)
	}
	listed := get[AccountPlayback](t, th.s, phone, "/v1/playback/sessions")
	if len(listed.Items) != 1 || listed.Items[0].ID != watching.ID || listed.Items[0].Kind != "live" || listed.Items[0].Title != channels[0].Name ||
		listed.Items[0].DeviceName == "" || listed.Limit != 1 {
		t.Fatalf("the list at the limit reads %+v", listed)
	}
	if code := status(t, th.s, "DELETE", "/v1/playback/sessions/"+watching.ID, phone, nil); code != 204 {
		t.Fatalf("stopping the channel from the phone answered %d", code)
	}
	if code, _ := th.start(phone, "", PlaybackStart{ItemID: th.film}); code != 201 && code != 202 {
		t.Fatalf("after the channel was stopped the title answered %d", code)
	}
}
